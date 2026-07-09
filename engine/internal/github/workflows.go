package github

// Workflow-run surface (F3 pivot): the conductor's auto_if_safe approval — list
// runs waiting for approval, inspect a PR's changed files, and rerun a run
// (approving it as the acting user, the mechanism the ADR PoC validated).

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// PendingRun is a workflow run stuck in action_required (agent PR gate).
type PendingRun struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	HeadSHA   string `json:"head_sha"`
	PRNumbers []int  `json:"-"`
}

// ListActionRequiredRuns returns the repo's workflow runs waiting for approval,
// with the PRs they belong to.
func (c *Client) ListActionRequiredRuns(ctx context.Context, repoURL string) ([]PendingRun, error) {
	slug, err := slugFromURL(repoURL)
	if err != nil {
		return nil, err
	}
	out, err := c.runner(ctx, "", "gh", "api",
		fmt.Sprintf("repos/%s/actions/runs?status=action_required&per_page=50", slug))
	if err != nil {
		return nil, fmt.Errorf("gh api action_required runs: %w: %s", err, strings.TrimSpace(out))
	}
	var raw struct {
		WorkflowRuns []struct {
			ID           int64  `json:"id"`
			Name         string `json:"name"`
			HeadSHA      string `json:"head_sha"`
			PullRequests []struct {
				Number int `json:"number"`
			} `json:"pull_requests"`
		} `json:"workflow_runs"`
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return nil, fmt.Errorf("decode runs: %w", err)
	}
	runs := make([]PendingRun, 0, len(raw.WorkflowRuns))
	for _, r := range raw.WorkflowRuns {
		pr := PendingRun{ID: r.ID, Name: r.Name, HeadSHA: r.HeadSHA}
		for _, p := range r.PullRequests {
			pr.PRNumbers = append(pr.PRNumbers, p.Number)
		}
		runs = append(runs, pr)
	}
	return runs, nil
}

// WorkflowRunsActive reports whether workflowFile has any run that is NOT in a
// terminal state right now (queued/in_progress/requested/waiting/pending). It is
// the GitHub-observable liveness signal the conductor uses to decide whether a
// stale `agent:running` label is backed by a real run: the label is put by
// claude.yml at start and removed in its if:always() step, but a run that DIES
// without cleanup (cancelled, killed, out of session) leaves the label pinned —
// with NO active run, that label is stale and the story is stuck `running`.
//
// Fail-CLOSED (returns true) on any API error: an inconclusive read must never
// let the conductor declare a possibly-live run dead and yank its story.
func (c *Client) WorkflowRunsActive(ctx context.Context, repoURL, workflowFile string) (bool, error) {
	slug, err := slugFromURL(repoURL)
	if err != nil {
		return true, err
	}
	out, err := c.runner(ctx, "", "gh", "api",
		fmt.Sprintf("repos/%s/actions/workflows/%s/runs?per_page=30", slug, workflowFile))
	if err != nil {
		return true, fmt.Errorf("gh api workflow runs %s: %w: %s", workflowFile, err, strings.TrimSpace(out))
	}
	var raw struct {
		WorkflowRuns []struct {
			Status string `json:"status"`
		} `json:"workflow_runs"`
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return true, fmt.Errorf("decode workflow runs: %w", err)
	}
	for _, r := range raw.WorkflowRuns {
		switch r.Status {
		case "queued", "in_progress", "requested", "waiting", "pending":
			return true, nil
		}
	}
	return false, nil
}

// ListPRFiles returns the changed file paths of a PR (paginated).
func (c *Client) ListPRFiles(ctx context.Context, repoURL string, number int) ([]string, error) {
	slug, err := slugFromURL(repoURL)
	if err != nil {
		return nil, err
	}
	out, err := c.runner(ctx, "", "gh", "api", "--paginate",
		fmt.Sprintf("repos/%s/pulls/%d/files?per_page=100", slug, number), "--jq", ".[].filename")
	if err != nil {
		return nil, fmt.Errorf("gh api pr files #%d: %w: %s", number, err, strings.TrimSpace(out))
	}
	var files []string
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if l != "" {
			files = append(files, l)
		}
	}
	return files, nil
}

// RerunWorkflowRun re-fires an action_required run as the authenticated user —
// the effective "approve" for same-repo agent PRs (POC-validated: /approve is
// fork-PRs-only; rerun flips action_required → queued).
func (c *Client) RerunWorkflowRun(ctx context.Context, repoURL string, runID int64) error {
	slug, err := slugFromURL(repoURL)
	if err != nil {
		return err
	}
	out, err := c.runner(ctx, "", "gh", "api", "-X", "POST",
		fmt.Sprintf("repos/%s/actions/runs/%d/rerun", slug, runID))
	if err != nil {
		return fmt.Errorf("gh api rerun %d: %w: %s", runID, err, strings.TrimSpace(out))
	}
	return nil
}

// PRMergeInfo es la foto de mergeabilidad que el auto-merge del conductor
// consulta. mergeStateStatus CLEAN = sin conflictos, checks requeridos verdes.
type PRMergeInfo struct {
	State            string `json:"state"`
	Draft            bool   `json:"isDraft"`
	MergeStateStatus string `json:"mergeStateStatus"`
	ReviewDecision   string `json:"reviewDecision"`
}

// PRMergeInfo lee estado/mergeabilidad/review de un PR vía gh pr view.
func (c *Client) PRMergeInfo(ctx context.Context, repoURL string, number int) (PRMergeInfo, error) {
	slug, err := slugFromURL(repoURL)
	if err != nil {
		return PRMergeInfo{}, err
	}
	out, err := c.runner(ctx, "", "gh", "pr", "view", fmt.Sprintf("%d", number),
		"-R", slug, "--json", "state,isDraft,mergeStateStatus,reviewDecision")
	if err != nil {
		return PRMergeInfo{}, fmt.Errorf("gh pr view #%d: %w: %s", number, err, strings.TrimSpace(out))
	}
	var info PRMergeInfo
	if err := json.Unmarshal([]byte(out), &info); err != nil {
		return PRMergeInfo{}, fmt.Errorf("decode pr view: %w", err)
	}
	return info, nil
}
