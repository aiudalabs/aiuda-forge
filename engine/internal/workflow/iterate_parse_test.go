package workflow

import (
	"path/filepath"
	"testing"
)

// TestIterateYAMLParses validates the iteration workflow (1.1b): plan a delta
// backlog → human gate (loops back on reject) → publish (append). It must parse
// with the same loader the kernel uses and reference real step targets.
func TestIterateYAMLParses(t *testing.T) {
	path := filepath.Join("..", "..", "registry", "workflows", "iterate.yaml")
	wf, err := ParseFile(path)
	if err != nil {
		t.Fatalf("iterate.yaml failed to parse: %v", err)
	}
	if wf.ID != "iterate" {
		t.Errorf("id: got %q, want iterate", wf.ID)
	}
	if len(wf.Steps) != 3 {
		t.Fatalf("step count: got %d, want 3 (plan, plan_gate, handoff)", len(wf.Steps))
	}
	if wf.Steps[0].ID != "plan" || wf.Steps[0].Agent != "iteration-planner" {
		t.Errorf("step 0: got id=%s agent=%s, want plan/iteration-planner", wf.Steps[0].ID, wf.Steps[0].Agent)
	}
	gate := wf.Steps[1]
	if gate.Type != "human_gate" || gate.OnFail == nil || gate.OnFail.Goto != "plan" {
		t.Errorf("plan_gate must loop back to plan on reject, got %+v", gate.OnFail)
	}
	last := wf.Steps[2]
	if last.ID != "handoff" || last.Type != "ticket_publish" {
		t.Errorf("last step: got id=%s type=%s, want handoff/ticket_publish", last.ID, last.Type)
	}
}
