package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
)

// ghClient implements GitHub by shelling out to the `gh` CLI.
type ghClient struct {
	repo string // "owner/repo"
}

// NewGitHubClient returns a GitHub that calls `gh issue list` against repo.
// repo must be in "owner/repo" form (e.g. "acme/backend").
func NewGitHubClient(repo string) GitHub {
	return &ghClient{repo: repo}
}

// ghIssueJSON is the shape `gh issue list --json` returns per element.
type ghIssueJSON struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	State  string `json:"state"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
}

// ListIssues shells out to `gh issue list` and returns all issues (open and
// closed). We request all states so we can check whether deps are closed.
func (g *ghClient) ListIssues(ctx context.Context) ([]Issue, error) {
	args := []string{
		"issue", "list",
		"--repo", g.repo,
		"--state", "all",
		"--limit", "200",
		"--json", "number,title,body,labels,state",
	}
	out, err := exec.CommandContext(ctx, "gh", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("gh issue list: %w", err)
	}

	var raw []ghIssueJSON
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parse gh output: %w", err)
	}

	issues := make([]Issue, 0, len(raw))
	for _, r := range raw {
		labels := make([]string, 0, len(r.Labels))
		for _, l := range r.Labels {
			labels = append(labels, l.Name)
		}
		issues = append(issues, Issue{
			Number: r.Number,
			Title:  r.Title,
			Body:   r.Body,
			State:  r.State,
			Labels: labels,
		})
	}
	return issues, nil
}
