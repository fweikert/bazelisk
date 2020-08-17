// Copyright 2019 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/bazelbuild/bazelisk/core"
	"github.com/bazelbuild/bazelisk/httputil"
	"github.com/bazelbuild/bazelisk/platforms"
	"github.com/bazelbuild/bazelisk/repositories"
	"github.com/bazelbuild/bazelisk/versions"
	homedir "github.com/mitchellh/go-homedir"
)

const (
	bazelReal      = "BAZEL_REAL"
	skipWrapperEnv = "BAZELISK_SKIP_WRAPPER"
	wrapperPath    = "./tools/bazel"
)

var (
	// BazeliskVersion is filled in via x_defs when building a release.
	BazeliskVersion = "development"

	fileConfig     map[string]string
	fileConfigOnce sync.Once
)

// getEnvOrConfig will read a configuration value from the environment, but fall back to reading it from .bazeliskrc in the workspace root.
func getEnvOrConfig(name string) string {
	if val := os.Getenv(name); val != "" {
		return val
	}

	// Parse .bazeliskrc in the workspace root, once, if it can be found.
	fileConfigOnce.Do(func() {
		workingDirectory, err := os.Getwd()
		if err != nil {
			return
		}
		workspaceRoot := findWorkspaceRoot(workingDirectory)
		if workspaceRoot == "" {
			return
		}
		rcFilePath := filepath.Join(workspaceRoot, ".bazeliskrc")
		contents, err := ioutil.ReadFile(rcFilePath)
		if err != nil {
			if os.IsNotExist(err) {
				return
			}
			log.Fatal(err)
		}
		fileConfig = make(map[string]string)
		for _, line := range strings.Split(string(contents), "\n") {
			if strings.HasPrefix(line, "#") {
				// comments
				continue
			}
			parts := strings.SplitN(line, "=", 2)
			if len(parts) < 2 {
				continue
			}
			key := strings.TrimSpace(parts[0])
			fileConfig[key] = strings.TrimSpace(parts[1])
		}
	})

	return fileConfig[name]
}

func findWorkspaceRoot(root string) string {
	if _, err := os.Stat(filepath.Join(root, "WORKSPACE")); err == nil {
		return root
	}

	if _, err := os.Stat(filepath.Join(root, "WORKSPACE.bazel")); err == nil {
		return root
	}

	parentDirectory := filepath.Dir(root)
	if parentDirectory == root {
		return ""
	}

	return findWorkspaceRoot(parentDirectory)
}

func getBazelVersion() (string, error) {
	// Check in this order:
	// - env var "USE_BAZEL_VERSION" is set to a specific version.
	// - env var "USE_NIGHTLY_BAZEL" or "USE_BAZEL_NIGHTLY" is set -> latest
	//   nightly. (TODO)
	// - env var "USE_CANARY_BAZEL" or "USE_BAZEL_CANARY" is set -> latest
	//   rc. (TODO)
	// - the file workspace_root/tools/bazel exists -> that version. (TODO)
	// - workspace_root/.bazeliskrc exists and contains a 'USE_BAZEL_VERSION'
	//   variable -> read contents, that version.
	// - workspace_root/.bazelversion exists -> read contents, that version.
	// - workspace_root/WORKSPACE contains a version -> that version. (TODO)
	// - fallback: latest release
	bazelVersion := getEnvOrConfig("USE_BAZEL_VERSION")
	if len(bazelVersion) != 0 {
		return bazelVersion, nil
	}

	workingDirectory, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("could not get working directory: %v", err)
	}

	workspaceRoot := findWorkspaceRoot(workingDirectory)
	if len(workspaceRoot) != 0 {
		bazelVersionPath := filepath.Join(workspaceRoot, ".bazelversion")
		if _, err := os.Stat(bazelVersionPath); err == nil {
			f, err := os.Open(bazelVersionPath)
			if err != nil {
				return "", fmt.Errorf("could not read %s: %v", bazelVersionPath, err)
			}
			defer f.Close()

			scanner := bufio.NewScanner(f)
			scanner.Scan()
			bazelVersion := scanner.Text()
			if err := scanner.Err(); err != nil {
				return "", fmt.Errorf("could not read version from file %s: %v", bazelVersion, err)
			}

			if len(bazelVersion) != 0 {
				return bazelVersion, nil
			}
		}
	}

	return "latest", nil
}

