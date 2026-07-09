package workflow

import (
	"path/filepath"
	"testing"
)

func TestDesignYAMLParses(t *testing.T) {
	path := filepath.Join("..", "..", "registry", "workflows", "design.yaml")
	wf, err := ParseFile(path)
	if err != nil {
		t.Fatalf("design.yaml failed to parse: %v", err)
	}
	if wf.ID != "design" {
		t.Errorf("id: got %q, want design", wf.ID)
	}
	// Expect 20 steps: 8 design + 8 human_gate + 2 validate (prd, backlog) + 1
	// ticket_publish (handoff) + 1 pr (docs_pr). v2 (design.yaml 2.0.0) added two phases
	// — constitution (the durable anchor doc) and data_model — each with its human_gate,
	// on top of the original 6; the two `validate` steps lint the PRD and backlog before
	// their human gate.
	if len(wf.Steps) != 20 {
		t.Errorf("step count: got %d, want 20", len(wf.Steps))
		for _, s := range wf.Steps {
			t.Logf("  %s (%s)", s.ID, s.Type)
		}
	}
	// The validate steps must sit BETWEEN their phase and that phase's gate, and loop
	// back to the phase on failure (so a malformed doc bounces the phase, not the run).
	for _, tc := range []struct{ vID, phase, gate string }{
		{"validate_prd", "prd", "prd_gate"},
		{"validate_backlog", "backlog", "backlog_gate"},
	} {
		v, ok := wf.StepByID(tc.vID)
		if !ok || v.Type != "validate" {
			t.Errorf("%s missing or not type=validate (got %+v)", tc.vID, v)
			continue
		}
		if v.OnFail == nil || v.OnFail.Goto != tc.phase {
			t.Errorf("%s must loop back to %s on failure, got %+v", tc.vID, tc.phase, v.OnFail)
		}
	}
	// Invariant (reservas-belleza fix 2026-07-06): docs_pr (merge docs to main) must run
	// BEFORE handoff (publish backlog), so a story is NEVER dispatchable before its docs are
	// on main. Assert the ordering, not just the terminal step.
	idxOf := func(id string) int {
		for i, s := range wf.Steps {
			if s.ID == id {
				return i
			}
		}
		return -1
	}
	docsPR, handoff := idxOf("docs_pr"), idxOf("handoff")
	if docsPR < 0 || handoff < 0 {
		t.Fatalf("missing steps: docs_pr=%d handoff=%d", docsPR, handoff)
	}
	if docsPR >= handoff {
		t.Errorf("order: docs_pr (%d) must come BEFORE handoff (%d) — docs must reach main before the backlog is published", docsPR, handoff)
	}
	last := wf.Steps[len(wf.Steps)-1]
	if last.ID != "handoff" || last.Type != "ticket_publish" {
		t.Errorf("last step: got id=%s type=%s, want id=handoff type=ticket_publish", last.ID, last.Type)
	}
}
