package store

// Retention helpers (audit A3): keep disk and the events table from growing without
// bound on a long-running deployment. Wired by the app's RetentionLoop, opt-in via env.

// PruneEvents deletes events older than `before` (unix ms). Returns rows removed.
func (s *Store) PruneEvents(before int64) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM events WHERE created_at < ?`, before)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// TerminalRunIDsBefore returns ids of runs in a DISPOSABLE terminal state (DONE or
// CANCELLED) last updated before `before`. FAILED runs are deliberately excluded — a
// failed run can still be re-run, and RerunStep reuses its workdir, so we must not GC it.
func (s *Store) TerminalRunIDsBefore(before int64) ([]string, error) {
	rows, err := s.db.Query(`SELECT id FROM runs WHERE status IN (?, ?) AND updated_at < ?`,
		string(StatusDone), string(StatusCancelled), before)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
