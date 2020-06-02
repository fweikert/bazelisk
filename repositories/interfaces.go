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
