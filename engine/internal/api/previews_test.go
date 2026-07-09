package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

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
// directory to its index.html, refuses a directory with no index (no listing), and
// contains path traversal within the run dir.
func TestServePreviewServesAndContains(t *testing.T) {
	root := t.TempDir()
	stagePreview(t, root, "proj", "run1")
	s := &Server{PreviewsRoot: root}

	if rec := preview(s, nil, "proj", "run1", ""); rec.Code != 200 || rec.Body.String() != "<h1>preview</h1>" {
		t.Fatalf("dir → index.html: code=%d body=%q", rec.Code, rec.Body.String())
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

// With a projects store, a non-member gets 404 (no existence leak); a member is served.
func TestServePreviewProjectScoped(t *testing.T) {
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
	s := &Server{PreviewsRoot: root, Projects: pr}

	// A stranger (valid session, not a member) → 404.
	strangerCtx := httpx.WithUserID(context.Background(), "usr-stranger")
	if rec := preview(s, strangerCtx, "p1", "r1", ""); rec.Code != 404 {
		t.Fatalf("non-member should 404, got %d", rec.Code)
	}
	// The owner → served.
	ownerCtx := httpx.WithUserID(context.Background(), "usr-owner")
	if rec := preview(s, ownerCtx, "p1", "r1", ""); rec.Code != 200 {
		t.Fatalf("owner should be served, got %d body=%q", rec.Code, rec.Body.String())
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

// Behind the mandatory-auth middleware the previews route rejects an unauthenticated
// request (401) and accepts the session token as ?token= (browser navigation cannot
// set an Authorization header), mirroring /ws.
func TestServePreviewAuthMiddleware(t *testing.T) {
	root := t.TempDir()
	stagePreview(t, root, "p1", "r1")
	// Projects nil → enforcement off, so a valid session alone reaches the file.
	s := &Server{PreviewsRoot: root, linkCodes: newLinkCodeStore(), mux: http.NewServeMux()}
	s.routes()

	cfg := httpx.AuthConfig{Sessions: fakeSessions{"good-token": "usr-1"}}
	handler := httpx.Auth(cfg, s)

	// No credentials → 401.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/previews/p1/r1/", nil))
	if rec.Code != 401 {
		t.Fatalf("unauthenticated preview should be 401, got %d", rec.Code)
	}

	// ?token= carries the session for a plain browser navigation → 200.
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/previews/p1/r1/?token=good-token", nil))
	if rec.Code != 200 || rec.Body.String() != "<h1>preview</h1>" {
		t.Fatalf("token-authed preview should serve index, got %d body=%q", rec.Code, rec.Body.String())
	}

	// A bad token → 401.
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/previews/p1/r1/?token=nope", nil))
	if rec.Code != 401 {
		t.Fatalf("bad-token preview should be 401, got %d", rec.Code)
	}
}
