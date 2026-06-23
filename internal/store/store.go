// Package store is the kernel's persistence + integrity layer: schema, the
// state machine (legal transitions), atomic claim, fencing, and the single
// event-emit point. This is deterministic, security-critical kernel code — per
// the constitution it lives here, never in markdown.
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// Sentinel errors so callers (and tests) can branch deterministically.
var (
	ErrIllegalTransition = errors.New("illegal state transition")
	ErrStaleFence        = errors.New("stale fence token")
	ErrNotFound          = errors.New("not found")
)

// Store wraps a sqlite database. The DSN forces BEGIN IMMEDIATE on every
// transaction (so atomic claim takes the write lock up front) and a generous
// busy_timeout so concurrent claimers serialize instead of erroring.
//
// The Postgres port swaps this DSN/claim for `FOR UPDATE SKIP LOCKED`; the
// rest of the kernel is unchanged. That hook is the only dialect-specific spot.
type Store struct {
	db  *sql.DB
	Now func() time.Time // injectable clock for deterministic tests
}

const schema = `
CREATE TABLE IF NOT EXISTS runs (
  id          TEXT PRIMARY KEY,
  workflow_id TEXT NOT NULL,
  status      TEXT NOT NULL,
  payload     TEXT NOT NULL DEFAULT '{}',
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS tasks (
  id           TEXT PRIMARY KEY,
  run_id       TEXT NOT NULL,
  workflow_id  TEXT NOT NULL,
  step_id      TEXT NOT NULL,
  type         TEXT NOT NULL,
  status       TEXT NOT NULL,
  payload      TEXT NOT NULL DEFAULT '{}',
  result       TEXT NOT NULL DEFAULT '{}',
  error        TEXT NOT NULL DEFAULT '',
  attempts     INTEGER NOT NULL DEFAULT 0,
  fence        INTEGER NOT NULL DEFAULT 0,
  depends_on   TEXT NOT NULL DEFAULT '[]',
  wave         INTEGER NOT NULL DEFAULT 0,
  claimed_by   TEXT NOT NULL DEFAULT '',
  heartbeat_at INTEGER NOT NULL DEFAULT 0,
  created_at   INTEGER NOT NULL,
  updated_at   INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status);
CREATE INDEX IF NOT EXISTS idx_tasks_run ON tasks(run_id);

CREATE TABLE IF NOT EXISTS events (
  seq        INTEGER PRIMARY KEY AUTOINCREMENT,
  run_id     TEXT NOT NULL,
  task_id    TEXT NOT NULL DEFAULT '',
  type       TEXT NOT NULL,
  data       TEXT NOT NULL DEFAULT '{}',
  created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_events_run ON events(run_id, seq);
`

// Open opens (creating if needed) the sqlite database at path and applies the
// schema. Use ":memory:" — actually a shared temp file — for tests via OpenTest.
func Open(path string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_txlock=immediate&_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &Store{db: db, Now: time.Now}, nil
}

// Close closes the underlying database.
func (s *Store) Close() error { return s.db.Close() }

// DB exposes the raw handle for advanced callers (kept minimal on purpose).
func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) now() int64 { return s.Now().UnixMilli() }

// ---- Runs -------------------------------------------------------------------

// CreateRun inserts a run in QUEUED state and emits run.created.
func (s *Store) CreateRun(id, workflowID, payload string) (*Run, error) {
	if payload == "" {
		payload = "{}"
	}
	now := s.now()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`INSERT INTO runs(id, workflow_id, status, payload, created_at, updated_at)
		VALUES(?,?,?,?,?,?)`, id, workflowID, string(StatusQueued), payload, now, now)
	if err != nil {
		return nil, err
	}
	if err := emitTx(tx, id, "", EventRunCreated, map[string]any{"workflow": workflowID}, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &Run{ID: id, WorkflowID: workflowID, Status: StatusQueued, Payload: payload, CreatedAt: now, UpdatedAt: now}, nil
}

