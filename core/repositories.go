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
	GetCandidateVersions() ([]string, error)
	DownloadCandidate(version, destDir, destFile string) (string, error)
}

type ForkRepo interface {
	GetVersions(fork string) ([]string, error)
	DownloadVersion(fork, version, destDir, destFile string)
}

type LastGreenRepo interface {
	DownloadLastGreen(commit, destDir, destFile string)
}

type Repositories struct {
	releases        ReleaseRepo
	candidates      CandidateRepo
	fork            ForkRepo
	lastGreen       LastGreenRepo
	supportsBaseURL bool
}

func CreateRepositories(releases ReleaseRepo, candidates CandidateRepo, fork ForkRepo, lastGreen LastGreenRepo, supportsBaseURL bool) *Repositories {
	return &Repositories{releases: releases, candidates: candidates, fork: fork, lastGreen: lastGreen, supportsBaseURL: supportsBaseURL}
}

func (r *Repositories) Releases() (ReleaseRepo, error) {
	if r.releases == nil {
		return nil, errors.New("official Bazel releases are not supported")
	}
	return r.releases, nil
}

func (r *Repositories) Candidates() (CandidateRepo, error) {
	if r.candidates == nil {
		return nil, errors.New("Bazel release candidates are not supported")
	}
	return r.candidates, nil
}

func (r *Repositories) Fork() (ForkRepo, error) {
	if r.fork == nil {
		return nil, errors.New("forked versions of Bazel are not supported")
	}
	return r.fork, nil
}

func (r *Repositories) LastGreen() (LastGreenRepo, error) {
	if r.lastGreen == nil {
		return nil, errors.New("Bazel-at-last-green versions are not supported")
	}
	return r.lastGreen, nil
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
