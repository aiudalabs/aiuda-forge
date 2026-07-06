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
	// Expect 18 steps: 8 design + 8 human_gate + 1 ticket_publish (handoff) + 1 pr (docs_pr).
	// v2 (design.yaml 2.0.0) added two phases — constitution (the durable anchor doc)
	// and data_model — each with its human_gate, on top of the original 6.
	if len(wf.Steps) != 18 {
		t.Errorf("step count: got %d, want 18", len(wf.Steps))
		for _, s := range wf.Steps {
			t.Logf("  %s (%s)", s.ID, s.Type)
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
