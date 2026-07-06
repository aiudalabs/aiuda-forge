package workflow

import (
	"context"
	"testing"

	"forge/internal/store"
)

// drain runs ready tasks until the queue is empty.
func drain(t *testing.T, e *Engine) {
	t.Helper()
	for i := 0; i < 20; i++ {
		more, err := e.ExecuteOne(context.Background(), "w")
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if !more {
			return
		}
	}
	t.Fatal("drain did not converge")
}

func stepCounts(e *Engine, runID string) map[string]int {
	c := map[string]int{}
	tasks, _ := e.Store.TasksForRun(runID)
	for _, tk := range tasks {
		c[tk.StepID]++
	}
	return c
}

// TestRerunStepDoesNotCascade: re-running one step of a finished run re-runs ONLY
// that step (in place) and does NOT cascade to the downstream steps — which for a
// real design run would re-publish the backlog/PR to GitHub.
func TestRerunStepDoesNotCascade(t *testing.T) {
	wf, _ := Parse([]byte(`
id: pipe
version: 1.0.0
steps:
  - id: a
    type: echo
  - id: b
    type: echo
  - id: c
    type: echo
`))
	e := newEngine(t, MapLoader{"pipe": wf})

	runID, err := e.StartRun("pipe", nil)
	if err != nil {
		t.Fatal(err)
	}
	drain(t, e) // a → b → c → DONE

	if run, _ := e.Store.GetRun(runID); run.Status != store.StatusDone {
		t.Fatalf("run should be DONE, got %s", run.Status)
	}
	before := stepCounts(e, runID)

	// Re-run the MIDDLE step in place.
	if err := e.RerunStep(runID, "b"); err != nil {
		t.Fatalf("RerunStep: %v", err)
	}
	drain(t, e)

	after := stepCounts(e, runID)
	if after["b"] != before["b"]+1 {
		t.Errorf("b should have re-run once: before=%d after=%d", before["b"], after["b"])
	}
	if after["c"] != before["c"] {
		t.Errorf("c must NOT cascade: before=%d after=%d", before["c"], after["c"])
	}
	if after["a"] != before["a"] {
		t.Errorf("a must NOT re-run: before=%d after=%d", before["a"], after["a"])
	}
	if run, _ := e.Store.GetRun(runID); run.Status != store.StatusDone {
		t.Errorf("run should be DONE after rerun, got %s", run.Status)
	}
}

func TestRerunStepUnknownStep(t *testing.T) {
	wf, _ := Parse([]byte("id: p\nversion: 1.0.0\nsteps:\n  - id: a\n    type: echo\n"))
	e := newEngine(t, MapLoader{"p": wf})
	runID, _ := e.StartRun("p", nil)
	drain(t, e)
	if err := e.RerunStep(runID, "nope"); err == nil {
		t.Fatal("expected error for unknown step")
	}
}
