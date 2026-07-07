package scaffold

import (
	"context"
	"sort"
	"strings"
)

// BranchProtector adds required status checks to a branch's protection rule. It is
// the narrow surface the scaffold helper needs — implemented later by
// *github.Client against the GitHub branch-protection API. Keeping the dependency
// this thin mirrors FileWriter: the scaffold package stays pure and unit-testable,
// and the kernel keeps no GitHub knowledge.
//
// The implementation must be ADDITIVE (union with any existing required checks),
// never replacing the branch's protection wholesale — GitHub's
// "add required status check contexts" endpoint has exactly these semantics.
type BranchProtector interface {
	AddRequiredStatusChecks(ctx context.Context, repoURL, branch string, contexts []string) error
}

// EnsureRequiredChecks marks the given check contexts as required on branch, so the
// Conductor's auto-merge waits for them without any change to native.go — a required
// check is just an extra gate the merge honors. It is additive and idempotent:
// blank and duplicate contexts are dropped, the order is stabilized, and it no-ops
// (making NO call) when nothing is left to require. It never removes an existing
// required check.
//
// NOTE (S1): this is plumbing only. Nothing calls it yet — new checks ship as
// continue-on-error (visible, not required) and are promoted to required in a later
// sprint, per-project, only after they have gone green on a real PR.
func EnsureRequiredChecks(ctx context.Context, bp BranchProtector, repoURL, branch string, contexts []string) error {
	want := normalizeRequiredChecks(contexts)
	if len(want) == 0 {
		return nil
	}
	return bp.AddRequiredStatusChecks(ctx, repoURL, branch, want)
}

// normalizeRequiredChecks trims, drops empties, dedupes, and sorts the contexts so
// the required-check set is stable regardless of input order or accidental repeats.
func normalizeRequiredChecks(contexts []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range contexts {
		c = strings.TrimSpace(c)
		if c == "" || seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}
