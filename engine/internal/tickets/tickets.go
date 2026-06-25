// Package tickets is the native ticket store (control-plane, not kernel).
// It is the source of truth for the backlog: epics, sprints, stories, deps,
// and derived readiness. GitHub/JIRA become optional sync targets later.
// The store opens its OWN sqlite file and never touches the kernel's runs DB.
package tickets

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	_ "modernc.org/sqlite"
)

// Status is the lifecycle column (Kanban vocabulary). "ready" is DERIVED —
// see Store.Ready(); it is never written to the DB.
type Status string

const (
	StatusBacklog  Status = "backlog"
	StatusReady    Status = "ready" // derived only — not stored
	StatusRunning  Status = "running"
	StatusInReview Status = "in_review"
	StatusDone     Status = "done"
	StatusFailed   Status = "failed"
)

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("not found")

// Epic is a high-level grouping of Stories.
type Epic struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

// Sprint is a time-box that Stories can be assigned to.
type Sprint struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Goal string `json:"goal"`
}

// Story is the unit of work. epic_id and sprint_id are optional. deps is the
// list of story IDs that must reach StatusDone before this story is "ready".
// run_id records the control-plane run that is executing this story.
type Story struct {
	ID       string   `json:"id"`
	EpicID   string   `json:"epic_id,omitempty"`
	SprintID string   `json:"sprint_id,omitempty"`
	Title    string   `json:"title"`
	Body     string   `json:"body"`
	Accept   string   `json:"acceptance"`
	Owner    string   `json:"owner,omitempty"`
	Deps     []string `json:"deps"`
	Status   Status   `json:"status"`
	RunID    string   `json:"run_id,omitempty"`
}

const schema = `
CREATE TABLE IF NOT EXISTS epics (
  id          TEXT PRIMARY KEY,
  title       TEXT NOT NULL DEFAULT '',
  description TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS sprints (
  id   TEXT PRIMARY KEY,
  name TEXT NOT NULL DEFAULT '',
  goal TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS stories (
  id        TEXT PRIMARY KEY,
  epic_id   TEXT NOT NULL DEFAULT '',
  sprint_id TEXT NOT NULL DEFAULT '',
  title     TEXT NOT NULL DEFAULT '',
  body      TEXT NOT NULL DEFAULT '',
  accept    TEXT NOT NULL DEFAULT '',
  owner     TEXT NOT NULL DEFAULT '',
  status    TEXT NOT NULL DEFAULT 'backlog',
  run_id    TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS story_deps (
  story_id TEXT NOT NULL,
  dep_id   TEXT NOT NULL,
  PRIMARY KEY (story_id, dep_id)
);
CREATE INDEX IF NOT EXISTS idx_story_deps_story ON story_deps(story_id);
CREATE INDEX IF NOT EXISTS idx_story_deps_dep   ON story_deps(dep_id);
`

// Store is the ticket store backed by a sqlite database.
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the sqlite database at path and applies the
// schema. Matches the DSN pattern used in internal/store for WAL + busy_timeout.
func Open(path string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &Store{db: db}, nil
}

// Close closes the underlying database.
func (s *Store) Close() error { return s.db.Close() }

// ---- Epics ------------------------------------------------------------------

// CreateEpic inserts an Epic. Returns ErrNotFound if id is empty.
func (s *Store) CreateEpic(e Epic) error {
	if e.ID == "" {
		return errors.New("epic id is required")
	}
	_, err := s.db.Exec(`INSERT INTO epics(id, title, description) VALUES(?,?,?)`,
		e.ID, e.Title, e.Description)
	return err
}

// GetEpic loads an Epic by id.
func (s *Store) GetEpic(id string) (Epic, error) {
	var e Epic
	err := s.db.QueryRow(`SELECT id, title, description FROM epics WHERE id=?`, id).
		Scan(&e.ID, &e.Title, &e.Description)
	if errors.Is(err, sql.ErrNoRows) {
		return Epic{}, ErrNotFound
	}
	return e, err
}

