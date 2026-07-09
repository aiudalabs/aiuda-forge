package workflow

import (
	"path/filepath"
	"testing"
)

// TestSprintReviewYAMLParses validates the sprint-review ceremony workflow:
// release → report (design/demo-reporter) → corrections (design/scrum-master) →
// review_gate (human, loops back to corrections on reject/answer) →
// publish_corrections (ticket_publish) → close (review_close). It must parse with the
// kernel loader and reference real step ids.
func TestSprintReviewYAMLParses(t *testing.T) {
	path := filepath.Join("..", "..", "registry", "workflows", "sprint-review.yaml")
	wf, err := ParseFile(path)
	if err != nil {
		t.Fatalf("sprint-review.yaml failed to parse: %v", err)
	}
	if wf.ID != "sprint-review" {
		t.Errorf("id: got %q, want sprint-review", wf.ID)
	}
	if len(wf.Steps) != 6 {
		t.Fatalf("step count: got %d, want 6 (release, report, corrections, review_gate, publish_corrections, close)", len(wf.Steps))
	}
	want := []struct{ id, typ string }{
		{"release", "release"},
		{"report", "design"},
		{"corrections", "design"},
		{"review_gate", "human_gate"},
		{"publish_corrections", "ticket_publish"},
		{"close", "review_close"},
	}
	for i, w := range want {
		if wf.Steps[i].ID != w.id || wf.Steps[i].Type != w.typ {
			t.Errorf("step %d: got id=%s type=%s, want %s/%s", i, wf.Steps[i].ID, wf.Steps[i].Type, w.id, w.typ)
		}
	}
	if wf.Steps[1].Agent != "demo-reporter" {
		t.Errorf("report agent: got %q, want demo-reporter", wf.Steps[1].Agent)
	}
	if wf.Steps[2].Agent != "scrum-master" {
		t.Errorf("corrections agent: got %q, want scrum-master", wf.Steps[2].Agent)
	}
	// The gate's on_fail.goto=corrections is what makes reject/answer regenerate the
	// corrections and re-park the gate (the loop body sits before the gate).
	gate := wf.Steps[3]
	if gate.OnFail == nil || gate.OnFail.Goto != "corrections" {
		t.Errorf("review_gate must loop back to corrections on reject/answer, got %+v", gate.OnFail)
	}
}
