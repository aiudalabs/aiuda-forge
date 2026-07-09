package workflow

import (
	"context"
	"sync/atomic"
	"testing"
)

// countingRunner counts how many times it runs and succeeds — stands in for an agent
// (LLM) step so a test can assert it was NOT invoked when skipped.
type countingRunner struct{ n *int32 }

func (c countingRunner) Run(_ context.Context, _ Step, inputs map[string]any, _ string) (StepResult, error) {
	atomic.AddInt32(c.n, 1)
	return StepResult{Success: true, Output: map[string]any{"ran": true, "feedback": inputs["feedback"]}}, nil
}

// skipFlow: head → body(skip_if_empty:[feedback]) → gate(on_fail goto body) → tail.
// body sits before the gate so on_fail can loop back to it, but on the happy (linear)
// path it must be skipped — no run — because no feedback is present.
const skipFlow = `
id: skipflow
version: 1.0.0
steps:
  - id: head
    type: echo
  - id: body
    type: counting
    skip_if_empty: [feedback]
  - id: gate
    type: human_gate
    on_fail:
      goto: body
      max: 5
      feedback: $gate.detail
  - id: tail
    type: echo
`

// TestSkipIfEmptyHappyPath: on the linear path with no feedback, the guarded `body`
// step is skipped entirely (its runner is never invoked) and the flow reaches the gate.
// Approving then completes the run without ever running body.
func TestSkipIfEmptyHappyPath(t *testing.T) {
	wf, err := Parse([]byte(skipFlow))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var n int32
	e := newEngine(t, MapLoader{"skipflow": wf})
	e.Register("counting", countingRunner{&n})
	e.Register("human_gate", parkRunner{})

	runID, _ := e.StartRun("skipflow", nil)
	drain(t, e) // head → (body skipped) → gate parks
	if got := atomic.LoadInt32(&n); got != 0 {
		t.Fatalf("body must NOT run on the happy path, ran %d times", got)
	}
	// No body task should have been created at all (zero cost, not a no-op task).
	tasks, _ := e.Store.TasksForRun(runID)
	for _, tk := range tasks {
		if tk.StepID == "body" {
			t.Fatalf("a body task was created despite the skip: %+v", tk)
		}
	}

	if err := e.ApproveStep(runID, "gate"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	drain(t, e) // tail → DONE
	if got := atomic.LoadInt32(&n); got != 0 {
		t.Fatalf("body still must not have run after approve, ran %d times", got)
	}
}

// TestSkipIfEmptyRunsOnReject: rejecting the gate injects feedback into `body` (via
// on_fail.goto), so the guarded step RUNS on the reject loop and re-parks the gate.
// This proves skip_if_empty only suppresses the LINEAR path, not the reject/answer one.
func TestSkipIfEmptyRunsOnReject(t *testing.T) {
	wf, err := Parse([]byte(skipFlow))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var n int32
	e := newEngine(t, MapLoader{"skipflow": wf})
	e.Register("counting", countingRunner{&n})
	e.Register("human_gate", parkRunner{})

	runID, _ := e.StartRun("skipflow", nil)
	drain(t, e) // gate parks, body skipped
	if got := atomic.LoadInt32(&n); got != 0 {
		t.Fatalf("body should not have run yet, ran %d", got)
	}

	if err := e.RejectStep(runID, "gate", "please fix the empty state"); err != nil {
		t.Fatalf("reject: %v", err)
	}
	drain(t, e) // on_fail → body runs (feedback present) → gate re-parks
	if got := atomic.LoadInt32(&n); got != 1 {
		t.Fatalf("body must run once on reject, ran %d", got)
	}

	// Approve the re-parked gate → tail → DONE, body not run again.
	if err := e.ApproveStep(runID, "gate"); err != nil {
		t.Fatalf("approve after fix: %v", err)
	}
	drain(t, e)
	if got := atomic.LoadInt32(&n); got != 1 {
		t.Fatalf("body should have run exactly once total, ran %d", got)
	}
}
