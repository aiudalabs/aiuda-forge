package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"forge/internal/httpx"
)

// stageMultiFilePreview writes a realistic multi-file preview: index.html pulling in
// a RELATIVE stylesheet + script — the exact shape whose subresource requests the
// browser resolves against the current /pv/{token}/ path (so they carry the token).
func stageMultiFilePreview(t *testing.T, root, proj, run string) {
	t.Helper()
	dir := filepath.Join(root, proj, run)
	mustWrite(t, filepath.Join(dir, "index.html"),
		`<!doctype html><html><head><link rel="stylesheet" href="style.css"></head>`+
			`<body><h1>preview</h1><script src="app.js"></script></body></html>`)
	mustWrite(t, filepath.Join(dir, "app.js"), `console.log("hello from preview");`)
	mustWrite(t, filepath.Join(dir, "style.css"), `h1{color:rebeccapurple}`)
}

// TestPreviewSubresourcesLoadViaPathToken is the E2E for a multi-file preview: the
// token lives in the PATH, so a browser resolving relative subresources of
// /pv/{token}/ requests /pv/{token}/app.js and /pv/{token}/style.css — each carrying
// the token — and both load. The token authorizes NOTHING outside /pv/{token}/. (The
// empirical browser check under the CSP sandbox is in the PR notes; this pins the
// HTTP contract deterministically.)
func TestPreviewSubresourcesLoadViaPathToken(t *testing.T) {
	root := t.TempDir()
	stageMultiFilePreview(t, root, "p1", "r1")
	stageMultiFilePreview(t, root, "p1", "r2") // a second preview, to prove non-leakage
	secret := []byte("preview-hmac-secret-for-e2e-00001")

	s := &Server{PreviewsRoot: root, PreviewSecret: secret, linkCodes: newLinkCodeStore(), mux: http.NewServeMux()}
	s.routes()
	cfg := httpx.AuthConfig{ServiceToken: "svc", Sessions: fakeSessions{"sess": "u1"}, PreviewSecret: secret}
	ts := httptest.NewServer(httpx.Auth(cfg, s))
	t.Cleanup(ts.Close)

	client := &http.Client{}
	tok := httpx.MintPreviewToken(secret, "p1", "r1", 10*time.Minute, time.Now())
	pv := ts.URL + "/pv/" + tok

	// The index and BOTH relative subresources (resolved under /pv/{token}/) load.
	for _, sub := range []string{"/", "/app.js", "/style.css"} {
		if code := status(t, client, pv+sub); code != 200 {
			t.Fatalf("preview %s should load (token in path), got %d", sub, code)
		}
	}

	// The token authorizes NOTHING outside /pv/{token}/: the API is unreachable with
	// it, whether presented as a query param or a bearer header.
	if code := status(t, client, ts.URL+"/runs?token="+tok); code != 401 {
		t.Fatalf("/runs must be 401 with a preview token (query), got %d", code)
	}
	// It only ever serves ITS OWN preview — the handler decodes the token to p1/r1, so
	// reaching p1/r2 requires r2's own token (which serves r2 and only r2).
	tok2 := httpx.MintPreviewToken(secret, "p1", "r2", 10*time.Minute, time.Now())
	if code := status(t, client, ts.URL+"/pv/"+tok2+"/app.js"); code != 200 {
		t.Fatalf("r2's own token should serve r2, got %d", code)
	}
}

func status(t *testing.T, c *http.Client, url string) int {
	t.Helper()
	resp, err := c.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
