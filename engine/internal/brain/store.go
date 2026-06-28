package brain

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// Store persists Brain conversations, their messages, and proposed mutating
// actions. One conversation per project (memory across turns). Mirrors the
// DSN/schema pattern of internal/auth and internal/projects.
type Store struct {
	db  *sql.DB
	now func() int64
}

const brainSchema = `
CREATE TABLE IF NOT EXISTS brain_conversations (
  id         TEXT PRIMARY KEY,
  project_id TEXT NOT NULL,
  created_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_brain_conv_project ON brain_conversations(project_id);
CREATE TABLE IF NOT EXISTS brain_messages (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  conv_id    TEXT NOT NULL,
  role       TEXT NOT NULL,
  content    TEXT NOT NULL,
  created_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_brain_msg_conv ON brain_messages(conv_id);
CREATE TABLE IF NOT EXISTS brain_actions (
  id         TEXT PRIMARY KEY,
  conv_id    TEXT NOT NULL,
  tool       TEXT NOT NULL,
  args       TEXT NOT NULL,
  status     TEXT NOT NULL DEFAULT 'pending',
  created_at INTEGER NOT NULL DEFAULT 0
);
`

// Open opens (creating if needed) the Brain sqlite database at path.
func Open(path string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_txlock=immediate&_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(brainSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("brain schema: %w", err)
	}
	return &Store{db: db, now: func() int64 { return time.Now().UnixMilli() }}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func newID(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return prefix + "-" + hex.EncodeToString(b)
}

// ConversationFor returns the project's single conversation id, creating it the
// first time. (One persistent thread per project.)
func (s *Store) ConversationFor(projectID string) (string, error) {
	var id string
	err := s.db.QueryRow(`SELECT id FROM brain_conversations WHERE project_id=? ORDER BY created_at ASC LIMIT 1`, projectID).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != sql.ErrNoRows {
		return "", err
	}
	id = newID("conv")
	if _, err := s.db.Exec(`INSERT INTO brain_conversations(id, project_id, created_at) VALUES(?,?,?)`, id, projectID, s.now()); err != nil {
		return "", err
	}
	return id, nil
}

// AppendMessage stores a display/history message (role + text).
func (s *Store) AppendMessage(convID, role, content string) error {
	_, err := s.db.Exec(`INSERT INTO brain_messages(conv_id, role, content, created_at) VALUES(?,?,?,?)`,
		convID, role, content, s.now())
	return err
}

// History returns the conversation as Anthropic Messages (one text block each),
// oldest first — the cross-turn context fed to the model.
func (s *Store) History(convID string) ([]Message, error) {
	rows, err := s.db.Query(`SELECT role, content FROM brain_messages WHERE conv_id=? ORDER BY id ASC`, convID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var role, content string
		if err := rows.Scan(&role, &content); err != nil {
			return nil, err
		}
		out = append(out, Message{Role: role, Content: []ContentBlock{{Type: "text", Text: content}}})
	}
	return out, rows.Err()
}

// HistoryView returns the conversation as {role, content} maps for the UI.
func (s *Store) HistoryView(convID string) ([]map[string]any, error) {
	rows, err := s.db.Query(`SELECT role, content, created_at FROM brain_messages WHERE conv_id=? ORDER BY id ASC`, convID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var role, content string
		var ts int64
		if err := rows.Scan(&role, &content, &ts); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"role": role, "content": content, "created_at": ts})
	}
	return out, rows.Err()
}

// CreateAction records a proposed mutating action (status pending) and returns its id.
func (s *Store) CreateAction(convID, tool, argsJSON string) (string, error) {
	id := newID("act")
	_, err := s.db.Exec(`INSERT INTO brain_actions(id, conv_id, tool, args, status, created_at) VALUES(?,?,?,?,?,?)`,
		id, convID, tool, argsJSON, "pending", s.now())
	return id, err
}

// ActionStatus returns a proposed action's status (pending|approved|rejected).
func (s *Store) ActionStatus(actionID string) (string, error) {
	var st string
	err := s.db.QueryRow(`SELECT status FROM brain_actions WHERE id=?`, actionID).Scan(&st)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("action not found")
	}
	return st, err
}

// SetActionStatus updates a proposed action's status.
func (s *Store) SetActionStatus(actionID, status string) error {
	_, err := s.db.Exec(`UPDATE brain_actions SET status=? WHERE id=?`, status, actionID)
	return err
}
