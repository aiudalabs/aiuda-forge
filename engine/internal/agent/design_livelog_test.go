package agent

import (
	"context"
	"testing"

	"forge/internal/store"
	"forge/internal/workflow"
)

// TestDesignRunnerEmitsStepEvent (#13): a `design`-type step, wired exactly as
// app.Build wires the design runner (an agent StepRunner with Sandboxed=false),
// must stream its live-log — persisting step.event rows — when driven through the
// generic engine. The emitter is threaded via ctx (workflow.WithEmitter in
// ExecuteOne), so a design run's live view is NOT empty. Guards against a
// regression that would silence the design terminal in the console.
func TestDesignRunnerEmitsStepEvent(t *testing.T) {
	wfSrc := []byte(`
id: design
version: 1.0.0
steps:
  - id: discovery
    type: design
    agent: discovery
    inputs: { ticket: $trigger.ticket }
`)
	wf, err := workflow.Parse(wfSrc)
	if err != nil {
		t.Fatal(err)
	}
	agents := MapLoader{"discovery": &Manifest{ID: "discovery", Model: "claude-opus-4-8", Role: "analyst"}}
	// FakeBackend streams one KindText event to onEvent — the same shape a real
	// design agent streams while it reasons/writes a doc.
	backend := FakeBackend{Reply: "brief: the product is ..."}

	e := newEngineWithAgent(t, backend, agents)
	// Register the design step type exactly as app.Build does: an agent StepRunner
	// with Sandboxed=false (design phases run on the host, produce docs not code).
	design := NewStepRunnerWith(backend, agents)
	design.Sandboxed = false
	e.Register("design", design)
	e.Loader = workflow.MapLoader{"design": wf}

	runID, err := e.StartRun("design", map[string]any{"ticket": "an idea"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.RunToCompletion(context.Background(), runID); err != nil {
		t.Fatalf("run design: %v", err)
	}

	events, err := e.Store.EventsAfter(runID, 0)
	if err != nil {
		t.Fatal(err)
	}
	var stepEvents int
	for _, ev := range events {
		if ev.Type == store.EventStepEvent {
			stepEvents++
		}
	}
	if stepEvents == 0 {
		t.Fatalf("design run emitted no step.event rows — live-log is empty (got %d events total)", len(events))
	}
}