// GetRun loads a run by id.
func (s *Store) GetRun(id string) (*Run, error) {
	r := &Run{}
	err := s.db.QueryRow(`SELECT id, workflow_id, status, payload, created_at, updated_at FROM runs WHERE id=?`, id).
		Scan(&r.ID, &r.WorkflowID, &r.Status, &r.Payload, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return r, nil
}

// ListRuns returns runs, optionally filtered by status (empty = all), newest first.
func (s *Store) ListRuns(status Status) ([]*Run, error) {
	q := `SELECT id, workflow_id, status, payload, created_at, updated_at FROM runs`
	args := []any{}
	if status != "" {
		q += ` WHERE status=?`
		args = append(args, string(status))
	}
	q += ` ORDER BY created_at DESC`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Run
	for rows.Next() {
		r := &Run{}
		if err := rows.Scan(&r.ID, &r.WorkflowID, &r.Status, &r.Payload, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SetRunStatus transitions a run and emits run.status_changed (+ a terminal
// run.done/failed/cancelled when applicable). This is the single emit point for
// run-level transitions.
func (s *Store) SetRunStatus(runID string, to Status) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var from Status
	err = tx.QueryRow(`SELECT status FROM runs WHERE id=?`, runID).Scan(&from)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if from == to {
		return nil
	}
	if !transitionAllowed(from, to) {
		return fmt.Errorf("%w: run %s %s->%s", ErrIllegalTransition, runID, from, to)
	}
	now := s.now()
	if _, err := tx.Exec(`UPDATE runs SET status=?, updated_at=? WHERE id=?`, string(to), now, runID); err != nil {
		return err
	}
	if err := emitTx(tx, runID, "", EventRunStatusChanged, map[string]any{"from": from, "to": to}, now); err != nil {
		return err
	}
	if terminal := terminalRunEvent(to); terminal != "" {
		if err := emitTx(tx, runID, "", terminal, map[string]any{"status": to}, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func terminalRunEvent(to Status) string {
	switch to {
	case StatusDone:
		return EventRunDone
	case StatusFailed:
		return EventRunFailed
	case StatusCancelled:
		return EventRunCancelled
	}
	return ""
}

// ---- Tasks ------------------------------------------------------------------

// EnqueueTask inserts a QUEUED task. It does NOT emit a transition event (the
// task is born queued); creation is implied by run.created + the task listing.
func (s *Store) EnqueueTask(t *Task) error {
	if t.Payload == "" {
		t.Payload = "{}"
	}
	if t.Result == "" {
		t.Result = "{}"
	}
	if t.Status == "" {
		t.Status = StatusQueued
	}
	deps, _ := json.Marshal(t.DependsOn)
	now := s.now()
	t.CreatedAt, t.UpdatedAt = now, now
	_, err := s.db.Exec(`INSERT INTO tasks
		(id, run_id, workflow_id, step_id, type, status, payload, result, error, attempts, fence, depends_on, wave, claimed_by, heartbeat_at, created_at, updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		t.ID, t.RunID, t.WorkflowID, t.StepID, t.Type, string(t.Status), t.Payload, t.Result, t.Error,
		t.Attempts, t.Fence, string(deps), t.Wave, t.ClaimedBy, t.HeartbeatAt, now, now)
	return err
}

// GetTask loads a task by id.
func (s *Store) GetTask(id string) (*Task, error) {
	return scanTask(s.db.QueryRow(taskCols+` WHERE id=?`, id))
}

// TasksForRun returns all tasks of a run, oldest first.
func (s *Store) TasksForRun(runID string) ([]*Task, error) {
	rows, err := s.db.Query(taskCols+` WHERE run_id=? ORDER BY created_at ASC, id ASC`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Task
	for rows.Next() {
		t, err := scanTaskRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

const taskCols = `SELECT id, run_id, workflow_id, step_id, type, status, payload, result, error, attempts, fence, depends_on, wave, claimed_by, heartbeat_at, created_at, updated_at FROM tasks`

type rowScanner interface{ Scan(dest ...any) error }

func scanTask(row rowScanner) (*Task, error) {
	t, err := scanTaskCore(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return t, err
}

func scanTaskRows(row rowScanner) (*Task, error) { return scanTaskCore(row) }

func scanTaskCore(row rowScanner) (*Task, error) {
	t := &Task{}
	var deps string
	err := row.Scan(&t.ID, &t.RunID, &t.WorkflowID, &t.StepID, &t.Type, &t.Status, &t.Payload, &t.Result,
		&t.Error, &t.Attempts, &t.Fence, &deps, &t.Wave, &t.ClaimedBy, &t.HeartbeatAt, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if deps != "" {
		_ = json.Unmarshal([]byte(deps), &t.DependsOn)
	}
	return t, nil
}

// ---- Events -----------------------------------------------------------------

// emitTx writes one event inside an existing transaction. EVERY state
// transition routes through here (single emit point, golden rule #5).
func emitTx(tx *sql.Tx, runID, taskID, typ string, data map[string]any, now int64) error {
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO events(run_id, task_id, type, data, created_at) VALUES(?,?,?,?,?)`,
		runID, taskID, typ, string(b), now)
	return err
}

// EventsAfter returns events for a run with seq > after, ascending (replay).
func (s *Store) EventsAfter(runID string, after int64) ([]*Event, error) {
	rows, err := s.db.Query(`SELECT seq, run_id, task_id, type, data, created_at FROM events
		WHERE run_id=? AND seq>? ORDER BY seq ASC`, runID, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Event
	for rows.Next() {
		e := &Event{}
		if err := rows.Scan(&e.Seq, &e.RunID, &e.TaskID, &e.Type, &e.Data, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// AppendEvent writes a non-transition event (e.g. step.event streaming logs,
// step.gate, step.verify). Returns the assigned seq. Transition events are
// emitted internally by the state machine; this is for step-level signals.
func (s *Store) AppendEvent(runID, taskID, typ string, data map[string]any) (int64, error) {
	b, err := json.Marshal(data)
	if err != nil {
		return 0, err
	}
	res, err := s.db.Exec(`INSERT INTO events(run_id, task_id, type, data, created_at) VALUES(?,?,?,?,?)`,
		runID, taskID, typ, string(b), s.now())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// MaxSeq returns the highest event seq overall (cursor for new subscribers).
func (s *Store) MaxSeq() (int64, error) {
	var seq sql.NullInt64
	if err := s.db.QueryRow(`SELECT MAX(seq) FROM events`).Scan(&seq); err != nil {
		return 0, err
	}
	return seq.Int64, nil
}
