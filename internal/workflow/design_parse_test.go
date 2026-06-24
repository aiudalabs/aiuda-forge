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
	// Expect 11 steps: 5 design + 5 human_gate + 1 ticket_publish (handoff)
	if len(wf.Steps) != 11 {
		t.Errorf("step count: got %d, want 11", len(wf.Steps))
		for _, s := range wf.Steps {
			t.Logf("  %s (%s)", s.ID, s.Type)
		}
	}
	last := wf.Steps[len(wf.Steps)-1]
	if last.ID != "handoff" || last.Type != "ticket_publish" {
		t.Errorf("last step: got id=%s type=%s, want id=handoff type=ticket_publish", last.ID, last.Type)
	}
}
