package agent

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"

	"forge/internal/store"
	"forge/internal/workflow"
)

// scriptedBackend returns "broken" for the first N verify calls, then "works".
type scriptedBackend struct {
	calls     int64
	brokenFor int64
}

func (s *scriptedBackend) Run(_ context.Context, _ string, _ Options, _ func(Event)) (Result, error) {
	n := atomic.AddInt64(&s.calls, 1)
	if n <= s.brokenFor {
		return Result{Text: "I checked it.\nVERDICT: broken\nthe function returns the wrong sum", Success: true}, nil
	}
	return Result{Text: "I checked it.\nVERDICT: works\nadd(2,3)=5 as expected", Success: true}, nil
}

func newVerifyEngine(t *testing.T, backend Backend, agents Loader) *workflow.Engine {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	e := workflow.NewEngine(st, nil, t.TempDir())
	e.Register("echo", workflow.EchoRunner{})
	vr := NewVerifyRunner(backend, agents)
	vr.Timeout = 0
	e.Register("agentic_verify", vr)
	e.Register("human_gate", HumanGateRunner{})
	return e
}

// TestAgenticVerifyLoopRecovers: verify returns broken once (-> on_fail goto
// implement) then works; the run reaches DONE. This is the layered, agentic
// verification loop ("does it work?", not just "tests pass").
func TestAgenticVerifyLoopRecovers(t *testing.T) {
	wf, err := workflow.Parse([]byte(`
id: verifyflow
version: 1.0.0
steps:
  - id: implement
    type: echo
  - id: verify
    type: agentic_verify
    agent: verifier
    model: claude-sonnet-4-6
    on_fail:
      goto: implement
      max: 2
`))
	if err != nil {
		t.Fatal(err)
	}
	agents := MapLoader{"verifier": &Manifest{ID: "verifier", Model: "claude-sonnet-4-6", Tools: []string{"read"}}}
	backend := &scriptedBackend{brokenFor: 1} // broken once, then works
	e := newVerifyEngine(t, backend, agents)
	e.Loader = workflow.MapLoader{"verifyflow": wf}

	runID, err := e.StartRun("verifyflow", nil)
	if err != nil {
		t.Fatal(err)
	}
	status, err := e.RunToCompletion(context.Background(), runID)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if status != store.StatusDone {
		t.Fatalf("expected DONE after verify recovery, got %s", status)
	}

	tasks, _ := e.Store.TasksForRun(runID)
	var verifyBroken, verifyWorks, implementRuns int
	for _, tk := range tasks {
		switch {
		case tk.StepID == "verify" && tk.Status == store.StatusFailed:
			verifyBroken++
		case tk.StepID == "verify" && tk.Status == store.StatusDone:
			verifyWorks++
		case tk.StepID == "implement":
			implementRuns++
		}
	}
	if verifyBroken != 1 || verifyWorks != 1 {
		t.Fatalf("expected 1 broken + 1 works verify, got broken=%d works=%d", verifyBroken, verifyWorks)
	}
	if implementRuns != 2 {
		t.Fatalf("expected implement to re-run on broken (2), got %d", implementRuns)
	}

	// step.verify events emitted with both verdicts.
	events, _ := e.Store.EventsAfter(runID, 0)
	var sawBroken, sawWorks bool
	for _, ev := range events {
		if ev.Type == store.EventStepVerify {
			if contains(ev.Data, "broken") {
				sawBroken = true
			}
			if contains(ev.Data, "works") {
				sawWorks = true
			}
		}
	}
	if !sawBroken || !sawWorks {
		t.Fatalf("expected step.verify events for broken and works, got broken=%v works=%v", sawBroken, sawWorks)
	}
}

// TestAgenticVerifyCapHonored: a verifier that always returns broken exhausts the
// on_fail cap and the run FAILS.
func TestAgenticVerifyCapHonored(t *testing.T) {
	wf, _ := workflow.Parse([]byte(`
id: alwaysbroken
version: 1.0.0
steps:
  - id: implement
    type: echo
  - id: verify
    type: agentic_verify
    agent: verifier
    on_fail:
      goto: implement
      max: 2
`))
	agents := MapLoader{"verifier": &Manifest{ID: "verifier"}}
	backend := &scriptedBackend{brokenFor: 1000} // always broken
	e := newVerifyEngine(t, backend, agents)
	e.Loader = workflow.MapLoader{"alwaysbroken": wf}
	runID, _ := e.StartRun("alwaysbroken", nil)
	status, err := e.RunToCompletion(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	if status != store.StatusFailed {
		t.Fatalf("expected FAILED after cap, got %s", status)
	}
}

// TestHumanGateParksAndApproves: a human_gate parks the run (AWAITING +
// run.awaiting_approval), the run is NOT terminal, and ApproveStep resumes it to
// DONE.
func TestHumanGateParksAndApproves(t *testing.T) {
	wf, _ := workflow.Parse([]byte(`
id: gated
version: 1.0.0
steps:
  - id: implement
    type: echo
  - id: approve
    type: human_gate
    inputs: { reason: "high-risk change needs sign-off" }
  - id: ship
    type: echo
`))
	e := newVerifyEngine(t, &scriptedBackend{}, MapLoader{})
	e.Loader = workflow.MapLoader{"gated": wf}
	runID, err := e.StartRun("gated", nil)
	if err != nil {
		t.Fatal(err)
	}

	// Drive until the gate parks (implement done, approve AWAITING).
	for i := 0; i < 50; i++ {
		busy, err := e.ExecuteOne(context.Background(), "w")
		if err != nil {
			t.Fatal(err)
		}
		if !busy {
			break
		}
	}

	// Run must NOT be terminal; the approve task is AWAITING.
	run, _ := e.Store.GetRun(runID)
	if store.IsTerminal(run.Status) {
		t.Fatalf("run should be parked (RUNNING), got %s", run.Status)
	}
	tasks, _ := e.Store.TasksForRun(runID)
	var awaiting bool
	for _, tk := range tasks {
		if tk.StepID == "approve" && tk.Status == store.StatusAwaiting {
			awaiting = true
		}
	}
	if !awaiting {
		t.Fatalf("expected approve step AWAITING, tasks=%+v", tasks)
	}
	// run.awaiting_approval emitted.
	events, _ := e.Store.EventsAfter(runID, 0)
	var sawAwait bool
	for _, ev := range events {
		if ev.Type == store.EventRunAwaitingApprv {
			sawAwait = true
		}
	}
	if !sawAwait {
		t.Fatalf("expected run.awaiting_approval event")
	}

	// Approve via the control op; the flow resumes to DONE.
	if err := e.ApproveStep(runID, "approve"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	status, err := e.RunToCompletion(context.Background(), runID)
	if err != nil {
		t.Fatalf("post-approve run: %v", err)
	}
	if status != store.StatusDone {
		t.Fatalf("expected DONE after approval, got %s", status)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
