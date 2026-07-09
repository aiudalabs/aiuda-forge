package release

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"forge/internal/sandbox"
	"forge/internal/store"
	"forge/internal/workflow"
)

// fakeClone returns a Clone func that materializes files into dst instead of
// running git — deterministic and fast for the runner's build/publish logic.
func fakeClone(files map[string]string) func(context.Context, string, string, string) (string, error) {
	return func(_ context.Context, _, _, dst string) (string, error) {
		for name, content := range files {
			p := filepath.Join(dst, name)
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return "", err
			}
			if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
				return "", err
			}
		}
		return "", nil
	}
}

func newRunner(t *testing.T, files map[string]string) *Runner {
	t.Helper()
	return &Runner{
		PreviewsRoot: t.TempDir(),
		Clone:        fakeClone(files),
	}
}

func runRelease(t *testing.T, r *Runner, runID string, inputs map[string]any) workflow.StepResult {
	t.Helper()
	workdir := filepath.Join(t.TempDir(), runID)
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatal(err)
	}
	res, err := r.Run(context.Background(), workflow.Step{ID: "release", Type: "release"}, inputs, workdir)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	return res
}

// TestStaticLooseIndexPublishes: a repo with a loose index.html (no build) is
// published and a servable preview_url is returned pointing at the copied file.
func TestStaticLooseIndexPublishes(t *testing.T) {
	r := newRunner(t, map[string]string{"index.html": "<h1>hello</h1>"})
	res := runRelease(t, r, "run_abc", map[string]any{
		"project_id": "proj1", "repo": "x", "target": "static",
	})
	if !res.Success {
		t.Fatalf("expected success, detail=%q", res.Detail)
	}
	url, _ := res.Output["preview_url"].(string)
	if url != "/previews/proj1/run_abc/" {
		t.Fatalf("unexpected preview_url %q", url)
	}
	if built, _ := res.Output["built"].(bool); built {
		t.Fatalf("loose index.html should not trigger a build")
	}
	// The artifact is on disk under PreviewsRoot/proj1/run_abc/index.html.
	served := filepath.Join(r.PreviewsRoot, "proj1", "run_abc", "index.html")
	b, err := os.ReadFile(served)
	if err != nil {
		t.Fatalf("published artifact missing: %v", err)
	}
	if string(b) != "<h1>hello</h1>" {
		t.Fatalf("artifact content mismatch: %q", b)
	}
}

// TestStaticNoBuildable: neither package.json nor index.html → clean failure that
// names what it looked for (not a crash, not a silent success).
func TestStaticNoBuildable(t *testing.T) {
	r := newRunner(t, map[string]string{"README.md": "nothing to build"})
	res := runRelease(t, r, "run_x", map[string]any{"project_id": "p", "repo": "x", "target": "static"})
	if res.Success {
		t.Fatalf("expected failure for a repo with nothing buildable")
	}
	if !strings.Contains(res.Detail, "package.json") || !strings.Contains(res.Detail, "index.html") {
		t.Fatalf("detail should name what it looked for, got %q", res.Detail)
	}
}

// TestStaticBuildRequiresDocker: a repo needing a build (package.json) MUST run it in
// docker; with RequireDocker set and docker unavailable (local runtime) the step FAILS
// with a clear detail rather than running the untrusted build on the host (audit C2).
func TestStaticBuildRequiresDocker(t *testing.T) {
	r := newRunner(t, map[string]string{"package.json": `{"name":"x","scripts":{"build":"true"}}`})
	r.SandboxTemplate = sandbox.Config{Runtime: "local", RequireDocker: true}
	res := runRelease(t, r, "run_b", map[string]any{"project_id": "p", "repo": "x", "target": "static"})
	if res.Success {
		t.Fatalf("expected failure: build must not run on the host when docker is required")
	}
	if !strings.Contains(res.Detail, "docker isolation required") {
		t.Fatalf("detail should explain docker is required, got %q", res.Detail)
	}
}

// TestFirebaseMissingConfigFile: firebase target without firebase.json fails cleanly.
func TestFirebaseMissingConfigFile(t *testing.T) {
	r := newRunner(t, map[string]string{"index.html": "x"}) // no firebase.json
	res := runRelease(t, r, "run_f", map[string]any{"project_id": "p", "repo": "x", "target": "firebase"})
	if res.Success {
		t.Fatalf("expected failure without firebase.json")
	}
	if !strings.Contains(res.Detail, "firebase.json") {
		t.Fatalf("detail should mention firebase.json, got %q", res.Detail)
	}
}

