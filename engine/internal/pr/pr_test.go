package pr

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"vibeforge-kernel/internal/workflow"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

// TestLocalPRCommits: in a real git workdir the pr step creates the branch and
// commits the agent's changes (regression test for the `git git` arg bug).
func TestLocalPRCommits(t *testing.T) {
	root := t.TempDir()
	workdir := filepath.Join(root, "run_test123")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, workdir, "init", "-q")
	git(t, workdir, "config", "user.email", "t@t")
	git(t, workdir, "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(workdir, "base.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, workdir, "add", "-A")
	git(t, workdir, "commit", "-q", "-m", "base")

	// Agent produced a change.
	if err := os.WriteFile(filepath.Join(workdir, "calc.py"), []byte("def add(a,b): return a+b\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := NewRunner()
	res, err := r.Run(context.Background(), workflow.Step{ID: "pr", Type: "pr"}, map[string]any{"ticket": "implement add"}, workdir)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Success {
		t.Fatalf("pr should succeed, detail=%q", res.Detail)
	}
	if committed, _ := res.Output["committed"].(bool); !committed {
		t.Fatalf("expected committed=true, output=%v", res.Output)
	}

	// We are on the vibeforge branch and calc.py is committed.
	branch := runGit(t, workdir, "rev-parse", "--abbrev-ref", "HEAD")
	if branch != "vibeforge/run_test123" {
		t.Fatalf("expected vibeforge branch, got %q", branch)
	}
	files := runGit(t, workdir, "show", "--name-only", "--format=", "HEAD")
	if !contains(files, "calc.py") {
		t.Fatalf("expected calc.py in the commit, got %q", files)
	}
}

// TestLocalPRNoRepo: with no git repo the pr step records a synthetic PR (used by
// stub flows) instead of erroring.
func TestLocalPRNoRepo(t *testing.T) {
	workdir := filepath.Join(t.TempDir(), "run_norepo")
	_ = os.MkdirAll(workdir, 0o755)
	r := NewRunner()
	res, err := r.Run(context.Background(), workflow.Step{ID: "pr", Type: "pr"}, nil, workdir)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Success {
		t.Fatalf("synthetic pr should succeed, detail=%q", res.Detail)
	}
	if committed, _ := res.Output["committed"].(bool); committed {
		t.Fatalf("expected committed=false for no-repo synthetic PR")
	}
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return string(trimSpace(out))
}

func trimSpace(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == ' ' || b[len(b)-1] == '\t' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
