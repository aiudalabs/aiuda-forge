package conductor

// Workflow approval (F3): agent PRs leave their CI runs in action_required by
// design (an agent could edit CI to exfiltrate secrets). Under
// workflow_approval=auto_if_safe the conductor approves them ONLY when the PR's
// diff stays clear of .github/workflows/** — the exact security caveat the ADR
// PoC documented. Anything touching workflows stays for the human.

import (
	"context"
	"fmt"
	"log"
	"strings"

	"forge/internal/github"
)

// WorkflowApprover is the GitHub surface the approver needs (*github.Client).
type WorkflowApprover interface {
	ListActionRequiredRuns(ctx context.Context, repoURL string) ([]github.PendingRun, error)
	ListPRFiles(ctx context.Context, repoURL string, number int) ([]string, error)
	RerunWorkflowRun(ctx context.Context, repoURL string, runID int64) error
}

// unsafePath reports whether a changed file could alter what CI executes.
func unsafePath(path string) bool {
	return strings.HasPrefix(path, ".github/workflows/") || path == ".github/workflows"
}

// ApproveResult reports one approval sweep / single approval.
type ApproveResult struct {
	Approved []int64 `json:"approved"` // run ids re-fired
	Blocked  []int64 `json:"blocked"`  // run ids left for the human (unsafe diff)
}

// Approver runs the safe-approval policy.
type Approver struct {
	GH WorkflowApprover
}

// prSafe decides whether every file of every PR behind the run is safe. A run
// with NO associated PR is left alone (out of scope: manual dispatches etc.).
func (a *Approver) prSafe(ctx context.Context, repoURL string, prNumbers []int, fileCache map[int][]string) (bool, error) {
	if len(prNumbers) == 0 {
		return false, nil
	}
	for _, n := range prNumbers {
		files, ok := fileCache[n]
		if !ok {
			var err error
			files, err = a.GH.ListPRFiles(ctx, repoURL, n)
			if err != nil {
				return false, err
			}
			fileCache[n] = files
		}
		for _, f := range files {
			if unsafePath(f) {
				return false, nil
			}
		}
	}
	return true, nil
}

// SweepSafeApprovals approves every pending run whose PR diff is safe. Used by
// the conductor tick under workflow_approval=auto_if_safe.
func (a *Approver) SweepSafeApprovals(ctx context.Context, repoURL string) (ApproveResult, error) {
	var res ApproveResult
	runs, err := a.GH.ListActionRequiredRuns(ctx, repoURL)
	if err != nil {
		return res, err
	}
	fileCache := map[int][]string{}
	for _, r := range runs {
		safe, err := a.prSafe(ctx, repoURL, r.PRNumbers, fileCache)
		if err != nil {
			log.Printf("conductor: approve sweep %s run %d: %v", repoURL, r.ID, err)
			continue
		}
		if !safe {
			res.Blocked = append(res.Blocked, r.ID)
			continue
		}
		if err := a.GH.RerunWorkflowRun(ctx, repoURL, r.ID); err != nil {
			log.Printf("conductor: approve run %d: %v", r.ID, err)
			continue
		}
		res.Approved = append(res.Approved, r.ID)
	}
	return res, nil
}

// ApproveOne approves a single pending run after the same safety check (the
// console's per-run button; works regardless of the project's approval mode —
// a human clicked, but the workflows-touching guard still applies).
func (a *Approver) ApproveOne(ctx context.Context, repoURL string, runID int64) (bool, error) {
	runs, err := a.GH.ListActionRequiredRuns(ctx, repoURL)
	if err != nil {
		return false, err
	}
	for _, r := range runs {
		if r.ID != runID {
			continue
		}
		safe, err := a.prSafe(ctx, repoURL, r.PRNumbers, map[int][]string{})
		if err != nil {
			return false, err
		}
		if !safe {
			return false, nil
		}
		return true, a.GH.RerunWorkflowRun(ctx, repoURL, runID)
	}
	return false, fmt.Errorf("run %d is not awaiting approval", runID)
}
