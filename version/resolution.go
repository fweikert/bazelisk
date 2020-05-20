package version

import (
	"errors"
	"fmt"
	"log"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/hashicorp/go-version"
)

func resolveVersionLabel(bazeliskHome, bazelFork, bazelVersion string) (string, bool, error) {
	if bazelFork == bazelUpstream {
		// Returns three values:
		// 1. The label of a Blaze release (if the label resolves to a release) or a commit (for unreleased binaries),
		// 2. Whether the first value refers to a commit,
		// 3. An error.
		lastGreenCommitPathSuffixes := map[string]string{
			"last_green":            "github.com/bazelbuild/bazel.git/bazel-bazel",
			"last_downstream_green": "downstream_pipeline",
		}
		if pathSuffix, ok := lastGreenCommitPathSuffixes[bazelVersion]; ok {
			commit, err := getLastGreenCommit(pathSuffix)
			if err != nil {
				return "", false, fmt.Errorf("cannot resolve last green commit: %v", err)
			}

			return commit, true, nil
		}

		if bazelVersion == "last_rc" {
			version, err := resolveLatestRcVersion()
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
		version, err := resolveLatestVersion(bazeliskHome, bazelFork, offset)
		return version, false, err
	}

	return bazelVersion, false, nil
}

const lastGreenBasePath = "https://storage.googleapis.com/bazel-untrusted-builds/last_green_commit/"

func getLastGreenCommit(pathSuffix string) (string, error) {
	content, err := readRemoteFile(lastGreenBasePath+pathSuffix, "")
	if err != nil {
		return "", fmt.Errorf("could not determine last green commit: %v", err)
	}
	return strings.TrimSpace(string(content)), nil
}

func resolveLatestRcVersion() (string, error) {
	versions, err := getVersionHistoryFromGCS(false)
	if err != nil {
		return "", err
	}

	if len(versions) == 0 {
		return "", errors.New("could not find any Bazel versions")
	}
	latestVersion := versions[len(versions)-1]
	// Append slash to match directories
	rcVersions, _, err := listDirectoriesInReleaseBucket(latestVersion + "/")
	if err != nil {
		return "", fmt.Errorf("could not list release candidates for latest release: %v", err)
	}
	return getHighestRcVersion(rcVersions)
}

func getHighestRcVersion(versions []string) (string, error) {
	var version string
	var lastRc int
	re := regexp.MustCompile(`(\d+.\d+.\d+)/rc(\d+)/`)
	for _, v := range versions {
		// Fallback: use latest release if there is no active RC.
		if strings.Index(v, "release") > -1 {
			return strings.Split(v, "/")[0], nil
		}

		m := re.FindStringSubmatch(v)
		version = m[1]
		rc, err := strconv.Atoi(m[2])
		if err != nil {
			return "", fmt.Errorf("Invalid version number %s: %v", strings.TrimSuffix(v, "/"), err)
		}
		if rc > lastRc {
			lastRc = rc
		}
	}
	return fmt.Sprintf("%src%d", version, lastRc), nil
}

func resolveLatestVersion(bazeliskHome, bazelFork string, offset int) (string, error) {
	versions, err := getVersionHistoryFromGitHub(bazeliskHome, bazelFork)
	if err != nil {
		if bazelFork == bazelUpstream {
			log.Printf("Falling back to GCS due to GitHub error: %v", err)
			versions, err = getVersionHistoryFromGCS(true)
		}
		if err != nil {
			return "", err
		}
	}

	if offset >= len(versions) {
		return "", fmt.Errorf("cannot resolve version \"latest-%d\": There are only %d Bazel versions", offset, len(versions))
	}

	return versions[len(versions)-1-offset], nil
}

func getVersionsInAscendingOrder(versions []string) ([]string, error) {
	wrappers := make([]*version.Version, len(versions))
	for i, v := range versions {
		wrapper, err := version.NewVersion(v)
		if err != nil {
			log.Printf("WARN: Could not parse version: %s", v)
		}
		wrappers[i] = wrapper
	}
	sort.Sort(version.Collection(wrappers))

	sorted := make([]string, len(versions))
	for i, w := range wrappers {
		sorted[i] = w.Original()
	}
	return sorted, nil
}
