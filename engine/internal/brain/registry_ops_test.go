package brain

import (
	"os"
	"path/filepath"
	"testing"
)

func tmpRegistry(t *testing.T) EngineOps {
	t.Helper()
	dir := t.TempDir()
	for _, sub := range []string{"workflows", "agents", "skills"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return EngineOps{RegistryDir: dir} // nil Engine: workflow writes skip InvalidateWorkflow
}

func TestRegistryWriteReadListDelete(t *testing.T) {
	o := tmpRegistry(t)
	if err := o.WriteRegistry("skill", "my-skill", "# Skill\nhello"); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := o.ReadRegistry("skill", "my-skill")
	if err != nil || got != "# Skill\nhello" {
		t.Fatalf("read: %q err=%v", got, err)
	}
	ids, err := o.ListRegistry("skill")
	if err != nil || len(ids) != 1 || ids[0] != "my-skill" {
		t.Fatalf("list: %v err=%v", ids, err)
	}
	if err := o.DeleteRegistry("skill", "my-skill"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if ids, _ := o.ListRegistry("skill"); len(ids) != 0 {
		t.Fatalf("expected empty after delete, got %v", ids)
	}
}

func TestRegistryWorkflowValidation(t *testing.T) {
	o := tmpRegistry(t)
	// Valid workflow parses and writes.
	valid := "id: t\nversion: 1.0.0\nsteps:\n  - id: s1\n    type: echo\n"
	if err := o.WriteRegistry("workflow", "good", valid); err != nil {
		t.Fatalf("valid workflow rejected: %v", err)
	}
	// Malformed YAML is rejected by the kernel's own parser — never lands on disk.
	if err := o.WriteRegistry("workflow", "bad", "steps: [unclosed\n"); err == nil {
		t.Fatal("expected malformed workflow to be rejected")
	}
	if _, err := o.ReadRegistry("workflow", "bad"); err == nil {
		t.Fatal("rejected workflow must not have been written")
	}
}

func TestRegistryRejectsBadKindAndTraversal(t *testing.T) {
	o := tmpRegistry(t)
	if err := o.WriteRegistry("nope", "x", "y"); err == nil {
		t.Fatal("expected unknown kind rejected")
	}
	if _, err := o.regPath("skill", "../../etc/passwd"); err == nil {
		t.Fatal("expected traversal id rejected")
	}
	if _, err := o.regPath("skill", "with/slash"); err == nil {
		t.Fatal("expected id with slash rejected")
	}
}
