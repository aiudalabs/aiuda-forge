package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"forge/internal/sandbox"
	"forge/internal/workflow"
)

// TestEgressEnvSecretsAndCredential (the "3 that matter" #2): the agent container
// env carries the LLM credential but NEVER a daemon secret.
//
// EgressEnv has two api_key sub-modes (egress.go):
//   - With AnthropicBase (reverse proxy): inject the sentinel; proxy rewrites the key
//     server-side so the real key never enters the container.
//   - Without AnthropicBase (direct): inject the real token via TLS CONNECT tunnel.
func TestEgressEnvSecretsAndCredential(t *testing.T) {
	// Daemon secrets present in the environment must not influence EgressEnv.
	t.Setenv("GH_TOKEN", "ghp_secret")
	t.Setenv("DB_PASSWORD", "hunter2")

	// api_key + reverse proxy: sentinel key + base url + proxy; the real token
	// never enters the container.
	proxy := EgressEnv(Auth{Mode: AuthAPIKey, Token: "sk-real"}, EgressConfig{
		AnthropicBase: "http://egress-proxy:8080/anthropic",
	})
	if !has(proxy, "ANTHROPIC_API_KEY="+SandboxSentinelAPIKey) {
		t.Fatalf("api_key+proxy must inject the sentinel key, got %v", proxy)
	}
	if !hasKey(proxy, "ANTHROPIC_BASE_URL") || !hasKey(proxy, "HTTPS_PROXY") {
		t.Fatalf("api_key+proxy must set base url + proxy, got %v", proxy)
	}
	if has(proxy, "ANTHROPIC_API_KEY=sk-real") {
		t.Fatalf("api_key+proxy must NOT expose the real key in the container env")
	}
	assertNoDaemonSecret(t, proxy)

	// api_key without reverse proxy: real token is passed directly (TLS CONNECT
	// ensures it never travels in cleartext through the forward proxy).
	direct := EgressEnv(Auth{Mode: AuthAPIKey, Token: "sk-real"}, EgressConfig{})
	if !has(direct, "ANTHROPIC_API_KEY=sk-real") {
		t.Fatalf("api_key+direct must inject the real token, got %v", direct)
	}
	if hasKey(direct, "ANTHROPIC_BASE_URL") {
		t.Fatalf("api_key+direct must NOT set a base URL override, got %v", direct)
	}
	assertNoDaemonSecret(t, direct)

	// passthrough (subscription/oauth): the OAuth token crosses, no sentinel.
	pass := EgressEnv(Auth{Mode: AuthSubscription, Token: "oauth-xyz"}, EgressConfig{})
	if !has(pass, "CLAUDE_CODE_OAUTH_TOKEN=oauth-xyz") {
		t.Fatalf("passthrough must inject the oauth token, got %v", pass)
	}
	if hasKey(pass, "ANTHROPIC_API_KEY") {
		t.Fatalf("passthrough must NOT set the sentinel/key")
	}
	if !hasKey(pass, "HTTPS_PROXY") {
		t.Fatalf("passthrough must still route through the allowlist proxy")
	}
	assertNoDaemonSecret(t, pass)
}

func assertNoDaemonSecret(t *testing.T, env []string) {
	t.Helper()
	for _, kv := range env {
		for _, secret := range []string{"GH_TOKEN", "DB_PASSWORD", "ghp_secret", "hunter2", "VIBEFORGE_DAEMON_SECRET"} {
			if contains(kv, secret) {
				t.Fatalf("daemon secret leaked into agent container env: %q", kv)
			}
		}
	}
}

// TestForbiddenNeverMergedIn: even if a task tries to smuggle a forbidden var, the
// merge guard drops it.
func TestForbiddenNeverMergedIn(t *testing.T) {
	base := EgressEnv(Auth{Mode: AuthAPIKey}, EgressConfig{})
	merged := MergeAllowed(base, map[string]string{"GH_TOKEN": "sneaky", "MY_BUILD_FLAG": "ok"})
	for _, kv := range merged {
		if contains(kv, "sneaky") || contains(kv, "GH_TOKEN") {
			t.Fatalf("forbidden var crossed via merge: %q", kv)
		}
	}
	if !has(merged, "MY_BUILD_FLAG=ok") {
		t.Fatalf("an allowed task var should pass through")
	}
}

