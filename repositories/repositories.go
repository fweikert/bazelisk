package repositories

import (
	"errors"
)

type ReleaseRepo interface {
	GetReleaseVersions() ([]string, error)
	DownloadRelease(version, targetDirectory string) (string, error)
}

type CandidateRepo interface {
	GetCandidateVersions() ([]string, error)
	DownloadCandidate(version, targetDirectory string) (string, error)
}

type ForkRepo interface {
	GetVersions(fork string) ([]string, error)
	DownloadVersion(fork, version, targetDirectory string)
}

type Repositories struct {
	releases   ReleaseRepo
	candidates CandidateRepo
	fork       ForkRepo
}

func SetUp(releases ReleaseRepo, candidates CandidateRepo, fork ForkRepo) *Repositories {
	return &Repositories{releases: releases, candidates: candidates, fork: fork}
}

func (r *Repositories) Releases() (ReleaseRepo, error) {
	if r.releases == nil {
		return nil, errors.New("")
	}
	return r.releases, nil
}

func (r *Repositories) Candidates() (CandidateRepo, error) {
	if r.candidates == nil {
		return nil, errors.New("")
	}
	return r.candidates, nil
}

func (r *Repositories) Fork() (ForkRepo, error) {
	if r.fork == nil {
		return nil, errors.New("")
	}
	return r.fork, nil
}
