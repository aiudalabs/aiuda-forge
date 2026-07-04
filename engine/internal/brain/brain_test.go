package brain

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// scriptedLLM returns a fixed sequence of turns; extra calls return end_turn.
type scriptedLLM struct {
	mu    sync.Mutex
	calls int
	steps []StreamResult
}

func (l *scriptedLLM) Stream(_ context.Context, _ string, _ []Message, _ []Tool, onText func(string)) (StreamResult, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	i := l.calls
	l.calls++
	if i < len(l.steps) {
		if l.steps[i].Text != "" && onText != nil {
			onText(l.steps[i].Text)
		}
		return l.steps[i], nil
	}
	return StreamResult{Text: "done", StopReason: "end_turn"}, nil
}

// fakeOps is an in-memory ControlOps recording mutating calls.
type fakeOps struct {
	mu      sync.Mutex
	paused  bool
	started []string
}

func (o *fakeOps) Status() (bool, int64)                     { o.mu.Lock(); defer o.mu.Unlock(); return o.paused, 0 }
func (o *fakeOps) Pause()                                    { o.mu.Lock(); o.paused = true; o.mu.Unlock() }
func (o *fakeOps) Resume()                                   { o.mu.Lock(); o.paused = false; o.mu.Unlock() }
func (o *fakeOps) ListRuns(string) ([]map[string]any, error) { return nil, nil }
func (o *fakeOps) GetRun(string) (map[string]any, error)     { return map[string]any{}, nil }
func (o *fakeOps) CancelRun(string) error                    { return nil }
func (o *fakeOps) RetryRun(string) error                     { return nil }
func (o *fakeOps) RequeueRun(string) (int, error)            { return 0, nil }
func (o *fakeOps) StartRun(wf string, _ map[string]any) (string, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.started = append(o.started, wf)
	return "run-x", nil
}
func (o *fakeOps) ApproveStep(string, string) error        { return nil }
func (o *fakeOps) RejectStep(string, string, string) error { return nil }
func (o *fakeOps) Metrics(string) (map[string]any, error)  { return map[string]any{}, nil }
func (o *fakeOps) ActiveState(string) (map[string]any, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return map[string]any{"paused": o.paused, "active_runs": []map[string]any{}, "awaiting_approval": []map[string]any{}, "terminal_runs": 0}, nil
}

func newTestBrain(t *testing.T, llm LLM, ops ControlOps, emit func(string, string, map[string]any)) *Brain {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "brain.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return New(llm, ops, st, emit)
}

func waitDone(t *testing.T, done chan map[string]any) map[string]any {
	t.Helper()
	select {
	case d := <-done:
		return d
	case <-time.After(5 * time.Second):
		t.Fatal("turn did not finish")
		return nil
	}
}

// A reversible tool call executes automatically and the turn finishes.
func TestBrainReversibleToolRunsAndFinishes(t *testing.T) {
	llm := &scriptedLLM{steps: []StreamResult{
		{ToolUses: []ToolUse{{ID: "t1", Name: "get_status", Input: json.RawMessage(`{}`)}}, StopReason: "tool_use"},
		{Text: "el motor está activo", StopReason: "end_turn"},
	}}
	done := make(chan map[string]any, 1)
	emit := func(_, typ string, data map[string]any) {
		if typ == EvtDone {
			done <- data
		}
	}
	b := newTestBrain(t, llm, &fakeOps{}, emit)
	if _, err := b.Send("proj1", "owner", "¿estado?"); err != nil {
		t.Fatal(err)
	}
	if d := waitDone(t, done); d["error"] != nil {
		t.Fatalf("turn errored: %v", d["error"])
	}
	if llm.calls < 2 {
		t.Fatalf("expected ≥2 LLM calls (tool round + final), got %d", llm.calls)
	}
}

// A mutating tool is proposed, blocks, and runs only after approval.
func TestBrainMutatingRunsOnApprove(t *testing.T) {
	llm := &scriptedLLM{steps: []StreamResult{
		{ToolUses: []ToolUse{{ID: "t1", Name: "launch_run", Input: json.RawMessage(`{"workflow":"design","instructions":"x"}`)}}, StopReason: "tool_use"},
		{Text: "lancé el run", StopReason: "end_turn"},
	}}
	ops := &fakeOps{}
	done := make(chan map[string]any, 1)
	var b *Brain
	emit := func(_, typ string, data map[string]any) {
		switch typ {
		case EvtAction:
			actionID, _ := data["action_id"].(string)
			go func() {
				for i := 0; i < 100; i++ {
					if err := b.Resolve(actionID, true); err == nil {
						return
					}
					time.Sleep(5 * time.Millisecond)
				}
			}()
		case EvtDone:
			done <- data
		}
	}
	b = newTestBrain(t, llm, ops, emit)
	if _, err := b.Send("proj1", "owner", "agrega login"); err != nil {
		t.Fatal(err)
	}
	if d := waitDone(t, done); d["error"] != nil {
		t.Fatalf("turn errored: %v", d["error"])
	}
	ops.mu.Lock()
	n := len(ops.started)
	ops.mu.Unlock()
	if n != 1 {
		t.Fatalf("expected StartRun called once after approve, got %d", n)
	}
}

// A rejected mutating action does NOT run; the turn still finishes.
func TestBrainMutatingSkippedOnReject(t *testing.T) {
	llm := &scriptedLLM{steps: []StreamResult{
		{ToolUses: []ToolUse{{ID: "t1", Name: "launch_run", Input: json.RawMessage(`{"workflow":"design"}`)}}, StopReason: "tool_use"},
		{Text: "ok, no lo lanzo", StopReason: "end_turn"},
	}}
	ops := &fakeOps{}
	done := make(chan map[string]any, 1)
	var b *Brain
	emit := func(_, typ string, data map[string]any) {
		switch typ {
		case EvtAction:
			actionID, _ := data["action_id"].(string)
			go func() {
				for i := 0; i < 100; i++ {
					if err := b.Resolve(actionID, false); err == nil {
						return
					}
					time.Sleep(5 * time.Millisecond)
				}
			}()
		case EvtDone:
			done <- data
		}
	}
	b = newTestBrain(t, llm, ops, emit)
	if _, err := b.Send("proj1", "owner", "agrega login"); err != nil {
		t.Fatal(err)
	}
	waitDone(t, done)
	ops.mu.Lock()
	n := len(ops.started)
	ops.mu.Unlock()
	if n != 0 {
		t.Fatalf("rejected action must NOT run StartRun, got %d calls", n)
	}
}

// Tool classification + role gating are wired as designed.
func TestBrainToolClassification(t *testing.T) {
	if registry["pause"].Kind != Reversible || registry["pause"].MinRole != "editor" {
		t.Errorf("pause should be reversible/editor, got %+v", registry["pause"])
	}
	if registry["get_status"].MinRole != "viewer" {
		t.Errorf("get_status should be viewer-readable")
	}
	if registry["launch_run"].Kind != Mutating {
		t.Errorf("launch_run should be mutating")
	}
	if roleAllows("editor", "viewer") {
		t.Errorf("viewer must NOT pass the editor gate")
	}
	if !roleAllows("editor", "owner") {
		t.Errorf("owner must pass the editor gate")
	}
}