// TestAgentWiredToDockerSandbox (the "3 that matter" #1 wiring): with a docker
// template the runner hands the backend a docker Sandbox + an egress container env.
func TestAgentWiredToDockerSandbox(t *testing.T) {
	rec := &recordingBackend{}
	r := NewStepRunner(rec, MapLoader{"dev": &Manifest{ID: "dev", Model: "m", Tools: []string{"read"}}})
	r.Sandboxed = true
	r.SandboxTemplate = sandbox.Config{Runtime: "docker", Image: "claude-img", Network: "vibeforge-egress"}
	r.Auth = Auth{Mode: AuthAPIKey}

	wd := filepath.Join(t.TempDir(), "run")
	if err := os.MkdirAll(wd, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Run(context.Background(), workflow.Step{Type: "agent", Agent: "dev"}, nil, wd); err != nil {
		t.Fatal(err)
	}
	if rec.lastOpts.Sandbox == nil || rec.lastOpts.Sandbox.Kind() != "docker" {
		t.Fatalf("agent must run inside a docker sandbox, got %#v", rec.lastOpts.Sandbox)
	}
	if !hasKey(rec.lastOpts.ContainerEnv, "HTTPS_PROXY") {
		t.Fatalf("docker agent must get the egress proxy env, got %v", rec.lastOpts.ContainerEnv)
	}
}

// TestAgentEditsVisibleToGate (the "3 that matter" #3): a fake agent writes a file
// INSIDE its sandbox worktree; after the step it is visible on the run worktree
// (so the next gate sees it), and the agent's .git-less staging is cleaned up.
func TestAgentEditsVisibleToGate(t *testing.T) {
	r := NewStepRunner(FakeBackend{Reply: "done", Script: func(workdir, prompt string) error {
		// the fake "agent" writes into ITS sandbox worktree
		return os.WriteFile(filepath.Join(workdir, "built.txt"), []byte("artifact"), 0o644)
	}}, MapLoader{"dev": &Manifest{ID: "dev"}})
	r.Sandboxed = true
	r.SandboxTemplate = sandbox.Config{Runtime: "local"} // no docker needed
	r.Timeout = 0

	run := filepath.Join(t.TempDir(), "run")
	if err := os.MkdirAll(filepath.Join(run, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(run, ".git", "HEAD"), []byte("ref"), 0o644)

	if _, err := r.Run(context.Background(), workflow.Step{Type: "agent", Agent: "dev"}, nil, run); err != nil {
		t.Fatal(err)
	}
	// the agent's file is now on the RUN worktree (the gate would see it)
	if _, err := os.Stat(filepath.Join(run, "built.txt")); err != nil {
		t.Fatalf("agent edit not visible on the run worktree: %v", err)
	}
	// .git preserved; staging cleaned
	if _, err := os.Stat(filepath.Join(run, ".git", "HEAD")); err != nil {
		t.Fatalf(".git must be preserved")
	}
	if _, err := os.Stat(run + ".agent"); !os.IsNotExist(err) {
		t.Fatalf("the .git-less agent staging dir must be cleaned up")
	}
}

// blockingBackend blocks in Run until release is closed, after signaling started.
// It writes a marker into the agent's sandbox worktree so a test can detect
// whether that worktree was synced back to the run worktree.
type blockingBackend struct {
	started chan struct{}
	release chan struct{}
	marker  string
}

func (b *blockingBackend) Run(_ context.Context, _ string, opts Options, onEvent func(Event)) (Result, error) {
	// Stage a "stale" edit inside the agent worktree, then block as a long step.
	_ = os.WriteFile(filepath.Join(opts.Workdir, b.marker), []byte("stale-worker-A"), 0o644)
	close(b.started)
	<-b.release
	if onEvent != nil {
		onEvent(Event{Kind: KindResult, Text: "late"})
	}
	return Result{Text: "late", Success: true}, nil
}

// TestSyncBackSkippedAfterReap (B5): a sandboxed agent step that is reaped
// mid-run (RequeueStale bumps the fence; a second worker re-claims) must NOT run
// its deferred SyncBack when the stale worker finally returns — otherwise it
// RemoveAll+recopies the run worktree that the live worker now owns. The fence
// discards the stale DB result; the ownership guard must likewise skip the
// filesystem side effect.
func TestSyncBackSkippedAfterReap(t *testing.T) {
	wfSrc := []byte(`
id: longstep
version: 1.0.0
steps:
  - id: implement
    type: agent
    agent: dev
`)
	wf, err := workflow.Parse(wfSrc)
	if err != nil {
		t.Fatal(err)
	}
	agents := MapLoader{"dev": &Manifest{ID: "dev"}}
	be := &blockingBackend{started: make(chan struct{}), release: make(chan struct{}), marker: "stale.txt"}

	e := newEngineWithAgent(t, be, agents)
	e.Loader = workflow.MapLoader{"longstep": wf}
	runner := NewStepRunnerWith(be, agents)
	runner.Sandboxed = true
	runner.SandboxTemplate = sandbox.Config{Runtime: "local"} // no docker needed
	e.Register("agent", runner)
	e.HeartbeatInterval = time.Hour // keep the heartbeat out of the way

	runID, err := e.StartRun("longstep", nil)
	if err != nil {
		t.Fatal(err)
	}
	// Run worker A in the background; it claims + blocks inside the agent backend.
	aDone := make(chan struct{})
	go func() {
		_, _ = e.ExecuteOne(context.Background(), "worker-A")
		close(aDone)
	}()
	<-be.started

	// Seed the LIVE run worktree with content the live worker "owns". If A's stale
	// SyncBack fires it will RemoveAll this and recopy A's (stale) staging.
	workdir := e.Workdir(runID)
	if err := os.WriteFile(filepath.Join(workdir, "live.txt"), []byte("worker-B-owns-this"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Reap: bump the fence so A's claim is stale (staleMillis=0 requeues regardless
	// of heartbeat). A second worker would re-claim the now-QUEUED task.
	if n, err := e.Store.RequeueStale(0); err != nil || n != 1 {
		t.Fatalf("expected to requeue 1 stale task, got n=%d err=%v", n, err)
	}

	// Let stale worker A return; its deferred SyncBack must be skipped.
	close(be.release)
	<-aDone

	// The live worktree content survives, and A's stale staging was NOT synced in.
	if got, err := os.ReadFile(filepath.Join(workdir, "live.txt")); err != nil || string(got) != "worker-B-owns-this" {
		t.Fatalf("live worktree was clobbered by stale SyncBack: got %q err=%v", got, err)
	}
	if _, err := os.Stat(filepath.Join(workdir, "stale.txt")); !os.IsNotExist(err) {
		t.Fatalf("stale worker A's edit leaked into the live worktree via SyncBack")
	}
}

func has(env []string, kv string) bool {
	for _, e := range env {
		if e == kv {
			return true
		}
	}
	return false
}
func hasKey(env []string, key string) bool {
	for _, e := range env {
		if len(e) > len(key) && e[:len(key)+1] == key+"=" {
			return true
		}
	}
	return false
}
