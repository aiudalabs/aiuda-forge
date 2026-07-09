package method

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"forge/internal/workflow"
)

// fakeStamper records SetSprintRetroed calls so a test can assert retro_at stamping.
type fakeStamper struct {
	stamped   []string
	failStamp bool
}

func (f *fakeStamper) SetSprintRetroed(sprintID, _ string) error {
	if f.failStamp {
		return os.ErrPermission
	}
	f.stamped = append(f.stamped, sprintID)
	return nil
}

// run writes a RETRO doc into a fresh workdir and runs registry_apply against a fresh
// registry dir. Returns the result, the stamper, and the registry dir.
func run(t *testing.T, doc string) (workflow.StepResult, *fakeStamper, string) {
	t.Helper()
	work := t.TempDir()
	reg := t.TempDir()
	if err := os.WriteFile(filepath.Join(work, "RETRO.md"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	st := &fakeStamper{}
	r := &ApplyRunner{Tickets: st, RegistryDir: reg}
	res, err := r.Run(context.Background(), workflow.Step{}, map[string]any{
		"sprint_id": "SP1", "project_id": "p1", "retro": "RETRO.md",
	}, work)
	if err != nil {
		t.Fatalf("runner returned a hard error (should be a failed StepResult): %v", err)
	}
	return res, st, reg
}

// proposalsDoc wraps a proposals body in a fenced yaml block inside a markdown doc.
func proposalsDoc(body string) string {
	return "# Retro SP1\n\n## What didn't\n…\n\n## Proposals\n```yaml\n" + body + "\n```\n"
}

// TestApplyValidProposal writes a valid skill proposal, stamps retro_at, and emits an
// audit event.
func TestApplyValidProposal(t *testing.T) {
	doc := proposalsDoc(`proposals:
  - kind: skill
    id: ui-screens-template
    rationale: "ux stalled 3x"
    evidence: "SP1 runs[].steps ux stalled"
    content: |
      # Skill — ui-screens-template
      Shard the screens; cap output per pass.`)
	res, st, reg := run(t, doc)
	if !res.Success {
		t.Fatalf("apply should succeed: %s", res.Detail)
	}
	if res.Output["applied"] != 1 {
		t.Fatalf("applied = %v, want 1", res.Output["applied"])
	}
	got, err := os.ReadFile(filepath.Join(reg, "skills", "ui-screens-template.md"))
	if err != nil || !strings.Contains(string(got), "Shard the screens") {
		t.Fatalf("skill file not written correctly: err=%v content=%q", err, got)
	}
	if len(st.stamped) != 1 || st.stamped[0] != "SP1" {
		t.Fatalf("retro_at not stamped: %v", st.stamped)
	}
	if len(res.Events) != 1 || res.Events[0].Type != "step.registry_apply" {
		t.Fatalf("want one step.registry_apply audit event, got %+v", res.Events)
	}
}

// TestApplyValidWorkflowProposal validates a workflow proposal through the kernel parser.
func TestApplyValidWorkflowProposal(t *testing.T) {
	doc := proposalsDoc(`proposals:
  - kind: workflow
    id: demo2
    rationale: r
    evidence: e
    content: |
      id: demo2
      version: 1.0.0
      steps:
        - id: only
          type: echo`)
	res, _, reg := run(t, doc)
	if !res.Success {
		t.Fatalf("valid workflow proposal should apply: %s", res.Detail)
	}
	if _, err := os.Stat(filepath.Join(reg, "workflows", "demo2.yaml")); err != nil {
		t.Fatalf("workflow file not written: %v", err)
	}
}

// TestApplyAbortsAtomicallyOnUnparseable: a valid proposal followed by an unparseable
// one aborts the WHOLE apply naming the offender — nothing written, retro_at not stamped.
func TestApplyAbortsAtomicallyOnUnparseable(t *testing.T) {
	doc := proposalsDoc(`proposals:
  - kind: skill
    id: good-skill
    content: "# fine"
  - kind: workflow
    id: broken
    content: |
      this: is: not: a: valid: workflow`)
	res, st, reg := run(t, doc)
	if res.Success {
		t.Fatal("apply must fail when a proposal doesn't parse")
	}
	if !strings.Contains(res.Detail, "broken") {
		t.Errorf("failure must name the offending proposal, got: %s", res.Detail)
	}
	if _, err := os.Stat(filepath.Join(reg, "skills", "good-skill.md")); !os.IsNotExist(err) {
		t.Error("the valid proposal must NOT have been written (atomic abort)")
	}
	if len(st.stamped) != 0 {
		t.Error("retro_at must not be stamped on a failed apply")
	}
}

// TestApplyRejectsPathEscape: an id with a path separator / traversal is rejected before
// any write (guardrail: only registry/{agents,skills,workflows}/, no escapes).
func TestApplyRejectsPathEscape(t *testing.T) {
	for _, bad := range []string{"../evil", "a/b", "foo.bar"} {
		doc := proposalsDoc("proposals:\n  - kind: skill\n    id: \"" + bad + "\"\n    content: \"# x\"")
		res, st, _ := run(t, doc)
		if res.Success {
			t.Errorf("id %q must be rejected", bad)
		}
		if len(st.stamped) != 0 {
			t.Errorf("id %q: nothing should be stamped", bad)
		}
	}
}

// TestApplyRejectsProtectedMethod: the retro's own method (retro-analyst / retro-method /
// retro workflow) is not self-editable.
func TestApplyRejectsProtectedMethod(t *testing.T) {
	cases := []struct{ kind, id string }{
		{"agent", "retro-analyst"},
		{"skill", "retro-method"},
		{"workflow", "retro"},
	}
	for _, c := range cases {
		doc := proposalsDoc("proposals:\n  - kind: " + c.kind + "\n    id: " + c.id + "\n    content: |\n      id: retro\n      version: 1.0.0\n      steps:\n        - id: s\n          type: echo")
		res, _, _ := run(t, doc)
		if res.Success {
			t.Errorf("%s %q must be rejected (self-edit of the retro method)", c.kind, c.id)
		}
		if !strings.Contains(res.Detail, "not self-editable") {
			t.Errorf("%s %q: expected a self-editable rejection, got: %s", c.kind, c.id, res.Detail)
		}
	}
}

// TestApplyEmptyProposalsStampsRetro: an empty proposals block (or no block) stamps
// retro_at and writes nothing — a retro that found no method problem still completes.
func TestApplyEmptyProposalsStampsRetro(t *testing.T) {
	for _, doc := range []string{proposalsDoc("proposals: []"), "# Retro SP1\n\nThe method held up. No changes.\n"} {
		res, st, reg := run(t, doc)
		if !res.Success {
			t.Fatalf("empty retro should succeed: %s", res.Detail)
		}
		if res.Output["applied"] != 0 {
			t.Fatalf("applied = %v, want 0", res.Output["applied"])
		}
		if len(st.stamped) != 1 {
			t.Fatalf("retro_at should be stamped exactly once, got %v", st.stamped)
		}
		// Nothing written under the registry.
		entries, _ := os.ReadDir(reg)
		if len(entries) != 0 {
			t.Fatalf("empty retro must write nothing, found %v", entries)
		}
	}
}

// TestApplyMissingSprintID fails as a StepResult.
func TestApplyMissingSprintID(t *testing.T) {
	r := &ApplyRunner{Tickets: &fakeStamper{}, RegistryDir: t.TempDir()}
	res, _ := r.Run(context.Background(), workflow.Step{}, map[string]any{}, t.TempDir())
	if res.Success {
		t.Fatal("missing sprint_id should fail")
	}
}