func parseBazelForkAndVersion(bazelForkAndVersion string) (string, string, error) {
	var bazelFork, bazelVersion string

	versionInfo := strings.Split(bazelForkAndVersion, "/")

	if len(versionInfo) == 1 {
		bazelFork, bazelVersion = core.BazelUpstream, versionInfo[0]
	} else if len(versionInfo) == 2 {
		bazelFork, bazelVersion = versionInfo[0], versionInfo[1]
	} else {
		return "", "", fmt.Errorf("invalid version \"%s\", could not parse version with more than one slash", bazelForkAndVersion)
	}

	return bazelFork, bazelVersion, nil
}

func resolveLatestVersion(bazeliskHome, bazelFork string, offset int, repos *core.Repositories) (string, error) {
	available, err := func() ([]string, error) {
		if bazelFork == "" {
			return repos.Releases.GetReleaseVersions(bazeliskHome)
		}
		return repos.Fork.GetVersions(bazeliskHome, bazelFork)
	}()

	if err != nil {
		return "", fmt.Errorf("unable to determine latest version: %v", err)
	}

	if offset >= len(available) {
		return "", fmt.Errorf("cannot resolve version \"latest-%d\": There are only %d Bazel versions", offset, len(available))
	}

	sorted := versions.GetInAscendingOrder(available)
	return sorted[len(available)-1-offset], nil
}

func resolveLatestRcVersion(bazeliskHome string, repo core.CandidateRepo) (string, error) {
	rcVersions, err := repo.GetCandidateVersions(bazeliskHome)
	if err != nil {
		return "", err
	}

	if len(rcVersions) == 0 {
		return "", errors.New("could not find any Bazel versions")
	}
	return getHighestRcVersion(rcVersions)
}

func getHighestRcVersion(availableVersions []string) (string, error) {
	sorted := versions.GetInAscendingOrder(availableVersions)
	latest := sorted[len(sorted)-1]

	re := regexp.MustCompile(`(\d+.\d+.\d+)rc(\d+)$`)
	m := re.FindStringSubmatch(latest)
	_, err := strconv.Atoi(m[2])
	if err != nil {
		return "", fmt.Errorf("Invalid version number %s: %v", latest, err)
	}

	return latest, nil
}

func resolveVersionLabel(bazeliskHome, bazelFork, bazelVersion string, repos *core.Repositories) (string, bool, error) {
	if !core.IsFork(bazelFork) {
		// Returns three values:
		// 1. The label of a Blaze release (if the label resolves to a release) or a commit (for unreleased binaries),
		// 2. Whether the first value refers to a commit,
		// 3. An error.
		if ok, downstreamGreen := isLastGreen(bazelVersion); ok {
			commit, err := repos.LastGreen.GetLastGreenVersion(bazeliskHome, downstreamGreen)
			if err != nil {
				return "", false, fmt.Errorf("cannot resolve last green commit: %v", err)
			}

			return commit, true, nil
		}

		if bazelVersion == "last_rc" {
			version, err := resolveLatestRcVersion(bazeliskHome, repos.Candidates)
			return version, false, err
		}
	}

	r := regexp.MustCompile(`^latest(?:-(?P<offset>\d+))?$`)

	match := r.FindStringSubmatch(bazelVersion)
	if match != nil {
		offset := 0
		if match[1] != "" {
			var err error
			offset, err = strconv.Atoi(match[1])
			if err != nil {
				return "", false, fmt.Errorf("invalid version \"%s\", could not parse offset: %v", bazelVersion, err)
			}
		}
		version, err := resolveLatestVersion(bazeliskHome, bazelFork, offset, repos)
		return version, false, err
	}

	return bazelVersion, false, nil
}

func isLastGreen(version string) (ok bool, includeDownstream bool) {
	includeDownstream = version == "last_downstream_green"
	ok = version == "last_green" || includeDownstream
	return
}

