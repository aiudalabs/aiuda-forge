package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"forge/internal/agent"
	"forge/internal/store"
)

// TestValidateRunnerRegistered proves app.Build wires the `validate` step runner: a run
// reaches a terminal state via the runner's OWN validation ("missing schema") rather
// than the executor's "no runner for step type" — the latter would mean the step type
// was never registered. It also confirms `validate` is wired unconditionally (no
// TicketsDB here), since it needs neither a store nor an LLM.
func TestValidateRunnerRegistered(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	dir := t.TempDir()
	wfDir := filepath.Join(dir, "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	wf := "id: v\nversion: 1.0.0\nsteps:\n  - id: v\n    type: validate\n"
	if err := os.WriteFile(filepath.Join(wfDir, "v.yaml"), []byte(wf), 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := Build(Config{
		DBPath:       filepath.Join(dir, "control.db"),
		RegistryRoot: dir,
		WorkdirRoot:  filepath.Join(dir, "runs"),
		EngineMode:   "echo",
		Backend:      agent.FakeBackend{Reply: "ok"},
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	defer a.Close()

	runID, err := a.Engine.StartRun("v", map[string]any{}) // no schema → runner fails cleanly
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	status, err := a.Engine.RunToCompletion(context.Background(), runID)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if status != store.StatusFailed {
		t.Fatalf("run status = %s, want failed (no schema)", status)
	}
	tasks, _ := a.Store.TasksForRun(runID)
	if len(tasks) == 0 {
		t.Fatal("no tasks recorded")
	}
	last := tasks[len(tasks)-1]
	if strings.Contains(last.Error, "no runner for step type") {
		t.Fatalf("validate runner not registered: %q", last.Error)
	}
	if !strings.Contains(last.Error, "missing schema") {
		t.Fatalf("expected the validate runner's own failure, got %q", last.Error)
	}
}
