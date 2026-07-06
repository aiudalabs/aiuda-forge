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
	Labels    []string `json:"-"` // label names (the conductor's agent:running lives here)
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
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
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
		for _, l := range r.Labels {
			st.Labels = append(st.Labels, l.Name)
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

// OpenPRDetailed añade el autor y la mergeabilidad (para la cola de PRs de la
// consola). Mergeable es el enum de GraphQL: "MERGEABLE" | "CONFLICTING" |
// "UNKNOWN" — CONFLICTING = el PR choca con main y necesita resolución.
type OpenPRDetailed struct {
	OpenPR
	Author    string
	Mergeable string
}

// ListOpenPRsDetailed es ListOpenPRs con el login del autor y la mergeabilidad.
// Usa `gh pr list` (GraphQL) en vez del REST /pulls porque el campo `mergeable`
// (CONFLICTING) NO lo expone el listado REST — solo el GET de un PR individual.
// Una sola llamada trae los N PRs con su estado de conflicto; --limit 100 replica
// el techo del --paginate del resto del paquete.
func (c *Client) ListOpenPRsDetailed(ctx context.Context, repoURL string) ([]OpenPRDetailed, error) {
	slug, err := slugFromURL(repoURL)
	if err != nil {
		return nil, err
	}
	out, err := c.runner(ctx, "", "gh", "pr", "list", "-R", slug, "--state", "open",
		"--limit", "100", "--json", "number,title,body,url,isDraft,author,mergeable")
	if err != nil {
		return nil, fmt.Errorf("gh pr list %s: %w: %s", slug, err, strings.TrimSpace(out))
	}
	type raw struct {
		Number  int    `json:"number"`
		Title   string `json:"title"`
		Body    string `json:"body"`
		URL     string `json:"url"`
		IsDraft bool   `json:"isDraft"`
		Author  struct {
			Login string `json:"login"`
		} `json:"author"`
		Mergeable string `json:"mergeable"`
	}
	// gh pr list --json devuelve UN solo array JSON (no concatenación paginada).
	var raws []raw
	if err := json.Unmarshal([]byte(out), &raws); err != nil {
		return nil, fmt.Errorf("decode pr list: %w", err)
	}
	prs := make([]OpenPRDetailed, 0, len(raws))
	for _, r := range raws {
		prs = append(prs, OpenPRDetailed{
			OpenPR:    OpenPR{Number: r.Number, Title: r.Title, Body: r.Body, URL: r.URL, Draft: r.IsDraft},
			Author:    r.Author.Login,
			Mergeable: r.Mergeable,
		})
	}
	return prs, nil
}
