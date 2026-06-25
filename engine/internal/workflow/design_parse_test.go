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
	// Expect 14 steps: 6 design + 6 human_gate + 1 ticket_publish (handoff) + 1 pr (docs_pr)
	// The mockups phase (design + human_gate) was added between ui_gate and backlog.
	if len(wf.Steps) != 14 {
		t.Errorf("step count: got %d, want 14", len(wf.Steps))
		for _, s := range wf.Steps {
			t.Logf("  %s (%s)", s.ID, s.Type)
		}
	}
	last := wf.Steps[len(wf.Steps)-1]
	if last.ID != "docs_pr" || last.Type != "pr" {
		t.Errorf("last step: got id=%s type=%s, want id=docs_pr type=pr", last.ID, last.Type)
	}
}
