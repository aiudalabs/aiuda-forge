package ghapp

// Acuñación de credenciales multi-tenant (GTM §auth):
//   · AppJWT: el JWT RS256 de la App (10 min) — solo sirve para hablar de
//     App-a-GitHub (buscar installations, acuñar installation tokens).
//   · InstallationTokenForRepo: el token con el que el conductor OPERA sobre
//     los repos del tenant (issues, PRs, actions, contents, secrets). Dura 1h;
//     se cachea ~50min por owner.
// El token user-to-server (Agent tasks de Copilot, billing) NO se acuña aquí:
// viene del OAuth y vive en auth.github_tokens.

import (
	"context"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"crypto/rand"
)

// AppJWT firma un JWT RS256 de 9 minutos para la App.
func AppJWT(creds Credentials) (string, error) {
	block, _ := pem.Decode([]byte(creds.PEM))
	if block == nil {
		return "", fmt.Errorf("invalid App PEM")
	}
	var key *rsa.PrivateKey
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		key = k
	} else if k8, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		var ok bool
		key, ok = k8.(*rsa.PrivateKey)
		if !ok {
			return "", fmt.Errorf("App PEM is not an RSA key")
		}
	} else {
		return "", fmt.Errorf("parse App PEM: %w", err)
	}

	now := time.Now()
	header := b64json(map[string]any{"alg": "RS256", "typ": "JWT"})
	claims := b64json(map[string]any{
		"iat": now.Add(-30 * time.Second).Unix(),
		"exp": now.Add(9 * time.Minute).Unix(),
		"iss": creds.AppID,
	})
	signing := header + "." + claims
	h := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, 5, h[:]) // 5 = crypto.SHA256
	if err != nil {
		return "", fmt.Errorf("sign App JWT: %w", err)
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

func b64json(v any) string {
	b, _ := json.Marshal(v)
	return base64.RawURLEncoding.EncodeToString(b)
}

// instTokenCache: owner/repo → token con expiración (in-memory; los tokens
// duran 1h, cacheamos 50min).
var (
	instMu    sync.Mutex
	instCache = map[string]struct {
		token   string
		expires time.Time
	}{}
)

// InstallationTokenForRepo acuña (con cache) el installation token que cubre
// el repo dado. Falla claro si la App no está instalada en esa cuenta.
func InstallationTokenForRepo(ctx context.Context, creds Credentials, ownerRepo string) (string, error) {
	instMu.Lock()
	if e, ok := instCache[ownerRepo]; ok && time.Now().Before(e.expires) {
		instMu.Unlock()
		return e.token, nil
	}
	instMu.Unlock()

	jwt, err := AppJWT(creds)
	if err != nil {
		return "", err
	}

	// 1) installation id del repo
	instID, err := appGET(ctx, jwt, "https://api.github.com/repos/"+ownerRepo+"/installation")
	if err != nil {
		return "", fmt.Errorf("la App %q no está instalada para %s: %w", creds.Slug, ownerRepo, err)
	}

	// 2) acuñar el token
	req, err := http.NewRequestWithContext(ctx, "POST",
		fmt.Sprintf("https://api.github.com/app/installations/%d/access_tokens", instID), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+jwt)
	resp, err := httpClient().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("mint installation token: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.Token == "" {
		return "", fmt.Errorf("decode installation token: %w", err)
	}

	instMu.Lock()
	instCache[ownerRepo] = struct {
		token   string
		expires time.Time
	}{out.Token, time.Now().Add(50 * time.Minute)}
	instMu.Unlock()
	return out.Token, nil
}

// appGET hace un GET autenticado con el App JWT y devuelve el campo id.
func appGET(ctx context.Context, jwt, url string) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+jwt)
	resp, err := httpClient().Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return 0, err
	}
	return out.ID, nil
}
