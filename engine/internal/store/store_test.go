package store

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func seedRunWithTask(t *testing.T, s *Store, runID, taskID string) *Task {
	t.Helper()
	if _, err := s.CreateRun(runID, "wf", "", "{}"); err != nil {
		t.Fatalf("create run: %v", err)
	}
	task := &Task{ID: taskID, RunID: runID, WorkflowID: "wf", StepID: "s1", Type: "echo"}
	if err := s.EnqueueTask(task); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	return task
}

// TestConcurrentClaimNoDoubleClaim: N goroutines race to claim from a queue of M
// tasks. Each task must be claimed by exactly one worker (zero double-claims).
func TestConcurrentClaimNoDoubleClaim(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.CreateRun("run1", "wf", "", "{}"); err != nil {
		t.Fatal(err)
	}
	const ntasks = 50
	for i := 0; i < ntasks; i++ {
		if err := s.EnqueueTask(&Task{
			ID: fmt.Sprintf("t%02d", i), RunID: "run1", WorkflowID: "wf",
			StepID: fmt.Sprintf("s%02d", i), Type: "echo",
		}); err != nil {
			t.Fatal(err)
		}
	}

	const nworkers = 16
	var mu sync.Mutex
	claimed := map[string]int{} // taskID -> times claimed
	var total int64
	var wg sync.WaitGroup
	for w := 0; w < nworkers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			worker := fmt.Sprintf("w%d", id)
			for {
				task, err := s.Claim(worker)
				if err != nil {
					t.Errorf("claim error: %v", err)
					return
				}
				if task == nil {
					return // queue drained
				}
				atomic.AddInt64(&total, 1)
				mu.Lock()
				claimed[task.ID]++
				mu.Unlock()
			}
		}(w)
	}
	wg.Wait()

	if total != ntasks {
		t.Fatalf("expected %d total claims, got %d", ntasks, total)
	}
	for id, n := range claimed {
		if n != 1 {
			t.Fatalf("task %s claimed %d times (double-claim!)", id, n)
		}
	}
	if len(claimed) != ntasks {
		t.Fatalf("expected %d distinct tasks claimed, got %d", ntasks, len(claimed))
	}
}

// TestIllegalTransitionRejected: the state machine rejects transitions not in
// the legal table (e.g. QUEUED->DONE directly, or anything out of a terminal).
func TestIllegalTransitionRejected(t *testing.T) {
	s := newTestStore(t)
	task := seedRunWithTask(t, s, "run1", "t1")

	// QUEUED -> DONE is illegal (must go through RUNNING).
	err := s.Transition(task.ID, -1, StatusDone, nil, "")
	if err == nil || !isIllegal(err) {
		t.Fatalf("expected illegal transition QUEUED->DONE, got %v", err)
	}

	// Legal: claim -> RUNNING, then RUNNING -> DONE.
	got, err := s.Claim("w1")
	if err != nil || got == nil {
		t.Fatalf("claim: %v", err)
	}
	if err := s.Transition(got.ID, got.Fence, StatusDone, map[string]any{"ok": true}, ""); err != nil {
		t.Fatalf("RUNNING->DONE should be legal: %v", err)
	}
	// DONE is terminal: DONE -> RUNNING is illegal.
	if err := s.Transition(got.ID, -1, StatusRunning, nil, ""); !isIllegal(err) {
		t.Fatalf("expected illegal transition out of terminal DONE, got %v", err)
	}
}

func isIllegal(err error) bool {
	return err != nil && (err == ErrIllegalTransition || contains(err.Error(), "illegal state transition"))
}
func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

// TestStaleFenceRejected: after requeue_stale rotates the fence, the original
// worker's report/heartbeat (old fence) is rejected.
func TestStaleFenceRejected(t *testing.T) {
	s := newTestStore(t)
	// Controllable clock.
	var nowMs int64 = 1_000_000
	s.Now = func() time.Time { return time.UnixMilli(atomic.LoadInt64(&nowMs)) }

	seedRunWithTask(t, s, "run1", "t1")
	claimed, err := s.Claim("worker-A")
	if err != nil || claimed == nil {
		t.Fatalf("claim: %v", err)
	}
	oldFence := claimed.Fence

	// Advance the clock past the stale window and reap.
	atomic.AddInt64(&nowMs, 60_000)
	n, err := s.RequeueStale(30_000)
	if err != nil {
		t.Fatalf("requeue: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 requeued, got %d", n)
	}

	// Zombie worker-A still thinks it owns the task with the old fence.
	if err := s.Heartbeat(claimed.ID, oldFence); !isStale(err) {
		t.Fatalf("expected stale-fence on heartbeat, got %v", err)
	}
	if err := s.Transition(claimed.ID, oldFence, StatusDone, nil, ""); !isStale(err) {
		t.Fatalf("expected stale-fence on report, got %v", err)
	}

	// A fresh claim gets a higher fence and can complete.
	reclaimed, err := s.Claim("worker-B")
	if err != nil || reclaimed == nil {
		t.Fatalf("reclaim: %v", err)
	}
	if reclaimed.Fence <= oldFence {
		t.Fatalf("expected rotated fence > %d, got %d", oldFence, reclaimed.Fence)
	}
	if err := s.Transition(reclaimed.ID, reclaimed.Fence, StatusDone, nil, ""); err != nil {
		t.Fatalf("fresh worker should complete: %v", err)
	}
}

