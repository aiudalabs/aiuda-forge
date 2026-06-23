package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"vibeforge-kernel/internal/sandbox"
	"vibeforge-kernel/internal/workflow"
)

// TestEgressEnvSecretsAndCredential (the "3 that matter" #2): the agent container
// env carries the LLM credential but NEVER a daemon secret.
func TestEgressEnvSecretsAndCredential(t *testing.T) {
	// Daemon secrets present in the environment must not influence EgressEnv.
	t.Setenv("GH_TOKEN", "ghp_secret")
	t.Setenv("DB_PASSWORD", "hunter2")

	// api_key mode: sentinel key + base url + proxy; no real secret.
	api := EgressEnv(Auth{Mode: AuthAPIKey}, EgressConfig{})
	if !has(api, "ANTHROPIC_API_KEY="+SandboxSentinelAPIKey) {
		t.Fatalf("api_key mode must inject the sentinel key, got %v", api)
	}
	if !hasKey(api, "ANTHROPIC_BASE_URL") || !hasKey(api, "HTTPS_PROXY") {
		t.Fatalf("api_key mode must set base url + proxy, got %v", api)
	}
	assertNoDaemonSecret(t, api)

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
