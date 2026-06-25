package orchestrator

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// ---- fakes -------------------------------------------------------------------

// fakeGitHub is a hermetic GitHub stub: returns a fixed issue list.
type fakeGitHub struct {
	mu     sync.Mutex
	issues []Issue
}

func (f *fakeGitHub) ListIssues(_ context.Context) ([]Issue, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make([]Issue, len(f.issues))
	copy(cp, f.issues)
	return cp, nil
}

// setIssue replaces (or adds) an issue in the fake.
func (f *fakeGitHub) setIssue(issue Issue) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, iss := range f.issues {
		if iss.Number == issue.Number {
			f.issues[i] = issue
			return
		}
	}
	f.issues = append(f.issues, issue)
}

// fakeControlPlane records fired runs. Thread-safe.
type fakeControlPlane struct {
	mu       sync.Mutex
	runs     []firedRun
	statuses map[string]string // runID → status; absent → "RUNNING"
}

type firedRun struct {
	workflow string
	payload  any
}

func (f *fakeControlPlane) FireRun(_ context.Context, workflow string, payload any) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runs = append(f.runs, firedRun{workflow: workflow, payload: payload})
	return fakeRunID(len(f.runs)), nil
}

// RunStatus returns the simulated status for runID ("RUNNING" by default).
func (f *fakeControlPlane) RunStatus(_ context.Context, runID string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s, ok := f.statuses[runID]; ok {
		return s, nil
	}
	return "RUNNING", nil
}

// setStatus simulates a run reaching a terminal state.
func (f *fakeControlPlane) setStatus(runID, status string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.statuses == nil {
		f.statuses = map[string]string{}
	}
	f.statuses[runID] = status
}

func (f *fakeControlPlane) firedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.runs)
}

func (f *fakeControlPlane) firedWorkflows() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	wf := make([]string, len(f.runs))
	for i, r := range f.runs {
		wf[i] = r.workflow
	}
	return wf
}

func fakeRunID(n int) string {
	return "run-" + itoa(n)
}

// ---- helper -----------------------------------------------------------------

func newTestOrchestrator(t *testing.T, gh GitHub, cp ControlPlane) *Orchestrator {
	t.Helper()
	stateFile := filepath.Join(t.TempDir(), "state.json")
	orch, err := New(Config{
		Workflow:  "dev",
		StateFile: stateFile,
	}, gh, cp)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return orch
}

// ---- tests ------------------------------------------------------------------

// TestDepOrderingBlockedNotFired: an open issue whose dep is also open must NOT
// be fired.
func TestDepOrderingBlockedNotFired(t *testing.T) {
	gh := &fakeGitHub{issues: []Issue{
		{Number: 1, Title: "dep task", State: "open"},
		{Number: 2, Title: "main task", Body: "Depends-on: #1", State: "open"},
	}}
	cp := &fakeControlPlane{}
	orch := newTestOrchestrator(t, gh, cp)

	if err := orch.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Only issue #1 should be fired (it has no deps).
	// Issue #2 must NOT be fired yet (dep #1 is still open).
	if cp.firedCount() != 1 {
		t.Fatalf("expected 1 fired run, got %d", cp.firedCount())
	}
}

// TestDepOrderingFiredAfterDepDone: after dep is marked closed, the blocked
// issue becomes READY and is fired on the next cycle.
func TestDepOrderingFiredAfterDepDone(t *testing.T) {
	gh := &fakeGitHub{issues: []Issue{
		{Number: 1, Title: "dep task", State: "open"},
		{Number: 2, Title: "main task", Body: "Depends-on: #1", State: "open"},
	}}
	cp := &fakeControlPlane{}
	orch := newTestOrchestrator(t, gh, cp)

	// Cycle 1: only #1 fires.
	if err := orch.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if cp.firedCount() != 1 {
		t.Fatalf("cycle 1: expected 1 fired, got %d", cp.firedCount())
	}

	// Simulate dep #1 closing on GitHub.
	gh.setIssue(Issue{Number: 1, Title: "dep task", State: "closed"})

	// Cycle 2: #2 should now fire.
	if err := orch.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if cp.firedCount() != 2 {
		t.Fatalf("cycle 2: expected 2 fired total, got %d", cp.firedCount())
	}
}

