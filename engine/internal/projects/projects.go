// Package projects is the control-plane project store: persists project→repo
// mappings in a sqlite database. A project is the unit that owns a GitHub repo;
// runs for a project carry the repo URL in their trigger payload.
package projects

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// ErrNotFound is returned when a project does not exist.
var ErrNotFound = errors.New("not found")

// ErrExists is returned when a project with the given ID already exists.
var ErrExists = errors.New("project already exists")

// ErrInvalid wraps a per-project settings validation failure so the HTTP layer
// can answer 400 instead of 500.
var ErrInvalid = errors.New("invalid project settings")

// Per-project execution settings vocabulary (audit A2). These were process-global
// in internal/settings; they now live per-project so two projects can run under
// different modes in the same scheduler cycle.
const (
	ExecutionUnitSprint = "sprint" // implement a whole sprint in ONE run → ONE PR (default)
	ExecutionUnitStory  = "story"  // one run/PR per story
	MergeModeManual     = "manual" // a human merges the PR on GitHub (default)
	MergeModeAuto       = "auto"   // the scheduler merges the reviewed PR itself
)

// DefaultProjectID is the project that pre-multi-tenant data is backfilled to. It
// is created (owner_id="") on first Open so the backfilled runs/stories have a
// real project row to point at. Mirrors store.DefaultProjectID.
const DefaultProjectID = "default"

// Settings is a project's execution configuration (audit A2). The scheduler reads
// it per-project each cycle to apply the right execution_unit and merge_mode.
type Settings struct {
	ExecutionUnit string `json:"execution_unit"`
	MergeMode     string `json:"merge_mode"`
}

// Project holds the metadata for a single project. OwnerID is the auth user id
// (usr-…) that created it; GET /projects is filtered by it so a user sees only
// their own projects. ExecutionUnit/MergeMode are the per-project settings.
type Project struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	Repo          string `json:"repo"` // HTTPS GitHub URL
	OwnerID       string `json:"owner_id"`
	ExecutionUnit string `json:"execution_unit"`
	MergeMode     string `json:"merge_mode"`
	CreatedAt     int64  `json:"created_at"`
}

const schema = `
CREATE TABLE IF NOT EXISTS projects (
  id             TEXT PRIMARY KEY,
  name           TEXT NOT NULL DEFAULT '',
  description    TEXT NOT NULL DEFAULT '',
  repo           TEXT NOT NULL DEFAULT '',
  owner_id       TEXT NOT NULL DEFAULT '',
  execution_unit TEXT NOT NULL DEFAULT 'sprint',
  merge_mode     TEXT NOT NULL DEFAULT 'manual',
  created_at     INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS project_members (
  project_id TEXT NOT NULL,
  user_id    TEXT NOT NULL,
  role       TEXT NOT NULL,
  created_at INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (project_id, user_id)
);
CREATE TABLE IF NOT EXISTS project_invites (
  token       TEXT PRIMARY KEY,
  project_id  TEXT NOT NULL,
  email       TEXT NOT NULL,
  role        TEXT NOT NULL,
  created_at  INTEGER NOT NULL DEFAULT 0,
  accepted_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_invites_project ON project_invites(project_id);
CREATE TABLE IF NOT EXISTS project_channels (
  project_id TEXT NOT NULL,
  connector  TEXT NOT NULL,
  target     TEXT NOT NULL,
  events     TEXT NOT NULL DEFAULT '*',
  created_at INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (project_id, connector, target)
);
CREATE INDEX IF NOT EXISTS idx_channels_project ON project_channels(project_id);
`

// migrations add the multi-tenant columns to project DBs predating them (audit
// A1/A2). Guarded: a "duplicate column name" error means already migrated.
var migrations = []string{
	`ALTER TABLE projects ADD COLUMN owner_id TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE projects ADD COLUMN execution_unit TEXT NOT NULL DEFAULT 'sprint'`,
	`ALTER TABLE projects ADD COLUMN merge_mode TEXT NOT NULL DEFAULT 'manual'`,
}

