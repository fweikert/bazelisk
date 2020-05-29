type bazelRepo interface {
	GetVersions() ([]string, error)
	DownloadCandidate(version, targetDirectory string) (string, error)
}

type UpstreamRepo interface {
	bazelRepo
}

type ReleaseCandidatesRepo interface{
	bazelRepo
}

type ForkRepo interface {
	GetVersions(fork string) ([]string, error)
	DownloadVersion(fork, version, targetDirectory string)
}