// TestIdempotency: running the loop twice must not fire the same issue twice.
func TestIdempotency(t *testing.T) {
	gh := &fakeGitHub{issues: []Issue{
		{Number: 1, Title: "task A", State: "open"},
		{Number: 2, Title: "task B", State: "open"},
	}}
	cp := &fakeControlPlane{}
	orch := newTestOrchestrator(t, gh, cp)

	ctx := context.Background()
	if err := orch.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if err := orch.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}

	// Both issues have no deps so they fire on cycle 1; cycle 2 must not re-fire.
	if cp.firedCount() != 2 {
		t.Fatalf("idempotency: expected 2 total fires, got %d", cp.firedCount())
	}
}

// TestIdempotencyAfterRestart: a new Orchestrator pointed at the same state
// file must not re-fire already-fired issues.
func TestIdempotencyAfterRestart(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "state.json")
	gh := &fakeGitHub{issues: []Issue{
		{Number: 1, Title: "task A", State: "open"},
	}}
	cp := &fakeControlPlane{}

	// First orchestrator instance.
	orch1, err := New(Config{Workflow: "dev", StateFile: stateFile}, gh, cp)
	if err != nil {
		t.Fatal(err)
	}
	if err := orch1.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if cp.firedCount() != 1 {
		t.Fatalf("expected 1 fire, got %d", cp.firedCount())
	}

	// Second orchestrator instance (simulates restart).
	orch2, err := New(Config{Workflow: "dev", StateFile: stateFile}, gh, cp)
	if err != nil {
		t.Fatal(err)
	}
	if err := orch2.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if cp.firedCount() != 1 {
		t.Fatalf("after restart: expected still 1 fire, got %d", cp.firedCount())
	}
}

// TestReadyComputation: an issue with a label "depends:#2" is blocked when #2
// is open, and becomes ready when #2 is closed.
func TestReadyComputation(t *testing.T) {
	gh := &fakeGitHub{issues: []Issue{
		{Number: 2, Title: "dep", State: "open"},
		{Number: 3, Title: "consumer", State: "open", Labels: []string{"depends:#2"}},
	}}
	cp := &fakeControlPlane{}
	orch := newTestOrchestrator(t, gh, cp)

	tickets, err := orch.Tickets(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	statusOf := func(id int) TicketStatus {
		for _, tk := range tickets {
			if tk.ID == id {
				return tk.Status
			}
		}
		t.Fatalf("ticket %d not found", id)
		return ""
	}

	if s := statusOf(2); s != StatusReady {
		t.Errorf("issue 2 status: want ready, got %s", s)
	}
	if s := statusOf(3); s != StatusBlocked {
		t.Errorf("issue 3 status: want blocked, got %s", s)
	}

	// Close dep and recompute.
	gh.setIssue(Issue{Number: 2, Title: "dep", State: "closed"})
	tickets2, err := orch.Tickets(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	statusOf2 := func(id int) TicketStatus {
		for _, tk := range tickets2 {
			if tk.ID == id {
				return tk.Status
			}
		}
		t.Fatalf("ticket %d not found", id)
		return ""
	}
	if s := statusOf2(3); s != StatusReady {
		t.Errorf("after dep closed, issue 3 status: want ready, got %s", s)
	}
}

// TestTicketsJSONEndpoint: GET /tickets returns valid JSON with the expected
// shape, exercised via httptest (no real network).
func TestTicketsJSONEndpoint(t *testing.T) {
	gh := &fakeGitHub{issues: []Issue{
		{Number: 10, Title: "alpha", State: "open"},
		{Number: 11, Title: "beta", State: "closed"},
	}}
	cp := &fakeControlPlane{}
	orch := newTestOrchestrator(t, gh, cp)

	srv := NewHTTPServer(orch)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/tickets")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /tickets: status %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type: want application/json, got %s", ct)
	}

	var body ticketsResp
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Tickets) != 2 {
		t.Fatalf("expected 2 tickets, got %d", len(body.Tickets))
	}

	byID := make(map[int]Ticket)
	for _, tk := range body.Tickets {
		byID[tk.ID] = tk
	}

	if byID[10].Status != StatusReady {
		t.Errorf("ticket 10 status: want ready, got %s", byID[10].Status)
	}
	if byID[11].Status != StatusDone {
		t.Errorf("ticket 11 status: want done, got %s", byID[11].Status)
	}
}

