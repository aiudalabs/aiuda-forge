package tickets_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"forge/internal/tickets"
	"forge/internal/workflow"
)

// fixture is the canonical backlog.yaml the scrum-master persona produces.
const fixture = `
epic:
  id: E1
  title: "Foundation"
  description: "Core infrastructure stories"
stories:
  - id: S1-01
    title: "Auth service"
    body: "Implement the authentication service with JWT."
    acceptance: "Users can log in and receive a token."
    owner: dev
    sprint_id: SP1
    deps: []
  - id: S1-02
    title: "User CRUD"
    body: "REST endpoints for user create/read/update/delete."
    acceptance: "All four endpoints return correct status codes."
    owner: dev
    sprint_id: SP1
    deps: [S1-01]
`

func runPublish(t *testing.T, st *tickets.Store, workdir string, inputs map[string]any) workflow.StepResult {
	t.Helper()
	r := &tickets.PublishRunner{Store: st}
	res, err := r.Run(context.Background(), workflow.Step{ID: "handoff", Type: "ticket_publish"}, inputs, workdir)
	if err != nil {
		t.Fatalf("Run returned unexpected error: %v", err)
	}
	return res
}

func writeBacklog(t *testing.T, workdir, relpath, content string) {
	t.Helper()
	full := filepath.Join(workdir, relpath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}

// TestPublishRunnerHappyPath: fixture → epic + 2 stories with correct deps.
func TestPublishRunnerHappyPath(t *testing.T) {
	st := openTemp(t)
	workdir := t.TempDir()
	writeBacklog(t, workdir, "docs/backlog.yaml", fixture)

	res := runPublish(t, st, workdir, nil)
	if !res.Success {
		t.Fatalf("expected success, got detail: %s", res.Detail)
	}
	if res.Output["epic"] != "E1" {
		t.Errorf("output epic: got %v, want E1", res.Output["epic"])
	}
	if res.Output["created"] != 2 {
		t.Errorf("output created: got %v, want 2", res.Output["created"])
	}
	if res.Output["skipped"] != 0 {
		t.Errorf("output skipped: got %v, want 0", res.Output["skipped"])
	}

	// Verify the epic was stored.
	epic, err := st.GetEpic("E1")
	if err != nil {
		t.Fatalf("GetEpic E1: %v", err)
	}
	if epic.Title != "Foundation" {
		t.Errorf("epic title: got %q, want Foundation", epic.Title)
	}

	// Verify stories and deps.
	s1, err := st.GetStory("S1-01")
	if err != nil {
		t.Fatalf("GetStory S1-01: %v", err)
	}
	if s1.EpicID != "E1" {
		t.Errorf("S1-01 epic_id: got %q, want E1", s1.EpicID)
	}
	if s1.Status != tickets.StatusBacklog {
		t.Errorf("S1-01 status: got %s, want backlog", s1.Status)
	}
	if len(s1.Deps) != 0 {
		t.Errorf("S1-01 deps: got %v, want empty", s1.Deps)
	}

	s2, err := st.GetStory("S1-02")
	if err != nil {
		t.Fatalf("GetStory S1-02: %v", err)
	}
	if len(s2.Deps) != 1 || s2.Deps[0] != "S1-01" {
		t.Errorf("S1-02 deps: got %v, want [S1-01]", s2.Deps)
	}
}

// TestPublishRunnerIdempotent: running twice with same fixture must not
// duplicate rows and must not error.
func TestPublishRunnerIdempotent(t *testing.T) {
	st := openTemp(t)
	workdir := t.TempDir()
	writeBacklog(t, workdir, "docs/backlog.yaml", fixture)

	// First run.
	res1 := runPublish(t, st, workdir, nil)
	if !res1.Success {
		t.Fatalf("first run failed: %s", res1.Detail)
	}

	// Second run — must succeed (skipped=2, created=0).
	res2 := runPublish(t, st, workdir, nil)
	if !res2.Success {
		t.Fatalf("second run failed: %s", res2.Detail)
	}
	if res2.Output["created"] != 0 {
		t.Errorf("second run created: got %v, want 0", res2.Output["created"])
	}
	if res2.Output["skipped"] != 2 {
		t.Errorf("second run skipped: got %v, want 2", res2.Output["skipped"])
	}

	// Confirm no duplicates: still 2 stories.
	stories, err := st.ListStories()
	if err != nil {
		t.Fatalf("ListStories: %v", err)
	}
	if len(stories) != 2 {
		t.Errorf("story count after 2 runs: got %d, want 2", len(stories))
	}
}

// TestPublishRunnerMissingFile: absent backlog → failed StepResult, no panic.
func TestPublishRunnerMissingFile(t *testing.T) {
	st := openTemp(t)
	workdir := t.TempDir() // no backlog.yaml written

	res := runPublish(t, st, workdir, nil)
	if res.Success {
		t.Fatal("expected failure for missing file, got success")
	}
	if res.Detail == "" {
		t.Error("expected non-empty Detail for missing file error")
	}
}

// TestPublishRunnerGarbageYAML: unparseable content → failed StepResult, no panic.
func TestPublishRunnerGarbageYAML(t *testing.T) {
	st := openTemp(t)
	workdir := t.TempDir()
	// Use YAML that is genuinely malformed (unclosed flow sequence).
	writeBacklog(t, workdir, "docs/backlog.yaml", "{unclosed: [bracket")

	res := runPublish(t, st, workdir, nil)
	if res.Success {
		t.Fatal("expected failure for garbage YAML, got success")
	}
	if res.Detail == "" {
		t.Error("expected non-empty Detail for parse error")
	}
}

// TestPublishRunnerCustomPath: inputs["backlog"] overrides the default path.
func TestPublishRunnerCustomPath(t *testing.T) {
	st := openTemp(t)
	workdir := t.TempDir()
	writeBacklog(t, workdir, "custom/path.yaml", fixture)

	res := runPublish(t, st, workdir, map[string]any{"backlog": "custom/path.yaml"})
	if !res.Success {
		t.Fatalf("custom path failed: %s", res.Detail)
	}
	if res.Output["created"] != 2 {
		t.Errorf("created: got %v, want 2", res.Output["created"])
	}
}