func isStale(err error) bool {
	return err != nil && (err == ErrStaleFence || contains(err.Error(), "stale fence"))
}

// TestHeartbeatKeepsTaskAlive: a live worker's heartbeat prevents requeue.
func TestHeartbeatKeepsTaskAlive(t *testing.T) {
	s := newTestStore(t)
	var nowMs int64 = 1_000_000
	s.Now = func() time.Time { return time.UnixMilli(atomic.LoadInt64(&nowMs)) }

	seedRunWithTask(t, s, "run1", "t1")
	claimed, err := s.Claim("worker-A")
	if err != nil || claimed == nil {
		t.Fatalf("claim: %v", err)
	}
	// Advance, but heartbeat keeps it fresh.
	atomic.AddInt64(&nowMs, 20_000)
	if err := s.Heartbeat(claimed.ID, claimed.Fence); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	n, err := s.RequeueStale(30_000)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("expected 0 requeued (heartbeat fresh), got %d", n)
	}
}

// TestDependencyGatesClaim: a task depending on another is not claimable until
// the dependency is DONE.
func TestDependencyGatesClaim(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.CreateRun("run1", "wf", "", "{}"); err != nil {
		t.Fatal(err)
	}
	if err := s.EnqueueTask(&Task{ID: "a", RunID: "run1", WorkflowID: "wf", StepID: "first", Type: "echo"}); err != nil {
		t.Fatal(err)
	}
	if err := s.EnqueueTask(&Task{ID: "b", RunID: "run1", WorkflowID: "wf", StepID: "second", Type: "echo", DependsOn: []string{"first"}}); err != nil {
		t.Fatal(err)
	}
	// First claim must be "a" (b is blocked).
	got, err := s.Claim("w1")
	if err != nil || got == nil {
		t.Fatalf("claim: %v", err)
	}
	if got.ID != "a" {
		t.Fatalf("expected to claim a, got %s", got.ID)
	}
	// b still blocked.
	none, err := s.Claim("w2")
	if err != nil {
		t.Fatal(err)
	}
	if none != nil {
		t.Fatalf("expected nothing claimable (b blocked), got %s", none.ID)
	}
	// Finish a -> b becomes claimable.
	if err := s.Transition(got.ID, got.Fence, StatusDone, nil, ""); err != nil {
		t.Fatal(err)
	}
	b, err := s.Claim("w2")
	if err != nil || b == nil {
		t.Fatalf("claim b: %v", err)
	}
	if b.ID != "b" {
		t.Fatalf("expected b, got %s", b.ID)
	}
}

// TestEventsEmittedOnTransition: every transition lands an event on the bus.
func TestEventsEmittedOnTransition(t *testing.T) {
	s := newTestStore(t)
	seedRunWithTask(t, s, "run1", "t1")
	got, _ := s.Claim("w1")
	_ = s.Transition(got.ID, got.Fence, StatusDone, nil, "")

	events, err := s.EventsAfter("run1", 0)
	if err != nil {
		t.Fatal(err)
	}
	// Expect at least: run.created, step.status_changed(QUEUED->RUNNING), step.status_changed(RUNNING->DONE)
	var created, toRunning, toDone bool
	for _, e := range events {
		switch e.Type {
		case EventRunCreated:
			created = true
		case EventStepStatusChange:
			if contains(e.Data, "RUNNING") {
				toRunning = true
			}
			if contains(e.Data, "DONE") {
				toDone = true
			}
		}
	}
	if !created || !toRunning || !toDone {
		t.Fatalf("missing events: created=%v toRunning=%v toDone=%v (got %d events)", created, toRunning, toDone, len(events))
	}
}

