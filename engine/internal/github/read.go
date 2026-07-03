package github

// Read surface for the conductor's projection (F1 of the GitHub-native pivot):
// issue states + open PRs, enough to mirror a repo's execution state back into
// the native ticket store. Same runner seam as everything else in this package.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// IssueState is the projection's view of one issue.
type IssueState struct {
	Number    int      `json:"number"`
	State     string   `json:"state"` // "open" | "closed"
	Assignees []string `json:"-"`
}

// issueRaw matches the REST issues list; entries carrying pull_request are PRs
// masquerading as issues and must be filtered out.
type issueRaw struct {
	Number      int    `json:"number"`
	State       string `json:"state"`
	PullRequest *struct {
		URL string `json:"url"`
	} `json:"pull_request,omitempty"`
	Assignees []struct {
		Login string `json:"login"`
	} `json:"assignees"`
}

// ListIssueStates returns the state of ALL issues (open+closed, PRs excluded) of
// repoURL. --paginate walks past the first 100.
func (c *Client) ListIssueStates(ctx context.Context, repoURL string) ([]IssueState, error) {
	slug, err := slugFromURL(repoURL)
	if err != nil {
		return nil, err
	}
	out, err := c.runner(ctx, "", "gh", "api", "--paginate",
		fmt.Sprintf("repos/%s/issues?state=all&per_page=100", slug))
	if err != nil {
		return nil, fmt.Errorf("gh api issues %s: %w: %s", slug, err, strings.TrimSpace(out))
	}
	// --paginate concatenates JSON arrays back-to-back; decode them in sequence.
	var raws []issueRaw
	dec := json.NewDecoder(strings.NewReader(out))
	for dec.More() {
		var page []issueRaw
		if err := dec.Decode(&page); err != nil {
			return nil, fmt.Errorf("decode issues page: %w", err)
		}
		raws = append(raws, page...)
	}
	states := make([]IssueState, 0, len(raws))
	for _, r := range raws {
		if r.PullRequest != nil {
			continue // a PR, not an issue
		}
		st := IssueState{Number: r.Number, State: r.State}
		for _, a := range r.Assignees {
			st.Assignees = append(st.Assignees, a.Login)
		}
		states = append(states, st)
	}
	return states, nil
}

// OpenPR is the projection's view of one open pull request.
type OpenPR struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	URL    string `json:"html_url"`
	Draft  bool   `json:"draft"`
}

// ListOpenPRs returns the open PRs of repoURL (body included, to resolve which
// issues each PR closes).
func (c *Client) ListOpenPRs(ctx context.Context, repoURL string) ([]OpenPR, error) {
	slug, err := slugFromURL(repoURL)
	if err != nil {
		return nil, err
	}
	out, err := c.runner(ctx, "", "gh", "api", "--paginate",
		fmt.Sprintf("repos/%s/pulls?state=open&per_page=100", slug))
	if err != nil {
		return nil, fmt.Errorf("gh api pulls %s: %w: %s", slug, err, strings.TrimSpace(out))
	}
	var prs []OpenPR
	dec := json.NewDecoder(strings.NewReader(out))
	for dec.More() {
		var page []OpenPR
		if err := dec.Decode(&page); err != nil {
			return nil, fmt.Errorf("decode pulls page: %w", err)
		}
		prs = append(prs, page...)
	}
	return prs, nil
}

// OpenPRDetailed añade el autor (para la cola de PRs de la consola).
type OpenPRDetailed struct {
	OpenPR
	Author string
}

// ListOpenPRsDetailed es ListOpenPRs con el login del autor.
func (c *Client) ListOpenPRsDetailed(ctx context.Context, repoURL string) ([]OpenPRDetailed, error) {
	slug, err := slugFromURL(repoURL)
	if err != nil {
		return nil, err
	}
	out, err := c.runner(ctx, "", "gh", "api", "--paginate",
		fmt.Sprintf("repos/%s/pulls?state=open&per_page=100", slug))
	if err != nil {
		return nil, fmt.Errorf("gh api pulls %s: %w: %s", slug, err, strings.TrimSpace(out))
	}
	type raw struct {
		OpenPR
		User struct {
			Login string `json:"login"`
		} `json:"user"`
	}
	var prs []OpenPRDetailed
	dec := json.NewDecoder(strings.NewReader(out))
	for dec.More() {
		var page []raw
		if err := dec.Decode(&page); err != nil {
			return nil, fmt.Errorf("decode pulls page: %w", err)
		}
		for _, r := range page {
			prs = append(prs, OpenPRDetailed{OpenPR: r.OpenPR, Author: r.User.Login})
		}
	}
	return prs, nil
}