// TestParseDepsLabel: parseDeps correctly handles "depends:ENG-3" and "depends:#5".
func TestParseDepsLabel(t *testing.T) {
	issue := &Issue{
		Number: 99,
		Labels: []string{"depends:ENG-3", "depends:#5"},
	}
	got := parseDeps(issue)
	want := map[int]bool{3: true, 5: true}
	if len(got) != len(want) {
		t.Fatalf("parseDeps: want %v, got %v", want, got)
	}
	for _, n := range got {
		if !want[n] {
			t.Errorf("unexpected dep %d", n)
		}
	}
}

// TestParseDepsBody: parseDeps correctly handles "Depends-on: #12, #13" in body.
func TestParseDepsBody(t *testing.T) {
	issue := &Issue{
		Number: 99,
		Body:   "Some description\nDepends-on: #12, #13\nMore text",
	}
	got := parseDeps(issue)
	want := map[int]bool{12: true, 13: true}
	if len(got) != len(want) {
		t.Fatalf("parseDeps body: want %v, got %v", want, got)
	}
	for _, n := range got {
		if !want[n] {
			t.Errorf("unexpected dep %d", n)
		}
	}
}

// TestWorkflowPassedToControlPlane: the workflow name from config is passed
// through to the control-plane fire call.
func TestWorkflowPassedToControlPlane(t *testing.T) {
	gh := &fakeGitHub{issues: []Issue{
		{Number: 1, Title: "task", State: "open"},
	}}
	cp := &fakeControlPlane{}
	stateFile := filepath.Join(t.TempDir(), "state.json")
	orch, err := New(Config{Workflow: "my-workflow", StateFile: stateFile}, gh, cp)
	if err != nil {
		t.Fatal(err)
	}
	if err := orch.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	wfs := cp.firedWorkflows()
	if len(wfs) != 1 || wfs[0] != "my-workflow" {
		t.Errorf("expected workflow my-workflow, got %v", wfs)
	}
}

// TestStateFilePersistence: MarkFired writes to disk and IsFired reads back.
func TestStateFilePersistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	st, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.MarkFired(42, "run-abc"); err != nil {
		t.Fatal(err)
	}
	if !st.IsFired(42) {
		t.Error("IsFired(42): want true")
	}
	if st.RunID(42) != "run-abc" {
		t.Errorf("RunID(42): want run-abc, got %s", st.RunID(42))
	}

	// Re-load from disk and check.
	st2, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if !st2.IsFired(42) {
		t.Error("after reload, IsFired(42): want true")
	}
	if st2.RunID(42) != "run-abc" {
		t.Errorf("after reload, RunID(42): want run-abc, got %s", st2.RunID(42))
	}
}

