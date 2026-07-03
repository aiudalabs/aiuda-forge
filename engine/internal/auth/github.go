package auth

// Identidad GitHub (onboarding Forja): el login OAuth de la GitHub App crea o
// enlaza el usuario local y guarda su token user-to-server — la credencial que
// la Agent tasks API de Copilot y el billing EXIGEN humana (una GitHub App
// installation token no sirve ahí; hallazgo GTM 2026-07-03).

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
)

// GitHubConnector es el connector de channel_identities para GitHub.
const GitHubConnector = "github"

// GitHubToken es el token user-to-server almacenado de un usuario.
type GitHubToken struct {
	Login        string
	AccessToken  string
	RefreshToken string
	ExpiresAt    int64 // unix ms; 0 = no expira (App sin expiración activada)
}

// EnsureGitHubUser resuelve el usuario local detrás de un usuario de GitHub:
// identidad ya enlazada → ese usuario; email coincidente → enlaza a ese usuario
// (account linking); si no existe → crea la cuenta (password aleatorio: el
// login de esa cuenta es GitHub). Devuelve el usuario listo para sesión.
func (s *Store) EnsureGitHubUser(ghID, login, email string) (User, error) {
	if uid, ok, err := s.UserForChannelIdentity(GitHubConnector, ghID); err != nil {
		return User{}, err
	} else if ok {
		return s.GetUser(uid)
	}
	// Sin identidad previa: intentar por email; si no, crear.
	if email == "" {
		// GitHub puede no exponer email — cuenta sintética estable por login.
		email = login + "@users.noreply.github.com"
	}
	u, err := s.getUserByEmail(normalizeEmail(email))
	if errors.Is(err, ErrNotFound) {
		pw := make([]byte, 24)
		if _, rerr := rand.Read(pw); rerr != nil {
			return User{}, rerr
		}
		u, err = s.CreateUser(email, hex.EncodeToString(pw))
	}
	if err != nil {
		return User{}, err
	}
	if err := s.BindChannelIdentity(GitHubConnector, ghID, u.ID); err != nil {
		return User{}, err
	}
	return u, nil
}

// GetUser devuelve un usuario por id.
func (s *Store) GetUser(id string) (User, error) {
	var u User
	err := s.db.QueryRow(`SELECT id, email, password_hash, created_at FROM users WHERE id=?`, id).
		Scan(&u.ID, &u.Email, &u.hash, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return u, err
}

// SessionForUser mints a session for an already-authenticated user (OAuth).
func (s *Store) SessionForUser(userID string) (Session, error) {
	return s.newSession(userID)
}

// SetGitHubToken guarda (upsert) el token user-to-server de un usuario.
func (s *Store) SetGitHubToken(userID string, t GitHubToken) error {
	if userID == "" || t.AccessToken == "" {
		return fmt.Errorf("userID and access token required")
	}
	_, err := s.db.Exec(`INSERT INTO github_tokens(user_id, gh_login, access_token, refresh_token, expires_at, created_at)
		VALUES(?,?,?,?,?,?)
		ON CONFLICT(user_id) DO UPDATE SET gh_login=excluded.gh_login, access_token=excluded.access_token,
			refresh_token=excluded.refresh_token, expires_at=excluded.expires_at`,
		userID, t.Login, t.AccessToken, t.RefreshToken, t.ExpiresAt, s.now().UnixMilli())
	return err
}

// GitHubTokenFor devuelve el token guardado del usuario, ok=false si no hay.
func (s *Store) GitHubTokenFor(userID string) (GitHubToken, bool, error) {
	var t GitHubToken
	err := s.db.QueryRow(`SELECT gh_login, access_token, refresh_token, expires_at FROM github_tokens WHERE user_id=?`, userID).
		Scan(&t.Login, &t.AccessToken, &t.RefreshToken, &t.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return GitHubToken{}, false, nil
	}
	if err != nil {
		return GitHubToken{}, false, err
	}
	return t, true, nil
}
