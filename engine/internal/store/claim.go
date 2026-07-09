package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// Claim atomically picks one ready QUEUED task and moves it to RUNNING for
// workerID. Returns (nil, nil) when nothing is ready. Atomicity comes from the
// BEGIN IMMEDIATE transaction (DSN _txlock=immediate): the write lock is taken
// before any read, so two concurrent claimers can never see the same QUEUED row
// as claimable — exactly the SKIP LOCKED semantics, on sqlite.
//
// "Ready" = QUEUED and every task it depends_on (by step_id, same run) is DONE.
// Ordering: lowest wave first, then creation order (a wave barrier falls out of
// this for free).
func (s *Store) Claim(workerID string) (*Task, error) {
	tx, err := s.db.Begin() // BEGIN IMMEDIATE via DSN — takes the write lock now
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// available_at gates transient-retry backoff (R1): a step requeued after a
	// provider limit is not re-claimable until its backoff elapses.
	rows, err := tx.Query(taskCols+` WHERE status='QUEUED' AND available_at <= ? ORDER BY wave ASC, created_at ASC, id ASC`, s.now())
	if err != nil {
		return nil, err
	}
	candidates := []*Task{}
	for rows.Next() {
		t, err := scanTaskCore(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		candidates = append(candidates, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for _, t := range candidates {
		ready, err := depsSatisfiedTx(tx, t)
		if err != nil {
			return nil, err
		}
		if !ready {
			continue
		}
		now := s.now()
		newFence := t.Fence + 1
		if _, err := tx.Exec(`UPDATE tasks SET status=?, fence=?, attempts=attempts+1, claimed_by=?, heartbeat_at=?, updated_at=? WHERE id=?`,
			string(StatusRunning), newFence, workerID, now, now, t.ID); err != nil {
			return nil, err
		}
		if err := emitTx(tx, t.RunID, t.ID, t.ProjectID, EventStepStatusChange,
			map[string]any{"step": t.StepID, "from": StatusQueued, "to": StatusRunning, "worker": workerID}, now); err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		t.Status = StatusRunning
		t.Fence = newFence
		t.Attempts++
		t.ClaimedBy = workerID
		t.HeartbeatAt = now
		t.UpdatedAt = now
		return t, nil
	}
	return nil, nil
}

// depsSatisfiedTx reports whether all of t's declared dependencies are DONE.
func depsSatisfiedTx(tx *sql.Tx, t *Task) (bool, error) {
	if len(t.DependsOn) == 0 {
		return true, nil
	}
	for _, dep := range t.DependsOn {
		var st Status
		err := tx.QueryRow(`SELECT status FROM tasks WHERE run_id=? AND step_id=? ORDER BY created_at DESC LIMIT 1`, t.RunID, dep).Scan(&st)
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil // dependency step not enqueued yet
		}
		if err != nil {
			return false, err
		}
		if st != StatusDone {
			return false, nil
		}
	}
	return true, nil
}

// Transition moves a task from its current state to `to`, validating both the
// legal-transition table and the fencing token. Pass fence < 0 to skip the
// fence check (control-plane actions like cancel are not fenced). result/errMsg
// are applied when non-nil. This is the single emit point for task transitions.
func (s *Store) Transition(taskID string, fence int64, to Status, result map[string]any, errMsg string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var from Status
	var curFence int64
	var runID, stepID, projectID string
	err = tx.QueryRow(`SELECT status, fence, run_id, step_id, project_id FROM tasks WHERE id=?`, taskID).Scan(&from, &curFence, &runID, &stepID, &projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}

	if fence >= 0 && fence != curFence {
		return fmt.Errorf("%w: task %s presented fence %d, current %d", ErrStaleFence, taskID, fence, curFence)
	}
	if !transitionAllowed(from, to) {
		return fmt.Errorf("%w: task %s %s->%s", ErrIllegalTransition, taskID, from, to)
	}

	now := s.now()
	resultJSON := ""
	if result != nil {
		b, err := json.Marshal(result)
		if err != nil {
			return err
		}
		resultJSON = string(b)
	}
	if resultJSON != "" {
		_, err = tx.Exec(`UPDATE tasks SET status=?, result=?, error=?, updated_at=? WHERE id=?`,
			string(to), resultJSON, errMsg, now, taskID)
	} else {
		_, err = tx.Exec(`UPDATE tasks SET status=?, error=?, updated_at=? WHERE id=?`,
			string(to), errMsg, now, taskID)
	}
	if err != nil {
		return err
	}
	data := map[string]any{"step": stepID, "from": from, "to": to}
	if errMsg != "" {
		data["error"] = errMsg
	}
	if err := emitTx(tx, runID, taskID, projectID, EventStepStatusChange, data, now); err != nil {
		return err
	}
	return tx.Commit()
}

// ResolveAwaiting atomically finds the AWAITING task for (runID, stepID) and
// transitions it to `to` with result/errMsg, in ONE transaction. It returns the
// resolved task (so the caller can advance the flow) and ok=true on success, or
// (nil, false, nil) when no task is AWAITING — the loser of a concurrent
// approve/reject, or an approve racing a cancel. This closes the find-then-act
// window of the old engine path (M3): the read and the write share the write
// lock, so two concurrent resolvers can never both transition the same gate.
func (s *Store) ResolveAwaiting(runID, stepID string, to Status, result map[string]any, errMsg string) (*Task, bool, error) {
	tx, err := s.db.Begin() // BEGIN IMMEDIATE — write lock before the read
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()

	t, err := scanTaskCore(tx.QueryRow(taskCols+` WHERE run_id=? AND step_id=? AND status=? ORDER BY created_at DESC LIMIT 1`,
		runID, stepID, string(StatusAwaiting)))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil // nothing awaiting (already resolved / cancelled / lost the race)
	}
	if err != nil {
		return nil, false, err
	}
	if !transitionAllowed(StatusAwaiting, to) {
		return nil, false, fmt.Errorf("%w: task %s %s->%s", ErrIllegalTransition, t.ID, StatusAwaiting, to)
	}

	now := s.now()
	resultJSON := "{}"
	if result != nil {
		b, err := json.Marshal(result)
		if err != nil {
			return nil, false, err
		}
		resultJSON = string(b)
	}
	if _, err := tx.Exec(`UPDATE tasks SET status=?, result=?, error=?, updated_at=? WHERE id=?`,
		string(to), resultJSON, errMsg, now, t.ID); err != nil {
		return nil, false, err
	}
	data := map[string]any{"step": stepID, "from": StatusAwaiting, "to": to}
	if errMsg != "" {
		data["error"] = errMsg
	}
	if err := emitTx(tx, runID, t.ID, t.ProjectID, EventStepStatusChange, data, now); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	// Reflect the applied transition on the returned task so advance() sees it.
	t.Status = to
	t.Result = resultJSON
	t.Error = errMsg
	t.UpdatedAt = now
	return t, true, nil
}

// AnswerAwaiting atomically resolves the AWAITING (runID, stepID) human_gate as
// ANSWERED — a third verb next to approve/reject. Unlike reject (which resolves
// the gate FAILED so on_fail counts it as a failed attempt), answer resolves the
// gate to DONE: the human is not rejecting the work, only supplying answers to the
// phase's open questions. Because the gate lands DONE (not FAILED), countFailures
// structurally never sees it, so answering does NOT consume the reject on_fail.max
// budget. The caller re-enqueues the phase with the answers as input.
//
// It emits TWO events in the same transaction (single emit point, golden rule #5):
// step.status_changed for the AWAITING->DONE transition, and step.answer carrying
// the text so the live-log/UI can distinguish an answer from a reject. Returns the
// resolved task + ok=true, or (nil,false,nil) if nothing is awaiting (the loser of
// a concurrent resolve, or a racing cancel) — same winner-selection as ResolveAwaiting.
func (s *Store) AnswerAwaiting(runID, stepID, text string) (*Task, bool, error) {
	tx, err := s.db.Begin() // BEGIN IMMEDIATE — write lock before the read
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()

	t, err := scanTaskCore(tx.QueryRow(taskCols+` WHERE run_id=? AND step_id=? AND status=? ORDER BY created_at DESC LIMIT 1`,
		runID, stepID, string(StatusAwaiting)))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil // nothing awaiting (already resolved / cancelled / lost the race)
	}
	if err != nil {
		return nil, false, err
	}
	if !transitionAllowed(StatusAwaiting, StatusDone) {
		return nil, false, fmt.Errorf("%w: task %s %s->%s", ErrIllegalTransition, t.ID, StatusAwaiting, StatusDone)
	}

	now := s.now()
	result := map[string]any{"success": true, "output": map[string]any{"answered": true}, "detail": text}
	b, err := json.Marshal(result)
	if err != nil {
		return nil, false, err
	}
	resultJSON := string(b)
	if _, err := tx.Exec(`UPDATE tasks SET status=?, result=?, error=?, updated_at=? WHERE id=?`,
		string(StatusDone), resultJSON, "", now, t.ID); err != nil {
		return nil, false, err
	}
	if err := emitTx(tx, runID, t.ID, t.ProjectID, EventStepStatusChange,
		map[string]any{"step": stepID, "from": StatusAwaiting, "to": StatusDone, "answered": true}, now); err != nil {
		return nil, false, err
	}
	if err := emitTx(tx, runID, t.ID, t.ProjectID, EventStepAnswer,
		map[string]any{"step": stepID, "text": text}, now); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	t.Status = StatusDone
	t.Result = resultJSON
	t.UpdatedAt = now
	return t, true, nil
}