func determineURL(fork string, version string, isCommit bool, filename string) string {
	baseURL := getEnvOrConfig("BAZELISK_BASE_URL")

	// Technically this function should only be called when BAZELISK_BASE_URL is set.
	if isCommit {
		if len(baseURL) == 0 {
			baseURL = "https://storage.googleapis.com/bazel-builds/artifacts"
		}
		// No need to check the OS thanks to determineBazelFilename().
		log.Printf("Using unreleased version at commit %s", version)
		return fmt.Sprintf("%s/%s/%s/bazel", baseURL, platforms.GetPlatform(), version)
	}

	kind := "release"
	if strings.Contains(version, "rc") {
		versionComponents := strings.Split(version, "rc")
		// Replace version with the part before rc
		version = versionComponents[0]
		kind = "rc" + versionComponents[1]
	}

	if len(baseURL) != 0 {
		return fmt.Sprintf("%s/%s/%s", baseURL, version, filename)
	}

	if !core.IsFork(fork) {
		return fmt.Sprintf("https://releases.bazel.build/%s/%s/%s", version, kind, filename)
	}

	return fmt.Sprintf("https://github.com/%s/bazel/releases/download/%s/%s", fork, version, filename)
}

func downloadBazel(fork string, version string, isCommit bool, baseDirectory string, repos *core.Repositories) (string, error) {
	filename, err := platforms.DetermineBazelFilename(version)
	if err != nil {
		return "", fmt.Errorf("could not determine filename to use for Bazel binary: %v", err)
	}

	filenameSuffix := platforms.DetermineExecutableFilenameSuffix()
	directoryName := strings.TrimSuffix(filename, filenameSuffix)
	destinationDir := filepath.Join(baseDirectory, directoryName, "bin")

	if getEnvOrConfig("BAZELISK_BASE_URL") != "" {
		url := determineURL(fork, version, isCommit, filename)
		return repos.DownloadFromBaseURL(url, version, destinationDir, filename)
	}

	return repos.DownloadFromRepo(fork, version, isCommit, destinationDir, filename)
}

func copyFile(src, dst string, perm os.FileMode) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE, perm)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)

	return err
}

func linkLocalBazel(baseDirectory string, bazelPath string) (string, error) {
	normalizedBazelPath := dirForURL(bazelPath)
	destinationDir := filepath.Join(baseDirectory, normalizedBazelPath, "bin")
	err := os.MkdirAll(destinationDir, 0755)
	if err != nil {
		return "", fmt.Errorf("could not create directory %s: %v", destinationDir, err)
	}
	destinationPath := filepath.Join(destinationDir, "bazel"+platforms.DetermineExecutableFilenameSuffix())
	if _, err := os.Stat(destinationPath); err != nil {
		err = os.Symlink(bazelPath, destinationPath)
		// If can't create Symlink, fallback to copy
		if err != nil {
			err = copyFile(bazelPath, destinationPath, 0755)
			if err != nil {
				return "", fmt.Errorf("cound not copy file from %s to %s: %v", bazelPath, destinationPath, err)
			}
		}
	}
	return destinationPath, nil
}

func maybeDelegateToWrapper(bazel string) string {
	if getEnvOrConfig(skipWrapperEnv) != "" {
		return bazel
	}

	wd, err := os.Getwd()
	if err != nil {
		return bazel
	}

	root := findWorkspaceRoot(wd)
	wrapper := filepath.Join(root, wrapperPath)
	if stat, err := os.Stat(wrapper); err != nil || stat.IsDir() || stat.Mode().Perm()&0001 == 0 {
		return bazel
	}

	return wrapper
}

func prependDirToPathList(cmd *exec.Cmd, dir string) {
	found := false
	for idx, val := range cmd.Env {
		splits := strings.Split(val, "=")
		if len(splits) != 2 {
			continue
		}
		if splits[0] == "PATH" {
			found = true
			cmd.Env[idx] = fmt.Sprintf("PATH=%s%s%s", dir, string(os.PathListSeparator), splits[1])
			break
		}
	}

	if !found {
		cmd.Env = append(cmd.Env, fmt.Sprintf("PATH=%s", dir))
	}
}

func makeBazelCmd(bazel string, args []string) *exec.Cmd {
	execPath := maybeDelegateToWrapper(bazel)

	cmd := exec.Command(execPath, args...)
	cmd.Env = append(os.Environ(), skipWrapperEnv+"=true")
	if execPath != bazel {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", bazelReal, bazel))
	}
	prependDirToPathList(cmd, filepath.Dir(execPath))
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd
}

