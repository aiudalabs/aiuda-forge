package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// ghClient implements GitHub by shelling out to the `gh` CLI.
type ghClient struct {
	repo string // "owner/repo"
}

// NewGitHubClient returns a GitHub that creates issues against repo.
// repo must be in "owner/repo" form (e.g. "acme/backend").
func NewGitHubClient(repo string) GitHub {
	return &ghClient{repo: repo}
}

// ghIssueResp is the shape `gh issue create --json` returns.
type ghIssueResp struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
}

// CreateIssue shells out to `gh issue create` and returns the new issue number.
func (g *ghClient) CreateIssue(ctx context.Context, title, body string, labels []string) (int, error) {
	args := []string{
		"issue", "create",
		"--repo", g.repo,
		"--title", title,
		"--body", body,
		"--json", "number,url",
	}
	for _, l := range labels {
		args = append(args, "--label", l)
	}

	out, err := exec.CommandContext(ctx, "gh", args...).Output()
	if err != nil {
		var detail string
		if ee, ok := err.(*exec.ExitError); ok {
			detail = strings.TrimSpace(string(ee.Stderr))
		} else {
			detail = err.Error()
		}
		return 0, fmt.Errorf("gh issue create %q: %s", title, detail)
	}

	var resp ghIssueResp
	if err := json.Unmarshal(out, &resp); err != nil {
		return 0, fmt.Errorf("parse gh issue create response: %w", err)
	}
	if resp.Number == 0 {
		return 0, fmt.Errorf("gh issue create: no issue number in response")
	}
	return resp.Number, nil
}
