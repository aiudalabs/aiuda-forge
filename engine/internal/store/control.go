package store

import "encoding/json"

// Control-plane operations exposed to the API. These mutate state ONLY through
// the same transition path (single emit point), so cancel/delete/retry all show
// up on the event bus — there is no privileged backdoor.

// CancelRun cancels every non-terminal task of a run, then the run itself.
// Unfenced (fence=-1): cancellation is a control action, not a worker report.
func (s *Store) CancelRun(runID string) error {
	if _, err := s.GetRun(runID); err != nil {
		return err
	}
	tasks, err := s.TasksForRun(runID)
	if err != nil {
		return err
	}
	for _, t := range tasks {
		if IsTerminal(t.Status) {
			continue
		}
		if err := s.Transition(t.ID, -1, StatusCancelled, nil, "cancelled"); err != nil {
			return err
		}
	}
	run, err := s.GetRun(runID)
	if err != nil {
		return err
	}
	if IsTerminal(run.Status) {
		return nil
	}
	return s.SetRunStatus(runID, StatusCancelled)
}

// DeleteRun removes a run and all of its tasks and events.
func (s *Store) DeleteRun(runID string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM events WHERE run_id=?`, runID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM tasks WHERE run_id=?`, runID); err != nil {
		return err
	}
	res, err := tx.Exec(`DELETE FROM runs WHERE id=?`, runID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

// ReopenRun moves a terminal run back to RUNNING for a retry. Legal path:
// FAILED/CANCELLED -> QUEUED -> RUNNING. DONE runs are not reopened.
func (s *Store) ReopenRun(runID string) error {
	run, err := s.GetRun(runID)
	if err != nil {
		return err
	}
	switch run.Status {
	case StatusFailed, StatusCancelled:
		// allowed
	case StatusRunning, StatusQueued:
		return nil // already active
	default:
		return ErrIllegalTransition
	}
	// FAILED/CANCELLED are terminal in the task table but runs allow FAILED->QUEUED.
	// CANCELLED has no outgoing edge, so jump via a direct status set for runs.
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := s.now()
	if _, err := tx.Exec(`UPDATE runs SET status=?, updated_at=? WHERE id=?`, string(StatusRunning), now, runID); err != nil {
		return err
	}
	if err := emitTx(tx, runID, "", run.ProjectID, EventRunStatusChanged, map[string]any{"from": run.Status, "to": StatusRunning, "reason": "retry"}, now); err != nil {
		return err
	}
	return tx.Commit()
}

// LastRetryAt returns the created_at (ms) of the most recent retry boundary for
// a run — the run.status_changed event ReopenRun emits with reason "retry" — or
// 0 if the run has never been retried. The engine uses it as a watermark so a
// retry gets a fresh on_fail budget instead of inheriting prior FAILED tasks.
func (s *Store) LastRetryAt(runID string) (int64, error) {
	rows, err := s.db.Query(`SELECT data, created_at FROM events
		WHERE run_id=? AND type=? ORDER BY seq DESC`, runID, EventRunStatusChanged)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var data string
		var createdAt int64
		if err := rows.Scan(&data, &createdAt); err != nil {
			return 0, err
		}
		var d map[string]any
		if json.Unmarshal([]byte(data), &d) == nil {
			if reason, _ := d["reason"].(string); reason == "retry" {
				return createdAt, nil
			}
		}
	}
	return 0, rows.Err()
}

// LastFailedTask returns the most recent FAILED task of a run, or nil.
func (s *Store) LastFailedTask(runID string) (*Task, error) {
	tasks, err := s.TasksForRun(runID)
	if err != nil {
		return nil, err
	}
	var last *Task
	for _, t := range tasks {
		if t.Status == StatusFailed {
			last = t
		}
	}
	return last, nil
}
