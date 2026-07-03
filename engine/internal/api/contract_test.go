package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"forge/internal/agent"
	"forge/internal/app"
	"forge/internal/gate"
	"forge/internal/httpx"
)

// testKernel builds an in-process kernel wired to the repo's real registry, with
// a deterministic fake backend (the implement agent stages impl.txt) and an
// OnSeed that writes+seals a gate checking for that file. Sandbox forced local so
// the suite needs no Docker. Returns an httptest base URL and a cancel func.
func testKernel(t *testing.T) (string, *app.App, context.CancelFunc) {
	t.Helper()
	// La suite ejercita el ejecutor factory legacy (agent/gate/agentic_verify),
	// que desde F4 es opt-in (la ejecución por defecto vive en GitHub).
	t.Setenv("VIBEFORGE_LEGACY_FACTORY", "1")
	// Copy the repo registry into a temp dir so registry PUTs in tests never
	// pollute the committed manifests.
	srcReg, err := filepath.Abs(filepath.Join("..", "..", "registry"))
	if err != nil {
		t.Fatal(err)
	}
	registryRoot := filepath.Join(t.TempDir(), "registry")
	if err := copyTree(srcReg, registryRoot); err != nil {
		t.Fatalf("copy registry: %v", err)
	}
	fake := agent.FakeBackend{
		Reply: "implemented",
		Script: func(workdir, prompt string) error {
			return os.WriteFile(filepath.Join(workdir, "impl.txt"), []byte("done"), 0o644)
		},
	}
	a, err := app.Build(app.Config{
		DBPath:         filepath.Join(t.TempDir(), "k.db"),
		ProjectsDB:     filepath.Join(t.TempDir(), "projects.db"),
		RegistryRoot:   registryRoot,
		WorkdirRoot:    t.TempDir(),
		EngineMode:     "echo",
		SandboxRuntime: "local",
		Backend:        fake,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Seed each run: a real gate command + seal, before any step runs.
	a.Engine.OnSeed = func(runID, workdir string) error {
		if err := os.WriteFile(filepath.Join(workdir, ".vibeforge-gate"), []byte("test -f impl.txt\n"), 0o755); err != nil {
			return err
		}
		return gate.SealWorkdir(workdir)
	}

	ctx, cancel := context.WithCancel(context.Background())
	a.StartBackground(ctx)
	ts := httptest.NewServer(a.Server)
	t.Cleanup(func() {
		ts.Close()
		cancel()
		a.Close()
	})
	return ts.URL, a, cancel
}

func do(t *testing.T, method, url string, body any) (*http.Response, []byte) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, rd)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, data
}

