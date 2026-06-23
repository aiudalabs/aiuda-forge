package store

import (
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
	if _, err := s.CreateRun(runID, "wf", "{}"); err != nil {
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
	if _, err := s.CreateRun("run1", "wf", "{}"); err != nil {
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
	if _, err := s.CreateRun("run1", "wf", "{}"); err != nil {
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