func runBazel(bazel string, args []string) (int, error) {
	cmd := makeBazelCmd(bazel, args)
	err := cmd.Start()
	if err != nil {
		return 1, fmt.Errorf("could not start Bazel: %v", err)
	}

	c := make(chan os.Signal)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		s := <-c
		if runtime.GOOS != "windows" {
			cmd.Process.Signal(s)
		} else {
			cmd.Process.Kill()
		}
	}()

	err = cmd.Wait()
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			waitStatus := exitError.Sys().(syscall.WaitStatus)
			return waitStatus.ExitStatus(), nil
		}
		return 1, fmt.Errorf("could not launch Bazel: %v", err)
	}
	return 0, nil
}

type label struct {
	Name string `json:"name"`
}

type issue struct {
	Title  string  `json:"title"`
	URL    string  `json:"html_url"`
	Labels []label `json:"labels"`
}

type issueList struct {
	Items []issue `json:"items"`
}

type flagDetails struct {
	Name          string
	ReleaseToFlip string
	IssueURL      string
}

func (f *flagDetails) String() string {
	return fmt.Sprintf("%s (Bazel %s: %s)", f.Name, f.ReleaseToFlip, f.IssueURL)
}

func getIncompatibleFlags(bazeliskHome, resolvedBazelVersion string) (map[string]*flagDetails, error) {
	// GitHub labels use only major and minor version, we ignore the patch number (and any other suffix).
	re := regexp.MustCompile(`^\d+\.\d+`)
	version := re.FindString(resolvedBazelVersion)
	if len(version) == 0 {
		return nil, fmt.Errorf("invalid version %v", resolvedBazelVersion)
	}
	url := "https://api.github.com/search/issues?per_page=100&q=repo:bazelbuild/bazel+label:migration-" + version
	issuesJSON, err := httputil.MaybeDownload(bazeliskHome, url, "flags-"+version, "list of flags from GitHub", getEnvOrConfig("BAZELISK_GITHUB_TOKEN"))
	if err != nil {
		return nil, fmt.Errorf("could not get issues from GitHub: %v", err)
	}

	result, err := scanIssuesForIncompatibleFlags(issuesJSON)
	return result, err
}

func scanIssuesForIncompatibleFlags(issuesJSON []byte) (map[string]*flagDetails, error) {
	result := make(map[string]*flagDetails)
	var issueList issueList
	if err := json.Unmarshal(issuesJSON, &issueList); err != nil {
		return nil, fmt.Errorf("could not parse JSON into list of issues: %v", err)
	}

	re := regexp.MustCompile(`^incompatible_\w+`)
	s_re := regexp.MustCompile(`^//.*[^/]:incompatible_\w+`)
	for _, issue := range issueList.Items {
		flag := re.FindString(issue.Title)
		if len(flag) <= 0 {
			flag = s_re.FindString(issue.Title)
		}
		if len(flag) > 0 {
			name := "--" + flag
			result[name] = &flagDetails{
				Name:          name,
				ReleaseToFlip: getBreakingRelease(issue.Labels),
				IssueURL:      issue.URL,
			}
		}
	}

	return result, nil
}

func getBreakingRelease(labels []label) string {
	for _, l := range labels {
		if release := strings.TrimPrefix(l.Name, "breaking-change-"); release != l.Name {
			return release
		}
	}
	return "TBD"
}

// insertArgs will insert newArgs in baseArgs. If baseArgs contains the
// "--" argument, newArgs will be inserted before that. Otherwise, newArgs
// is appended.
func insertArgs(baseArgs []string, newArgs []string) []string {
	var result []string
	inserted := false
	for _, arg := range baseArgs {
		if !inserted && arg == "--" {
			result = append(result, newArgs...)
			inserted = true
		}
		result = append(result, arg)
	}

	if !inserted {
		result = append(result, newArgs...)
	}
	return result
}

func shutdownIfNeeded(bazelPath string) {
	bazeliskClean := getEnvOrConfig("BAZELISK_SHUTDOWN")
	if len(bazeliskClean) == 0 {
		return
	}

	fmt.Printf("bazel shutdown\n")
	exitCode, err := runBazel(bazelPath, []string{"shutdown"})
	fmt.Printf("\n")
	if err != nil {
		log.Fatalf("failed to run bazel shutdown: %v", err)
	}
	if exitCode != 0 {
		fmt.Printf("Failure: shutdown command failed.\n")
		os.Exit(exitCode)
	}
}

