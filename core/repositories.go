package core

import (
	"errors"
	"fmt"

	"github.com/bazelbuild/bazelisk/httputil"
	"github.com/bazelbuild/bazelisk/platforms"
)

type ReleaseRepo interface {
	GetReleaseVersions() ([]string, error)
	DownloadRelease(version, destDir, destFile string) (string, error)
}

type CandidateRepo interface {
	GetLatestCandidateVersion() (string, error)
	DownloadCandidate(version, destDir, destFile string) (string, error)
}

type ForkRepo interface {
	GetVersions(fork string) ([]string, error)
	DownloadVersion(fork, version, destDir, destFile string) (string, error)
}

type LastGreenRepo interface {
	GetLastGreenVersion(downstreamGreen bool) (string, error)
	DownloadLastGreen(commit, destDir, destFile string) (string, error)
}

type Repositories struct {
	Releases        ReleaseRepo
	Candidates      CandidateRepo
	Fork            ForkRepo
	LastGreen       LastGreenRepo
	supportsBaseURL bool
}

func (r *Repositories) DownloadFromBaseURL(baseURL, version, destDir, destFile string) (string, error) {
	if !r.supportsBaseURL {
		return "", fmt.Errorf("downloads from BAZELISK_BASE_URL are forbidden")
	} else if baseURL == "" {
		return "", errors.New("BAZELISK_BASE_URL is not set")
	}
	url := fmt.Sprintf("%s/%s/%s", baseURL, platforms.GetPlatform(), version)
	return httputil.DownloadBinary(url, destDir, destFile)
}

func CreateRepositories(releases ReleaseRepo, candidates CandidateRepo, fork ForkRepo, lastGreen LastGreenRepo, supportsBaseURL bool) *Repositories {
	repos := &Repositories{supportsBaseURL: supportsBaseURL}

	if releases == nil {
		repo.releases = &noReleaseRepo{errors.New("official Bazel releases are not supported")}
	} else {
		repo.releases = releases
	}

	if candidates == nil {
		repo.candidates = &noCandidateRepo{errors.New("Bazel release candidates are not supported")}
	} else {
		repo.candidates = candidates
	}

	if fork == nil {
		repo.fork = &noForkRepo{errors.New("forked versions of Bazel are not supported")}
	} else {
		repo.fork = fork
	}

	if lastGreen == nil {
		repo.lastGreen = &noLastGreenRepo{errors.New("Bazel-at-last-green versions are not supported")}
	} else {
		repo.lastGreen = lastGreen
	}

	return repos
}

type noReleaseRepo struct {
	Error error
}

func (nrr *noReleaseRepo) GetReleaseVersions() ([]string, error) {
	return []string{}, nrr.Error
}

func (nrr *noReleaseRepo) DownloadRelease(version, destDir, destFile string) (string, error) {
	return "", nrr.releaseError
}

type noCandidateRepo struct {
	Error error
}

func (ncc *noCandidateRepo) GetLatestCandidateVersion() (string, error) {
	return "", ncc.Error
}

func (ncc *noCandidateRepo) DownloadCandidate(version, destDir, destFile string) (string, error) {
	return "", ncc.Error
}

type noForkRepo struct {
	Error error
}

func (nfr *noForkRepo) GetVersions(fork string) ([]string, error) {
	return "", nfr.kError
}

func (nfr *noForkRepo) DownloadVersion(fork, version, destDir, destFile string) (string, error) {
	return "", nfr.kError
}

type noLastGreenRepo struct {
	Error error
}

func (nlgr *noLastGreenRepo) GetLastGreenVersion(downstreamGreen bool) (string, error) {
	return "", nlgr.Error
}

func (nlgr *noLastGreenRepo) DownloadLastGreen(commit, destDir, destFile string) (string, error) {
	return "", nlgr.Error
}
