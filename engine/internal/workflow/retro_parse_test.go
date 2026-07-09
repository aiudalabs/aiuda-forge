package workflow

import (
	"path/filepath"
	"testing"
)

// TestRetroYAMLParses validates the retrospective ceremony workflow:
// analyze (design/retro-analyst) → retro_gate (human, loops back to analyze on
// reject/answer) → apply (registry_apply). It must parse and reference real step ids.
func TestRetroYAMLParses(t *testing.T) {
	path := filepath.Join("..", "..", "registry", "workflows", "retro.yaml")
	wf, err := ParseFile(path)
	if err != nil {
		t.Fatalf("retro.yaml failed to parse: %v", err)
	}
	if wf.ID != "retro" {
		t.Errorf("id: got %q, want retro", wf.ID)
	}
	if len(wf.Steps) != 3 {
		t.Fatalf("step count: got %d, want 3 (analyze, retro_gate, apply)", len(wf.Steps))
	}
	if wf.Steps[0].ID != "analyze" || wf.Steps[0].Type != "design" || wf.Steps[0].Agent != "retro-analyst" {
		t.Errorf("step 0: got id=%s type=%s agent=%s, want analyze/design/retro-analyst",
			wf.Steps[0].ID, wf.Steps[0].Type, wf.Steps[0].Agent)
	}
	gate := wf.Steps[1]
	if gate.Type != "human_gate" || gate.OnFail == nil || gate.OnFail.Goto != "analyze" {
		t.Errorf("retro_gate must loop back to analyze on reject/answer, got %+v", gate.OnFail)
	}
	last := wf.Steps[2]
	if last.ID != "apply" || last.Type != "registry_apply" {
		t.Errorf("last step: got id=%s type=%s, want apply/registry_apply", last.ID, last.Type)
	}
}
