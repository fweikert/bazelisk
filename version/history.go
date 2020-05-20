package version

import (
	"encoding/json"
	"fmt"
	"strings"
)

func getVersionHistoryFromGitHub(bazeliskHome, bazelFork string) ([]string, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/bazel/releases", bazelFork)
	releasesJSON, err := maybeDownload(bazeliskHome, url, bazelFork+"-releases.json", "list of Bazel releases from github.com/"+bazelFork)
	if err != nil {
		return []string{}, fmt.Errorf("could not get releases from github.com/%s/bazel: %v", bazelFork, err)
	}

	var releases []release
	if err := json.Unmarshal(releasesJSON, &releases); err != nil {
		return []string{}, fmt.Errorf("could not parse JSON into list of releases: %v", err)
	}

	var tags []string
	for _, release := range releases {
		if release.Prerelease {
			continue
		}
		tags = append(tags, release.TagName)
	}
	return getVersionsInAscendingOrder(tags)
}

func getVersionHistoryFromGCS(onlyFullReleases bool) ([]string, error) {
	prefixes, _, err := listDirectoriesInReleaseBucket("")
	if err != nil {
		return []string{}, fmt.Errorf("could not list Bazel versions in GCS bucket: %v", err)
	}

	versions := getVersionsFromGCSPrefixes(prefixes)
	sorted, err := getVersionsInAscendingOrder(versions)
	if err != nil {
		return []string{}, fmt.Errorf("invalid version label: %v", err)
	}

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

func listDirectoriesInReleaseBucket(prefix string) ([]string, bool, error) {
	url := "https://www.googleapis.com/storage/v1/b/bazel/o?delimiter=/"
	if prefix != "" {
		url = fmt.Sprintf("%s&prefix=%s", url, prefix)
	}
	content, err := readRemoteFile(url, "")
	if err != nil {
		return nil, false, fmt.Errorf("could not list GCS objects at %s: %v", url, err)
	}

	var response gcsListResponse
	if err := json.Unmarshal(content, &response); err != nil {
		return nil, false, fmt.Errorf("could not parse GCS index JSON: %v", err)
	}
	return response.Prefixes, len(response.Items) > 0, nil
}
