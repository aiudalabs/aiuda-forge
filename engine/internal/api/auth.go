package api

import (
	"errors"
	"net/http"
	"strings"

	"forge/internal/auth"
)

// ---- auth handlers ----------------------------------------------------------
//
// These implement the local email+password flow (audit C1). They are wired only
// when an auth store is configured (needAuth guard). POST /auth/login is the one
// authenticated-API exemption in the middleware; logout and me require a token.

// needAuth guards handlers that require the auth store, returning 503 if it has
// not been wired in (no VIBEFORGE_AUTH_DB configured).
func (s *Server) needAuth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.Auth == nil {
			httpErr(w, http.StatusServiceUnavailable, "auth not configured")
			return
		}
		h(w, r)
	}
}

type loginReq struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// userView is the safe public projection of a user (no password hash).
type userView struct {
	ID        string `json:"id"`
	Email     string `json:"email"`
	CreatedAt int64  `json:"created_at"`
}

func toUserView(u auth.User) userView {
	return userView{ID: u.ID, Email: u.Email, CreatedAt: u.CreatedAt}
}

// login handles POST /auth/login: verifies credentials and returns a session
// token. A bad credential is 401 with a generic message (no enumeration). The
// password is never logged or echoed.
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var req loginReq
	if !readJSON(w, r, &req) {
		return
	}
	if req.Email == "" || req.Password == "" {
		httpErr(w, http.StatusBadRequest, "email and password are required")
		return
	}
	u, sess, err := s.Auth.Authenticate(req.Email, req.Password)
	if err != nil {
		if errors.Is(err, auth.ErrBadCredential) {
			httpErr(w, http.StatusUnauthorized, "invalid email or password")
			return
		}
		httpErr(w, http.StatusInternalServerError, "login failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token": sess.Token,
		"user":  toUserView(u),
	})
}

// register handles POST /auth/register: open self-service signup. Creates the user
// and immediately mints a session (so the console lands logged-in), mirroring the
// login response shape. A duplicate email is 409; a weak/empty password is 400. The
// password is never logged or echoed. Email verification/captcha are out of scope
// for v1.2 (HARDENING_BACKLOG).
func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	var req loginReq // same {email,password} shape as login
	if !readJSON(w, r, &req) {
		return
	}
	if req.Email == "" || req.Password == "" {
		httpErr(w, http.StatusBadRequest, "email and password are required")
		return
	}
	if len(req.Password) < 8 {
		httpErr(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}
	if _, err := s.Auth.CreateUser(req.Email, req.Password); err != nil {
		if errors.Is(err, auth.ErrExists) {
			httpErr(w, http.StatusConflict, "an account with that email already exists")
			return
		}
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	// Log the new user in by authenticating the just-set credentials.
	u, sess, err := s.Auth.Authenticate(req.Email, req.Password)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, "account created but sign-in failed")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"token": sess.Token,
		"user":  toUserView(u),
	})
}

// logout handles POST /auth/logout: deletes the presented session token. Always
// returns 200 (idempotent) — an unknown token is simply a no-op.
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	tok := bearer(r)
	if tok != "" {
		_ = s.Auth.DeleteSession(tok)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// me handles GET /auth/me: returns the current user for the presented session
// token. A service-token request (no session) returns 200 with user:null so the
// orchestrator's token doesn't 404 the endpoint.
func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	tok := bearer(r)
	u, err := s.Auth.UserForToken(tok)
	if err != nil {
		// No session user (e.g. service-token caller). The middleware already
		// authorized the request; report no user rather than an error.
		writeJSON(w, http.StatusOK, map[string]any{"user": nil})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": toUserView(u)})
}

// changePassword lets a logged-in user rotate their own password (D8/#10). It
// requires a user session (not the service token), verifies the current password,
// and invalidates all of the user's sessions so they re-authenticate.
func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	u, err := s.Auth.UserForToken(bearer(r))
	if err != nil {
		httpErr(w, http.StatusForbidden, "change-password requires a user session")
		return
	}
	var req struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if err := s.Auth.ChangePassword(u.ID, req.OldPassword, req.NewPassword); err != nil {
		if errors.Is(err, auth.ErrBadCredential) {
			httpErr(w, http.StatusUnauthorized, "current password is incorrect")
			return
		}
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"changed": true})
}

// bearer extracts the token from an Authorization: Bearer header.
func bearer(r *http.Request) string {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, prefix) {
		return ""
	}
	return strings.TrimPrefix(h, prefix)
}