func startFactory(t *testing.T, base string) string {
	t.Helper()
	resp, data := do(t, "POST", base+"/runs", map[string]any{
		"workflow": "factory",
		"payload":  map[string]any{"ticket": "implement add(a,b)"},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /runs = %d: %s", resp.StatusCode, data)
	}
	var run map[string]any
	_ = json.Unmarshal(data, &run)
	id, _ := run["id"].(string)
	if id == "" {
		t.Fatalf("no run id in %s", data)
	}
	return id
}

func waitTerminal(t *testing.T, base, id string) string {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		resp, data := do(t, "GET", base+"/runs/"+id, nil)
		if resp.StatusCode == http.StatusOK {
			var run map[string]any
			_ = json.Unmarshal(data, &run)
			switch run["status"] {
			case "DONE", "FAILED", "CANCELLED":
				return run["status"].(string)
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("run %s did not terminate in time", id)
	return ""
}

// TestContractPresence (§D.1): every §A operation exists — none returns 404/405.
func TestContractPresence(t *testing.T) {
	base, _, _ := testKernel(t)
	id := startFactory(t, base)

	type op struct {
		method, path string
		body         any
	}
	ops := []op{
		{"POST", "/runs", map[string]any{"workflow": "factory", "payload": map[string]any{"ticket": "x"}}},
		{"GET", "/runs", nil},
		{"GET", "/runs?status=RUNNING", nil},
		{"GET", "/runs/" + id, nil},
		{"GET", "/runs/" + id + "/events", nil},
		{"GET", "/runs/" + id + "/artifacts/draft_story", nil}, // draft_story is the factory's first step
		{"POST", "/control/pause", nil},
		{"POST", "/control/resume", nil},
		{"POST", "/runs/" + id + "/steps/pr/approve", nil},
		{"POST", "/runs/" + id + "/steps/pr/merge", nil},
		{"POST", "/runs/" + id + "/retry", nil},
		{"GET", "/metrics", nil},
		{"GET", "/analytics", nil},
		{"GET", "/healthz", nil},
		{"GET", "/readyz", nil},
		{"GET", "/registry/workflows/factory", nil},
		{"GET", "/registry/agents/dev", nil},
		// internal §C
		{"POST", "/runs/claim", map[string]any{"worker": "probe"}},
		// cancel + delete last (they end the run)
		{"POST", "/runs/" + id + "/cancel", nil},
		{"DELETE", "/runs/" + id, nil},
	}
	for _, o := range ops {
		resp, data := do(t, o.method, base+o.path, o.body)
		if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed {
			t.Errorf("operation %s %s missing: %d %s", o.method, o.path, resp.StatusCode, data)
		}
	}
}

// TestContractEmission (§D.2): a full factory run emits the §B event types via
// the state machine, visible through GET /runs/{id}/events.
func TestContractEmission(t *testing.T) {
	base, _, _ := testKernel(t)
	id := startFactory(t, base)
	status := waitTerminal(t, base, id)
	if status != "DONE" {
		t.Fatalf("expected factory stub to reach DONE, got %s", status)
	}

	resp, data := do(t, "GET", base+"/runs/"+id+"/events", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("events = %d", resp.StatusCode)
	}
	var payload struct {
		Events []struct {
			Type string `json:"type"`
		} `json:"events"`
	}
	_ = json.Unmarshal(data, &payload)
	seen := map[string]bool{}
	for _, e := range payload.Events {
		seen[e.Type] = true
	}
	for _, want := range []string{"run.created", "step.status_changed", "run.status_changed", "run.done"} {
		if !seen[want] {
			t.Errorf("missing required event %q (saw %v)", want, keys(seen))
		}
	}
}

// TestNoPrivilegedPath (§D.3): board operations are driven ONLY over HTTP and
// take effect — proving the UI can do everything without a backdoor.
func TestNoPrivilegedPath(t *testing.T) {
	base, _, _ := testKernel(t)

	// cancel via HTTP, observe via HTTP
	id := startFactory(t, base)
	if resp, data := do(t, "POST", base+"/runs/"+id+"/cancel", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("cancel = %d %s", resp.StatusCode, data)
	}
	if got := getStatus(t, base, id); got != "CANCELLED" {
		t.Fatalf("expected CANCELLED via HTTP, got %s", got)
	}

	// retry via HTTP moves it out of terminal
	if resp, _ := do(t, "POST", base+"/runs/"+id+"/retry", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("retry failed")
	}

	// delete via HTTP, then GET 404
	if resp, _ := do(t, "DELETE", base+"/runs/"+id, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("delete failed")
	}
	if resp, _ := do(t, "GET", base+"/runs/"+id, nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 after delete, got %d", resp.StatusCode)
	}
}

// TestE2EFactoryStub (§D.4): trigger -> claim -> implement -> gate -> review ->
// pr-local -> DONE, all with the stub backend, entirely through the API.
func TestE2EFactoryStub(t *testing.T) {
	base, _, _ := testKernel(t)
	id := startFactory(t, base)
	if status := waitTerminal(t, base, id); status != "DONE" {
		t.Fatalf("expected DONE, got %s", status)
	}
	// All four factory steps ran and the last (pr) succeeded.
	resp, data := do(t, "GET", base+"/runs/"+id, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatal("get run")
	}
	var view struct {
		Steps []struct {
			StepID string `json:"step_id"`
			Status string `json:"status"`
		} `json:"steps"`
	}
	_ = json.Unmarshal(data, &view)
	got := map[string]string{}
	for _, s := range view.Steps {
		got[s.StepID] = s.Status
	}
	for _, step := range []string{"implement", "gate", "review", "pr"} {
		if got[step] != "DONE" {
			t.Errorf("step %s expected DONE, got %q (all: %v)", step, got[step], got)
		}
	}
}

// TestAddStepNoRecompile: dropping a `simplify` step into factory.yaml at runtime
// (via the registry PUT) changes the flow with no Go change — the v2 criterion.
// We do it against a throwaway workflow id so the committed factory.yaml is intact.
func TestAddStepNoRecompile(t *testing.T) {
	base, a, _ := testKernel(t)

	// Author a brand-new workflow purely as data, via the registry endpoint.
	yaml := `id: dynamic
version: 1.0.0
steps:
  - id: implement
    type: agent
    agent: dev
    inputs: { ticket: $trigger.ticket }
  - id: simplify
    type: agent
    agent: dev
    inputs: { ticket: "simplify the change" }
  - id: gate
    type: gate
    command_from: repo
  - id: pr
    type: pr
`
	req, _ := http.NewRequest("PUT", base+"/registry/workflows/dynamic", bytes.NewReader([]byte(yaml)))
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT registry workflow failed: %v %d", err, resp.StatusCode)
	}
	resp.Body.Close()
	_ = a // loader reads the new "dynamic" id fresh from disk (not previously cached)

	id := startFactory2(t, base, "dynamic")
	if status := waitTerminal(t, base, id); status != "DONE" {
		t.Fatalf("dynamic workflow expected DONE, got %s", status)
	}
	// The 'simplify' step — which exists ONLY in data — ran.
	_, data := do(t, "GET", base+"/runs/"+id, nil)
	if !bytes.Contains(data, []byte(`"simplify"`)) {
		t.Fatalf("expected the data-defined 'simplify' step to have run: %s", data)
	}
}

func startFactory2(t *testing.T, base, wf string) string {
	t.Helper()
	resp, data := do(t, "POST", base+"/runs", map[string]any{"workflow": wf, "payload": map[string]any{"ticket": "do it"}})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /runs(%s) = %d: %s", wf, resp.StatusCode, data)
	}
	var run map[string]any
	_ = json.Unmarshal(data, &run)
	return run["id"].(string)
}

func getStatus(t *testing.T, base, id string) string {
	t.Helper()
	_, data := do(t, "GET", base+"/runs/"+id, nil)
	var run map[string]any
	_ = json.Unmarshal(data, &run)
	s, _ := run["status"].(string)
	return s
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// copyTree recursively copies src into dst (test helper for an isolated registry).
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
}

// TestProjectsOwnerScopedAndFilters: POST /projects stamps owner_id from the auth
// context; GET /projects returns ONLY that user's projects; ?project= scopes
// /tickets and /runs (audit A1/A2). The request user is injected via a wrapper
// since the test server runs the open-mode middleware.
func TestProjectsOwnerScopedAndFilters(t *testing.T) {
	_, a, _ := testKernel(t)

	// Wrap the raw server so each request carries a user id taken from a header.
	userServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if uid := r.Header.Get("X-Test-User"); uid != "" {
			r = r.WithContext(httpx.WithUserID(r.Context(), uid))
		}
		a.Server.ServeHTTP(w, r)
	}))
	t.Cleanup(userServer.Close)
	base := userServer.URL

	create := func(user, name string) {
		body, _ := json.Marshal(map[string]any{"name": name, "repo": "https://github.com/acme/" + name})
		req, _ := http.NewRequest("POST", base+"/projects", bytes.NewReader(body))
		req.Header.Set("X-Test-User", user)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("POST /projects (%s) = %d: %s", user, resp.StatusCode, data)
		}
		var p map[string]any
		_ = json.Unmarshal(data, &p)
		if p["owner_id"] != user {
			t.Fatalf("created project owner_id: got %v, want %s", p["owner_id"], user)
		}
	}
	create("usr-1", "a1")
	create("usr-1", "a2")
	create("usr-2", "b1")

	list := func(user string) []any {
		req, _ := http.NewRequest("GET", base+"/projects", nil)
		req.Header.Set("X-Test-User", user)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		var out struct {
			Projects []any `json:"projects"`
		}
		_ = json.Unmarshal(data, &out)
		return out.Projects
	}
	if got := list("usr-1"); len(got) != 2 {
		t.Fatalf("usr-1 GET /projects: got %d, want 2 (owner-scoped)", len(got))
	}
	if got := list("usr-2"); len(got) != 1 {
		t.Fatalf("usr-2 GET /projects: got %d, want 1 (owner-scoped)", len(got))
	}

	// ?project= filter on /runs returns only that project's runs (none here for a
	// made-up id) — the endpoint must accept the param and scope, not 400.
	resp, d := do(t, "GET", userServer.URL+"/runs?project=nonesuch", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /runs?project= = %d: %s", resp.StatusCode, d)
	}
	if !strings.Contains(string(d), `"runs":[]`) {
		t.Fatalf("project-scoped /runs for unknown project should be empty, got: %s", d)
	}
}