// ListEpics returns all epics ordered by id.
func (s *Store) ListEpics() ([]Epic, error) {
	rows, err := s.db.Query(`SELECT id, title, description FROM epics ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Epic
	for rows.Next() {
		var e Epic
		if err := rows.Scan(&e.ID, &e.Title, &e.Description); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ---- Sprints ----------------------------------------------------------------

// CreateSprint inserts a Sprint.
func (s *Store) CreateSprint(sp Sprint) error {
	if sp.ID == "" {
		return errors.New("sprint id is required")
	}
	_, err := s.db.Exec(`INSERT INTO sprints(id, name, goal) VALUES(?,?,?)`,
		sp.ID, sp.Name, sp.Goal)
	return err
}

// ListSprints returns all sprints ordered by id.
func (s *Store) ListSprints() ([]Sprint, error) {
	rows, err := s.db.Query(`SELECT id, name, goal FROM sprints ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Sprint
	for rows.Next() {
		var sp Sprint
		if err := rows.Scan(&sp.ID, &sp.Name, &sp.Goal); err != nil {
			return nil, err
		}
		out = append(out, sp)
	}
	return out, rows.Err()
}

// ---- Stories ----------------------------------------------------------------

// CreateStory inserts a Story and its deps into story_deps.
func (s *Store) CreateStory(st Story) error {
	if st.ID == "" {
		return errors.New("story id is required")
	}
	if st.Status == "" {
		st.Status = StatusBacklog
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.Exec(`INSERT INTO stories(id, epic_id, sprint_id, title, body, accept, owner, status, run_id)
		VALUES(?,?,?,?,?,?,?,?,?)`,
		st.ID, st.EpicID, st.SprintID, st.Title, st.Body, st.Accept, st.Owner, string(st.Status), st.RunID)
	if err != nil {
		return err
	}
	for _, dep := range st.Deps {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO story_deps(story_id, dep_id) VALUES(?,?)`, st.ID, dep); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// GetStory loads a Story by id, including its deps.
func (s *Store) GetStory(id string) (Story, error) {
	var st Story
	err := s.db.QueryRow(`SELECT id, epic_id, sprint_id, title, body, accept, owner, status, run_id
		FROM stories WHERE id=?`, id).
		Scan(&st.ID, &st.EpicID, &st.SprintID, &st.Title, &st.Body, &st.Accept, &st.Owner, &st.Status, &st.RunID)
	if errors.Is(err, sql.ErrNoRows) {
		return Story{}, ErrNotFound
	}
	if err != nil {
		return Story{}, err
	}
	deps, err := s.loadDeps(id)
	if err != nil {
		return Story{}, err
	}
	st.Deps = deps
	return st, nil
}

// ListStories returns all stories with their deps.
func (s *Store) ListStories() ([]Story, error) {
	rows, err := s.db.Query(`SELECT id, epic_id, sprint_id, title, body, accept, owner, status, run_id
		FROM stories ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Story
	for rows.Next() {
		var st Story
		if err := rows.Scan(&st.ID, &st.EpicID, &st.SprintID, &st.Title, &st.Body, &st.Accept, &st.Owner, &st.Status, &st.RunID); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		deps, err := s.loadDeps(out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].Deps = deps
	}
	return out, nil
}

// UpdateStoryStatus sets the status column of a Story. Does not affect deps.
func (s *Store) UpdateStoryStatus(id string, status Status) error {
	res, err := s.db.Exec(`UPDATE stories SET status=? WHERE id=?`, string(status), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ClaimStory atomically transitions a story from backlog → running.
// Returns true if this caller claimed it, false if it was already taken.
func (s *Store) ClaimStory(id string) (bool, error) {
	res, err := s.db.Exec(`UPDATE stories SET status='running' WHERE id=? AND status='backlog'`, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// MarkFailed sets a story's status to failed. Used when the run driving it
// reaches a terminal non-DONE state (FAILED, CANCELLED) so the story is not
// stuck in "running" forever.
func (s *Store) MarkFailed(id string) error {
	res, err := s.db.Exec(`UPDATE stories SET status='failed' WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetStoryRun records the run_id that is executing a story.
func (s *Store) SetStoryRun(id, runID string) error {
	res, err := s.db.Exec(`UPDATE stories SET run_id=? WHERE id=?`, runID, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// AddDep adds dep story IDs to a story. Silently skips duplicates.
func (s *Store) AddDep(storyID string, deps []string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, dep := range deps {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO story_deps(story_id, dep_id) VALUES(?,?)`, storyID, dep); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Ready returns all stories in StatusBacklog whose every dep has StatusDone.
// A story with no deps is ready immediately when its stored status is backlog.
func (s *Store) Ready() ([]Story, error) {
	// Load all backlog stories and check deps in one pass.
	rows, err := s.db.Query(`SELECT id, epic_id, sprint_id, title, body, accept, owner, status, run_id
		FROM stories WHERE status='backlog' ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var candidates []Story
	for rows.Next() {
		var st Story
		if err := rows.Scan(&st.ID, &st.EpicID, &st.SprintID, &st.Title, &st.Body, &st.Accept, &st.Owner, &st.Status, &st.RunID); err != nil {
			return nil, err
		}
		candidates = append(candidates, st)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var out []Story
	for i := range candidates {
		deps, err := s.loadDeps(candidates[i].ID)
		if err != nil {
			return nil, err
		}
		candidates[i].Deps = deps
		ready, err := s.depsDone(deps)
		if err != nil {
			return nil, err
		}
		if ready {
			out = append(out, candidates[i])
		}
	}
	return out, nil
}

// loadDeps returns the dep IDs for storyID, sorted.
func (s *Store) loadDeps(storyID string) ([]string, error) {
	rows, err := s.db.Query(`SELECT dep_id FROM story_deps WHERE story_id=? ORDER BY dep_id ASC`, storyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var dep string
		if err := rows.Scan(&dep); err != nil {
			return nil, err
		}
		out = append(out, dep)
	}
	return out, rows.Err()
}

// depsDone reports whether every dep story has StatusDone.
func (s *Store) depsDone(deps []string) (bool, error) {
	for _, dep := range deps {
		var st Status
		err := s.db.QueryRow(`SELECT status FROM stories WHERE id=?`, dep).Scan(&st)
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil // dep doesn't exist yet → not done
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

// ---- JSON helpers for http layer --------------------------------------------

// MarshalDeps encodes a []string to a JSON column (used when embedding deps in
// a single-row response that doesn't use story_deps). Provided for callers that
// prefer a JSON column rather than the join table.
func MarshalDeps(deps []string) string {
	if len(deps) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(deps)
	return string(b)
}