// validExecutionUnit / validMergeMode bound the accepted settings values so the
// scheduler never reads a garbage mode (audit A2).
func validExecutionUnit(v string) bool { return v == ExecutionUnitSprint || v == ExecutionUnitStory }
func validMergeMode(v string) bool     { return v == MergeModeManual || v == MergeModeAuto }

// Store is the project store backed by a sqlite database.
type Store struct {
	db  *sql.DB
	Now func() time.Time // injectable for tests
}

// Open opens (creating if needed) the sqlite database at path and applies the schema.
// Matches the DSN pattern used by internal/tickets.
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
	// Multi-tenant column migration (audit A1/A2) for DBs predating owner_id and
	// the per-project settings. Idempotent — duplicate-column means already migrated.
	for _, m := range migrations {
		if _, err := db.Exec(m); err != nil && !isDuplicateColumn(err) {
			db.Close()
			return nil, fmt.Errorf("migrate projects: %w", err)
		}
	}
	s := &Store{db: db, Now: time.Now}
	// Ensure the backfill target exists: pre-multi-tenant runs/stories are stamped
	// project_id="default", so the default project row must exist (owner_id="") for
	// those rows to point at a real project. Idempotent on restart.
	if err := s.ensureDefaultProject(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ensure default project: %w", err)
	}
	// Backfill owner memberships (v1.2 roles): projects created before the
	// project_members table have no owner row, so the owner wouldn't appear in the
	// members list. Record each owner_id as an 'owner' member. Idempotent.
	if err := s.backfillOwnerMembers(); err != nil {
		db.Close()
		return nil, fmt.Errorf("backfill owner members: %w", err)
	}
	return s, nil
}

// backfillOwnerMembers ensures every owned project has its owner_id recorded as an
// 'owner' member. INSERT OR IGNORE skips projects already carrying the row.
func (s *Store) backfillOwnerMembers() error {
	_, err := s.db.Exec(
		`INSERT OR IGNORE INTO project_members(project_id, user_id, role, created_at)
		 SELECT id, owner_id, ?, created_at FROM projects WHERE owner_id != ''`,
		RoleOwner)
	return err
}

// Close closes the underlying database.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) now() int64 { return s.Now().UnixMilli() }

// ensureDefaultProject creates the "default" project (owner_id="") if absent so
// backfilled single-tenant data has a real project row. An existing row (any
// owner) is left untouched.
func (s *Store) ensureDefaultProject() error {
	_, err := s.db.Exec(
		`INSERT OR IGNORE INTO projects(id, name, description, repo, owner_id, execution_unit, merge_mode, created_at)
		 VALUES(?,?,?,?,?,?,?,?)`,
		DefaultProjectID, "Default", "Backfill project for pre-multi-tenant data", "",
		"", ExecutionUnitSprint, MergeModeManual, s.now())
	return err
}

// Create inserts a Project. id must be non-empty. Unset ExecutionUnit/MergeMode
// default to sprint/manual. Returns ErrExists on duplicate.
func (s *Store) Create(p Project) (Project, error) {
	if p.ID == "" {
		return Project{}, errors.New("project id is required")
	}
	if p.CreatedAt == 0 {
		p.CreatedAt = s.now()
	}
	if p.ExecutionUnit == "" {
		p.ExecutionUnit = ExecutionUnitSprint
	}
	if p.MergeMode == "" {
		p.MergeMode = MergeModeManual
	}
	_, err := s.db.Exec(
		`INSERT INTO projects(id, name, description, repo, owner_id, execution_unit, merge_mode, created_at)
		 VALUES(?,?,?,?,?,?,?,?)`,
		p.ID, p.Name, p.Description, p.Repo, p.OwnerID, p.ExecutionUnit, p.MergeMode, p.CreatedAt)
	if err != nil {
		if isUniqueConstraint(err) {
			return Project{}, fmt.Errorf("%w: %s", ErrExists, p.ID)
		}
		return Project{}, err
	}
	// The creator is the project's owner — also recorded as a member so the roles
	// model (owner·editor·viewer) has a row from day one.
	if p.OwnerID != "" {
		_ = s.AddMember(p.ID, p.OwnerID, RoleOwner)
	}
	return p, nil
}