// TestRunsUnscopedReturnsMemberProjectsOnly: an authenticated user calling GET /runs
// WITHOUT ?project= gets the runs of EVERY project they are a member of (the Studio
// cross-project design list) — not an empty list, and not another tenant's runs.
// Regression guard for the bug where enabling auth made Studio's Diseño tab empty.
func TestRunsUnscopedReturnsMemberProjectsOnly(t *testing.T) {
	_, a, _ := testKernel(t)

	userServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if uid := r.Header.Get("X-Test-User"); uid != "" {
			r = r.WithContext(httpx.WithUserID(r.Context(), uid))
		}
		a.Server.ServeHTTP(w, r)
	}))
	t.Cleanup(userServer.Close)
	base := userServer.URL

	// Create one project per user and return its owner-stamped id.
	createProject := func(user, name string) string {
		body, _ := json.Marshal(map[string]any{"name": name, "repo": "https://github.com/acme/" + name})
		req, _ := http.NewRequest("POST", base+"/projects", bytes.NewReader(body))
		req.Header.Set("X-Test-User", user)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("POST /projects (%s) = %d: %s", user, resp.StatusCode, data)
		}
		var p map[string]any
		_ = json.Unmarshal(data, &p)
		id, _ := p["id"].(string)
		if id == "" {
			t.Fatalf("created project has no id: %s", data)
		}
		return id
	}
	p1 := createProject("usr-1", "alpha")
	p2 := createProject("usr-2", "beta")

	// Seed a design run under each project directly in the store.
	if _, err := a.Server.Store.CreateRun("run-u1", "design", p1, "{}"); err != nil {
		t.Fatalf("seed run p1: %v", err)
	}
	if _, err := a.Server.Store.CreateRun("run-u2", "design", p2, "{}"); err != nil {
		t.Fatalf("seed run p2: %v", err)
	}

	// GET /runs (no ?project=) as usr-1 → sees run-u1 only, never run-u2.
	runIDs := func(user string) string {
		req, _ := http.NewRequest("GET", base+"/runs", nil)
		req.Header.Set("X-Test-User", user)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /runs (%s) = %d: %s", user, resp.StatusCode, data)
		}
		return string(data)
	}
	got1 := runIDs("usr-1")
	if !strings.Contains(got1, "run-u1") {
		t.Fatalf("usr-1 GET /runs should include its own run, got: %s", got1)
	}
	if strings.Contains(got1, "run-u2") {
		t.Fatalf("usr-1 GET /runs leaked another tenant's run: %s", got1)
	}
	got2 := runIDs("usr-2")
	if !strings.Contains(got2, "run-u2") || strings.Contains(got2, "run-u1") {
		t.Fatalf("usr-2 GET /runs scoping wrong, got: %s", got2)
	}
}
