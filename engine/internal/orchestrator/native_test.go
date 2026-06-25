package orchestrator

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

// ---- fakeStoryProvider -------------------------------------------------------

// fakeStoryProvider is an in-memory StoryProvider for tests. It models a small
// set of stories with optional deps and tracks status transitions so tests can
// assert exactly which stories were fired or completed.
type fakeStoryProvider struct {
	mu      sync.Mutex
	stories map[string]*fakeStory
	order   []string // insertion order for deterministic Ready/Running output
}

type fakeStory struct {
	id       string
	title    string
	status   string // "backlog" | "running" | "done"
	runID    string
	deps     []string
	sprintID string // "" in story-mode fakes; set for sprint-mode tests
	repo     string
	body     string
	accept   string
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
		out = append(out, NativeTicket{ID: s.id, Title: s.title, Status: s.status, RunID: s.runID, Deps: s.deps, SprintID: s.sprintID})
	}
	return out, nil
}

// Claim atomically transitions a story from backlog → running.
// Returns true if this caller won the claim, false if already taken.
func (p *fakeStoryProvider) Claim(_ context.Context, id string) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	s, ok := p.stories[id]
	if !ok || s.status != "backlog" {
		return false, nil
	}
	s.status = "running"
	return true, nil
}

// MarkRunning records the run_id on an already-running story.
func (p *fakeStoryProvider) MarkRunning(_ context.Context, id, runID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	s, ok := p.stories[id]
	if !ok {
		return nil
	}
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

// MarkFailed flips a story to failed.
func (p *fakeStoryProvider) MarkFailed(_ context.Context, id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	s, ok := p.stories[id]
	if !ok {
		return nil
	}
	s.status = "failed"
	s.runID = ""
	return nil
}

// GetStory returns a minimal story (title only) for the fake.
func (p *fakeStoryProvider) GetStory(_ context.Context, id string) (NativeStory, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	s, ok := p.stories[id]
	if !ok {
		return NativeStory{}, fmt.Errorf("story %s not found", id)
	}
	return NativeStory{ID: id, Title: s.title, Body: s.body, Accept: s.accept, Repo: s.repo}, nil
}

// ---- sprint-batched fake methods --------------------------------------------

// ReadySprints returns the distinct sprints whose every story is backlog and
// whose external deps are done (intra-sprint deps don't block).
func (p *fakeStoryProvider) ReadySprints(_ context.Context) ([]NativeSprint, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	// Collect sprint ids in deterministic insertion order.
	var sprintOrder []string
	seen := map[string]bool{}
	members := map[string][]*fakeStory{}
	for _, id := range p.order {
		s := p.stories[id]
		if s.sprintID == "" {
			continue
		}
		if !seen[s.sprintID] {
			seen[s.sprintID] = true
			sprintOrder = append(sprintOrder, s.sprintID)
		}
		members[s.sprintID] = append(members[s.sprintID], s)
	}
	var out []NativeSprint
	for _, sid := range sprintOrder {
		ms := members[sid]
		if len(ms) == 0 {
			continue
		}
		ready := true
		inSprint := map[string]bool{}
		for _, s := range ms {
			inSprint[s.id] = true
		}
		for _, s := range ms {
			if s.status != "backlog" {
				ready = false
				break
			}
			for _, dep := range s.deps {
				if inSprint[dep] {
					continue
				}
				if d, ok := p.stories[dep]; !ok || d.status != "done" {
					ready = false
					break
				}
			}
			if !ready {
				break
			}
		}
		if ready {
			out = append(out, NativeSprint{ID: sid, Name: sid})
		}
	}
	return out, nil
}

// SprintStories returns the sprint's stories in insertion order (good enough for
// the fake; the real store does topological ordering, tested in tickets_test.go).
func (p *fakeStoryProvider) SprintStories(_ context.Context, sprintID string) ([]NativeStory, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []NativeStory
	for _, id := range p.order {
		s := p.stories[id]
		if s.sprintID != sprintID {
			continue
		}
		out = append(out, NativeStory{ID: s.id, Title: s.title, Body: s.body, Accept: s.accept, Repo: s.repo})
	}
	return out, nil
}

// ClaimSprint atomically claims all the sprint's backlog stories. ok=false if any
// story is not backlog (a concurrent claimer already moved one).
func (p *fakeStoryProvider) ClaimSprint(_ context.Context, sprintID string) ([]string, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var ids []string
	for _, id := range p.order {
		s := p.stories[id]
		if s.sprintID != sprintID {
			continue
		}
		if s.status != "backlog" {
			return nil, false, nil // already claimed by someone else
		}
		ids = append(ids, s.id)
	}
	if len(ids) == 0 {
		return nil, false, nil
	}
	for _, id := range ids {
		p.stories[id].status = "running"
	}
	return ids, true, nil
}

// MarkSprintRunning records runID on every story in the sprint.
func (p *fakeStoryProvider) MarkSprintRunning(_ context.Context, sprintID, runID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, id := range p.order {
		if p.stories[id].sprintID == sprintID {
			p.stories[id].runID = runID
		}
	}
	return nil
}

// MarkSprintDone flips every running story in the sprint to done.
func (p *fakeStoryProvider) MarkSprintDone(_ context.Context, sprintID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, id := range p.order {
		s := p.stories[id]
		if s.sprintID == sprintID && s.status == "running" {
			s.status = "done"
		}
	}
	return nil
}

// MarkSprintFailed flips every running story in the sprint to failed.
func (p *fakeStoryProvider) MarkSprintFailed(_ context.Context, sprintID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, id := range p.order {
		s := p.stories[id]
		if s.sprintID == sprintID && s.status == "running" {
			s.status = "failed"
		}
	}
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

	if _, err := sched.RunOnce(context.Background()); err != nil {
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

	if _, err := sched.RunOnce(context.Background()); err != nil {
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

	if _, err := sched.RunOnce(context.Background()); err != nil {
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
	if _, err := sched.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := sched.RunOnce(ctx); err != nil {
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
	if _, err := sched.RunOnce(ctx); err != nil {
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
	if _, err := sched.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if cp.firedCount() != 1 {
		t.Fatalf("while S1 RUNNING, S2 should stay blocked; fired=%d", cp.firedCount())
	}

	// Simulate S1's run finishing.
	s1RunID := provider.runIDOf("S1")
	cp.setStatus(s1RunID, "DONE")

	// Cycle 3: S1 gets marked done → S2 becomes ready and fires.
	if _, err := sched.RunOnce(ctx); err != nil {
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

// ---- Bug 1: terminal non-DONE runs mark the story failed ---------------------

// TestNativeFailedRunMarksStoryFailed: a running story whose run reaches FAILED
// must be marked "failed" — not left stuck in "running" forever.
func TestNativeFailedRunMarksStoryFailed(t *testing.T) {
	provider := newFakeProvider(
		&fakeStory{id: "S1", title: "will fail", status: "running", runID: "run-fail"},
	)
	cp := &fakeControlPlane{}
	cp.setStatus("run-fail", "FAILED")

	sched := NewNativeScheduler(provider, cp, "dev")

	if _, err := sched.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	if provider.statusOf("S1") != "failed" {
		t.Errorf("S1 status: want failed, got %s", provider.statusOf("S1"))
	}
}

// TestNativeCancelledRunMarksStoryFailed: CANCELLED is also a terminal failure.
func TestNativeCancelledRunMarksStoryFailed(t *testing.T) {
	provider := newFakeProvider(
		&fakeStory{id: "S1", title: "will cancel", status: "running", runID: "run-cancel"},
	)
	cp := &fakeControlPlane{}
	cp.setStatus("run-cancel", "CANCELLED")

	sched := NewNativeScheduler(provider, cp, "dev")

	if _, err := sched.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	if provider.statusOf("S1") != "failed" {
		t.Errorf("S1 status: want failed, got %s", provider.statusOf("S1"))
	}
}

// ---- Bug 2: double-fire prevention via Claim-then-fire ----------------------

// TestNativeClaimPreventsDoubleFire: two concurrent-ish claims of the same
// story — only one succeeds, so FireRun is called exactly once.
func TestNativeClaimPreventsDoubleFire(t *testing.T) {
	// shared provider models the store: a single "backlog" story.
	provider := newFakeProvider(
		&fakeStory{id: "S1", title: "race target", status: "backlog"},
	)
	cp := &fakeControlPlane{}

	sched1 := NewNativeScheduler(provider, cp, "dev")
	sched2 := NewNativeScheduler(provider, cp, "dev")
	ctx := context.Background()

	// Both schedulers see S1 as ready and try to claim-then-fire.
	// Because fakeStoryProvider.Claim is mutex-guarded and transitions
	// backlog→running atomically, only one can claim it.
	if _, err := sched1.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := sched2.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}

	if cp.firedCount() != 1 {
		t.Fatalf("want exactly 1 FireRun (double-fire prevented), got %d", cp.firedCount())
	}
	if provider.statusOf("S1") != "running" {
		t.Errorf("S1 status: want running, got %s", provider.statusOf("S1"))
	}
}

// ---- Sprint mode (goal mode) ------------------------------------------------

// sprintMode returns a control plane configured to report execution_unit=sprint.
func sprintMode() *fakeControlPlane { return &fakeControlPlane{execUnit: "sprint"} }

// payloadOf returns the payload of the n-th fired run (0-indexed) as a map.
func (f *fakeControlPlane) payloadOf(n int) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.runs[n].payload.(map[string]any)
}

// TestSprintModeFiresOneRunForWholeSprint: in sprint mode a ready sprint claims
// ALL its stories and fires exactly ONE run carrying a combined goal-mode ticket.
func TestSprintModeFiresOneRunForWholeSprint(t *testing.T) {
	provider := newFakeProvider(
		&fakeStory{id: "A", title: "Story A", status: "backlog", sprintID: "SP1",
			body: "do A", accept: "A works", repo: "github.com/acme/x"},
		&fakeStory{id: "B", title: "Story B", status: "backlog", sprintID: "SP1",
			body: "do B", accept: "B works", repo: "github.com/acme/x", deps: []string{"A"}},
	)
	cp := sprintMode()
	sched := NewNativeScheduler(provider, cp, "factory")

	if _, err := sched.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Exactly one run for the whole sprint.
	if cp.firedCount() != 1 {
		t.Fatalf("want 1 fired run for the sprint, got %d", cp.firedCount())
	}
	// Both stories claimed → running, sharing the run_id.
	for _, id := range []string{"A", "B"} {
		if provider.statusOf(id) != "running" {
			t.Errorf("%s: want running, got %s", id, provider.statusOf(id))
		}
		if provider.runIDOf(id) == "" {
			t.Errorf("%s: run_id should be recorded", id)
		}
	}
	if provider.runIDOf("A") != provider.runIDOf("B") {
		t.Errorf("both stories should share the sprint run_id: %s vs %s",
			provider.runIDOf("A"), provider.runIDOf("B"))
	}

	// Payload carries sprint_id, story_ids, repo, and a combined goal-mode ticket.
	p := cp.payloadOf(0)
	if p["sprint_id"] != "SP1" {
		t.Errorf("payload sprint_id: got %v, want SP1", p["sprint_id"])
	}
	ids, _ := p["story_ids"].([]string)
	if len(ids) != 2 {
		t.Errorf("payload story_ids: got %v, want 2 ids", p["story_ids"])
	}
	if p["repo"] != "github.com/acme/x" {
		t.Errorf("payload repo: got %v", p["repo"])
	}
	ticket, _ := p["ticket"].(string)
	for _, want := range []string{"Goal mode", "### A — Story A", "### B — Story B", "Acceptance criteria:", "A works", "B works"} {
		if !contains2(ticket, want) {
			t.Errorf("ticket missing %q\n---\n%s", want, ticket)
		}
	}
}

// TestSprintModeNoReadySprintFiresNothing: a sprint with a started story is not
// ready, so nothing fires.
func TestSprintModeNoReadySprintFiresNothing(t *testing.T) {
	provider := newFakeProvider(
		&fakeStory{id: "A", title: "A", status: "running", sprintID: "SP1", runID: "r1"},
		&fakeStory{id: "B", title: "B", status: "backlog", sprintID: "SP1"},
	)
	cp := sprintMode()
	sched := NewNativeScheduler(provider, cp, "factory")
	if _, err := sched.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if cp.firedCount() != 0 {
		t.Fatalf("want 0 fired (sprint not all-backlog), got %d", cp.firedCount())
	}
}

// TestSprintModeDoneMarksAllStories: when the sprint's shared run reaches DONE,
// ALL its stories are marked done in one cycle.
func TestSprintModeDoneMarksAllStories(t *testing.T) {
	provider := newFakeProvider(
		&fakeStory{id: "A", title: "A", status: "running", sprintID: "SP1", runID: "run-sp"},
		&fakeStory{id: "B", title: "B", status: "running", sprintID: "SP1", runID: "run-sp"},
	)
	cp := sprintMode()
	cp.setStatus("run-sp", "DONE")
	sched := NewNativeScheduler(provider, cp, "factory")

	if _, err := sched.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"A", "B"} {
		if provider.statusOf(id) != "done" {
			t.Errorf("%s: want done, got %s", id, provider.statusOf(id))
		}
	}
}

// TestSprintModeFailedMarksAllStories: a FAILED sprint run fails all its stories.
func TestSprintModeFailedMarksAllStories(t *testing.T) {
	provider := newFakeProvider(
		&fakeStory{id: "A", title: "A", status: "running", sprintID: "SP1", runID: "run-sp"},
		&fakeStory{id: "B", title: "B", status: "running", sprintID: "SP1", runID: "run-sp"},
	)
	cp := sprintMode()
	cp.setStatus("run-sp", "FAILED")
	sched := NewNativeScheduler(provider, cp, "factory")

	if _, err := sched.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"A", "B"} {
		if provider.statusOf(id) != "failed" {
			t.Errorf("%s: want failed, got %s", id, provider.statusOf(id))
		}
	}
}

// TestSprintModeNoDoubleFire: a second cycle does not re-fire an already-running
// sprint (its stories are no longer backlog → not ready).
func TestSprintModeNoDoubleFire(t *testing.T) {
	provider := newFakeProvider(
		&fakeStory{id: "A", title: "A", status: "backlog", sprintID: "SP1"},
		&fakeStory{id: "B", title: "B", status: "backlog", sprintID: "SP1"},
	)
	cp := sprintMode()
	sched := NewNativeScheduler(provider, cp, "factory")
	ctx := context.Background()

	if _, err := sched.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := sched.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if cp.firedCount() != 1 {
		t.Fatalf("want exactly 1 fire across two cycles, got %d", cp.firedCount())
	}
}

// contains2 is a tiny substring helper for ticket assertions.
func contains2(haystack, needle string) bool {
	return len(needle) == 0 || (len(haystack) >= len(needle) && indexOfSub(haystack, needle) >= 0)
}

func indexOfSub(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
