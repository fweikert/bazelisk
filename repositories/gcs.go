package repositories

import (
	"fmt"
	"log"

	"github.com/bazelbuild/bazelisk/httputil"
	"github.com/bazelbuild/bazelisk/platforms"
)

const (
	candidateBaseURL = "https://releases.bazel.build"
	nonCandidateBaseURL = "https://storage.googleapis.com/bazel-builds/artifacts"
	lastGreenBaseURL= "https://storage.googleapis.com/bazel-untrusted-builds/last_green_commit/"
)

var (
	// key: includeDownstream
	lastGreenCommitPathSuffixes := map[bool]string{
		false:            "github.com/bazelbuild/bazel.git/bazel-bazel",
		true: "downstream_pipeline",
	}
)

type GCSRepo struct{}

// ReleaseRepo
func (gcs *GCSRepo) GetReleaseVersions() ([]string, error)                             {
	
}

func (gcs *GCSRepo) DownloadRelease(version, destDir, destFile string) (string, error) {
	url := fmt.Sprintf("%s/%s/release/%s", candidateBaseURL, version, filename)
	return httputil.DownloadBinary(url, destDir, destFile)
}

// CandidateRepo
func (gcs *GCSRepo) GetLatestCandidateVersion() (string, error)                            {

}

func (gcs *GCSRepo) DownloadCandidate(version, destDir, destFile string) (string, error) {
	if !strings.Contains(version, "rc") {
		return "", fmt.Errorf("'%s' is not a release candidate version", version)
	}

	versionComponents := strings.Split(version, "rc")
	baseVersion := versionComponents[0]
	rcVersion := "rc" + versionComponents[1]
	url := fmt.Sprintf("%s/%s/%s/%s", candidateBaseURL, version, kind, filename)
	return httputil.DownloadBinary(url, destDir, destFile)
}

// LastGreenRepo
func (gcs *GCSRepo) GetLastGreenVersion(downstreamGreen bool) (string, error) {
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
}
