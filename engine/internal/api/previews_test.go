package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"forge/internal/httpx"
	"forge/internal/projects"
)

// stagePreview writes a minimal published preview tree under root and returns it.
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

// preview issues a GET against servePreview directly with the given path segments.
func preview(s *Server, ctx context.Context, proj, run, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/previews/"+proj+"/"+run+"/"+path, nil)
	req.SetPathValue("project", proj)
	req.SetPathValue("run", run)
	req.SetPathValue("path", path)
	if ctx != nil {
		req = req.WithContext(ctx)
	}
	s.servePreview(rec, req)
	return rec
}

// With no projects store, enforcement is off: the handler serves files, resolves a
// directory to its index.html, refuses a directory with no index (no listing),
// contains path traversal, and stamps the C2 serving headers on every response.
func TestServePreviewServesAndContains(t *testing.T) {
	root := t.TempDir()
	stagePreview(t, root, "proj", "run1")
	s := &Server{PreviewsRoot: root}

	rec := preview(s, nil, "proj", "run1", "")
	if rec.Code != 200 || rec.Body.String() != "<h1>preview</h1>" {
		t.Fatalf("dir → index.html: code=%d body=%q", rec.Code, rec.Body.String())
	}
	// C2 serving headers: opaque-origin sandbox + no MIME sniffing.
	if got := rec.Header().Get("Content-Security-Policy"); got != "sandbox allow-scripts allow-forms" {
		t.Fatalf("missing/incorrect CSP: %q", got)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("missing nosniff: %q", got)
	}
	if rec := preview(s, nil, "proj", "run1", "asset.txt"); rec.Code != 200 || rec.Body.String() != "asset" {
		t.Fatalf("file: code=%d body=%q", rec.Code, rec.Body.String())
	}
	// A directory without an index.html is NOT listed — it is a 404.
	if rec := preview(s, nil, "proj", "run1", "sub"); rec.Code != 404 {
		t.Fatalf("dir with no index should 404 (no listing), got %d body=%q", rec.Code, rec.Body.String())
	}
	// Traversal out of the run dir is contained (leading-slash Clean neutralizes ..).
	if rec := preview(s, nil, "proj", "run1", "../../../etc/hosts"); rec.Code != 404 {
		t.Fatalf("traversal should 404, got %d", rec.Code)
	}
	// Unconfigured previews → 404.
	if rec := preview(&Server{}, nil, "proj", "run1", ""); rec.Code != 404 {
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

// Behind the mandatory-auth middleware, /previews is authorized ONLY by a preview
// capability token scoped to the exact path — never a session or service token — and
// serves the artifact when a valid one is presented.
func TestPreviewAuthAcceptsOnlyPreviewToken(t *testing.T) {
	root := t.TempDir()
	stagePreview(t, root, "p1", "r1")
	secret := []byte("preview-hmac-secret-for-tests-0001")
	s := &Server{PreviewsRoot: root, PreviewSecret: secret, linkCodes: newLinkCodeStore(), mux: http.NewServeMux()}
	s.routes()
	cfg := httpx.AuthConfig{ServiceToken: "svc-token", Sessions: fakeSessions{"sess": "usr-1"}, PreviewSecret: secret}
	handler := httpx.Auth(cfg, s)

	get := func(url string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest("GET", url, nil))
		return rec
	}

	// No creds → 401.
	if rec := get("/previews/p1/r1/"); rec.Code != 401 {
		t.Fatalf("no creds should be 401, got %d", rec.Code)
	}
	// A SESSION token via ?token= is rejected (the whole point of the fix).
	if rec := get("/previews/p1/r1/?token=sess"); rec.Code != 401 {
		t.Fatalf("session token on /previews must be rejected, got %d", rec.Code)
	}
	// The SERVICE token via ?token= is rejected too.
	if rec := get("/previews/p1/r1/?token=svc-token"); rec.Code != 401 {
		t.Fatalf("service token on /previews must be rejected, got %d", rec.Code)
	}
	// A valid preview token → served, with the C2 headers.
	good := httpx.MintPreviewToken(secret, "p1", "r1", 10*time.Minute, time.Now())
	rec := get("/previews/p1/r1/?token=" + good)
	if rec.Code != 200 || rec.Body.String() != "<h1>preview</h1>" {
		t.Fatalf("valid preview token should serve, got %d body=%q", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Content-Security-Policy") == "" {
		t.Fatalf("served preview missing CSP header")
	}
	// A token scoped to a DIFFERENT preview does not authorize this path.
	other := httpx.MintPreviewToken(secret, "p1", "other", 10*time.Minute, time.Now())
	if rec := get("/previews/p1/r1/?token=" + other); rec.Code != 401 {
		t.Fatalf("cross-preview token must be rejected, got %d", rec.Code)
	}
	// An EXPIRED preview token → 401.
	expired := httpx.MintPreviewToken(secret, "p1", "r1", -1*time.Minute, time.Now())
	if rec := get("/previews/p1/r1/?token=" + expired); rec.Code != 401 {
		t.Fatalf("expired preview token must be 401, got %d", rec.Code)
	}
	// A traversal that would ride the p1/r1 token into another preview is rejected at
	// the auth layer (the path is cleaned before the scope check).
	if rec := get("/previews/p1/r1/../../p1/other/?token=" + good); rec.Code == 200 {
		t.Fatalf("traversal out of the token scope must not serve 200")
	}
}

// A preview token is useless anywhere but /previews: presenting it on /runs or
// /projects is rejected (it is not a session or service token).
func TestPreviewTokenDoesNotAuthorizeAPI(t *testing.T) {
	secret := []byte("preview-hmac-secret-for-tests-0002")
	s := &Server{PreviewSecret: secret, linkCodes: newLinkCodeStore(), mux: http.NewServeMux()}
	s.routes()
	cfg := httpx.AuthConfig{ServiceToken: "svc-token", Sessions: fakeSessions{"sess": "usr-1"}, PreviewSecret: secret}
	handler := httpx.Auth(cfg, s)

	tok := httpx.MintPreviewToken(secret, "p1", "r1", 10*time.Minute, time.Now())
	for _, path := range []string{"/runs", "/projects"} {
		for _, url := range []string{path + "?token=" + tok, path} {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("GET", url, nil)
			if url == path { // also try it as a bearer header
				req.Header.Set("Authorization", "Bearer "+tok)
			}
			handler.ServeHTTP(rec, req)
			if rec.Code != 401 {
				t.Fatalf("preview token must NOT authorize %s (url=%s), got %d", path, url, rec.Code)
			}
		}
	}
}

// mintPreviewToken is session-authenticated and member-gated, and the token it
// returns verifies for exactly that preview.
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
	secret := []byte("preview-hmac-secret-for-tests-0003")
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

	// A stranger (valid session, not a member) → 404 (no existence leak).
	if rec := mint("usr-stranger", "r1"); rec.Code != 404 {
		t.Fatalf("non-member mint should 404, got %d", rec.Code)
	}
	// A missing preview → 404 even for the owner.
	if rec := mint("usr-owner", "does-not-exist"); rec.Code != 404 {
		t.Fatalf("mint for missing preview should 404, got %d", rec.Code)
	}
	// The owner gets a token that verifies for exactly p1/r1.
	rec := mint("usr-owner", "r1")
	if rec.Code != 200 {
		t.Fatalf("owner mint should 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	tok, _ := out["token"].(string)
	proj, run, verr := httpx.VerifyPreviewToken(secret, tok, time.Now())
	if verr != nil || proj != "p1" || run != "r1" {
		t.Fatalf("minted token did not verify to p1/r1: proj=%q run=%q err=%v", proj, run, verr)
	}
}