const projectCols = `SELECT id, name, description, repo, owner_id, execution_unit, merge_mode, created_at FROM projects`

func scanProject(row interface{ Scan(...any) error }) (Project, error) {
	var p Project
	err := row.Scan(&p.ID, &p.Name, &p.Description, &p.Repo, &p.OwnerID, &p.ExecutionUnit, &p.MergeMode, &p.CreatedAt)
	return p, err
}

// Get loads a Project by id.
func (s *Store) Get(id string) (Project, error) {
	p, err := scanProject(s.db.QueryRow(projectCols+` WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Project{}, ErrNotFound
	}
	if err != nil {
		return Project{}, err
	}
	return p, nil
}

// List returns all projects (every owner) ordered by created_at descending. To
// scope to one owner use ListByOwner.
func (s *Store) List() ([]Project, error) {
	return s.query(projectCols + ` ORDER BY created_at DESC`)
}

// ListByOwner returns only the projects owned by ownerID (audit A1): GET /projects
// is scoped to the authenticated user so they never see another user's projects.
func (s *Store) ListByOwner(ownerID string) ([]Project, error) {
	return s.query(projectCols+` WHERE owner_id=? ORDER BY created_at DESC`, ownerID)
}

// ListForMember returns every project the user can access under the roles model
// (v1.2): projects where they hold ANY membership (owner·editor·viewer), unioned
// with projects whose legacy owner_id is them (back-compat for rows predating the
// members table). This is what GET /projects scopes to so a shared editor/viewer
// sees the projects invited to them, not only ones they created.
func (s *Store) ListForMember(userID string) ([]Project, error) {
	q := projectCols + `
		WHERE id IN (SELECT project_id FROM project_members WHERE user_id=?)
		   OR owner_id=?
		ORDER BY created_at DESC`
	return s.query(q, userID, userID)
}

func (s *Store) query(q string, args ...any) ([]Project, error) {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Project
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetSettings returns a project's per-project execution settings (audit A2).
func (s *Store) GetSettings(id string) (Settings, error) {
	p, err := s.Get(id)
	if err != nil {
		return Settings{}, err
	}
	return Settings{ExecutionUnit: p.ExecutionUnit, MergeMode: p.MergeMode}, nil
}

// PutSettings validates and persists a project's execution settings, returning
// the stored result. An empty field means "unchanged"; a present field must be a
// valid value (ErrInvalid otherwise). Returns ErrNotFound for an unknown project.
func (s *Store) PutSettings(id string, in Settings) (Settings, error) {
	cur, err := s.GetSettings(id)
	if err != nil {
		return Settings{}, err
	}
	if in.ExecutionUnit != "" {
		if !validExecutionUnit(in.ExecutionUnit) {
			return Settings{}, fmt.Errorf("%w: execution_unit %q must be %q or %q",
				ErrInvalid, in.ExecutionUnit, ExecutionUnitSprint, ExecutionUnitStory)
		}
		cur.ExecutionUnit = in.ExecutionUnit
	}
	if in.MergeMode != "" {
		if !validMergeMode(in.MergeMode) {
			return Settings{}, fmt.Errorf("%w: merge_mode %q must be %q or %q",
				ErrInvalid, in.MergeMode, MergeModeManual, MergeModeAuto)
		}
		cur.MergeMode = in.MergeMode
	}
	res, err := s.db.Exec(`UPDATE projects SET execution_unit=?, merge_mode=? WHERE id=?`,
		cur.ExecutionUnit, cur.MergeMode, id)
	if err != nil {
		return Settings{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Settings{}, ErrNotFound
	}
	return cur, nil
}

// isUniqueConstraint reports whether err is a sqlite UNIQUE violation.
func isUniqueConstraint(err error) bool {
	if err == nil {
		return false
	}
	return contains(err.Error(), "UNIQUE constraint failed")
}

// isDuplicateColumn reports whether err is a SQLite "duplicate column name"
// error — the signal that a guarded ALTER TABLE ADD COLUMN already ran.
func isDuplicateColumn(err error) bool {
	return err != nil && contains(err.Error(), "duplicate column name")
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
