package orchestrator

import (
	"context"
	"sync"
	"testing"
)

// ---- fakeStoryProvider -------------------------------------------------------

// fakeStoryProvider is an in-memory StoryProvider for tests. It models a small
// set of stories with optional deps and tracks status transitions so tests can
// assert exactly which stories were fired or completed.
type fakeStoryProvider struct {
	mu     sync.Mutex
	stories map[string]*fakeStory
	order  []string // insertion order for deterministic Ready/Running output
}

type fakeStory struct {
	id     string
	title  string
	status string // "backlog" | "running" | "done"
	runID  string
	deps   []string
}

func newFakeProvider(stories ...*fakeStory) *fakeStoryProvider {
	p := &fakeStoryProvider{stories: make(map[string]*fakeStory)}
	for _, s := range stories {
		p.stories[s.id] = s
		p.order = append(p.order, s.id)
	}
	return p
}

// Ready returns stories whose status is "backlog" and all deps are "done".
func (p *fakeStoryProvider) Ready(_ context.Context) ([]NativeTicket, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []NativeTicket
	for _, id := range p.order {
		s := p.stories[id]
		if s.status != "backlog" {
			continue
		}
		if !p.depsAllDoneLocked(s.deps) {
			continue
		}
		out = append(out, NativeTicket{ID: s.id, Title: s.title, Status: s.status, Deps: s.deps})
	}
	return out, nil
}

// Running returns stories whose status is "running".
func (p *fakeStoryProvider) Running(_ context.Context) ([]NativeTicket, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []NativeTicket
	for _, id := range p.order {
		s := p.stories[id]
		if s.status != "running" {
			continue
		}
		out = append(out, NativeTicket{ID: s.id, Title: s.title, Status: s.status, RunID: s.runID, Deps: s.deps})
	}
	return out, nil
}

// MarkRunning flips a story to running and records the run_id.
func (p *fakeStoryProvider) MarkRunning(_ context.Context, id, runID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	s, ok := p.stories[id]
	if !ok {
		return nil
	}
	s.status = "running"
	s.runID = runID
	return nil
}

// MarkDone flips a story to done.
func (p *fakeStoryProvider) MarkDone(_ context.Context, id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	s, ok := p.stories[id]
	if !ok {
		return nil
	}
	s.status = "done"
	s.runID = ""
	return nil
}

// statusOf returns a story's current status (test helper).
func (p *fakeStoryProvider) statusOf(id string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if s, ok := p.stories[id]; ok {
		return s.status
	}
	return ""
}

// runIDOf returns a story's recorded run_id (test helper).
func (p *fakeStoryProvider) runIDOf(id string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if s, ok := p.stories[id]; ok {
		return s.runID
	}
	return ""
}

// depsAllDoneLocked checks deps without taking the lock (caller must hold it).
func (p *fakeStoryProvider) depsAllDoneLocked(deps []string) bool {
	for _, dep := range deps {
		s, ok := p.stories[dep]
		if !ok || s.status != "done" {
			return false
		}
	}
	return true
}

// ---- tests ------------------------------------------------------------------

// TestNativeReadyStoriesGetFired: a backlog story with no deps is fired and
// marked running with the returned run_id.
func TestNativeReadyStoriesGetFired(t *testing.T) {
	provider := newFakeProvider(
		&fakeStory{id: "S1", title: "first story", status: "backlog"},
	)
	cp := &fakeControlPlane{}
	sched := NewNativeScheduler(provider, cp, "dev")

	if err := sched.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	if cp.firedCount() != 1 {
		t.Fatalf("expected 1 fired run, got %d", cp.firedCount())
	}
	if provider.statusOf("S1") != "running" {
		t.Errorf("S1 status: want running, got %s", provider.statusOf("S1"))
	}
	if provider.runIDOf("S1") == "" {
		t.Error("S1 run_id should be set after firing")
	}
}