// TestRunProjectScoping: runs carry project_id and ListRunsByProject filters by
// it; tasks and events inherit their run's project (audit A1).
func TestRunProjectScoping(t *testing.T) {
	s := newTestStore(t)

	if _, err := s.CreateRun("rA", "wf", "pa", "{}"); err != nil {
		t.Fatalf("create rA: %v", err)
	}
	if _, err := s.CreateRun("rB", "wf", "pb", "{}"); err != nil {
		t.Fatalf("create rB: %v", err)
	}
	// A run created without a project defaults to "default".
	if _, err := s.CreateRun("rD", "wf", "", "{}"); err != nil {
		t.Fatalf("create rD: %v", err)
	}

	pa, err := s.ListRunsByProject("", "pa")
	if err != nil {
		t.Fatal(err)
	}
	if len(pa) != 1 || pa[0].ID != "rA" || pa[0].ProjectID != "pa" {
		t.Fatalf("ListRunsByProject(pa): got %+v, want [rA@pa]", pa)
	}
	if rd, err := s.GetRun("rD"); err != nil || rd.ProjectID != DefaultProjectID {
		t.Fatalf("default-scoped run: got %+v err=%v, want project_id=%q", rd, err, DefaultProjectID)
	}
	all, err := s.ListRuns("")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("ListRuns: got %d, want 3", len(all))
	}

	// A task enqueued under rA inherits project_id "pa", and so do its events.
	if err := s.EnqueueTask(&Task{ID: "tA", RunID: "rA", WorkflowID: "wf", StepID: "s", Type: "echo"}); err != nil {
		t.Fatalf("enqueue tA: %v", err)
	}
	task, err := s.GetTask("tA")
	if err != nil {
		t.Fatal(err)
	}
	if task.ProjectID != "pa" {
		t.Fatalf("task project_id: got %q, want pa", task.ProjectID)
	}
	evs, err := s.EventsAfter("rA", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) == 0 {
		t.Fatal("expected at least the run.created event")
	}
	for _, e := range evs {
		if e.ProjectID != "pa" {
			t.Fatalf("event %s project_id: got %q, want pa", e.Type, e.ProjectID)
		}
	}
}

// TestProjectIDMigrationBackfill: a DB created with the PRE-multi-tenant shape
// (no project_id columns, an existing run row) must upgrade idempotently on Open
// and backfill the legacy row to the default project (audit A1).
func TestProjectIDMigrationBackfill(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")

	// Build the old schema by hand and insert a pre-migration run + task + event.
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = raw.Exec(`
		CREATE TABLE runs (id TEXT PRIMARY KEY, workflow_id TEXT NOT NULL, status TEXT NOT NULL,
			payload TEXT NOT NULL DEFAULT '{}', created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL);
		CREATE TABLE tasks (id TEXT PRIMARY KEY, run_id TEXT NOT NULL, workflow_id TEXT NOT NULL,
			step_id TEXT NOT NULL, type TEXT NOT NULL, status TEXT NOT NULL, payload TEXT NOT NULL DEFAULT '{}',
			result TEXT NOT NULL DEFAULT '{}', error TEXT NOT NULL DEFAULT '', attempts INTEGER NOT NULL DEFAULT 0,
			fence INTEGER NOT NULL DEFAULT 0, depends_on TEXT NOT NULL DEFAULT '[]', wave INTEGER NOT NULL DEFAULT 0,
			claimed_by TEXT NOT NULL DEFAULT '', heartbeat_at INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL);
		CREATE TABLE events (seq INTEGER PRIMARY KEY AUTOINCREMENT, run_id TEXT NOT NULL,
			task_id TEXT NOT NULL DEFAULT '', type TEXT NOT NULL, data TEXT NOT NULL DEFAULT '{}', created_at INTEGER NOT NULL);
		INSERT INTO runs(id, workflow_id, status, payload, created_at, updated_at) VALUES('old-run','wf','DONE','{}',1,1);
		INSERT INTO tasks(id, run_id, workflow_id, step_id, type, status, created_at, updated_at) VALUES('old-task','old-run','wf','s','echo','DONE',1,1);
		INSERT INTO events(run_id, task_id, type, data, created_at) VALUES('old-run','old-task','x','{}',1);
	`)
	if err != nil {
		t.Fatalf("seed legacy schema: %v", err)
	}
	raw.Close()

	// Open via the store: migrations run, legacy rows are backfilled to "default".
	s, err := Open(path)
	if err != nil {
		t.Fatalf("open (migrate): %v", err)
	}
	defer s.Close()

	run, err := s.GetRun("old-run")
	if err != nil {
		t.Fatalf("legacy run lost after migration: %v", err)
	}
	if run.ProjectID != DefaultProjectID {
		t.Errorf("legacy run project_id: got %q, want %q", run.ProjectID, DefaultProjectID)
	}
	task, err := s.GetTask("old-task")
	if err != nil {
		t.Fatalf("legacy task lost: %v", err)
	}
	if task.ProjectID != DefaultProjectID {
		t.Errorf("legacy task project_id: got %q, want %q", task.ProjectID, DefaultProjectID)
	}
	evs, err := s.EventsAfter("old-run", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 || evs[0].ProjectID != DefaultProjectID {
		t.Errorf("legacy event project_id not backfilled: %+v", evs)
	}

	// Re-Open must be idempotent (no duplicate-column failure).
	s2, err := Open(path)
	if err != nil {
		t.Fatalf("re-open should be idempotent: %v", err)
	}
	s2.Close()
}
