package core

import (
	"errors"
	"fmt"
	"strings"

	"github.com/bazelbuild/bazelisk/config"
	"github.com/bazelbuild/bazelisk/httputil"
	"github.com/bazelbuild/bazelisk/platforms"
	"github.com/bazelbuild/bazelisk/versions"
)

const (
	// BaseURLEnv is the name of the environment variable that stores the base URL for downloads.
	BaseURLEnv = "BAZELISK_BASE_URL"

	// FormatURLEnv is the name of the environment variable that stores the format string to generate URLs for downloads.
	FormatURLEnv = "BAZELISK_FORMAT_URL"
)

// DownloadFunc downloads a specific Bazel binary to the given location and returns the absolute path.
type DownloadFunc func(destDir, destFile string) (string, error)

// VersionFilter filters Bazel versions based on specific criteria.
type VersionFilter func(string) bool

var IsRelease = func(version string) bool {
	return !strings.Contains(version, "pre") && !strings.Contains(version, "rc")
}

var IsCandidate = func(version string) bool {
	return !strings.Contains(version, "pre") && strings.Contains(version, "rc")
}

var IsLTS = func(version string) bool {
	return !strings.Contains(version, "pre")
}

func TrackFilter(track int) ReleaseFilter {
	prefix := fmt.Sprintf("%d.", track)
	return func(version string) bool {
		return strings.HasPrefix(version, prefix)
	}
}

type FilterOpts struct {
	MaxResults int
	Filters []VersionFilter
}

func NewFilterOpts(maxResults int, filters VersionFilter...) *FilterOpts {
	return &FilterOpts{
		MaxResults: maxResults,
		Filters: filters,
	}
}

// LTSRepo represents a repository that stores LTS Bazel releases and their candidates.
type LTSRepo interface {
	// GetVersions returns a list of all available LTS release (candidates) that match the given filter options.
	// Warning: Filters only work reliably if the versions are processed in descending order!
	GetVersions(bazeliskHome string, opts *FilterOpts) ([]string, error)

	// Download downloads the given Bazel version into the specified location and returns the absolute path.
	Download(version, destDir, destFile string, config config.Config) (string, error)
}

// ForkRepo represents a repository that stores a fork of Bazel (releases).
type ForkRepo interface {
	// GetVersions returns the versions of all available Bazel binaries in the given fork.
	GetVersions(bazeliskHome, fork string) ([]string, error)

	// DownloadVersion downloads the given Bazel binary from the specified fork into the given location and returns the absolute path.
	DownloadVersion(fork, version, destDir, destFile string, config config.Config) (string, error)
}

// CommitRepo represents a repository that stores Bazel binaries built at specific commits.
// It can also return the hashes of the most recent commits that passed Bazel CI pipelines successfully.
type CommitRepo interface {
	// GetLastGreenCommit returns the most recent commit at which a Bazel binary passed a specific Bazel CI pipeline.
	// If downstreamGreen is true, the pipeline is https://buildkite.com/bazel/bazel-at-head-plus-downstream, otherwise
	// it's https://buildkite.com/bazel/bazel-bazel
	GetLastGreenCommit(bazeliskHome string, downstreamGreen bool) (string, error)

	// DownloadAtCommit downloads a Bazel binary built at the given commit into the specified location and returns the absolute path.
	DownloadAtCommit(commit, destDir, destFile string, config config.Config) (string, error)
}

// RollingRepo represents a repository that stores rolling Bazel releases.
type RollingRepo interface {
	// GetRollingVersions returns a list of all available rolling release versions.
	GetRollingVersions(bazeliskHome string) ([]string, error)

	// DownloadRolling downloads the given Bazel version into the specified location and returns the absolute path.
	DownloadRolling(version, destDir, destFile string, config config.Config) (string, error)
}

// Repositories offers access to different types of Bazel repositories, mainly for finding and downloading the correct version of Bazel.
type Repositories struct {
	LTS        LTSRepo
	Fork            ForkRepo
	Commits         CommitRepo
	Rolling         RollingRepo
	supportsBaseURL bool
}

// ResolveVersion resolves a potentially relative Bazel version string such as "latest" to an absolute version identifier, and returns this identifier alongside a function to download said version.
func (r *Repositories) ResolveVersion(bazeliskHome, fork, version string, config config.Config) (string, DownloadFunc, error) {
	vi, err := versions.Parse(fork, version)
	if err != nil {
		return "", nil, err
	}

	if vi.IsFork {
		return r.resolveFork(bazeliskHome, vi, config)
	} else if vi.IsRelease {
		return r.resolveRelease(bazeliskHome, vi, config)
	} else if vi.IsCandidate {
		return r.resolveCandidate(bazeliskHome, vi, config)
	} else if vi.IsCommit {
		return r.resolveCommit(bazeliskHome, vi, config)
	} else if vi.IsRolling {
		return r.resolveRolling(bazeliskHome, vi, config)
	}

	return "", nil, fmt.Errorf("Unsupported version identifier '%s'", version)
}

