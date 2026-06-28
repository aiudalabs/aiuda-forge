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
	id        string
	title     string
	status    string // "backlog" | "running" | "done"
	runID     string
	deps      []string
	sprintID  string // "" in story-mode fakes; set for sprint-mode tests
	repo      string
	body      string
	accept    string
	owner     string // lane/agent id; "" in fakes that don't exercise routing
	prURL     string // recorded when the story moves to in_review
	projectID string // "" in single-project fakes; set for per-project (audit A2) tests
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
		out = append(out, NativeTicket{ID: s.id, Title: s.title, Status: s.status, Deps: s.deps, SprintID: s.sprintID, ProjectID: s.projectID})
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
		out = append(out, NativeTicket{ID: s.id, Title: s.title, Status: s.status, RunID: s.runID, Deps: s.deps, SprintID: s.sprintID, ProjectID: s.projectID})
	}
	return out, nil
}

// Failed returns the fake's failed stories (R3 re-sync source).
func (p *fakeStoryProvider) Failed(_ context.Context) ([]NativeTicket, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []NativeTicket
	for _, id := range p.order {
		s := p.stories[id]
		if s.status != "failed" {
			continue
		}
		out = append(out, NativeTicket{ID: s.id, Title: s.title, Status: s.status, RunID: s.runID, Deps: s.deps, SprintID: s.sprintID, ProjectID: s.projectID})
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
	// Mirror the real provider: PUT status=running + run_id. In the normal flow the
	// story is already running (from Claim) so this is a no-op; for the R3 re-sync
	// it performs the legal failed→running transition.
	s.status = "running"
	s.runID = runID
	return nil
}

// ResetClaim returns a claimed-but-unfired story (running, no run_id) to backlog.
// Mirrors the store's MarkBacklog guard: a story that already has a run_id is left
// running (it is genuinely executing).
func (p *fakeStoryProvider) ResetClaim(_ context.Context, id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	s, ok := p.stories[id]
	if !ok {
		return nil
	}
	if s.status == "running" && s.runID == "" {
		s.status = "backlog"
	}
	return nil
}

// ResetSprintClaim returns a sprint's just-claimed (running, no run_id) stories to
// backlog.
func (p *fakeStoryProvider) ResetSprintClaim(_ context.Context, sprintID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, id := range p.order {
		s := p.stories[id]
		if s.sprintID == sprintID && s.status == "running" && s.runID == "" {
			s.status = "backlog"
		}
	}
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

// MarkInReview moves a story to in_review and records its PR URL.
func (p *fakeStoryProvider) MarkInReview(_ context.Context, id, prURL string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	s, ok := p.stories[id]
	if !ok {
		return nil
	}
	s.status = "in_review"
	s.prURL = prURL
	return nil
}

// InReview returns stories whose status is "in_review".
func (p *fakeStoryProvider) InReview(_ context.Context) ([]NativeTicket, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []NativeTicket
	for _, id := range p.order {
		s := p.stories[id]
		if s.status != "in_review" {
			continue
		}
		out = append(out, NativeTicket{ID: s.id, Title: s.title, Status: s.status,
			RunID: s.runID, Deps: s.deps, SprintID: s.sprintID, PRURL: s.prURL, Repo: s.repo, ProjectID: s.projectID})
	}
	return out, nil
}

// GetStory returns a minimal story (title only) for the fake.
func (p *fakeStoryProvider) GetStory(_ context.Context, id string) (NativeStory, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	s, ok := p.stories[id]
	if !ok {
		return NativeStory{}, fmt.Errorf("story %s not found", id)
	}
	return NativeStory{ID: id, Title: s.title, Body: s.body, Accept: s.accept, Repo: s.repo, Owner: s.owner, ProjectID: s.projectID}, nil
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
			out = append(out, NativeSprint{ID: sid, Name: sid, ProjectID: ms[0].projectID})
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
		out = append(out, NativeStory{ID: s.id, Title: s.title, Body: s.body, Accept: s.accept, Repo: s.repo, Owner: s.owner, ProjectID: s.projectID})
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

// MarkSprintInReview flips every running story in the sprint to in_review and
// records the shared PR URL on each.
func (p *fakeStoryProvider) MarkSprintInReview(_ context.Context, sprintID, prURL string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, id := range p.order {
		s := p.stories[id]
		if s.sprintID == sprintID && s.status == "running" {
			s.status = "in_review"
			s.prURL = prURL
		}
	}
	return nil
}

// MarkSprintDone flips every running or in_review story in the sprint to done
// (matches the store: the merge-gated path advances from in_review).
func (p *fakeStoryProvider) MarkSprintDone(_ context.Context, sprintID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, id := range p.order {
		s := p.stories[id]
		if s.sprintID == sprintID && (s.status == "running" || s.status == "in_review") {
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

// ---- fakeMergeChecker -------------------------------------------------------

// fakeMergeChecker is an in-memory MergeChecker. merged[number] reports a PR as
// merged; MergePR records the merge and flips merged[number] true so a follow-up
// PRMerged sees it. mergeCalls/mergedNumbers let tests assert auto-merge behavior.
type fakeMergeChecker struct {
	mu            sync.Mutex
	merged        map[int]bool
	closed        map[int]bool // PR number → closed-unmerged (H2 terminal)
	mergeCalls    int
	mergedNumbers []int
	failMerge     bool // when true, MergePR returns an error (auto-merge failure path)
	gateMissing   bool // when true, FileOnBranch reports the gate ABSENT on dev (#19)
	fileChecks    int  // counts FileOnBranch calls (assert the #19 guard ran)
}

func newFakeMergeChecker() *fakeMergeChecker {
	return &fakeMergeChecker{merged: map[int]bool{}, closed: map[int]bool{}}
}

// PRClosed implements the optional PRStateChecker interface: reports a PR closed
// without merging so the scheduler can detect it as terminal (H2).
func (m *fakeMergeChecker) PRClosed(_ context.Context, _ string, number int) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closed[number], nil
}

func (m *fakeMergeChecker) PRMerged(_ context.Context, _ string, number int) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.merged[number], nil
}

// FileOnBranch implements the optional BranchFileChecker (#19). Defaults to
// present (gateMissing=false) so existing sprint-firing tests are unaffected;
// the #19 test sets gateMissing=true to assert the scheduler defers firing.
func (m *fakeMergeChecker) FileOnBranch(_ context.Context, _, _, _ string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.fileChecks++
	return !m.gateMissing, nil
}

func (m *fakeMergeChecker) MergePR(_ context.Context, _ string, number int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mergeCalls++
	if m.failMerge {
		return fmt.Errorf("merge %d failed", number)
	}
	m.merged[number] = true
	m.mergedNumbers = append(m.mergedNumbers, number)
	return nil
}

// ---- tests ------------------------------------------------------------------

// TestNativeReadyStoriesGetFired: a backlog story with no deps is fired and
// marked running with the returned run_id.
func TestNativeReadyStoriesGetFired(t *testing.T) {
	provider := newFakeProvider(
		&fakeStory{id: "S1", title: "first story", status: "backlog"},
	)
	cp := &fakeControlPlane{}
	sched := NewNativeScheduler(provider, cp, "dev", nil)

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

// TestNativeRunningStoryMovesToInReviewWhenRunDone: a story in "running" state
// whose run reaches DONE is moved to in_review (PR opened, not yet merged) by the
// next RunOnce cycle — under merge gating a finished run no longer means done.
func TestNativeRunningStoryMovesToInReviewWhenRunDone(t *testing.T) {
	provider := newFakeProvider(
		&fakeStory{id: "S1", title: "already running", status: "running", runID: "run-42"},
	)
	cp := &fakeControlPlane{}
	cp.setStatus("run-42", "DONE")
	cp.setPRURL("run-42", "https://github.com/acme/x/pull/5")

	sched := NewNativeScheduler(provider, cp, "dev", nil)

	if _, err := sched.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	if provider.statusOf("S1") != "in_review" {
		t.Errorf("S1 status: want in_review, got %s", provider.statusOf("S1"))
	}
}

// TestNativeRunningStoryUntouchedWhileRunning: a story whose run is still
// RUNNING must stay in "running" status — not advanced to "done".
func TestNativeRunningStoryUntouchedWhileRunning(t *testing.T) {
	provider := newFakeProvider(
		&fakeStory{id: "S1", title: "in flight", status: "running", runID: "run-99"},
	)
	cp := &fakeControlPlane{} // default status → "RUNNING"

	sched := NewNativeScheduler(provider, cp, "dev", nil)

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
	sched := NewNativeScheduler(provider, cp, "dev", nil)

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

// TestNativeMergeGatedDepUnblock: the core merge-gated lifecycle. Completing S1's
// run only moves it to in_review (PR opened) — S2 (which depends on S1) stays
// blocked until S1's PR is MERGED. Only after the merge does S1 → done and S2 fire.
// Run in auto merge mode so the scheduler merges the PR itself.
func TestNativeMergeGatedDepUnblock(t *testing.T) {
	provider := newFakeProvider(
		&fakeStory{id: "S1", title: "dep", status: "backlog", repo: "https://github.com/acme/x"},
		&fakeStory{id: "S2", title: "consumer", status: "backlog", deps: []string{"S1"},
			repo: "https://github.com/acme/x"},
	)
	cp := &fakeControlPlane{mergeMode: "auto"}
	gh := newFakeMergeChecker()
	sched := NewNativeScheduler(provider, cp, "dev", gh)
	ctx := context.Background()

	// Cycle 1: S1 has no deps → fires; S2 is blocked (dep not done).
	if _, err := sched.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if cp.firedCount() != 1 {
		t.Fatalf("cycle 1: expected 1 fired (S1 only), got %d", cp.firedCount())
	}
	if provider.statusOf("S2") != "backlog" {
		t.Errorf("S2 after cycle 1: want backlog (blocked), got %s", provider.statusOf("S2"))
	}

	// S1's run finishes and opens a PR. The run reaching DONE moves S1 to
	// in_review — NOT done — so S2 must STILL be blocked this cycle.
	s1RunID := provider.runIDOf("S1")
	cp.setStatus(s1RunID, "DONE")
	cp.setPRURL(s1RunID, "https://github.com/acme/x/pull/11")

	// Cycle 2: reconcile runs before completion-handling, so S1 (still running at
	// the top of this cycle) only reaches in_review here — no merge yet, S2 blocked.
	if _, err := sched.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if gh.mergeCalls != 0 {
		t.Fatalf("PR should not merge the cycle it becomes in_review, got %d", gh.mergeCalls)
	}
	if provider.statusOf("S1") != "in_review" {
		t.Errorf("S1 after run DONE: want in_review, got %s", provider.statusOf("S1"))
	}
	if provider.statusOf("S2") != "backlog" {
		t.Errorf("S2 while S1 in_review: want backlog (blocked), got %s", provider.statusOf("S2"))
	}

	// Cycle 3: reconcile now sees S1 in_review with an open PR → auto-merges it,
	// advances S1 to done, and S2 (unblocked) fires.
	if _, err := sched.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if gh.mergeCalls != 1 {
		t.Fatalf("auto mode should merge S1's PR exactly once, got %d", gh.mergeCalls)
	}
	if provider.statusOf("S1") != "done" {
		t.Errorf("S1 after PR merged: want done, got %s", provider.statusOf("S1"))
	}
	if provider.statusOf("S2") != "running" {
		t.Errorf("S2 after S1 merged: want running, got %s", provider.statusOf("S2"))
	}
	if cp.firedCount() != 2 {
		t.Fatalf("after S1 merged, S2 should fire; fired=%d", cp.firedCount())
	}
}

// TestNativeManualMergeGate: in manual mode, a DONE run moves the story to
// in_review and it STAYS there (the scheduler never merges); only when a human
// merges the PR on GitHub (PRMerged flips true) does the next cycle advance it to
// done and unblock the dependent.
func TestNativeManualMergeGate(t *testing.T) {
	provider := newFakeProvider(
		&fakeStory{id: "S1", title: "dep", status: "backlog", repo: "https://github.com/acme/x"},
		&fakeStory{id: "S2", title: "consumer", status: "backlog", deps: []string{"S1"},
			repo: "https://github.com/acme/x"},
	)
	cp := &fakeControlPlane{mergeMode: "manual"}
	gh := newFakeMergeChecker()
	sched := NewNativeScheduler(provider, cp, "dev", gh)
	ctx := context.Background()

	// Fire S1, finish its run, open a PR.
	if _, err := sched.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	s1RunID := provider.runIDOf("S1")
	cp.setStatus(s1RunID, "DONE")
	cp.setPRURL(s1RunID, "https://github.com/acme/x/pull/22")

	// Cycle: S1 → in_review. Manual mode → no merge, stays in_review, S2 blocked.
	if _, err := sched.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if gh.mergeCalls != 0 {
		t.Fatalf("manual mode must NOT merge; got %d merge calls", gh.mergeCalls)
	}
	if provider.statusOf("S1") != "in_review" {
		t.Errorf("S1 manual: want in_review, got %s", provider.statusOf("S1"))
	}
	if provider.statusOf("S2") != "backlog" {
		t.Errorf("S2 while S1 in_review: want backlog (blocked), got %s", provider.statusOf("S2"))
	}
	// Another cycle with the PR still open changes nothing.
	if _, err := sched.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if provider.statusOf("S1") != "in_review" {
		t.Errorf("S1 should stay in_review until merged, got %s", provider.statusOf("S1"))
	}

	// A human merges the PR on GitHub → PRMerged now reports true.
	gh.merged[22] = true
	if _, err := sched.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if provider.statusOf("S1") != "done" {
		t.Errorf("S1 after human merge: want done, got %s", provider.statusOf("S1"))
	}
	if provider.statusOf("S2") != "running" {
		t.Errorf("S2 after S1 merged: want running, got %s", provider.statusOf("S2"))
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

	sched := NewNativeScheduler(provider, cp, "dev", nil)

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

	sched := NewNativeScheduler(provider, cp, "dev", nil)

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

	sched1 := NewNativeScheduler(provider, cp, "dev", nil)
	sched2 := NewNativeScheduler(provider, cp, "dev", nil)
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
	sched := NewNativeScheduler(provider, cp, "factory", nil)

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
	sched := NewNativeScheduler(provider, cp, "factory", nil)
	if _, err := sched.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if cp.firedCount() != 0 {
		t.Fatalf("want 0 fired (sprint not all-backlog), got %d", cp.firedCount())
	}
}

// TestSprintModeDoneMovesAllToInReview: when the sprint's shared run reaches DONE,
// ALL its stories move to in_review (the single sprint PR is opened, not merged).
func TestSprintModeDoneMovesAllToInReview(t *testing.T) {
	provider := newFakeProvider(
		&fakeStory{id: "A", title: "A", status: "running", sprintID: "SP1", runID: "run-sp", repo: "https://github.com/acme/x"},
		&fakeStory{id: "B", title: "B", status: "running", sprintID: "SP1", runID: "run-sp", repo: "https://github.com/acme/x"},
	)
	cp := sprintMode()
	cp.setStatus("run-sp", "DONE")
	cp.setPRURL("run-sp", "https://github.com/acme/x/pull/9")
	sched := NewNativeScheduler(provider, cp, "factory", nil)

	if _, err := sched.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"A", "B"} {
		if provider.statusOf(id) != "in_review" {
			t.Errorf("%s: want in_review, got %s", id, provider.statusOf(id))
		}
	}
}

// TestSprintModeMergeGatedToDone: the whole merge-gated sprint flow. A DONE sprint
// run moves all stories to in_review with ONE shared PR; in auto mode the next
// cycle merges that PR ONCE (not once per story) and advances ALL stories to done.
func TestSprintModeMergeGatedToDone(t *testing.T) {
	provider := newFakeProvider(
		&fakeStory{id: "A", title: "A", status: "running", sprintID: "SP1", runID: "run-sp", repo: "https://github.com/acme/x"},
		&fakeStory{id: "B", title: "B", status: "running", sprintID: "SP1", runID: "run-sp", repo: "https://github.com/acme/x"},
	)
	cp := &fakeControlPlane{execUnit: "sprint", mergeMode: "auto"}
	cp.setStatus("run-sp", "DONE")
	cp.setPRURL("run-sp", "https://github.com/acme/x/pull/9")
	gh := newFakeMergeChecker()
	sched := NewNativeScheduler(provider, cp, "factory", gh)
	ctx := context.Background()

	// Cycle 1: DONE run → all stories in_review (PR opened, reconcile sees it open).
	if _, err := sched.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	// Cycle 2: reconcile merges the single PR and advances both stories to done.
	if _, err := sched.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if gh.mergeCalls != 1 {
		t.Fatalf("the sprint's single PR must be merged exactly once, got %d", gh.mergeCalls)
	}
	for _, id := range []string{"A", "B"} {
		if provider.statusOf(id) != "done" {
			t.Errorf("%s: want done after merge, got %s", id, provider.statusOf(id))
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
	sched := NewNativeScheduler(provider, cp, "factory", nil)

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
	sched := NewNativeScheduler(provider, cp, "factory", nil)
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

// ---- Lane-aware routing (B1) ------------------------------------------------

// TestStoryModePassesOwnerAsAgent: in story mode the story's owner is plumbed
// into the run payload as `agent`, so the runner routes to that specialist.
func TestStoryModePassesOwnerAsAgent(t *testing.T) {
	provider := newFakeProvider(
		&fakeStory{id: "S1", title: "backend story", status: "backlog", owner: "python-dev"},
	)
	cp := &fakeControlPlane{} // default execution_unit "" → sprint; force story below
	cp.execUnit = "story"
	sched := NewNativeScheduler(provider, cp, "factory", nil)

	if _, err := sched.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if cp.firedCount() != 1 {
		t.Fatalf("want 1 fired run, got %d", cp.firedCount())
	}
	if got := cp.payloadOf(0)["agent"]; got != "python-dev" {
		t.Errorf("payload agent: got %v, want python-dev", got)
	}
}

// TestStoryModeEmptyOwnerYieldsEmptyAgent: a story with no owner sends an empty
// `agent`, which the runner treats as "use the workflow's default (dev)".
func TestStoryModeEmptyOwnerYieldsEmptyAgent(t *testing.T) {
	provider := newFakeProvider(
		&fakeStory{id: "S1", title: "unowned story", status: "backlog"},
	)
	cp := &fakeControlPlane{execUnit: "story"}
	sched := NewNativeScheduler(provider, cp, "factory", nil)

	if _, err := sched.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := cp.payloadOf(0)["agent"]; got != "" {
		t.Errorf("payload agent: got %v, want empty (falls back to dev)", got)
	}
}

// TestSprintModeUsesCommonOwner: a mono-lane sprint (all stories share an owner)
// fires with that owner as the run's `agent`.
func TestSprintModeUsesCommonOwner(t *testing.T) {
	provider := newFakeProvider(
		&fakeStory{id: "A", title: "A", status: "backlog", sprintID: "SP1", owner: "react-dev"},
		&fakeStory{id: "B", title: "B", status: "backlog", sprintID: "SP1", owner: "react-dev", deps: []string{"A"}},
	)
	cp := sprintMode()
	sched := NewNativeScheduler(provider, cp, "factory", nil)

	if _, err := sched.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if cp.firedCount() != 1 {
		t.Fatalf("want 1 fired run, got %d", cp.firedCount())
	}
	if got := cp.payloadOf(0)["agent"]; got != "react-dev" {
		t.Errorf("payload agent: got %v, want react-dev", got)
	}
}

// TestSprintModeMixedOwnersFallsBackToEmpty: a multi-lane sprint (stories with
// different owners) falls back to "" — the runner then uses the default "dev".
// Mixed-lane sub-batching is a B2 concern.
func TestSprintModeMixedOwnersFallsBackToEmpty(t *testing.T) {
	provider := newFakeProvider(
		&fakeStory{id: "A", title: "A", status: "backlog", sprintID: "SP1", owner: "python-dev"},
		&fakeStory{id: "B", title: "B", status: "backlog", sprintID: "SP1", owner: "react-dev"},
	)
	cp := sprintMode()
	sched := NewNativeScheduler(provider, cp, "factory", nil)

	if _, err := sched.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if cp.firedCount() != 1 {
		t.Fatalf("want 1 fired run, got %d", cp.firedCount())
	}
	if got := cp.payloadOf(0)["agent"]; got != "" {
		t.Errorf("payload agent: got %v, want empty (mixed owners → default dev)", got)
	}
}

// ---- B3: FireRun failure after Claim resets to backlog ----------------------

// failFireCP is a control plane whose FireRun always errors — to exercise the B3
// compensation path (Claim succeeded, FireRun failed → reset to backlog).
type failFireCP struct {
	fakeControlPlane
}

func (f *failFireCP) FireRun(_ context.Context, _ string, _ any) (string, error) {
	return "", fmt.Errorf("fire boom")
}

// TestStoryFireFailResetsToBacklog: in story mode, a claimed story whose FireRun
// fails must be reset to backlog (not stranded running with empty run_id), so it
// becomes Ready again next cycle.
func TestStoryFireFailResetsToBacklog(t *testing.T) {
	provider := newFakeProvider(
		&fakeStory{id: "S1", title: "s", status: "backlog"},
	)
	cp := &failFireCP{}
	sched := NewNativeScheduler(provider, cp, "dev", nil)

	if _, err := sched.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := provider.statusOf("S1"); got != "backlog" {
		t.Errorf("S1 should be reset to backlog after FireRun failure (B3), got %s", got)
	}
}

// TestSprintFireFailResetsToBacklog: in sprint mode, a FireRun failure after
// ClaimSprint resets the whole sprint to backlog.
func TestSprintFireFailResetsToBacklog(t *testing.T) {
	provider := newFakeProvider(
		&fakeStory{id: "A", title: "A", status: "backlog", sprintID: "SP1"},
		&fakeStory{id: "B", title: "B", status: "backlog", sprintID: "SP1"},
	)
	cp := &failFireCP{fakeControlPlane{execUnit: "sprint"}}
	sched := NewNativeScheduler(provider, cp, "factory", nil)

	if _, err := sched.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"A", "B"} {
		if got := provider.statusOf(id); got != "backlog" {
			t.Errorf("%s should be reset to backlog after sprint FireRun failure (B3), got %s", id, got)
		}
	}
}

// ---- B4: a running story stranded with empty run_id is recovered ------------

// TestStrandedRunningStoryRecovered: a story left running with NO run_id (a fire
// that slipped past compensation) must not be skipped forever. The completion loop
// resets it to backlog, after which the SAME cycle re-fires it — so it ends up
// running again WITH a run_id (recovered), never stuck running with an empty one.
func TestStrandedRunningStoryRecovered(t *testing.T) {
	provider := newFakeProvider(
		&fakeStory{id: "S1", title: "s", status: "running", runID: ""}, // stranded
	)
	cp := &fakeControlPlane{}
	sched := NewNativeScheduler(provider, cp, "dev", nil)

	if _, err := sched.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Recovered: it was reset then re-fired in the same cycle → running with a run_id.
	if got := provider.statusOf("S1"); got == "running" && provider.runIDOf("S1") == "" {
		t.Errorf("S1 still stranded (running, empty run_id) — B4 recovery failed")
	}
	if provider.runIDOf("S1") == "" {
		t.Errorf("S1 should have been re-fired with a run_id after recovery, got empty")
	}
}

// TestStrandedRunningSprintRecovered: a sprint whose stories are running with no
// run_id is reset to backlog and re-fired (B4) — ending running WITH a run_id.
func TestStrandedRunningSprintRecovered(t *testing.T) {
	provider := newFakeProvider(
		&fakeStory{id: "A", title: "A", status: "running", sprintID: "SP1", runID: ""},
		&fakeStory{id: "B", title: "B", status: "running", sprintID: "SP1", runID: ""},
	)
	cp := sprintMode()
	sched := NewNativeScheduler(provider, cp, "factory", nil)

	if _, err := sched.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"A", "B"} {
		if provider.statusOf(id) == "running" && provider.runIDOf(id) == "" {
			t.Errorf("%s still stranded (running, empty run_id) — B4 recovery failed", id)
		}
		if provider.runIDOf(id) == "" {
			t.Errorf("%s should have been re-fired with a run_id after recovery, got empty", id)
		}
	}
}

// ---- H1: DONE run whose pr step failed is marked failed, not parked ----------

// TestDoneWithFailedPRStepMarksFailed: a story whose run is DONE but whose pr step
// FAILED (no usable PR) must be marked failed — NOT parked in_review with an empty
// PR (which would hang forever). Requires a gh client (merge loop enabled).
func TestDoneWithFailedPRStepMarksFailed(t *testing.T) {
	provider := newFakeProvider(
		&fakeStory{id: "S1", title: "s", status: "running", runID: "run-1", repo: "https://github.com/acme/x"},
	)
	cp := &fakeControlPlane{}
	cp.setStatus("run-1", "DONE")
	cp.setPRStepFailed("run-1") // DONE run, pr step failed → no usable PR
	gh := newFakeMergeChecker()
	sched := NewNativeScheduler(provider, cp, "dev", gh)

	if _, err := sched.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := provider.statusOf("S1"); got != "failed" {
		t.Errorf("DONE-with-failed-pr-step should be failed (H1), got %s", got)
	}
}

// TestDoneWithNoPRMarksFailedWhenMergeEnabled: a DONE run that produced no PR at
// all, with the merge loop enabled, can never be merged → mark failed (H1).
func TestDoneWithNoPRMarksFailedWhenMergeEnabled(t *testing.T) {
	provider := newFakeProvider(
		&fakeStory{id: "S1", title: "s", status: "running", runID: "run-1", repo: "https://github.com/acme/x"},
	)
	cp := &fakeControlPlane{}
	cp.setStatus("run-1", "DONE") // no PR URL, no pr step recorded
	gh := newFakeMergeChecker()
	sched := NewNativeScheduler(provider, cp, "dev", gh)

	if _, err := sched.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := provider.statusOf("S1"); got != "failed" {
		t.Errorf("DONE-with-no-PR (merge enabled) should be failed (H1), got %s", got)
	}
}

// TestDoneWithNoPRParksInReviewWhenLocal: with NO gh client (local mode), a DONE
// run with no PR is the expected terminal — park it in_review (pre-existing
// behavior preserved; H1 only fails when a merge was actually expected).
func TestDoneWithNoPRParksInReviewWhenLocal(t *testing.T) {
	provider := newFakeProvider(
		&fakeStory{id: "S1", title: "s", status: "running", runID: "run-1"},
	)
	cp := &fakeControlPlane{}
	cp.setStatus("run-1", "DONE")
	sched := NewNativeScheduler(provider, cp, "dev", nil) // no merge loop

	if _, err := sched.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := provider.statusOf("S1"); got != "in_review" {
		t.Errorf("local DONE-with-no-PR should park in_review, got %s", got)
	}
}

// ---- H2: closed PR is terminal; auto-merge failures are bounded -------------

// TestClosedPRMarksFailed: a PR a human CLOSED without merging is terminal — the
// reconcile loop marks the story failed and stops polling it.
func TestClosedPRMarksFailed(t *testing.T) {
	provider := newFakeProvider(
		&fakeStory{id: "S1", title: "s", status: "in_review", runID: "run-1",
			repo: "https://github.com/acme/x", prURL: "https://github.com/acme/x/pull/7"},
	)
	cp := &fakeControlPlane{mergeMode: "auto"}
	gh := newFakeMergeChecker()
	gh.closed[7] = true // human closed PR #7 without merging
	sched := NewNativeScheduler(provider, cp, "dev", gh)

	if _, err := sched.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := provider.statusOf("S1"); got != "failed" {
		t.Errorf("closed-unmerged PR should mark story failed (H2), got %s", got)
	}
	if gh.mergeCalls != 0 {
		t.Errorf("a closed PR must not be auto-merged, got %d merge calls", gh.mergeCalls)
	}
}

// TestAutoMergeFailureBounded: repeated auto-merge failures (conflict /
// branch-protection) are bounded — after maxMergeFails cycles the story is marked
// failed instead of retrying forever.
func TestAutoMergeFailureBounded(t *testing.T) {
	provider := newFakeProvider(
		&fakeStory{id: "S1", title: "s", status: "in_review", runID: "run-1",
			repo: "https://github.com/acme/x", prURL: "https://github.com/acme/x/pull/7"},
	)
	cp := &fakeControlPlane{mergeMode: "auto"}
	gh := newFakeMergeChecker()
	gh.failMerge = true // every MergePR fails (e.g. conflict)
	sched := NewNativeScheduler(provider, cp, "dev", gh)
	ctx := context.Background()

	// Drive cycles until the bound is hit. The story should NOT be failed before the
	// threshold, and SHOULD be failed at/after it.
	for i := 0; i < maxMergeFails-1; i++ {
		if _, err := sched.RunOnce(ctx); err != nil {
			t.Fatal(err)
		}
		if got := provider.statusOf("S1"); got != "in_review" {
			t.Fatalf("before bound (cycle %d) story should still be in_review, got %s", i+1, got)
		}
	}
	// The maxMergeFails-th failing attempt trips the bound.
	if _, err := sched.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got := provider.statusOf("S1"); got != "failed" {
		t.Errorf("after %d failed auto-merges the story should be failed (H2), got %s", maxMergeFails, got)
	}
	if gh.mergeCalls != maxMergeFails {
		t.Errorf("auto-merge should be attempted exactly %d times, got %d", maxMergeFails, gh.mergeCalls)
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

// ---- per-project scheduling (audit A2) --------------------------------------

// TestPerProjectExecutionUnitInOneCycle: two projects with DIFFERENT
// execution_unit are both handled correctly in the SAME RunOnce cycle — project
// "py" on story mode fires one run per ready story; project "node" on sprint mode
// fires ONE goal-mode run for its whole sprint. This is the core A2 guarantee.
func TestPerProjectExecutionUnitInOneCycle(t *testing.T) {
	provider := newFakeProvider(
		// project "py": two loose backlog stories → story mode → two runs.
		&fakeStory{id: "PY1", title: "py one", status: "backlog", projectID: "py", repo: "github.com/acme/py"},
		&fakeStory{id: "PY2", title: "py two", status: "backlog", projectID: "py", repo: "github.com/acme/py"},
		// project "node": one sprint with two stories → sprint mode → ONE run.
		&fakeStory{id: "ND1", title: "nd one", status: "backlog", projectID: "node", sprintID: "NSP", repo: "github.com/acme/node"},
		&fakeStory{id: "ND2", title: "nd two", status: "backlog", projectID: "node", sprintID: "NSP", repo: "github.com/acme/node", deps: []string{"ND1"}},
	)
	cp := &fakeControlPlane{}
	cp.setProjectMode("py", "story", "manual")
	cp.setProjectMode("node", "sprint", "auto")
	sched := NewNativeScheduler(provider, cp, "factory", nil)

	if _, err := sched.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	// 3 runs total: PY1, PY2 (story mode) + 1 sprint run for NSP (sprint mode).
	if cp.firedCount() != 3 {
		t.Fatalf("want 3 runs (2 story + 1 sprint), got %d", cp.firedCount())
	}

	// Every fired payload carries the right project_id.
	storyRuns, sprintRuns := 0, 0
	for i := 0; i < cp.firedCount(); i++ {
		p := cp.payloadOf(i)
		if _, isSprint := p["sprint_id"]; isSprint {
			sprintRuns++
			if p["project_id"] != "node" {
				t.Errorf("sprint run project_id: got %v, want node", p["project_id"])
			}
		} else {
			storyRuns++
			if p["project_id"] != "py" {
				t.Errorf("story run project_id: got %v, want py", p["project_id"])
			}
		}
	}
	if storyRuns != 2 || sprintRuns != 1 {
		t.Fatalf("want 2 story runs + 1 sprint run, got %d story / %d sprint", storyRuns, sprintRuns)
	}

	// node's sprint stories are all claimed running and share one run_id.
	if provider.runIDOf("ND1") != provider.runIDOf("ND2") || provider.runIDOf("ND1") == "" {
		t.Errorf("node sprint stories should share one run_id, got %q / %q", provider.runIDOf("ND1"), provider.runIDOf("ND2"))
	}
}

// TestPerProjectMergeModeInOneCycle: in ONE reconcile cycle a project on "auto"
// has its reviewed PR merged by the scheduler, while a project on "manual" is left
// for a human — both in the same cycle (audit A2).
func TestPerProjectMergeModeInOneCycle(t *testing.T) {
	provider := newFakeProvider(
		// auto-project story in_review with an open PR #10 → scheduler merges it.
		&fakeStory{id: "AUTO", title: "auto", status: "in_review", projectID: "pa", repo: "github.com/acme/a", runID: "rA", prURL: "github.com/acme/a/pull/10"},
		// manual-project story in_review with an open PR #20 → must NOT be merged.
		&fakeStory{id: "MAN", title: "man", status: "in_review", projectID: "pm", repo: "github.com/acme/m", runID: "rM", prURL: "github.com/acme/m/pull/20"},
	)
	cp := &fakeControlPlane{}
	cp.setProjectMode("pa", "story", "auto")
	cp.setProjectMode("pm", "story", "manual")
	gh := newFakeMergeChecker() // neither PR is merged yet

	sched := NewNativeScheduler(provider, cp, "factory", gh)
	if _, err := sched.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Exactly the auto-project's PR (#10) is merged; the manual one is untouched.
	if gh.mergeCalls != 1 || len(gh.mergedNumbers) != 1 || gh.mergedNumbers[0] != 10 {
		t.Fatalf("auto project PR #10 should be the only merge, got calls=%d numbers=%v", gh.mergeCalls, gh.mergedNumbers)
	}
	if got := provider.statusOf("AUTO"); got != "done" {
		t.Errorf("auto-merged story should be done, got %s", got)
	}
	if got := provider.statusOf("MAN"); got != "in_review" {
		t.Errorf("manual story must stay in_review (await human merge), got %s", got)
	}
}

// TestNativeSprintDefersUntilDocsOnDev reproduces #19: a ready sprint must NOT
// fire until the project's design docs (.vibeforge-gate) have been merged to dev.
// With the gate absent the scheduler defers (no fire, story stays backlog); once
// the docs PR lands the same sprint fires on the next cycle.
func TestNativeSprintDefersUntilDocsOnDev(t *testing.T) {
	provider := newFakeProvider(
		&fakeStory{id: "S1", title: "foundation", status: "backlog",
			repo: "https://github.com/acme/x", sprintID: "SP1", projectID: "p1"},
	)
	cp := &fakeControlPlane{execUnit: "sprint"}
	gh := newFakeMergeChecker()
	gh.gateMissing = true // docs_pr not merged yet → dev has no gate
	sched := NewNativeScheduler(provider, cp, "dev", gh)
	ctx := context.Background()

	if _, err := sched.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if cp.firedCount() != 0 {
		t.Fatalf("sprint fired before docs on dev: got %d fires, want 0", cp.firedCount())
	}
	if gh.fileChecks == 0 {
		t.Fatalf("#19 guard never ran — FileOnBranch was not consulted")
	}
	if got := provider.statusOf("S1"); got != "backlog" {
		t.Fatalf("deferred sprint's story should stay backlog, got %s", got)
	}

	// Human merges the docs PR → dev now has the gate → the sprint fires.
	gh.gateMissing = false
	if _, err := sched.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if cp.firedCount() != 1 {
		t.Fatalf("sprint did not fire after docs landed on dev: got %d fires, want 1", cp.firedCount())
	}
}

// TestNativeResyncsFailedStoryWithRevivedRun reproduces R3: a story marked failed
// whose run later REVIVED (the reaper requeued a stale step → run back to RUNNING)
// must be re-synced to running. A story whose run is genuinely terminal stays failed.
func TestNativeResyncsFailedStoryWithRevivedRun(t *testing.T) {
	provider := newFakeProvider(
		&fakeStory{id: "S1", title: "revived", status: "failed", runID: "run-alive"},
		&fakeStory{id: "S2", title: "really dead", status: "failed", runID: "run-dead"},
		&fakeStory{id: "S3", title: "design fail", status: "failed", runID: ""}, // never fired
	)
	cp := &fakeControlPlane{statuses: map[string]string{
		"run-alive": "RUNNING", // revived
		"run-dead":  "FAILED",  // genuinely terminal
	}}
	sched := NewNativeScheduler(provider, cp, "dev", nil)

	if _, err := sched.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := provider.statusOf("S1"); got != "running" {
		t.Fatalf("S1 (failed story, run RUNNING) should re-sync to running, got %s", got)
	}
	if got := provider.statusOf("S2"); got != "failed" {
		t.Fatalf("S2 (run genuinely FAILED) must stay failed, got %s", got)
	}
	if got := provider.statusOf("S3"); got != "failed" {
		t.Fatalf("S3 (never fired, no run) must stay failed, got %s", got)
	}
}
