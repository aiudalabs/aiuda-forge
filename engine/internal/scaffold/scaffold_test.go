package scaffold

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const realTemplates = "../../registry/templates/github-native"

func pathsOf(files []File) map[string]string {
	m := make(map[string]string, len(files))
	for _, f := range files {
		m[f.Path] = f.Content
	}
	return m
}

func TestRenderRealPythonStack(t *testing.T) {
	vars := Vars{"project_name": "Acme", "language": "es"}
	files, missing, err := Render(realTemplates, "python-fastapi-react", vars)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	byPath := pathsOf(files)

	// _common file present.
	if _, ok := byPath["AGENTS.md"]; !ok {
		t.Errorf("expected _common AGENTS.md in result; got paths %v", keys(byPath))
	}
	// stack-specific persona present, .tmpl suffix stripped.
	persona, ok := byPath[".github/agents/python-dev.agent.md"]
	if !ok {
		t.Fatalf("expected .github/agents/python-dev.agent.md in result; got paths %v", keys(byPath))
	}

	// passed variable substituted, and no {{project_name}} remains anywhere.
	if !strings.Contains(persona, "Acme") {
		t.Errorf("expected {{project_name}} -> Acme in persona, not found")
	}
	for _, f := range files {
		if strings.Contains(f.Content, "{{project_name}}") {
			t.Errorf("%s still contains unsubstituted {{project_name}}", f.Path)
		}
	}

	// missing reports variables that were NOT passed, and not the ones that were.
	if !contains(missing, "stack") {
		t.Errorf("expected 'stack' in missing (not passed); got %v", missing)
	}
	if contains(missing, "project_name") {
		t.Errorf("did not expect 'project_name' in missing (it was passed); got %v", missing)
	}
	if contains(missing, "language") {
		t.Errorf("did not expect 'language' in missing (it was passed); got %v", missing)
	}
	// missing is sorted + deduped.
	for i := 1; i < len(missing); i++ {
		if missing[i-1] >= missing[i] {
			t.Errorf("missing not sorted/deduped: %v", missing)
			break
		}
	}
}

func TestRenderUnknownStack(t *testing.T) {
	_, _, err := Render(realTemplates, "cobol-mainframe", nil)
	if err == nil {
		t.Fatal("expected error for unknown stack")
	}
	msg := err.Error()
	if !strings.Contains(msg, "cobol-mainframe") {
		t.Errorf("error should name the bad stack: %q", msg)
	}
	// error lists available stacks.
	if !strings.Contains(msg, "python-fastapi-react") {
		t.Errorf("error should list available stacks: %q", msg)
	}
	// _common must never be offered as a stack.
	if strings.Contains(msg, commonDir) {
		t.Errorf("error should not list %q as an available stack: %q", commonDir, msg)
	}
}

func TestRenderStackOverridesCommonAndKeepsUnpassedVars(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// same destination path present in both _common and the stack: stack wins.
	write("_common/shared.md.tmpl", "from common {{project_name}}")
	write("mystack/shared.md.tmpl", "from stack {{project_name}}")
	write("mystack/only.md.tmpl", "hi {{unpassed}} and {{project_name}}")
	// non-.tmpl and README.md are ignored.
	write("mystack/ignored.txt", "not a template {{project_name}}")
	write("mystack/README.md", "docs {{project_name}}")
	write("_common/README.md", "docs {{project_name}}")

	files, missing, err := Render(dir, "mystack", Vars{"project_name": "Zed"})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	byPath := pathsOf(files)

	if got := byPath["shared.md"]; got != "from stack Zed" {
		t.Errorf("stack should override _common: got %q", got)
	}
	// unpassed variable left intact in content...
	only := byPath["only.md"]
	if !strings.Contains(only, "{{unpassed}}") {
		t.Errorf("unpassed variable should be left intact: %q", only)
	}
	if strings.Contains(only, "{{project_name}}") {
		t.Errorf("passed variable should be substituted: %q", only)
	}
	// ...and reported in missing.
	if !contains(missing, "unpassed") {
		t.Errorf("expected 'unpassed' in missing; got %v", missing)
	}
	if contains(missing, "project_name") {
		t.Errorf("did not expect 'project_name' in missing; got %v", missing)
	}
	// non-.tmpl and README.md excluded.
	for _, bad := range []string{"ignored.txt", "README.md"} {
		if _, ok := byPath[bad]; ok {
			t.Errorf("%s should have been ignored", bad)
		}
	}
}

// fakeWriter records calls and returns a preset changed/unchanged verdict per path.
type fakeWriter struct {
	changed map[string]bool // path -> whether WriteFile reports a change
	calls   []call
}

type call struct {
	repoURL, branch, path, content, message string
}

func (f *fakeWriter) WriteFile(_ context.Context, repoURL, branch, path, content, message string) (bool, error) {
	f.calls = append(f.calls, call{repoURL, branch, path, content, message})
	return f.changed[path], nil
}

func TestApplyCountsAndCommitMessage(t *testing.T) {
	files := []File{
		{Path: "AGENTS.md", Content: "a"},
		{Path: ".github/agents/x.agent.md", Content: "b"},
		{Path: "CLAUDE.md", Content: "c"},
	}
	fw := &fakeWriter{changed: map[string]bool{
		"AGENTS.md":                 true,
		".github/agents/x.agent.md": false, // already identical -> skipped
		"CLAUDE.md":                 true,
	}}

	written, skipped, err := Apply(context.Background(), fw, "https://github.com/o/r", "main", files, "chore(scaffold)")
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if written != 2 || skipped != 1 {
		t.Fatalf("written=%d skipped=%d, want 2/1", written, skipped)
	}
	if len(fw.calls) != 3 {
		t.Fatalf("expected 3 WriteFile calls, got %d", len(fw.calls))
	}
	first := fw.calls[0]
	if first.repoURL != "https://github.com/o/r" || first.branch != "main" {
		t.Errorf("repoURL/branch not threaded: %+v", first)
	}
	if first.message != "chore(scaffold): AGENTS.md" {
		t.Errorf("commit message = %q, want %q", first.message, "chore(scaffold): AGENTS.md")
	}
}

type errWriter struct{ calls int }

func (e *errWriter) WriteFile(_ context.Context, _, _, _, _, _ string) (bool, error) {
	e.calls++
	return false, context.DeadlineExceeded
}

func TestApplyFailsFast(t *testing.T) {
	ew := &errWriter{}
	files := []File{{Path: "a"}, {Path: "b"}, {Path: "c"}}
	_, _, err := Apply(context.Background(), ew, "r", "main", files, "p")
	if err == nil {
		t.Fatal("expected error")
	}
	if ew.calls != 1 {
		t.Errorf("should fail fast on first error, made %d calls", ew.calls)
	}
	if !strings.Contains(err.Error(), "a") {
		t.Errorf("error should name the failing path: %v", err)
	}
}

func keys(m map[string]string) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}
