package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"forge/internal/httpx"
	"forge/internal/projects"
)

// stagePreview writes a minimal published preview tree under root.
// Layout: <root>/<proj>/<run>/index.html, /asset.txt, and an empty /sub directory.
func stagePreview(t *testing.T, root, proj, run string) {
	t.Helper()
	dir := filepath.Join(root, proj, run)
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<h1>preview</h1>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asset.txt"), []byte("asset"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// previewGet issues a GET against servePreview directly for a token + sub-path.
func previewGet(s *Server, tok, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/pv/"+tok+"/"+path, nil)
	req.SetPathValue("token", tok)
	req.SetPathValue("path", path)
	s.servePreview(rec, req)
	return rec
}

// The handler decodes the token to its project/run, serves files, resolves a
// directory to its index.html, refuses a directory with no index (no listing),
// contains traversal, and stamps the C2 serving headers.
func TestServePreviewServesAndContains(t *testing.T) {
	root := t.TempDir()
	stagePreview(t, root, "proj", "run1")
	secret := []byte("preview-hmac-secret-for-tests-0001")
	s := &Server{PreviewsRoot: root, PreviewSecret: secret}
	tok := httpx.MintPreviewToken(secret, "proj", "run1", 10*time.Minute, time.Now())

	rec := previewGet(s, tok, "")
	if rec.Code != 200 || rec.Body.String() != "<h1>preview</h1>" {
		t.Fatalf("dir → index.html: code=%d body=%q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Security-Policy"); got != "sandbox allow-scripts allow-forms" {
		t.Fatalf("missing/incorrect CSP: %q", got)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("missing nosniff: %q", got)
	}
	if rec := previewGet(s, tok, "asset.txt"); rec.Code != 200 || rec.Body.String() != "asset" {
		t.Fatalf("file: code=%d body=%q", rec.Code, rec.Body.String())
	}
	// A directory without an index.html is NOT listed — it is a 404.
	if rec := previewGet(s, tok, "sub"); rec.Code != 404 {
		t.Fatalf("dir with no index should 404 (no listing), got %d", rec.Code)
	}
	// Traversal out of the preview dir is contained (leading-slash Clean).
	if rec := previewGet(s, tok, "../../../etc/hosts"); rec.Code != 404 {
		t.Fatalf("traversal should 404, got %d", rec.Code)
	}
	// A garbage token → 404 (handler cannot decode it).
	if rec := previewGet(s, "not-a-token", ""); rec.Code != 404 {
		t.Fatalf("garbage token should 404, got %d", rec.Code)
	}
	// Unconfigured previews → 404.
	if rec := previewGet(&Server{}, tok, ""); rec.Code != 404 {
		t.Fatalf("no PreviewsRoot should 404, got %d", rec.Code)
	}
}

// fakeSessions validates a session token to a user id (a stand-in for the auth store).
type fakeSessions map[string]string

func (f fakeSessions) UserIDForToken(tok string) (string, error) {
	if id, ok := f[tok]; ok {
		return id, nil
	}
	return "", errors.New("bad token")
}

// Behind the mandatory-auth middleware, /pv is authorized ONLY by a valid path-
// embedded preview token — never a session or service token — and serves the artifact
// when one is presented.
func TestPreviewAuthAcceptsOnlyPreviewToken(t *testing.T) {
	root := t.TempDir()
	stagePreview(t, root, "p1", "r1")
	secret := []byte("preview-hmac-secret-for-tests-0002")
	s := &Server{PreviewsRoot: root, PreviewSecret: secret, linkCodes: newLinkCodeStore(), mux: http.NewServeMux()}
	s.routes()
	cfg := httpx.AuthConfig{ServiceToken: "svc-token", Sessions: fakeSessions{"sess": "usr-1"}, PreviewSecret: secret}
	handler := httpx.Auth(cfg, s)

	get := func(url string) int {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest("GET", url, nil))
		return rec.Code
	}

	good := httpx.MintPreviewToken(secret, "p1", "r1", 10*time.Minute, time.Now())
	// A SESSION token in the path is not a valid preview token → 401.
	if c := get("/pv/sess/"); c != 401 {
		t.Fatalf("session token as preview path must be 401, got %d", c)
	}
	// The SERVICE token likewise → 401.
	if c := get("/pv/svc-token/"); c != 401 {
		t.Fatalf("service token as preview path must be 401, got %d", c)
	}
	// A valid preview token → served.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/pv/"+good+"/", nil))
	if rec.Code != 200 || rec.Body.String() != "<h1>preview</h1>" {
		t.Fatalf("valid preview token should serve, got %d body=%q", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Content-Security-Policy") == "" {
		t.Fatalf("served preview missing CSP header")
	}
	// An EXPIRED preview token → 401.
	expired := httpx.MintPreviewToken(secret, "p1", "r1", -1*time.Minute, time.Now())
	if c := get("/pv/" + expired + "/"); c != 401 {
		t.Fatalf("expired preview token must be 401, got %d", c)
	}
}

// A preview token is useless anywhere but /pv: presenting it on /runs or /projects is
// rejected (it is not a session or service token).
func TestPreviewTokenDoesNotAuthorizeAPI(t *testing.T) {
	secret := []byte("preview-hmac-secret-for-tests-0003")
	s := &Server{PreviewSecret: secret, linkCodes: newLinkCodeStore(), mux: http.NewServeMux()}
	s.routes()
	cfg := httpx.AuthConfig{ServiceToken: "svc-token", Sessions: fakeSessions{"sess": "usr-1"}, PreviewSecret: secret}
	handler := httpx.Auth(cfg, s)

	tok := httpx.MintPreviewToken(secret, "p1", "r1", 10*time.Minute, time.Now())
	for _, path := range []string{"/runs", "/projects"} {
		// As a query param…
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest("GET", path+"?token="+tok, nil))
		if rec.Code != 401 {
			t.Fatalf("preview token as ?token= must NOT authorize %s, got %d", path, rec.Code)
		}
		// …and as a bearer header.
		rec = httptest.NewRecorder()
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		handler.ServeHTTP(rec, req)
		if rec.Code != 401 {
			t.Fatalf("preview token as bearer must NOT authorize %s, got %d", path, rec.Code)
		}
	}
}

// mintPreviewToken is session-authenticated and member-gated, and returns a /pv/ URL
// whose token verifies for exactly that preview.
func TestMintPreviewToken(t *testing.T) {
	root := t.TempDir()
	stagePreview(t, root, "p1", "r1")
	pr, err := projects.Open(filepath.Join(t.TempDir(), "projects.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pr.Close() })
	if _, err := pr.Create(projects.Project{ID: "p1", OwnerID: "usr-owner"}); err != nil {
		t.Fatal(err)
	}
	secret := []byte("preview-hmac-secret-for-tests-0004")
	s := &Server{PreviewsRoot: root, PreviewSecret: secret, PreviewsBaseURL: "https://app.example", Projects: pr}

	mint := func(uid, run string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/projects/p1/previews/"+run+"/token", nil)
		req.SetPathValue("id", "p1")
		req.SetPathValue("run", run)
		req = req.WithContext(httpx.WithUserID(context.Background(), uid))
		s.mintPreviewToken(rec, req)
		return rec
	}

	if rec := mint("usr-stranger", "r1"); rec.Code != 404 {
		t.Fatalf("non-member mint should 404, got %d", rec.Code)
	}
	if rec := mint("usr-owner", "does-not-exist"); rec.Code != 404 {
		t.Fatalf("mint for missing preview should 404, got %d", rec.Code)
	}
	rec := mint("usr-owner", "r1")
	if rec.Code != 200 {
		t.Fatalf("owner mint should 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	url, _ := out["url"].(string)
	if !strings.HasPrefix(url, "https://app.example/pv/") || !strings.HasSuffix(url, "/") {
		t.Fatalf("unexpected mint url %q", url)
	}
	tok, _ := out["token"].(string)
	proj, run, verr := httpx.VerifyPreviewToken(secret, tok, time.Now())
	if verr != nil || proj != "p1" || run != "r1" {
		t.Fatalf("minted token did not verify to p1/r1: proj=%q run=%q err=%v", proj, run, verr)
	}
}
