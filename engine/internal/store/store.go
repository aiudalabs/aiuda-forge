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
	"strings"
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
  project_id  TEXT NOT NULL DEFAULT '',
  deleted_at  INTEGER NOT NULL DEFAULT 0,
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
  available_at INTEGER NOT NULL DEFAULT 0,
  project_id   TEXT NOT NULL DEFAULT '',
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
  project_id TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_events_run ON events(run_id, seq);
`

// project_id migrations (audit A1): existing kernel DBs predate the multi-tenant
// column. Each ADD COLUMN is guarded so a re-run on an already-migrated DB is a
// no-op (SQLite reports "duplicate column name"). Existing rows default to ” and
// are backfilled to the "default" project by backfillDefaultProject so legacy
// single-tenant data keeps working; new rows carry a real project_id.
var projectIDMigrations = []string{
	`ALTER TABLE runs ADD COLUMN project_id TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE tasks ADD COLUMN project_id TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE events ADD COLUMN project_id TEXT NOT NULL DEFAULT ''`,
	// available_at gates the claim query so a transient-retry (R1: provider limit)
	// can defer re-claim by a backoff. Pre-existing DBs predate the column.
	`ALTER TABLE tasks ADD COLUMN available_at INTEGER NOT NULL DEFAULT 0`,
	// deleted_at soft-deletes runs (D4): a deleted run is retained (audit + so a
	// story still pointing at its run_id resolves) but hidden from listings.
	`ALTER TABLE runs ADD COLUMN deleted_at INTEGER NOT NULL DEFAULT 0`,
}

// DefaultProjectID is the project existing (pre-multi-tenant) rows are backfilled
// to so single-tenant data survives the migration. It is shared with the tickets
// and projects stores via this constant.
const DefaultProjectID = "default"

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
	// Upgrade guard for DBs predating project_id (audit A1). Idempotent: a
	// "duplicate column name" error means the column already exists — not an error.
	for _, m := range projectIDMigrations {
		if _, err := db.Exec(m); err != nil && !isDuplicateColumn(err) {
			db.Close()
			return nil, fmt.Errorf("migrate project_id: %w", err)
		}
	}
	st := &Store{db: db, Now: time.Now}
	if err := st.backfillDefaultProject(); err != nil {
		db.Close()
		return nil, fmt.Errorf("backfill default project: %w", err)
	}
	return st, nil
}

// backfillDefaultProject stamps the DefaultProjectID on any pre-migration row
// whose project_id is still empty, so legacy single-tenant runs/tasks/events are
// scoped to the "default" project instead of an unowned blank. New rows always
// carry a real project_id, so this only ever touches rows created before the
// column existed; it is a cheap no-op once they are all stamped.
func (s *Store) backfillDefaultProject() error {
	for _, table := range []string{"runs", "tasks", "events"} {
		if _, err := s.db.Exec(
			`UPDATE `+table+` SET project_id=? WHERE project_id=''`, DefaultProjectID); err != nil {
			return err
		}
	}
	return nil
}

// isDuplicateColumn reports whether err is a SQLite "duplicate column name"
// error, the signal that a guarded ALTER TABLE ADD COLUMN already ran.
func isDuplicateColumn(err error) bool {
	return err != nil && strings.Contains(err.Error(), "duplicate column name")
}

// Close closes the underlying database.
func (s *Store) Close() error { return s.db.Close() }

// DB exposes the raw handle for advanced callers (kept minimal on purpose).
func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) now() int64 { return s.Now().UnixMilli() }

// ---- Runs -------------------------------------------------------------------

// CreateRun inserts a run in QUEUED state and emits run.created. projectID scopes
// the run (and every task/event it spawns) to its project (audit A1); it defaults
// to DefaultProjectID when empty so back-compat callers (single-tenant tests,
// gate-less echo flows) stay valid while new project runs carry a real id.
func (s *Store) CreateRun(id, workflowID, projectID, payload string) (*Run, error) {
	if payload == "" {
		payload = "{}"
	}
	if projectID == "" {
		projectID = DefaultProjectID
	}
	now := s.now()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`INSERT INTO runs(id, workflow_id, status, payload, project_id, created_at, updated_at)
		VALUES(?,?,?,?,?,?,?)`, id, workflowID, string(StatusQueued), payload, projectID, now, now)
	if err != nil {
		return nil, err
	}
	if err := emitTx(tx, id, "", projectID, EventRunCreated, map[string]any{"workflow": workflowID}, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &Run{ID: id, WorkflowID: workflowID, Status: StatusQueued, Payload: payload, ProjectID: projectID, CreatedAt: now, UpdatedAt: now}, nil
}

// GetRun loads a run by id.
func (s *Store) GetRun(id string) (*Run, error) {
	r := &Run{}
	err := s.db.QueryRow(`SELECT id, workflow_id, status, payload, project_id, deleted_at, created_at, updated_at FROM runs WHERE id=?`, id).
		Scan(&r.ID, &r.WorkflowID, &r.Status, &r.Payload, &r.ProjectID, &r.DeletedAt, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return r, nil
}

// ListRuns returns runs, optionally filtered by status (empty = all), newest
// first. To scope by project as well, use ListRunsByProject.
func (s *Store) ListRuns(status Status) ([]*Run, error) {
	return s.ListRunsByProject(status, "")
}

// ListRunsByProject returns runs filtered by status (empty = all) and project
// (empty = all projects, for admin/back-compat), newest first (audit A1).
func (s *Store) ListRunsByProject(status Status, projectID string) ([]*Run, error) {
	q := `SELECT id, workflow_id, status, payload, project_id, created_at, updated_at FROM runs`
	// Soft-deleted runs (D4) never appear in listings.
	where := []string{"deleted_at=0"}
	var args []any
	if status != "" {
		where = append(where, "status=?")
		args = append(args, string(status))
	}
	if projectID != "" {
		where = append(where, "project_id=?")
		args = append(args, projectID)
	}
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
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
		if err := rows.Scan(&r.ID, &r.WorkflowID, &r.Status, &r.Payload, &r.ProjectID, &r.CreatedAt, &r.UpdatedAt); err != nil {
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
	var projectID string
	err = tx.QueryRow(`SELECT status, project_id FROM runs WHERE id=?`, runID).Scan(&from, &projectID)
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
	if err := emitTx(tx, runID, "", projectID, EventRunStatusChanged, map[string]any{"from": from, "to": to}, now); err != nil {
		return err
	}
	if terminal := terminalRunEvent(to); terminal != "" {
		if err := emitTx(tx, runID, "", projectID, terminal, map[string]any{"status": to}, now); err != nil {
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
	// A task is born into its run's project (audit A1). When the caller didn't set
	// it (the common path — the engine enqueues with just run/step), inherit the
	// run's project_id so every task of a run is scoped consistently.
	if t.ProjectID == "" {
		if pid, err := s.runProjectID(t.RunID); err == nil {
			t.ProjectID = pid
		}
	}
	if t.ProjectID == "" {
		t.ProjectID = DefaultProjectID
	}
	deps, _ := json.Marshal(t.DependsOn)
	now := s.now()
	t.CreatedAt, t.UpdatedAt = now, now
	_, err := s.db.Exec(`INSERT INTO tasks
		(id, run_id, workflow_id, step_id, type, status, payload, result, error, attempts, fence, depends_on, wave, claimed_by, heartbeat_at, project_id, created_at, updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		t.ID, t.RunID, t.WorkflowID, t.StepID, t.Type, string(t.Status), t.Payload, t.Result, t.Error,
		t.Attempts, t.Fence, string(deps), t.Wave, t.ClaimedBy, t.HeartbeatAt, t.ProjectID, now, now)
	return err
}

