package api

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"forge/internal/auth"
	github "forge/internal/github"
	"forge/internal/ghapp"
	"forge/internal/httpx"
)

// userOAuthToken returns the stored (refreshed if needed) GitHub user-to-server token for
// userID, or "" if the user hasn't connected GitHub. Mirrors step 1 of tenantToken but keyed
// by an explicit user (used before a project exists, e.g. project creation).
func (s *Server) userOAuthToken(ctx context.Context, userID string) string {
	if s.Auth == nil || userID == "" {
		return ""
	}
	t, ok, _ := s.Auth.GitHubTokenFor(userID)
	if !ok {
		return ""
	}
	if t.ExpiresAt == 0 || time.Now().UnixMilli() < t.ExpiresAt-60_000 {
		return t.AccessToken
	}
	creds, _ := ghapp.LoadCredentials(s.ghAppCredsPath())
	if t.RefreshToken != "" && creds.Configured() {
		if fresh, err := ghapp.RefreshUserToken(ctx, creds.ClientID, creds.ClientSecret, t.RefreshToken); err == nil {
			var exp int64
			if fresh.ExpiresIn > 0 {
				exp = time.Now().Add(time.Duration(fresh.ExpiresIn) * time.Second).UnixMilli()
			}
			_ = s.Auth.SetGitHubToken(userID, auth.GitHubToken{
				Login: t.Login, AccessToken: fresh.AccessToken,
				RefreshToken: fresh.RefreshToken, ExpiresAt: exp,
			})
			return fresh.AccessToken
		} else {
			log.Printf("onboarding: refresh del token de %s falló: %v", userID, err)
		}
	}
	return t.AccessToken // expired but unrefreshable — best effort
}

// ghForUser is a GitHub client scoped to the CURRENT session user's token — for repo
// creation and owner listing BEFORE a project exists (tenantToken keys off a project's
// owner, which doesn't help at creation). Falls back to host auth when the user hasn't
// connected GitHub, so the admin/host flow keeps working unchanged.
func (s *Server) ghForUser(ctx context.Context) *github.Client {
	if tok := s.userOAuthToken(ctx, httpx.UserIDFromContext(ctx)); tok != "" {
		return github.NewWithToken(tok)
	}
	return github.New()
}

// capabilities reports what the CURRENT user still needs for self-serve onboarding:
//
//	github — the user has connected their GitHub (can create repos under their account)
//	claude — the operator has an AI credential wired (design agents can run) — global, not
//	         per-user; informational so the wizard can warn if the instance isn't ready.
func (s *Server) capabilities(w http.ResponseWriter, r *http.Request) {
	uid := httpx.UserIDFromContext(r.Context())
	githubOK := false
	if s.Auth != nil && uid != "" {
		if _, ok, _ := s.Auth.GitHubTokenFor(uid); ok {
			githubOK = true
		}
	}
	claudeOK := os.Getenv("CLAUDE_CODE_OAUTH_TOKEN") != "" || os.Getenv("ANTHROPIC_API_KEY") != ""
	writeJSON(w, http.StatusOK, map[string]any{"github": githubOK, "claude": claudeOK})
}
