package workflow

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"vibeforge-kernel/internal/store"
)

func newEngine(t *testing.T, loader Loader) *Engine {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "wf.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	e := NewEngine(st, loader, t.TempDir())
	e.Register("echo", EchoRunner{})
	e.Register("gate", GateRunner{})
	return e
}

// TestEngineWorkdirAbsolute: run workdirs MUST be absolute. A relative workdir
// breaks the docker sandbox (Docker treats a relative -v source as an invalid
// named volume) and is fragile in general. Pins the root-cause fix.
func TestEngineWorkdirAbsolute(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "wd.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	e := NewEngine(st, nil, ".vibeforge-runs") // deliberately relative
	if !filepath.IsAbs(e.WorkdirRoot) {
		t.Fatalf("WorkdirRoot must be absolute, got %q", e.WorkdirRoot)
	}
	if !filepath.IsAbs(e.Workdir("run_x")) {
		t.Fatalf("per-run workdir must be absolute, got %q", e.Workdir("run_x"))
	}
}

// TestDemoWorkflowFromYAML: the demo manifest (echo -> gate) runs E2E with no
// flow-specific code. This is the core thesis check.
func TestDemoWorkflowFromYAML(t *testing.T) {
	wf, err := ParseFile(filepath.Join("..", "..", "registry", "workflows", "demo.yaml"))
	if err != nil {
		t.Fatalf("parse demo.yaml: %v", err)
	}
	e := newEngine(t, MapLoader{"demo": wf})

	runID, err := e.StartRun("demo", map[string]any{"name": "hello"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	status, err := e.RunToCompletion(context.Background(), runID)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if status != store.StatusDone {
		t.Fatalf("expected DONE, got %s", status)
	}

	// The marker the gate checked should exist in the run workdir.
	if _, err := os.Stat(filepath.Join(e.Workdir(runID), "marker.txt")); err != nil {
		t.Fatalf("marker missing: %v", err)
	}
}

// TestAddingStepChangesFlowNoCode: define a workflow inline with an extra step.
// The same generic engine runs it — proving a step added in data changes the
// flow without recompiling logic.
func TestAddingStepChangesFlowNoCode(t *testing.T) {
	src := []byte(`
id: three
version: 1.0.0
steps:
  - id: a
    type: echo
    inputs: { write_file: a.txt, write_content: "A" }
  - id: b
    type: echo
    inputs: { write_file: b.txt, write_content: "B" }
  - id: gate
    type: gate
    command: "test -f a.txt && test -f b.txt"
`)
	wf, err := Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	e := newEngine(t, MapLoader{"three": wf})
	runID, _ := e.StartRun("three", nil)
	status, err := e.RunToCompletion(context.Background(), runID)
	if err != nil || status != store.StatusDone {
		t.Fatalf("expected DONE, got %s err=%v", status, err)
	}
	// All three steps ran (a, b, gate) — 3 tasks, all DONE.
	tasks, _ := e.Store.TasksForRun(runID)
	if len(tasks) != 3 {
		t.Fatalf("expected 3 tasks, got %d", len(tasks))
	}
}

// TestOnFailLoopRecovers: a gate that fails once then passes. on_fail goto loops
// back to implement; the second gate run passes; run ends DONE.
func TestOnFailLoopRecovers(t *testing.T) {
	// Gate increments a counter file and fails until it reaches 2.
	src := []byte(`
id: recover
version: 1.0.0
steps:
  - id: implement
    type: echo
  - id: gate
    type: gate
    command: |
      n=$(cat .n 2>/dev/null || echo 0); n=$((n+1)); echo $n > .n; [ "$n" -ge 2 ]
    on_fail:
      goto: implement
      max: 3
      feedback: $gate.detail
`)
	wf, _ := Parse(src)
	e := newEngine(t, MapLoader{"recover": wf})
	runID, _ := e.StartRun("recover", nil)
	status, err := e.RunToCompletion(context.Background(), runID)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if status != store.StatusDone {
		t.Fatalf("expected DONE after recovery, got %s", status)
	}
	// gate should have a failed attempt then a done attempt; implement ran twice.
	tasks, _ := e.Store.TasksForRun(runID)
	var gateFail, gateDone, implementRuns int
	for _, tk := range tasks {
		switch {
		case tk.StepID == "gate" && tk.Status == store.StatusFailed:
			gateFail++
		case tk.StepID == "gate" && tk.Status == store.StatusDone:
			gateDone++
		case tk.StepID == "implement":
			implementRuns++
		}
	}
	if gateFail != 1 || gateDone != 1 {
		t.Fatalf("expected 1 gate fail + 1 gate done, got fail=%d done=%d", gateFail, gateDone)
	}
	if implementRuns != 2 {
		t.Fatalf("expected implement to run twice (loop), got %d", implementRuns)
	}
}

// TestOnFailCapExhausted: a gate that always fails exhausts the cap and the run
// ends FAILED, with exactly max+1 gate attempts.
func TestOnFailCapExhausted(t *testing.T) {
	src := []byte(`
id: cap
version: 1.0.0
steps:
  - id: implement
    type: echo
  - id: gate
    type: gate
    command: "false"
    on_fail:
      goto: implement
      max: 2
`)
	wf, _ := Parse(src)
	e := newEngine(t, MapLoader{"cap": wf})
	runID, _ := e.StartRun("cap", nil)
	status, err := e.RunToCompletion(context.Background(), runID)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if status != store.StatusFailed {
		t.Fatalf("expected FAILED after cap, got %s", status)
	}
	tasks, _ := e.Store.TasksForRun(runID)
	gateAttempts := 0
	for _, tk := range tasks {
		if tk.StepID == "gate" {
			gateAttempts++
		}
	}
	if gateAttempts != 3 { // max=2 => 3 total attempts
		t.Fatalf("expected 3 gate attempts (max+1), got %d", gateAttempts)
	}
}

// TestFeedbackInjectedIntoGotoTarget: on_fail feedback reaches the retried step's
// inputs as "feedback".
func TestFeedbackInjectedIntoGotoTarget(t *testing.T) {
	src := []byte(`
id: fb
version: 1.0.0
steps:
  - id: implement
    type: echo
  - id: gate
    type: gate
    command: |
      n=$(cat .n 2>/dev/null || echo 0); n=$((n+1)); echo $n > .n; [ "$n" -ge 2 ]
    on_fail:
      goto: implement
      max: 2
      feedback: $gate.detail
`)
	wf, _ := Parse(src)
	e := newEngine(t, MapLoader{"fb": wf})
	runID, _ := e.StartRun("fb", nil)
	if _, err := e.RunToCompletion(context.Background(), runID); err != nil {
		t.Fatal(err)
	}
	// The second implement task's payload should carry a non-empty feedback.
	tasks, _ := e.Store.TasksForRun(runID)
	var sawFeedback bool
	for _, tk := range tasks {
		if tk.StepID == "implement" && containsStr(tk.Payload, `"feedback"`) {
			sawFeedback = true
		}
	}
	if !sawFeedback {
		t.Fatalf("expected feedback injected into retried implement inputs")
	}
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
