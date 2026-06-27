// Package auth is the control-plane local-auth store: email+password users and
// opaque session tokens, persisted in sqlite. It mirrors the DSN/store patterns
// of internal/projects and internal/tickets.
//
// Security posture (audit C1):
//   - passwords are hashed with bcrypt; the plaintext is never stored or logged.
//   - session tokens are 32 random bytes (hex), opaque and unguessable; they are
//     the bearer credential the console sends as "Authorization: Bearer <token>".
//   - token lookup uses a constant-time compare on the bcrypt verify path; the
//     session token itself is a primary-key lookup (random + high-entropy).
package auth

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

// Errors returned by the store. Callers translate these to HTTP status codes.
var (
	ErrNotFound      = errors.New("not found")
	ErrExists        = errors.New("user already exists")
	ErrBadCredential = errors.New("invalid email or password")
)

// SessionTTL is how long a session token is valid after login.
const SessionTTL = 7 * 24 * time.Hour

// User is a control-plane account. PasswordHash is never serialized to JSON.
type User struct {
	ID        string `json:"id"`
	Email     string `json:"email"`
	hash      string // bcrypt hash; unexported so it never leaks via JSON
	CreatedAt int64  `json:"created_at"`
}

// Session is an opaque bearer token bound to a user with an expiry.
type Session struct {
	Token     string
	UserID    string
	CreatedAt int64
	ExpiresAt int64
}

const schema = `
CREATE TABLE IF NOT EXISTS users (
  id            TEXT PRIMARY KEY,
  email         TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,
  created_at    INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS sessions (
  token      TEXT PRIMARY KEY,
  user_id    TEXT NOT NULL,
  created_at INTEGER NOT NULL DEFAULT 0,
  expires_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id);
`

// Store is the auth store backed by a sqlite database.
type Store struct {
	db  *sql.DB
	Now func() time.Time // injectable for tests
}

// Open opens (creating if needed) the sqlite database at path and applies the
// schema. _txlock=immediate matches the kernel store so concurrent writers take
// the write lock up front; SetMaxOpenConns(1) keeps the single-writer invariant.
func Open(path string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_txlock=immediate&_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &Store{db: db, Now: time.Now}, nil
}

// Close closes the underlying database.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) now() time.Time { return s.Now() }

// normalizeEmail lower-cases and trims an email so login is case-insensitive and
// the UNIQUE constraint can't be bypassed by casing.
func normalizeEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

// CountUsers returns the number of users. Used at startup to decide whether to
// seed the first admin and whether auth is mandatory.
func (s *Store) CountUsers() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// CreateUser inserts a user with a bcrypt-hashed password. email is normalized;
// password must be non-empty. Returns ErrExists on a duplicate email.
func (s *Store) CreateUser(email, password string) (User, error) {
	email = normalizeEmail(email)
	if email == "" {
		return User{}, errors.New("email is required")
	}
	if password == "" {
		return User{}, errors.New("password is required")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return User{}, fmt.Errorf("hash password: %w", err)
	}
	u := User{
		ID:        newID("usr"),
		Email:     email,
		hash:      string(hash),
		CreatedAt: s.now().UnixMilli(),
	}
	_, err = s.db.Exec(`INSERT INTO users(id, email, password_hash, created_at) VALUES(?,?,?,?)`,
		u.ID, u.Email, u.hash, u.CreatedAt)
	if err != nil {
		if isUniqueConstraint(err) {
			return User{}, fmt.Errorf("%w: %s", ErrExists, email)
		}
		return User{}, err
	}
	return u, nil
}

// Authenticate verifies email+password and, on success, creates and returns a
// new session token. The bcrypt CompareHashAndPassword is constant-time. A
// missing user still runs a bcrypt compare against a dummy hash so the response
// time does not reveal whether the email exists (user-enumeration defense).
func (s *Store) Authenticate(email, password string) (User, Session, error) {
	email = normalizeEmail(email)
	u, err := s.getUserByEmail(email)
	if errors.Is(err, ErrNotFound) {
		// Compare against a fixed dummy hash to equalize timing, then fail.
		_ = bcrypt.CompareHashAndPassword([]byte(dummyHash), []byte(password))
		return User{}, Session{}, ErrBadCredential
	}
	if err != nil {
		return User{}, Session{}, err
	}
	if bcrypt.CompareHashAndPassword([]byte(u.hash), []byte(password)) != nil {
		return User{}, Session{}, ErrBadCredential
	}
	sess, err := s.newSession(u.ID)
	if err != nil {
		return User{}, Session{}, err
	}
	return u, sess, nil
}

