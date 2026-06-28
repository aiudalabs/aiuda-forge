package projects

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
)

// ErrInviteUsed is returned when accepting an invite that was already accepted.
var ErrInviteUsed = errors.New("invite already accepted")

// Invite is a pending membership for an email that may not yet have an account.
// Existing users are added directly (no invite); an invite is issued only when the
// email has no account yet. AcceptedAt==0 means pending.
type Invite struct {
	Token      string `json:"token"`
	ProjectID  string `json:"project_id"`
	Email      string `json:"email"`
	Role       string `json:"role"`
	CreatedAt  int64  `json:"created_at"`
	AcceptedAt int64  `json:"accepted_at"`
}

// CreateInvite issues a pending invite for email→role on a project. The email is
// normalized (lower/trim) so acceptance can match it case-insensitively. The token
// is the unguessable credential embedded in the invite link.
func (s *Store) CreateInvite(projectID, email, role string) (Invite, error) {
	tok, err := inviteToken()
	if err != nil {
		return Invite{}, err
	}
	inv := Invite{
		Token:     tok,
		ProjectID: projectID,
		Email:     strings.ToLower(strings.TrimSpace(email)),
		Role:      role,
		CreatedAt: s.now(),
	}
	_, err = s.db.Exec(
		`INSERT INTO project_invites(token, project_id, email, role, created_at, accepted_at) VALUES(?,?,?,?,?,0)`,
		inv.Token, inv.ProjectID, inv.Email, inv.Role, inv.CreatedAt)
	if err != nil {
		return Invite{}, err
	}
	return inv, nil
}

// GetInvite loads an invite by token, ErrNotFound if unknown.
func (s *Store) GetInvite(token string) (Invite, error) {
	var inv Invite
	err := s.db.QueryRow(
		`SELECT token, project_id, email, role, created_at, accepted_at FROM project_invites WHERE token=?`, token).
		Scan(&inv.Token, &inv.ProjectID, &inv.Email, &inv.Role, &inv.CreatedAt, &inv.AcceptedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Invite{}, ErrNotFound
	}
	return inv, err
}

// AcceptInvite consumes a pending invite for userID: it adds the membership and
// marks the invite accepted, atomically. ErrInviteUsed if already accepted. The
// caller is responsible for verifying the accepting user's email matches the invite.
func (s *Store) AcceptInvite(token, userID string) (Invite, error) {
	inv, err := s.GetInvite(token)
	if err != nil {
		return Invite{}, err
	}
	if inv.AcceptedAt != 0 {
		return Invite{}, ErrInviteUsed
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Invite{}, err
	}
	defer tx.Rollback()
	now := s.now()
	if _, err := tx.Exec(
		`INSERT INTO project_members(project_id, user_id, role, created_at) VALUES(?,?,?,?)
		 ON CONFLICT(project_id, user_id) DO UPDATE SET role=excluded.role`,
		inv.ProjectID, userID, inv.Role, now); err != nil {
		return Invite{}, err
	}
	if _, err := tx.Exec(`UPDATE project_invites SET accepted_at=? WHERE token=?`, now, token); err != nil {
		return Invite{}, err
	}
	if err := tx.Commit(); err != nil {
		return Invite{}, err
	}
	inv.AcceptedAt = now
	return inv, nil
}

// PendingInvites lists a project's not-yet-accepted invites (oldest first).
func (s *Store) PendingInvites(projectID string) ([]Invite, error) {
	rows, err := s.db.Query(
		`SELECT token, project_id, email, role, created_at, accepted_at FROM project_invites
		 WHERE project_id=? AND accepted_at=0 ORDER BY created_at ASC`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Invite
	for rows.Next() {
		var inv Invite
		if err := rows.Scan(&inv.Token, &inv.ProjectID, &inv.Email, &inv.Role, &inv.CreatedAt, &inv.AcceptedAt); err != nil {
			return nil, err
		}
		out = append(out, inv)
	}
	return out, rows.Err()
}

// RevokeInvite deletes a pending invite by token.
func (s *Store) RevokeInvite(token string) error {
	_, err := s.db.Exec(`DELETE FROM project_invites WHERE token=?`, token)
	return err
}

// inviteToken returns 24 random bytes hex-encoded (48 hex chars) — the unguessable
// credential in an invite link.
func inviteToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
