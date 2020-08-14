package repositories

import (
	"fmt"

	"github.com/bazelbuild/bazelisk/platforms"
)

const (
	urlPattern = "https://github.com/%s/bazel/releases/download/%s/%s"
)

type GitHubRepo struct {
}

func CreateGitHubRepo() {

}

// ForkRepo
func (gh *GitHubRepo) GetVersions(fork string) ([]string, error) {

}

func (gh *GitHubRepo) DownloadVersion(fork, version, destDir, destFile string) (string, error) {
	filename := platforms.DetermineExecutableFilenameSuffix()
	url := fmt.Sprintf(urlPattern, fork, version, filename)
}