// runProjectID returns the project_id of a run (the scope its tasks/events
// inherit). Returns ErrNotFound when the run does not exist.
func (s *Store) runProjectID(runID string) (string, error) {
	var pid string
	err := s.db.QueryRow(`SELECT project_id FROM runs WHERE id=?`, runID).Scan(&pid)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return pid, err
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

const taskCols = `SELECT id, run_id, workflow_id, step_id, type, status, payload, result, error, attempts, fence, depends_on, wave, claimed_by, heartbeat_at, project_id, created_at, updated_at FROM tasks`

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
		&t.Error, &t.Attempts, &t.Fence, &deps, &t.Wave, &t.ClaimedBy, &t.HeartbeatAt, &t.ProjectID, &t.CreatedAt, &t.UpdatedAt)
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
// transition routes through here (single emit point, golden rule #5). projectID
// scopes the event to its project (audit A1) so the event tail can be filtered
// per-project without a join back to runs.
func emitTx(tx *sql.Tx, runID, taskID, projectID, typ string, data map[string]any, now int64) error {
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO events(run_id, task_id, type, data, project_id, created_at) VALUES(?,?,?,?,?,?)`,
		runID, taskID, typ, string(b), projectID, now)
	return err
}

// EventsAfter returns events for a run with seq > after, ascending (replay).
func (s *Store) EventsAfter(runID string, after int64) ([]*Event, error) {
	rows, err := s.db.Query(`SELECT seq, run_id, task_id, type, data, project_id, created_at FROM events
		WHERE run_id=? AND seq>? ORDER BY seq ASC`, runID, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Event
	for rows.Next() {
		e := &Event{}
		if err := rows.Scan(&e.Seq, &e.RunID, &e.TaskID, &e.Type, &e.Data, &e.ProjectID, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// AllEventsAfter returns events across ALL runs with seq > after, ascending.
// This is the global tail the event bus polls.
func (s *Store) AllEventsAfter(after int64) ([]*Event, error) {
	rows, err := s.db.Query(`SELECT seq, run_id, task_id, type, data, project_id, created_at FROM events
		WHERE seq>? ORDER BY seq ASC`, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Event
	for rows.Next() {
		e := &Event{}
		if err := rows.Scan(&e.Seq, &e.RunID, &e.TaskID, &e.Type, &e.Data, &e.ProjectID, &e.CreatedAt); err != nil {
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
	// Scope the event to its run's project (audit A1). A best-effort lookup: an
	// event for an unknown run (shouldn't happen) falls back to DefaultProjectID
	// rather than failing the append.
	projectID, perr := s.runProjectID(runID)
	if perr != nil || projectID == "" {
		projectID = DefaultProjectID
	}
	res, err := s.db.Exec(`INSERT INTO events(run_id, task_id, type, data, project_id, created_at) VALUES(?,?,?,?,?,?)`,
		runID, taskID, typ, string(b), projectID, s.now())
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