// CountAnswers counts the step.answer events recorded for (runID, stepID) — the
// anti-loop budget for the answer verb (a hard cap independent of on_fail.max,
// since answers deliberately never fail the gate). Counted in Go rather than via
// SQL json_extract to stay portable across the pure-Go sqlite build.
func (s *Store) CountAnswers(runID, stepID string) (int, error) {
	rows, err := s.db.Query(`SELECT data FROM events WHERE run_id=? AND type=?`, runID, EventStepAnswer)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return 0, err
		}
		var d struct {
			Step string `json:"step"`
		}
		if json.Unmarshal([]byte(data), &d) == nil && d.Step == stepID {
			n++
		}
	}
	return n, rows.Err()
}

// Heartbeat updates a RUNNING task's liveness, rejecting a stale fence (a worker
// whose claim was superseded by requeue_stale). Returns ErrStaleFence if so.
func (s *Store) Heartbeat(taskID string, fence int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var curFence int64
	var st Status
	err = tx.QueryRow(`SELECT fence, status FROM tasks WHERE id=?`, taskID).Scan(&curFence, &st)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if fence != curFence {
		return fmt.Errorf("%w: heartbeat task %s fence %d != %d", ErrStaleFence, taskID, fence, curFence)
	}
	if st != StatusRunning {
		return fmt.Errorf("heartbeat on non-running task %s (status %s)", taskID, st)
	}
	if _, err := tx.Exec(`UPDATE tasks SET heartbeat_at=?, updated_at=? WHERE id=?`, s.now(), s.now(), taskID); err != nil {
		return err
	}
	return tx.Commit()
}

