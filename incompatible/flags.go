package incompatible

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)
package incompatible
type label struct {
	Name string `json:"name"`
}

type issue struct {
	Title  string  `json:"title"`
	URL    string  `json:"html_url"`
	Labels []label `json:"labels"`
}

type issueList struct {
	Items []issue `json:"items"`
}

type flagDetails struct {
	Name          string
	ReleaseToFlip string
	IssueURL      string
}

func (f *flagDetails) String() string {
	return fmt.Sprintf("%s (Bazel %s: %s)", f.Name, f.ReleaseToFlip, f.IssueURL)
}

func getIncompatibleFlags(bazeliskHome, resolvedBazelVersion string) (map[string]*flagDetails, error) {
	// GitHub labels use only major and minor version, we ignore the patch number (and any other suffix).
	re := regexp.MustCompile(`^\d+\.\d+`)
	version := re.FindString(resolvedBazelVersion)
	if len(version) == 0 {
		return nil, fmt.Errorf("invalid version %v", resolvedBazelVersion)
	}
	url := "https://api.github.com/search/issues?per_page=100&q=repo:bazelbuild/bazel+label:migration-" + version
	issuesJSON, err := maybeDownload(bazeliskHome, url, "flags-"+version, "list of flags from GitHub")
	if err != nil {
		return nil, fmt.Errorf("could not get issues from GitHub: %v", err)
	}

	result, err := scanIssuesForIncompatibleFlags(issuesJSON)
	return result, err
}

func scanIssuesForIncompatibleFlags(issuesJSON []byte) (map[string]*flagDetails, error) {
	result := make(map[string]*flagDetails)
	var issueList issueList
	if err := json.Unmarshal(issuesJSON, &issueList); err != nil {
		return nil, fmt.Errorf("could not parse JSON into list of issues: %v", err)
	}

	re := regexp.MustCompile(`^incompatible_\w+`)
	s_re := regexp.MustCompile(`^//.*[^/]:incompatible_\w+`)
	for _, issue := range issueList.Items {
		flag := re.FindString(issue.Title)
		if len(flag) <= 0 {
			flag = s_re.FindString(issue.Title)
		}
		if len(flag) > 0 {
			name := "--" + flag
			result[name] = &flagDetails{
				Name:          name,
				ReleaseToFlip: getBreakingRelease(issue.Labels),
				IssueURL:      issue.URL,
			}
		}
	}

	return result, nil
}

func getBreakingRelease(labels []label) string {
	for _, l := range labels {
		if release := strings.TrimPrefix(l.Name, "breaking-change-"); release != l.Name {
			return release
		}
	}
	return "TBD"
}