func (r *Repositories) resolveFork(bazeliskHome string, vi *versions.Info, config config.Config) (string, DownloadFunc, error) {
	if vi.IsRelative && (vi.IsCandidate || vi.IsCommit) {
		return "", nil, errors.New("forks do not support last_rc, last_green and last_downstream_green")
	}
	lister := func(bazeliskHome string) ([]string, error) {
		return r.Fork.GetVersions(bazeliskHome, vi.Fork)
	}
	version, err := resolvePotentiallyRelativeVersion(bazeliskHome, lister, vi)
	if err != nil {
		return "", nil, err
	}
	downloader := func(destDir, destFile string) (string, error) {
		return r.Fork.DownloadVersion(vi.Fork, version, destDir, destFile, config)
	}
	return version, downloader, nil
}

func (r *Repositories) resolveRelease(bazeliskHome string, vi *versions.Info, config config.Config) (string, DownloadFunc, error) {
	lister := func(bazeliskHome string) ([]string, error) {
		var filter ReleaseFilter
		if vi.TrackRestriction > 0 {
			// Optimization: only fetch matching releases if an LTS track is specified.
			filter = filterReleasesByTrack(vi.TrackRestriction)
		} else {
			// Optimization: only fetch last (x+1) releases if the version is "latest-x".
			filter = lastNReleases(vi.LatestOffset + 1)
		}
		return r.Releases.GetReleaseVersions(bazeliskHome, filter)
	}
	version, err := resolvePotentiallyRelativeVersion(bazeliskHome, lister, vi)
	if err != nil {
		return "", nil, err
	}
	downloader := func(destDir, destFile string) (string, error) {
		return r.Releases.DownloadRelease(version, destDir, destFile, config)
	}
	return version, downloader, nil
}

func (r *Repositories) resolveCandidate(bazeliskHome string, vi *versions.Info, config config.Config) (string, DownloadFunc, error) {
	version, err := resolvePotentiallyRelativeVersion(bazeliskHome, r.Candidates.GetCandidateVersions, vi)
	if err != nil {
		return "", nil, err
	}
	downloader := func(destDir, destFile string) (string, error) {
		return r.Candidates.DownloadCandidate(version, destDir, destFile, config)
	}
	return version, downloader, nil
}

func (r *Repositories) resolveCommit(bazeliskHome string, vi *versions.Info, config config.Config) (string, DownloadFunc, error) {
	version := vi.Value
	if vi.IsRelative {
		var err error
		version, err = r.Commits.GetLastGreenCommit(bazeliskHome, vi.IsDownstream)
		if err != nil {
			return "", nil, fmt.Errorf("cannot resolve last green commit: %v", err)
		}
	}
	downloader := func(destDir, destFile string) (string, error) {
		return r.Commits.DownloadAtCommit(version, destDir, destFile, config)
	}
	return version, downloader, nil
}

func (r *Repositories) resolveRolling(bazeliskHome string, vi *versions.Info, config config.Config) (string, DownloadFunc, error) {
	lister := func(bazeliskHome string) ([]string, error) {
		return r.Rolling.GetRollingVersions(bazeliskHome)
	}
	version, err := resolvePotentiallyRelativeVersion(bazeliskHome, lister, vi)
	if err != nil {
		return "", nil, err
	}
	downloader := func(destDir, destFile string) (string, error) {
		return r.Rolling.DownloadRolling(version, destDir, destFile, config)
	}
	return version, downloader, nil
}

type listVersionsFunc func(bazeliskHome string) ([]string, error)

func resolvePotentiallyRelativeVersion(bazeliskHome string, lister listVersionsFunc, vi *versions.Info) (string, error) {
	if !vi.IsRelative {
		return vi.Value, nil
	}

	available, err := lister(bazeliskHome)
	if err != nil {
		return "", fmt.Errorf("unable to determine latest version: %v", err)
	}

	index := len(available) - 1 - vi.LatestOffset
	if index < 0 {
		return "", fmt.Errorf("cannot resolve version \"%s\": There are not enough matching Bazel releases (%d)", vi.Value, len(available))
	}
	sorted := versions.GetInAscendingOrder(available)
	return sorted[index], nil
}

// DownloadFromBaseURL can download Bazel binaries from a specific URL while ignoring the predefined repositories.
func (r *Repositories) DownloadFromBaseURL(baseURL, version, destDir, destFile string, config config.Config) (string, error) {
	if !r.supportsBaseURL {
		return "", fmt.Errorf("downloads from %s are forbidden", BaseURLEnv)
	} else if baseURL == "" {
		return "", fmt.Errorf("%s is not set", BaseURLEnv)
	}

	srcFile, err := platforms.DetermineBazelFilename(version, true, config)
	if err != nil {
		return "", err
	}

	url := fmt.Sprintf("%s/%s/%s", baseURL, version, srcFile)
	return httputil.DownloadBinary(url, destDir, destFile, config)
}