func cleanIfNeeded(bazelPath string) {
	bazeliskClean := getEnvOrConfig("BAZELISK_CLEAN")
	if len(bazeliskClean) == 0 {
		return
	}

	fmt.Printf("bazel clean --expunge\n")
	exitCode, err := runBazel(bazelPath, []string{"clean", "--expunge"})
	fmt.Printf("\n")
	if err != nil {
		log.Fatalf("failed to run clean: %v", err)
	}
	if exitCode != 0 {
		fmt.Printf("Failure: clean command failed.\n")
		os.Exit(exitCode)
	}
}

// migrate will run Bazel with each newArgs separately and report which ones are failing.
func migrate(bazelPath string, baseArgs []string, flags map[string]*flagDetails) {
	newArgs := getSortedKeys(flags)
	// 1. Try with all the flags.
	args := insertArgs(baseArgs, newArgs)
	fmt.Printf("\n\n--- Running Bazel with all incompatible flags\n\n")
	shutdownIfNeeded(bazelPath)
	cleanIfNeeded(bazelPath)
	fmt.Printf("bazel %s\n", strings.Join(args, " "))
	exitCode, err := runBazel(bazelPath, args)
	if err != nil {
		log.Fatalf("could not run Bazel: %v", err)
	}
	if exitCode == 0 {
		fmt.Printf("Success: No migration needed.\n")
		os.Exit(0)
	}

	// 2. Try with no flags, as a sanity check.
	args = baseArgs
	fmt.Printf("\n\n--- Running Bazel with no incompatible flags\n\n")
	shutdownIfNeeded(bazelPath)
	cleanIfNeeded(bazelPath)
	fmt.Printf("bazel %s\n", strings.Join(args, " "))
	exitCode, err = runBazel(bazelPath, args)
	if err != nil {
		log.Fatalf("could not run Bazel: %v", err)
	}
	if exitCode != 0 {
		fmt.Printf("Failure: Command failed, even without incompatible flags.\n")
		os.Exit(exitCode)
	}

	// 3. Try with each flag separately.
	var passList []string
	var failList []string
	for _, arg := range newArgs {
		args = insertArgs(baseArgs, []string{arg})
		fmt.Printf("\n\n--- Running Bazel with %s\n\n", arg)
		shutdownIfNeeded(bazelPath)
		cleanIfNeeded(bazelPath)
		fmt.Printf("bazel %s\n", strings.Join(args, " "))
		exitCode, err = runBazel(bazelPath, args)
		if err != nil {
			log.Fatalf("could not run Bazel: %v", err)
		}
		if exitCode == 0 {
			passList = append(passList, arg)
		} else {
			failList = append(failList, arg)
		}
	}

	print := func(l []string) {
		for _, arg := range l {
			fmt.Printf("  %s\n", flags[arg])
		}
	}

	// 4. Print report
	fmt.Printf("\n\n+++ Result\n\n")
	fmt.Printf("Command was successful with the following flags:\n")
	print(passList)
	fmt.Printf("\n")
	fmt.Printf("Migration is needed for the following flags:\n")
	print(failList)

	os.Exit(1)
}