// TestFirebaseMissingToken: firebase.json present but no token → clean failure (the
// token comes from project config; a nil store yields none).
func TestFirebaseMissingToken(t *testing.T) {
	r := newRunner(t, map[string]string{"firebase.json": `{"hosting":{}}`})
	res := runRelease(t, r, "run_g", map[string]any{"project_id": "p", "repo": "x", "target": "firebase"})
	if res.Success {
		t.Fatalf("expected failure without a firebase token")
	}
	if !strings.Contains(res.Detail, "token") {
		t.Fatalf("detail should mention the missing token, got %q", res.Detail)
	}
}

// TestGCKeepsN: publishing more previews than KeepPreviews prunes the oldest, leaving
// exactly N per project (disk-full guard).
func TestGCKeepsN(t *testing.T) {
	r := newRunner(t, map[string]string{"index.html": "x"})
	r.KeepPreviews = 2
	for _, id := range []string{"run1", "run2", "run3", "run4"} {
		res := runRelease(t, r, id, map[string]any{"project_id": "proj", "repo": "x", "target": "static"})
		if !res.Success {
			t.Fatalf("publish %s failed: %s", id, res.Detail)
		}
	}
	entries, err := os.ReadDir(filepath.Join(r.PreviewsRoot, "proj"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("GC should keep 2 previews, found %d", len(entries))
	}
}

// TestSizeLimitFails: an artifact over MaxArtifactBytes fails (not truncated silently).
func TestSizeLimitFails(t *testing.T) {
	r := newRunner(t, map[string]string{"index.html": strings.Repeat("A", 4096)})
	r.MaxArtifactBytes = 1024
	res := runRelease(t, r, "run_big", map[string]any{"project_id": "p", "repo": "x", "target": "static"})
	if res.Success {
		t.Fatalf("expected failure for an oversized artifact")
	}
	if !strings.Contains(res.Detail, "limit") {
		t.Fatalf("detail should mention the size limit, got %q", res.Detail)
	}
}

// TestRealGitCloneStatic exercises the REAL host git clone path against a local repo
// fixture (a `dev` branch with an index.html), proving clone→detect→publish end to end.
func TestRealGitCloneStatic(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := t.TempDir()
	gitFixture(t, repo)
	r := &Runner{PreviewsRoot: t.TempDir()} // no injected Clone → real gitClone
	res := runRelease(t, r, "run_real", map[string]any{
		"project_id": "proj", "repo": repo, "branch": "dev", "target": "static",
	})
	if !res.Success {
		t.Fatalf("expected success, detail=%q", res.Detail)
	}
	if _, err := os.Stat(filepath.Join(r.PreviewsRoot, "proj", "run_real", "index.html")); err != nil {
		t.Fatalf("published artifact missing: %v", err)
	}
}

// TestPreviewURLResolvesInWorkflow: a `release` step's preview_url reaches the workflow
// context and resolves as $release.output.preview_url in a downstream step's inputs.
func TestPreviewURLResolvesInWorkflow(t *testing.T) {
	src := []byte(`
id: relflow
version: 1.0.0
steps:
  - id: release
    type: release
    inputs:
      project_id: proj1
      repo: dummy
      target: static
  - id: sink
    type: echo
    inputs:
      url: $release.output.preview_url
`)
	wf, err := workflow.Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "wf.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	eng := workflow.NewEngine(st, workflow.MapLoader{"relflow": wf}, t.TempDir())
	eng.Register("echo", workflow.EchoRunner{})
	eng.Register("release", &Runner{PreviewsRoot: t.TempDir(), Clone: fakeClone(map[string]string{"index.html": "x"})})

	runID, err := eng.StartRun("relflow", map[string]any{"project_id": "proj1"})
	if err != nil {
		t.Fatal(err)
	}
	status, err := eng.RunToCompletion(context.Background(), runID)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if status != store.StatusDone {
		t.Fatalf("run status = %s, want done", status)
	}
	// The echo sink echoed its resolved inputs; url must be the release's preview_url.
	tasks, _ := st.TasksForRun(runID)
	var sinkURL string
	for _, tk := range tasks {
		if tk.StepID != "sink" {
			continue
		}
		var res map[string]any
		if err := json.Unmarshal([]byte(tk.Result), &res); err != nil {
			t.Fatalf("decode sink result: %v", err)
		}
		out, _ := res["output"].(map[string]any)
		echoed, _ := out["echoed"].(map[string]any)
		sinkURL, _ = echoed["url"].(string)
	}
	if !strings.HasPrefix(sinkURL, "/previews/proj1/") {
		t.Fatalf("$release.output.preview_url did not resolve into sink inputs, got %q", sinkURL)
	}
}

func gitFixture(t *testing.T, dir string) {
	t.Helper()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q", "-b", "dev")
	run("config", "user.email", "t@t")
	run("config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<h1>fixture</h1>"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-q", "-m", "init")
}
