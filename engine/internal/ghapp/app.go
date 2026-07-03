package ghapp

// GitHub App "Forja" (onboarding cloud, GTM 2026-07-03): la App se crea vía el
// MANIFEST FLOW — el operador abre /setup/github-app, aprueba en GitHub bajo la
// org (aiudalabs), y GitHub nos devuelve un code que convertimos en las
// credenciales completas (id, client id/secret, webhook secret, PEM). Cero
// copy-paste de secretos. Las credenciales viven en un JSON 0600 junto al
// registry; el flujo OAuth user-to-server usa client_id/secret de aquí.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Credentials es el resultado de convertir el manifest: todo lo que la
// instancia necesita para operar como la App y para el OAuth de usuarios.
type Credentials struct {
	AppID         int64  `json:"app_id"`
	Slug          string `json:"slug"`
	Org           string `json:"org"`
	ClientID      string `json:"client_id"`
	ClientSecret  string `json:"client_secret"`
	WebhookSecret string `json:"webhook_secret"`
	PEM           string `json:"pem"`
	HTMLURL       string `json:"html_url"`
	CreatedAt     int64  `json:"created_at"`
}

// Configured reports whether the app credentials are usable.
func (c Credentials) Configured() bool { return c.AppID != 0 && c.ClientID != "" }

// LoadCredentials lee el JSON de credenciales; un archivo ausente devuelve
// Credentials{} sin error (instancia aún no configurada).
func LoadCredentials(path string) (Credentials, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Credentials{}, nil
	}
	if err != nil {
		return Credentials{}, err
	}
	var c Credentials
	if err := json.Unmarshal(b, &c); err != nil {
		return Credentials{}, fmt.Errorf("decode %s: %w", path, err)
	}
	return c, nil
}

// SaveCredentials persiste las credenciales con permisos restrictivos.
func SaveCredentials(path string, c Credentials) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

// Manifest arma el App Manifest que GitHub convierte en la App real. baseURL es
// la URL pública del control (webhooks + callbacks); consoleURL la del front.
// Permisos = la tabla operación→permiso del informe GTM (todo lo que el
// conductor hace vía installation token). El user-to-server token (Agent tasks
// API, billing) sale del OAuth de la misma App.
func Manifest(name, org, baseURL, consoleURL string) map[string]any {
	return map[string]any{
		"name":        name,
		"url":         consoleURL,
		"description": "Forja by aiudalabs — del diseño gateado al software entregado, en tu GitHub.",
		"public":      false,
		"redirect_url": baseURL + "/setup/github-app/callback",
		"callback_urls": []string{
			baseURL + "/auth/github/callback",
		},
		"setup_url": consoleURL + "/onboarding",
		"hook_attributes": map[string]any{
			"url":    baseURL + "/webhooks/github",
			"active": true,
		},
		// Pedir autorización OAuth del usuario durante la instalación: un solo
		// viaje deja App instalada + token user-to-server emitido.
		"request_oauth_on_install": true,
		"default_permissions": map[string]string{
			"administration": "write", // crear repos
			"contents":       "write", // scaffold, merge
			"workflows":      "write", // claude.yml, ui-verify
			"issues":         "write", // export + deps blocked_by + cerrar
			"pull_requests":  "write", // review, merge
			"actions":        "write", // dispatch + approve (rerun)
			"checks":         "read",
			"metadata":       "read",
			"secrets":        "write", // CLAUDE_CODE_OAUTH_TOKEN por repo/org
		},
		"default_events": []string{
			"issues", "pull_request", "workflow_run", "check_run", "issue_comment",
		},
	}
}

// ConvertManifest canjea el code temporal del manifest flow por las
// credenciales de la App recién creada (POST /app-manifests/{code}/conversions).
func ConvertManifest(ctx context.Context, code string) (Credentials, error) {
	req, err := http.NewRequestWithContext(ctx, "POST",
		"https://api.github.com/app-manifests/"+code+"/conversions", nil)
	if err != nil {
		return Credentials{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := httpClient().Do(req)
	if err != nil {
		return Credentials{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusCreated {
		return Credentials{}, fmt.Errorf("manifest conversion: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var raw struct {
		ID            int64  `json:"id"`
		Slug          string `json:"slug"`
		ClientID      string `json:"client_id"`
		ClientSecret  string `json:"client_secret"`
		WebhookSecret string `json:"webhook_secret"`
		PEM           string `json:"pem"`
		HTMLURL       string `json:"html_url"`
		Owner         struct {
			Login string `json:"login"`
		} `json:"owner"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return Credentials{}, fmt.Errorf("decode conversion: %w", err)
	}
	return Credentials{
		AppID: raw.ID, Slug: raw.Slug, Org: raw.Owner.Login,
		ClientID: raw.ClientID, ClientSecret: raw.ClientSecret,
		WebhookSecret: raw.WebhookSecret, PEM: raw.PEM, HTMLURL: raw.HTMLURL,
		CreatedAt: time.Now().Unix(),
	}, nil
}

// UserToken es el resultado del intercambio OAuth user-to-server.
type UserToken struct {
	AccessToken           string `json:"access_token"`
	RefreshToken          string `json:"refresh_token"`
	ExpiresIn             int64  `json:"expires_in"`
	RefreshTokenExpiresIn int64  `json:"refresh_token_expires_in"`
	TokenType             string `json:"token_type"`
	Scope                 string `json:"scope"`
}

// ExchangeOAuthCode canjea el code del callback OAuth por el token de usuario.
func ExchangeOAuthCode(ctx context.Context, clientID, clientSecret, code string) (UserToken, error) {
	return oauthTokenCall(ctx, map[string]string{
		"client_id": clientID, "client_secret": clientSecret, "code": code,
	})
}

// RefreshUserToken renueva un access token expirado con su refresh token.
func RefreshUserToken(ctx context.Context, clientID, clientSecret, refreshToken string) (UserToken, error) {
	return oauthTokenCall(ctx, map[string]string{
		"client_id": clientID, "client_secret": clientSecret,
		"grant_type": "refresh_token", "refresh_token": refreshToken,
	})
}

func oauthTokenCall(ctx context.Context, params map[string]string) (UserToken, error) {
	form := make([]string, 0, len(params))
	for k, v := range params {
		form = append(form, k+"="+v)
	}
	req, err := http.NewRequestWithContext(ctx, "POST",
		"https://github.com/login/oauth/access_token", strings.NewReader(strings.Join(form, "&")))
	if err != nil {
		return UserToken{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := httpClient().Do(req)
	if err != nil {
		return UserToken{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var tok UserToken
	if err := json.Unmarshal(body, &tok); err != nil {
		return UserToken{}, fmt.Errorf("decode token: %w", err)
	}
	if tok.AccessToken == "" {
		return UserToken{}, fmt.Errorf("oauth exchange failed: %s", strings.TrimSpace(string(body)))
	}
	return tok, nil
}

// GHUser es el usuario autenticado detrás de un token.
type GHUser struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
	Email string `json:"email"`
	Name  string `json:"name"`
}

// FetchUser resuelve el usuario del token (GET /user).
func FetchUser(ctx context.Context, accessToken string) (GHUser, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.github.com/user", nil)
	if err != nil {
		return GHUser{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := httpClient().Do(req)
	if err != nil {
		return GHUser{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return GHUser{}, fmt.Errorf("GET /user: HTTP %d", resp.StatusCode)
	}
	var u GHUser
	if err := json.Unmarshal(body, &u); err != nil {
		return GHUser{}, err
	}
	return u, nil
}

func httpClient() *http.Client { return &http.Client{Timeout: 30 * time.Second} }