// TestStateFileEmpty: LoadState on a non-existent file returns an empty state.
func TestStateFileEmpty(t *testing.T) {
	st, err := LoadState(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if err != nil {
		t.Fatal(err)
	}
	if st.IsFired(1) {
		t.Error("fresh state: IsFired should be false")
	}
}

// TestTicketRunIDPresent: after firing, GET /tickets includes the run_id.
func TestTicketRunIDPresent(t *testing.T) {
	gh := &fakeGitHub{issues: []Issue{
		{Number: 7, Title: "do work", State: "open"},
	}}
	cp := &fakeControlPlane{}
	orch := newTestOrchestrator(t, gh, cp)

	// Fire.
	if err := orch.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	srv := NewHTTPServer(orch)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/tickets")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var body ticketsResp
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Tickets) != 1 {
		t.Fatalf("expected 1 ticket, got %d", len(body.Tickets))
	}
	tk := body.Tickets[0]
	if tk.RunID == "" {
		t.Error("run_id should be set after firing")
	}
	// Status should be "firing" — the run has been launched but the issue is
	// still open on GitHub and not yet marked completed.
	if tk.Status != StatusFiring {
		t.Errorf("status after fire: want firing, got %s", tk.Status)
	}
}

// TestNoNewDepsDepParsedNumerically: body "Depends-on: 12" (no #) is parsed.
func TestDepBodyNoHash(t *testing.T) {
	issue := &Issue{
		Number: 99,
		Body:   "Depends-on: 7, 8",
	}
	got := parseDeps(issue)
	want := map[int]bool{7: true, 8: true}
	if len(got) != len(want) {
		t.Fatalf("parseDeps no-hash: want %v, got %v", want, got)
	}
	for _, n := range got {
		if !want[n] {
			t.Errorf("unexpected dep %d", n)
		}
	}
}

// TestStateFileNonExistentDir: MarkFired fails gracefully when the parent dir
// does not exist (simulates a bad config).
func TestStateFileNonExistentDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subdir", "state.json")
	_, err := LoadState(path)
	// LoadState of a file whose parent does not exist returns a fresh state
	// (IsNotExist on ReadFile). Writing it will fail — that's acceptable and
	// tested separately. Just ensure no panic here.
	if err != nil {
		t.Fatalf("unexpected LoadState error: %v", err)
	}
}

// TestClosedIssueIsNotFired: a closed issue is never fired even if it has no deps.
func TestClosedIssueIsNotFired(t *testing.T) {
	gh := &fakeGitHub{issues: []Issue{
		{Number: 1, Title: "already done", State: "closed"},
	}}
	cp := &fakeControlPlane{}
	orch := newTestOrchestrator(t, gh, cp)

	if err := orch.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if cp.firedCount() != 0 {
		t.Errorf("expected 0 fires for closed issue, got %d", cp.firedCount())
	}
}

// TestStateFileInvalid: LoadState on a corrupt file returns an error, not a
// panic.
func TestStateFileInvalid(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadState(path)
	if err == nil {
		t.Error("expected error for corrupt state file")
	}
}

// TestParseGhIssuesNormalizesState guards the bug found in e2e: `gh` returns
// state in UPPERCASE ("OPEN"/"CLOSED") but the engine's checks are lowercase,
// so without normalization no run would ever fire. parseGhIssues must lowercase.
func TestParseGhIssuesNormalizesState(t *testing.T) {
	out := []byte(`[
		{"number":1,"title":"A","body":"","state":"OPEN","labels":[]},
		{"number":2,"title":"B","body":"","state":"CLOSED","labels":[{"name":"depends:#1"}]}
	]`)
	issues, err := parseGhIssues(out)
	if err != nil {
		t.Fatalf("parseGhIssues: %v", err)
	}
	if len(issues) != 2 {
		t.Fatalf("want 2 issues, got %d", len(issues))
	}
	if issues[0].State != "open" || issues[1].State != "closed" {
		t.Fatalf("state not normalized to lowercase: %q, %q", issues[0].State, issues[1].State)
	}
	if deps := parseDeps(&issues[1]); len(deps) != 1 || deps[0] != 1 {
		t.Fatalf("label dep not parsed: %v", deps)
	}
}