func getSortedKeys(data map[string]*flagDetails) []string {
	result := make([]string, 0)
	for key := range data {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}

func dirForURL(url string) string {
	// Replace all characters that might not be allowed in filenames with "-".
	return regexp.MustCompile("[[:^alnum:]]").ReplaceAllString(url, "-")
}

func RunBazelisk(args []string, repos *core.Repositories) (int, error) {
	bazeliskHome := getEnvOrConfig("BAZELISK_HOME")
	if len(bazeliskHome) == 0 {
		userCacheDir, err := os.UserCacheDir()
		if err != nil {
			return -1, fmt.Errorf("could not get the user's cache directory: %v", err)
		}

		bazeliskHome = filepath.Join(userCacheDir, "bazelisk")
	}

	err := os.MkdirAll(bazeliskHome, 0755)
	if err != nil {
		return -1, fmt.Errorf("could not create directory %s: %v", bazeliskHome, err)
	}

	bazelVersionString, err := getBazelVersion()
	if err != nil {
		return -1, fmt.Errorf("could not get Bazel version: %v", err)
	}

	bazelPath, err := homedir.Expand(bazelVersionString)
	if err != nil {
		return -1, fmt.Errorf("could not expand home directory in path: %v", err)
	}

	// If the Bazel version is an absolute path to a Bazel binary in the filesystem, we can
	// use it directly. In that case, we don't know which exact version it is, though.
	resolvedBazelVersion := "unknown"
	isCommit := false

	// If we aren't using a local Bazel binary, we'll have to parse the version string and
	// download the version that the user wants.
	if !filepath.IsAbs(bazelPath) {
		bazelFork, bazelVersion, err := parseBazelForkAndVersion(bazelVersionString)
		if err != nil {
			return -1, fmt.Errorf("could not parse Bazel fork and version: %v", err)
		}

		resolvedBazelVersion, isCommit, err = resolveVersionLabel(bazeliskHome, bazelFork, bazelVersion, repos)
		if err != nil {
			return -1, fmt.Errorf("could not resolve the version '%s' to an actual version number: %v", bazelVersion, err)
		}

		bazelForkOrURL := dirForURL(getEnvOrConfig("BAZELISK_BASE_URL"))
		if len(bazelForkOrURL) == 0 {
			bazelForkOrURL = bazelFork
		}

		baseDirectory := filepath.Join(bazeliskHome, "downloads", bazelForkOrURL)
		bazelPath, err = downloadBazel(bazelFork, resolvedBazelVersion, isCommit, baseDirectory, repos)
		if err != nil {
			return -1, fmt.Errorf("could not download Bazel: %v", err)
		}
	} else {
		baseDirectory := filepath.Join(bazeliskHome, "local")
		bazelPath, err = linkLocalBazel(baseDirectory, bazelPath)
		if err != nil {
			return -1, fmt.Errorf("cound not link local Bazel: %v", err)
		}
	}

	// --print_env must be the first argument.
	if len(args) > 0 && args[0] == "--print_env" {
		// print environment variables for sub-processes
		cmd := makeBazelCmd(bazelPath, args)
		for _, val := range cmd.Env {
			fmt.Println(val)
		}
		return 0, nil
	}

	// --strict and --migrate must be the first argument.
	if len(args) > 0 && (args[0] == "--strict" || args[0] == "--migrate") {
		cmd := args[0]
		newFlags, err := getIncompatibleFlags(bazeliskHome, resolvedBazelVersion)
		if err != nil {
			return -1, fmt.Errorf("could not get the list of incompatible flags: %v", err)
		}

		if cmd == "--migrate" {
			migrate(bazelPath, args[1:], newFlags)
		} else {
			// When --strict is present, it expands to the list of --incompatible_ flags
			// that should be enabled for the given Bazel version.
			args = insertArgs(args[1:], getSortedKeys(newFlags))
		}
	}

	// print bazelisk version information if "version" is the first argument
	// bazel version is executed after this command
	if len(args) > 0 && args[0] == "version" {
		// Check if the --gnu_format flag is set, if that is the case,
		// the version is printed differently
		var gnuFormat bool
		for _, arg := range args {
			if arg == "--gnu_format" {
				gnuFormat = true
				break
			}
		}

		if gnuFormat {
			fmt.Printf("Bazelisk %s\n", BazeliskVersion)
		} else {
			fmt.Printf("Bazelisk version: %s\n", BazeliskVersion)
		}
	}

	exitCode, err := runBazel(bazelPath, args)
	if err != nil {
		return -1, fmt.Errorf("could not run Bazel: %v", err)
	}
	return exitCode, nil
}

func main() {
	gcs := &repositories.GCSRepo{}
	gitHub := repositories.CreateGitHubRepo(getEnvOrConfig("BAZELISK_GITHUB_TOKEN"))
	repositories := core.CreateRepositories(gcs, gcs, gitHub, gcs, true)

	exitCode, err := RunBazelisk(os.Args[1:], repositories)
	if err != nil {
		log.Fatal(err)
	}
	os.Exit(exitCode)
}