// ChangePassword verifies the user's current password and replaces it with a fresh
// bcrypt hash. All of the user's sessions are then invalidated — a password change
// logs the user out everywhere, so they must sign in again (D8/#10).
func (s *Store) ChangePassword(userID, oldPassword, newPassword string) error {
	if newPassword == "" {
		return errors.New("new password is required")
	}
	var hash string
	err := s.db.QueryRow(`SELECT password_hash FROM users WHERE id=?`, userID).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(oldPassword)) != nil {
		return ErrBadCredential
	}
	newHash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE users SET password_hash=? WHERE id=?`, string(newHash), userID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM sessions WHERE user_id=?`, userID); err != nil {
		return err
	}
	return tx.Commit()
}

// dummyHash is a valid bcrypt hash of a random string, used only to equalize
// Authenticate's timing for non-existent users. It is not a credential.
const dummyHash = "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"

// newSession mints a random opaque token bound to userID with the default TTL.
func (s *Store) newSession(userID string) (Session, error) {
	tok, err := randomToken()
	if err != nil {
		return Session{}, err
	}
	now := s.now()
	sess := Session{
		Token:     tok,
		UserID:    userID,
		CreatedAt: now.UnixMilli(),
		ExpiresAt: now.Add(SessionTTL).UnixMilli(),
	}
	_, err = s.db.Exec(`INSERT INTO sessions(token, user_id, created_at, expires_at) VALUES(?,?,?,?)`,
		sess.Token, sess.UserID, sess.CreatedAt, sess.ExpiresAt)
	if err != nil {
		return Session{}, err
	}
	return sess, nil
}

// UserForToken resolves a session token to its user, rejecting expired or
// unknown tokens with ErrNotFound. Expired sessions are deleted opportunistically.
func (s *Store) UserForToken(token string) (User, error) {
	if token == "" {
		return User{}, ErrNotFound
	}
	var userID string
	var expiresAt int64
	err := s.db.QueryRow(`SELECT user_id, expires_at FROM sessions WHERE token=?`, token).
		Scan(&userID, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, err
	}
	if s.now().UnixMilli() >= expiresAt {
		_, _ = s.db.Exec(`DELETE FROM sessions WHERE token=?`, token)
		return User{}, ErrNotFound
	}
	return s.getUserByID(userID)
}

// UserIDForToken resolves a session token to its user id, satisfying
// httpx.SessionValidator for the mandatory-auth middleware. It returns
// ErrNotFound for unknown/expired tokens.
func (s *Store) UserIDForToken(token string) (string, error) {
	u, err := s.UserForToken(token)
	if err != nil {
		return "", err
	}
	return u.ID, nil
}

// DeleteSession removes a session token (logout). A no-op for unknown tokens.
func (s *Store) DeleteSession(token string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE token=?`, token)
	return err
}

func (s *Store) getUserByEmail(email string) (User, error) {
	var u User
	err := s.db.QueryRow(`SELECT id, email, password_hash, created_at FROM users WHERE email=?`, email).
		Scan(&u.ID, &u.Email, &u.hash, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return u, err
}

func (s *Store) getUserByID(id string) (User, error) {
	var u User
	err := s.db.QueryRow(`SELECT id, email, password_hash, created_at FROM users WHERE id=?`, id).
		Scan(&u.ID, &u.Email, &u.hash, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return u, err
}

// randomToken returns 32 random bytes hex-encoded (64 hex chars).
func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// newID returns a short random id with a prefix (e.g. "usr-<16hex>").
func newID(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return prefix + "-" + hex.EncodeToString(b)
}

// isUniqueConstraint reports whether err is a sqlite UNIQUE violation.
func isUniqueConstraint(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}