// BuildURLFromFormat returns a Bazel download URL based on formatURL.
func BuildURLFromFormat(config config.Config, formatURL, version string) (string, error) {
	osName, err := platforms.DetermineOperatingSystem()
	if err != nil {
		return "", err
	}

	machineName, err := platforms.DetermineArchitecture(osName, version)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	b.Grow(len(formatURL) * 2) // Approximation.
	for i := 0; i < len(formatURL); i++ {
		ch := formatURL[i]
		if ch == '%' {
			i++
			if i == len(formatURL) {
				return "", errors.New("trailing %")
			}

			ch = formatURL[i]
			switch ch {
			case 'e':
				b.WriteString(platforms.DetermineExecutableFilenameSuffix())
			case 'h':
				b.WriteString(config.Get("BAZELISK_VERIFY_SHA256"))
			case 'm':
				b.WriteString(machineName)
			case 'o':
				b.WriteString(osName)
			case 'v':
				b.WriteString(version)
			case '%':
				b.WriteByte('%')
			default:
				return "", fmt.Errorf("unknown placeholder %%%c", ch)
			}
		} else {
			b.WriteByte(ch)
		}
	}
	return b.String(), nil
}

// DownloadFromFormatURL can download Bazel binaries from a specific URL while ignoring the predefined repositories.
func (r *Repositories) DownloadFromFormatURL(config config.Config, formatURL, version, destDir, destFile string) (string, error) {
	if formatURL == "" {
		return "", fmt.Errorf("%s is not set", FormatURLEnv)
	}

	url, err := BuildURLFromFormat(config, formatURL, version)
	if err != nil {
		return "", err
	}

	return httputil.DownloadBinary(url, destDir, destFile, config)
}

// CreateRepositories creates a new Repositories instance with the given repositories. Any nil repository will be replaced by a dummy repository that raises an error whenever a download is attempted.
func CreateRepositories(releases ReleaseRepo, candidates CandidateRepo, fork ForkRepo, commits CommitRepo, rolling RollingRepo, supportsBaseURL bool) *Repositories {
	repos := &Repositories{supportsBaseURL: supportsBaseURL}

	if releases == nil {
		repos.Releases = &noReleaseRepo{err: errors.New("Bazel LTS releases are not supported")}
	} else {
		repos.Releases = releases
	}

	if candidates == nil {
		repos.Candidates = &noCandidateRepo{err: errors.New("Bazel release candidates are not supported")}
	} else {
		repos.Candidates = candidates
	}

	if fork == nil {
		repos.Fork = &noForkRepo{err: errors.New("forked versions of Bazel are not supported")}
	} else {
		repos.Fork = fork
	}

	if commits == nil {
		repos.Commits = &noCommitRepo{err: errors.New("Bazel versions built at commits are not supported")}
	} else {
		repos.Commits = commits
	}

	if rolling == nil {
		repos.Rolling = &noRollingRepo{err: errors.New("Bazel rolling releases are not supported")}
	} else {
		repos.Rolling = rolling
	}

	return repos
}

// The whole point of the structs below this line is that users can simply call repos.Releases.GetReleaseVersions()
// (etc) without having to worry whether `Releases` points at an actual repo.

type noReleaseRepo struct {
	err error
}

func (nrr *noReleaseRepo) GetReleaseVersions(bazeliskHome string, filter ReleaseFilter) ([]string, error) {
	return nil, nrr.err
}

func (nrr *noReleaseRepo) DownloadRelease(version, destDir, destFile string, config config.Config) (string, error) {
	return "", nrr.err
}

type noCandidateRepo struct {
	err error
}

func (ncc *noCandidateRepo) GetCandidateVersions(bazeliskHome string) ([]string, error) {
	return nil, ncc.err
}

func (ncc *noCandidateRepo) DownloadCandidate(version, destDir, destFile string, config config.Config) (string, error) {
	return "", ncc.err
}

type noForkRepo struct {
	err error
}

func (nfr *noForkRepo) GetVersions(bazeliskHome, fork string) ([]string, error) {
	return nil, nfr.err
}

func (nfr *noForkRepo) DownloadVersion(fork, version, destDir, destFile string, config config.Config) (string, error) {
	return "", nfr.err
}

type noCommitRepo struct {
	err error
}

func (nlgr *noCommitRepo) GetLastGreenCommit(bazeliskHome string, downstreamGreen bool) (string, error) {
	return "", nlgr.err
}

func (nlgr *noCommitRepo) DownloadAtCommit(commit, destDir, destFile string, config config.Config) (string, error) {
	return "", nlgr.err
}

type noRollingRepo struct {
	err error
}

func (nrr *noRollingRepo) GetRollingVersions(bazeliskHome string) ([]string, error) {
	return nil, nrr.err
}

func (nrr *noRollingRepo) DownloadRolling(version, destDir, destFile string, config config.Config) (string, error) {
	return "", nrr.err
}
