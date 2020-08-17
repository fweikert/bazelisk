package repositories

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/bazelbuild/bazelisk/httputil"
	"github.com/bazelbuild/bazelisk/platforms"
	"github.com/bazelbuild/bazelisk/versions"
)

const (
	candidateBaseURL    = "https://releases.bazel.build"
	nonCandidateBaseURL = "https://storage.googleapis.com/bazel-builds/artifacts"
	lastGreenBaseURL    = "https://storage.googleapis.com/bazel-untrusted-builds/last_green_commit/"
)

var (
	// key == includeDownstream
	lastGreenCommitPathSuffixes = map[bool]string{
		false: "github.com/bazelbuild/bazel.git/bazel-bazel",
		true:  "downstream_pipeline",
	}
)

type GCSRepo struct{}

// ReleaseRepo
func (gcs *GCSRepo) GetReleaseVersions(bazeliskHome string) ([]string, error) {
	return getVersionHistoryFromGCS(true)
}

func getVersionHistoryFromGCS(onlyFullReleases bool) ([]string, error) {
	prefixes, _, err := listDirectoriesInReleaseBucket("")
	if err != nil {
		return []string{}, fmt.Errorf("could not list Bazel versions in GCS bucket: %v", err)
	}

	available := getVersionsFromGCSPrefixes(prefixes)
	sorted := versions.GetInAscendingOrder(available)

	if onlyFullReleases && len(sorted) > 0 {
		latestVersion := sorted[len(sorted)-1]
		_, isRelease, err := listDirectoriesInReleaseBucket(latestVersion + "/release/")
		if err != nil {
			return []string{}, fmt.Errorf("could not list release candidates for latest release: %v", err)
		}
		if !isRelease {
			sorted = sorted[:len(sorted)-1]
		}
	}

	return sorted, nil
}

func listDirectoriesInReleaseBucket(prefix string) ([]string, bool, error) {
	url := "https://www.googleapis.com/storage/v1/b/bazel/o?delimiter=/"
	if prefix != "" {
		url = fmt.Sprintf("%s&prefix=%s", url, prefix)
	}
	content, err := httputil.ReadRemoteFile(url, "")
	if err != nil {
		return nil, false, fmt.Errorf("could not list GCS objects at %s: %v", url, err)
	}

	var response gcsListResponse
	if err := json.Unmarshal(content, &response); err != nil {
		return nil, false, fmt.Errorf("could not parse GCS index JSON: %v", err)
	}
	return response.Prefixes, len(response.Items) > 0, nil
}

func getVersionsFromGCSPrefixes(versions []string) []string {
	result := make([]string, len(versions))
	for i, v := range versions {
		result[i] = strings.TrimSuffix(v, "/")
	}
	return result
}

type gcsListResponse struct {
	Prefixes []string      `json:"prefixes"`
	Items    []interface{} `json:"items"`
}

func (gcs *GCSRepo) DownloadRelease(version, destDir, destFile string) (string, error) {
	url := fmt.Sprintf("%s/%s/release/%s", candidateBaseURL, version, destFile)
	return httputil.DownloadBinary(url, destDir, destFile)
}

// CandidateRepo
func (gcs *GCSRepo) GetCandidateVersions(bazeliskHome string) ([]string, error) {
	return getVersionHistoryFromGCS(false)
}

func (gcs *GCSRepo) DownloadCandidate(version, destDir, destFile string) (string, error) {
	if !strings.Contains(version, "rc") {
		return "", fmt.Errorf("'%s' does not refer to a release candidate", version)
	}

	versionComponents := strings.Split(version, "rc")
	baseVersion := versionComponents[0]
	rcVersion := "rc" + versionComponents[1]
	url := fmt.Sprintf("%s/%s/%s/%s", candidateBaseURL, baseVersion, rcVersion, destFile)
	return httputil.DownloadBinary(url, destDir, destFile)
}

// LastGreenRepo
func (gcs *GCSRepo) GetLastGreenVersion(bazeliskHome string, downstreamGreen bool) (string, error) {
	pathSuffix := lastGreenCommitPathSuffixes[downstreamGreen]
	content, err := httputil.ReadRemoteFile(lastGreenBaseURL+pathSuffix, "")
	if err != nil {
		return "", fmt.Errorf("could not determine last green commit: %v", err)
	}
	return strings.TrimSpace(string(content)), nil
}

func (gcs *GCSRepo) DownloadLastGreen(commit, destDir, destFile string) (string, error) {
	log.Printf("Using unreleased version at commit %s", commit)
	url := fmt.Sprintf("%s/%s/%s/bazel", nonCandidateBaseURL, platforms.GetPlatform(), commit)
	return httputil.DownloadBinary(url, destDir, destFile)
}
