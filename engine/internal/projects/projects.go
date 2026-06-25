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

// Project holds the metadata for a single project.
type Project struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Repo        string `json:"repo"` // HTTPS GitHub URL
	CreatedAt   int64  `json:"created_at"`
}

const schema = `
CREATE TABLE IF NOT EXISTS projects (
  id          TEXT PRIMARY KEY,
  name        TEXT NOT NULL DEFAULT '',
  description TEXT NOT NULL DEFAULT '',
  repo        TEXT NOT NULL DEFAULT '',
  created_at  INTEGER NOT NULL DEFAULT 0
);
`

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
	return &Store{db: db, Now: time.Now}, nil
}

// Close closes the underlying database.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) now() int64 { return s.Now().UnixMilli() }

// Create inserts a Project. id must be non-empty. Returns ErrExists on duplicate.
func (s *Store) Create(p Project) (Project, error) {
	if p.ID == "" {
		return Project{}, errors.New("project id is required")
	}
	if p.CreatedAt == 0 {
		p.CreatedAt = s.now()
	}
	_, err := s.db.Exec(`INSERT INTO projects(id, name, description, repo, created_at) VALUES(?,?,?,?,?)`,
		p.ID, p.Name, p.Description, p.Repo, p.CreatedAt)
	if err != nil {
		if isUniqueConstraint(err) {
			return Project{}, fmt.Errorf("%w: %s", ErrExists, p.ID)
		}
		return Project{}, err
	}
	return p, nil
}

// Get loads a Project by id.
func (s *Store) Get(id string) (Project, error) {
	var p Project
	err := s.db.QueryRow(`SELECT id, name, description, repo, created_at FROM projects WHERE id=?`, id).
		Scan(&p.ID, &p.Name, &p.Description, &p.Repo, &p.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Project{}, ErrNotFound
	}
	if err != nil {
		return Project{}, err
	}
	return p, nil
}

// List returns all projects ordered by created_at descending.
func (s *Store) List() ([]Project, error) {
	rows, err := s.db.Query(`SELECT id, name, description, repo, created_at FROM projects ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Project
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.Repo, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// isUniqueConstraint reports whether err is a sqlite UNIQUE violation.
func isUniqueConstraint(err error) bool {
	if err == nil {
		return false
	}
	return contains(err.Error(), "UNIQUE constraint failed")
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