// TestCompletionUnblocksViaRunStatus: the orchestrator advances a ticket from
// "firing" to "done" when its run reaches DONE (without the GitHub issue being
// closed), and that completion unblocks a dependent — all via RunStatus polling.
func TestCompletionUnblocksViaRunStatus(t *testing.T) {
	gh := &fakeGitHub{issues: []Issue{
		{Number: 1, Title: "dep", State: "open"},
		{Number: 2, Title: "consumer", State: "open", Labels: []string{"depends:#1"}},
	}}
	cp := &fakeControlPlane{}
	orch := newTestOrchestrator(t, gh, cp)
	ctx := context.Background()

	// Cycle 1: #1 ready → fires run-1; #2 blocked (dep not done).
	if err := orch.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if cp.firedCount() != 1 {
		t.Fatalf("cycle 1: want 1 fired (only #1), got %d", cp.firedCount())
	}

	// #1's run is still RUNNING → ticket "firing", #2 still blocked, no new fire.
	if err := orch.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if cp.firedCount() != 1 {
		t.Fatalf("while dep RUNNING, #2 should stay blocked; fired=%d", cp.firedCount())
	}

	// Simulate #1's run finishing.
	cp.setStatus(fakeRunID(1), "DONE")

	// Cycle 3: reconcile marks #1 completed → #2 unblocks and fires.
	if err := orch.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if !orch.state.IsCompleted(1) {
		t.Fatalf("#1 should be marked completed after its run is DONE")
	}
	if cp.firedCount() != 2 {
		t.Fatalf("after dep DONE, #2 should fire; fired=%d", cp.firedCount())
	}

	// Ticket #1 now reports "done".
	tickets, err := orch.Tickets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, tk := range tickets {
		if tk.ID == 1 && tk.Status != StatusDone {
			t.Fatalf("ticket #1 status: want done, got %s", tk.Status)
		}
	}
}

// ---- Bug 1: GitHub orchestrator — failed runs surface as StatusFailed --------

// TestFailedRunSurfacesAsFailed: when a fired issue's run reaches FAILED, the
// ticket advances to StatusFailed and is no longer re-polled each cycle.
func TestFailedRunSurfacesAsFailed(t *testing.T) {
	gh := &fakeGitHub{issues: []Issue{
		{Number: 1, Title: "will fail", State: "open"},
	}}
	cp := &fakeControlPlane{}
	orch := newTestOrchestrator(t, gh, cp)
	ctx := context.Background()

	// Cycle 1: #1 fires.
	if err := orch.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if cp.firedCount() != 1 {
		t.Fatalf("want 1 fired run, got %d", cp.firedCount())
	}

	// Simulate the run failing.
	cp.setStatus(fakeRunID(1), "FAILED")

	// Cycle 2: reconcile marks #1 failed.
	if err := orch.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if !orch.state.IsFailed(1) {
		t.Fatal("#1 should be marked failed after its run reaches FAILED")
	}

	// GET /tickets must report StatusFailed for #1.
	tickets, err := orch.Tickets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, tk := range tickets {
		if tk.ID == 1 && tk.Status != StatusFailed {
			t.Errorf("ticket #1 status: want failed, got %s", tk.Status)
		}
	}

	// Cycle 3: #1 is failed — it must NOT be re-fired (IsFailed stops polling).
	if err := orch.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if cp.firedCount() != 1 {
		t.Errorf("failed ticket must not be re-fired; got %d fires", cp.firedCount())
	}
}

// TestCancelledRunSurfacesAsFailed: CANCELLED is a terminal failure like FAILED.
func TestCancelledRunSurfacesAsFailed(t *testing.T) {
	gh := &fakeGitHub{issues: []Issue{
		{Number: 5, Title: "will cancel", State: "open"},
	}}
	cp := &fakeControlPlane{}
	orch := newTestOrchestrator(t, gh, cp)
	ctx := context.Background()

	if err := orch.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	cp.setStatus(fakeRunID(1), "CANCELLED")

	if err := orch.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if !orch.state.IsFailed(5) {
		t.Fatal("#5 should be marked failed after CANCELLED run")
	}

	tickets, err := orch.Tickets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, tk := range tickets {
		if tk.ID == 5 && tk.Status != StatusFailed {
			t.Errorf("ticket #5 status: want failed, got %s", tk.Status)
		}
	}
}