// OwnsClaim reports whether the worker holding `fence` still owns taskID's live
// claim: the stored fence matches AND the task is still RUNNING. A reaper requeue
// (RUNNING->QUEUED, fence bumped) or a re-claim by a second worker (fence bumped
// again) both flip this to false. It is the read-side guard for side effects that
// the fence cannot undo — e.g. the agent's filesystem SyncBack (B5): a reaped
// worker that returns late must NOT clobber the worktree a live worker now owns.
func (s *Store) OwnsClaim(taskID string, fence int64) (bool, error) {
	var curFence int64
	var st Status
	err := s.db.QueryRow(`SELECT fence, status FROM tasks WHERE id=?`, taskID).Scan(&curFence, &st)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrNotFound
	}
	if err != nil {
		return false, err
	}
	return curFence == fence && st == StatusRunning, nil
}

// RequeueTransient requeues a single RUNNING task (RUNNING->QUEUED) after a
// recognized transient failure (R1: provider session/rate limit), deferring its
// re-claim until availableAt by gating the claim query on available_at. It rotates
// the fence so the current worker's late report/heartbeat fails the fence check,
// mirroring RequeueStale. The fence guard makes it a no-op if the caller no longer
// owns the claim. Returns ErrStaleFence if the fence does not match.
func (s *Store) RequeueTransient(taskID string, fence int64, availableAt int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var curFence int64
	var runID, stepID, projectID string
	err = tx.QueryRow(`SELECT fence, run_id, step_id, project_id FROM tasks WHERE id=?`, taskID).Scan(&curFence, &runID, &stepID, &projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if fence != curFence {
		return fmt.Errorf("%w: requeue-transient task %s fence %d != %d", ErrStaleFence, taskID, fence, curFence)
	}
	now := s.now()
	if _, err := tx.Exec(`UPDATE tasks SET status=?, fence=fence+1, claimed_by='', heartbeat_at=0, available_at=?, updated_at=? WHERE id=?`,
		string(StatusQueued), availableAt, now, taskID); err != nil {
		return err
	}
	if err := emitTx(tx, runID, taskID, projectID, EventStepStatusChange,
		map[string]any{"step": stepID, "from": StatusRunning, "to": StatusQueued, "reason": "transient_retry", "available_at": availableAt}, now); err != nil {
		return err
	}
	return tx.Commit()
}

// RequeueStale finds RUNNING tasks whose heartbeat is older than staleMillis and
// requeues them (RUNNING->QUEUED), rotating the fence so the zombie worker's next
// report/heartbeat fails the fence check. Returns the number requeued.
func (s *Store) RequeueStale(staleMillis int64) (int, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	cutoff := s.now() - staleMillis
	rows, err := tx.Query(`SELECT id, run_id, step_id, project_id, fence FROM tasks WHERE status='RUNNING' AND heartbeat_at < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	type stale struct {
		id, runID, stepID, projectID string
		fence                        int64
	}
	var found []stale
	for rows.Next() {
		var s stale
		if err := rows.Scan(&s.id, &s.runID, &s.stepID, &s.projectID, &s.fence); err != nil {
			rows.Close()
			return 0, err
		}
		found = append(found, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	now := s.now()
	for _, st := range found {
		newFence := st.fence + 1
		if _, err := tx.Exec(`UPDATE tasks SET status=?, fence=?, claimed_by='', updated_at=? WHERE id=?`,
			string(StatusQueued), newFence, now, st.id); err != nil {
			return 0, err
		}
		if err := emitTx(tx, st.runID, st.id, st.projectID, EventStepStatusChange,
			map[string]any{"step": st.stepID, "from": StatusRunning, "to": StatusQueued, "reason": "stale"}, now); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(found), nil
}
