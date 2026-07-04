package api

// Resolución de credenciales GitHub por tenant (multi-tenant, GTM §auth).
//
// ghFor(projectID) devuelve el cliente GitHub correcto para operar el proyecto:
//
//	1. Token user-to-server del DUEÑO del proyecto (del OAuth "Continuar con
//	   GitHub"), refrescado y persistido si expiró. Es el único que puede usar
//	   la Agent tasks API de Copilot y el billing.
//	2. Installation token de la App para el repo (acuñado con el PEM). Cubre
//	   issues/PRs/actions/contents/secrets sin presencia del usuario.
//	3. Fallback: la auth del HOST (gh CLI) — el modo dev/single-user de siempre.
//
// El fallback garantiza que una instancia local sin App configurada siga
// funcionando exactamente igual que antes.

import (
	"context"
	"log"
	"strings"
	"time"

	"forge/internal/auth"
	"forge/internal/ghapp"
	"forge/internal/github"
)

// GHForProject es la versión exportada de ghFor — la usa el conductor loop
// (app.go) para operar cada proyecto con su credencial.
func (s *Server) GHForProject(ctx context.Context, projectID string) *github.Client {
	return s.ghFor(ctx, projectID)
}

// ghFor resuelve el cliente GitHub para un proyecto (ver orden arriba).
func (s *Server) ghFor(ctx context.Context, projectID string) *github.Client {
	tok := s.tenantToken(ctx, projectID)
	if tok == "" {
		return github.New()
	}
	return github.NewWithToken(tok)
}

// tenantToken devuelve el mejor token disponible para el proyecto ("" = host).
func (s *Server) tenantToken(ctx context.Context, projectID string) string {
	if s.Projects == nil || s.Auth == nil {
		return ""
	}
	p, err := s.Projects.Get(projectID)
	if err != nil {
		return ""
	}
	creds, _ := ghapp.LoadCredentials(s.ghAppCredsPath())

	// 1) token de usuario del dueño (refrescado si hace falta)
	if p.OwnerID != "" {
		if t, ok, _ := s.Auth.GitHubTokenFor(p.OwnerID); ok {
			if t.ExpiresAt == 0 || time.Now().UnixMilli() < t.ExpiresAt-60_000 {
				return t.AccessToken
			}
			if t.RefreshToken != "" && creds.Configured() {
				fresh, err := ghapp.RefreshUserToken(ctx, creds.ClientID, creds.ClientSecret, t.RefreshToken)
				if err == nil {
					var exp int64
					if fresh.ExpiresIn > 0 {
						exp = time.Now().Add(time.Duration(fresh.ExpiresIn) * time.Second).UnixMilli()
					}
					_ = s.Auth.SetGitHubToken(p.OwnerID, auth.GitHubToken{
						Login: t.Login, AccessToken: fresh.AccessToken,
						RefreshToken: fresh.RefreshToken, ExpiresAt: exp,
					})
					return fresh.AccessToken
				}
				log.Printf("tenant: refresh del token de %s falló: %v", p.OwnerID, err)
			}
		}
	}

	// 2) installation token de la App para el repo
	if creds.Configured() && p.Repo != "" {
		if slug := repoSlugFromURL(p.Repo); slug != "" {
			tok, err := ghapp.InstallationTokenForRepo(ctx, creds, slug)
			if err == nil {
				return tok
			}
			log.Printf("tenant: installation token para %s falló: %v", slug, err)
		}
	}

	// 3) host
	return ""
}

// repoSlugFromURL extrae owner/repo de una URL https de GitHub.
func repoSlugFromURL(repoURL string) string {
	u := strings.TrimSuffix(strings.TrimSpace(repoURL), ".git")
	i := strings.Index(u, "github.com/")
	if i < 0 {
		return ""
	}
	parts := strings.Split(strings.Trim(u[i+len("github.com/"):], "/"), "/")
	if len(parts) < 2 {
		return ""
	}
	return parts[0] + "/" + parts[1]
}