// TestNativeRunningStoryMarkedDoneWhenRunDone: a story in "running" state whose
// run reaches DONE is marked done by the next RunOnce cycle.
func TestNativeRunningStoryMarkedDoneWhenRunDone(t *testing.T) {
	provider := newFakeProvider(
		&fakeStory{id: "S1", title: "already running", status: "running", runID: "run-42"},
	)
	cp := &fakeControlPlane{}
	cp.setStatus("run-42", "DONE")

	sched := NewNativeScheduler(provider, cp, "dev")

	if err := sched.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	if provider.statusOf("S1") != "done" {
		t.Errorf("S1 status: want done, got %s", provider.statusOf("S1"))
	}
}

// TestNativeRunningStoryUntouchedWhileRunning: a story whose run is still
// RUNNING must stay in "running" status — not advanced to "done".
func TestNativeRunningStoryUntouchedWhileRunning(t *testing.T) {
	provider := newFakeProvider(
		&fakeStory{id: "S1", title: "in flight", status: "running", runID: "run-99"},
	)
	cp := &fakeControlPlane{} // default status → "RUNNING"

	sched := NewNativeScheduler(provider, cp, "dev")

	if err := sched.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	if provider.statusOf("S1") != "running" {
		t.Errorf("S1 should still be running, got %s", provider.statusOf("S1"))
	}
}

// TestNativeNoDoubleFire: once a story is running it must not be re-fired on
// subsequent cycles. The fakeStoryProvider models this correctly because
// MarkRunning flips status to "running", removing it from Ready().
func TestNativeNoDoubleFire(t *testing.T) {
	provider := newFakeProvider(
		&fakeStory{id: "S1", title: "idempotent", status: "backlog"},
	)
	cp := &fakeControlPlane{}
	sched := NewNativeScheduler(provider, cp, "dev")

	ctx := context.Background()
	if err := sched.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if err := sched.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}

	if cp.firedCount() != 1 {
		t.Fatalf("expected exactly 1 fire, got %d (double-fire detected)", cp.firedCount())
	}
}

// TestNativeDepUnblockEndToEnd: completing story S1 unblocks S2 (which depends
// on S1), so S2 fires on the next cycle. Exercises the full ready→running→done
// state machine in the fake provider.
func TestNativeDepUnblockEndToEnd(t *testing.T) {
	provider := newFakeProvider(
		&fakeStory{id: "S1", title: "dep", status: "backlog"},
		&fakeStory{id: "S2", title: "consumer", status: "backlog", deps: []string{"S1"}},
	)
	cp := &fakeControlPlane{}
	sched := NewNativeScheduler(provider, cp, "dev")
	ctx := context.Background()

	// Cycle 1: S1 has no deps → fires; S2 is blocked (dep not done).
	if err := sched.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if cp.firedCount() != 1 {
		t.Fatalf("cycle 1: expected 1 fired (S1 only), got %d", cp.firedCount())
	}
	if provider.statusOf("S1") != "running" {
		t.Errorf("S1 after cycle 1: want running, got %s", provider.statusOf("S1"))
	}
	if provider.statusOf("S2") != "backlog" {
		t.Errorf("S2 after cycle 1: want backlog (blocked), got %s", provider.statusOf("S2"))
	}

	// S2 still blocked while S1 is RUNNING.
	if err := sched.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if cp.firedCount() != 1 {
		t.Fatalf("while S1 RUNNING, S2 should stay blocked; fired=%d", cp.firedCount())
	}

	// Simulate S1's run finishing.
	s1RunID := provider.runIDOf("S1")
	cp.setStatus(s1RunID, "DONE")

	// Cycle 3: S1 gets marked done → S2 becomes ready and fires.
	if err := sched.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if provider.statusOf("S1") != "done" {
		t.Errorf("S1 after DONE run: want done, got %s", provider.statusOf("S1"))
	}
	if cp.firedCount() != 2 {
		t.Fatalf("after S1 done, S2 should fire; fired=%d", cp.firedCount())
	}
	if provider.statusOf("S2") != "running" {
		t.Errorf("S2 after firing: want running, got %s", provider.statusOf("S2"))
	}
}
