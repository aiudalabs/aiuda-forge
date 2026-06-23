package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestNoDaemonSecretCrossesToSandbox: the headline security test. The daemon
// env carries secrets (GH_TOKEN, ANTHROPIC_API_KEY, DB_PASSWORD); only the
// allowlist crosses. A secret must never appear in the sandbox env.
func TestNoDaemonSecretCrossesToSandbox(t *testing.T) {
	parent := []string{
		"PATH=/usr/bin",
		"HOME=/home/x",
		"GH_TOKEN=ghp_supersecret",
		"ANTHROPIC_API_KEY=sk-ant-secret",
		"DB_PASSWORD=hunter2",
		"AWS_SECRET_ACCESS_KEY=aws-secret",
	}
	// Allow base + the agent's auth ONLY (commit/push token GH_TOKEN excluded).
	allow := append(append([]string{}, DefaultAllowEnv...), "ANTHROPIC_API_KEY")
	got := FilterEnv(parent, allow)

	gotSet := map[string]bool{}
	for _, kv := range got {
		gotSet[kv] = true
	}
	if !gotSet["PATH=/usr/bin"] || !gotSet["HOME=/home/x"] {
		t.Fatalf("expected base vars to cross, got %v", got)
	}
	if !gotSet["ANTHROPIC_API_KEY=sk-ant-secret"] {
		t.Fatalf("explicitly-allowed auth should cross")
	}
	for _, banned := range []string{"GH_TOKEN=ghp_supersecret", "DB_PASSWORD=hunter2", "AWS_SECRET_ACCESS_KEY=aws-secret"} {
		if gotSet[banned] {
			t.Fatalf("SECRET LEAKED to sandbox: %s", banned)
		}
	}
}

// TestDefaultAllowlistHasNoSecrets: the default allowlist must never name a
// credential-shaped variable.
func TestDefaultAllowlistHasNoSecrets(t *testing.T) {
	banned := []string{"GH_TOKEN", "ANTHROPIC_API_KEY", "AWS_SECRET_ACCESS_KEY", "DB_PASSWORD", "OPENAI_API_KEY"}
	for _, k := range DefaultAllowEnv {
		for _, b := range banned {
			if k == b {
				t.Fatalf("default allowlist must not contain secret %q", k)
			}
		}
	}
}

// TestLocalSandboxScrubsEnv: even the local fallback scrubs secrets from the
// child env (defense in depth) and runs the command.
func TestLocalSandboxScrubsEnv(t *testing.T) {
	t.Setenv("GH_TOKEN", "leaky")
	t.Setenv("MY_ALLOWED", "ok")
	wd := t.TempDir()
	sb := &LocalSandbox{cfg: Config{AllowEnv: []string{"MY_ALLOWED"}, Workdir: wd}}

	out, code, err := sb.Exec(context.Background(), `echo "TOKEN=[${GH_TOKEN}] ALLOWED=[${MY_ALLOWED}]"`)
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	if contains(out, "leaky") {
		t.Fatalf("GH_TOKEN leaked into local sandbox: %q", out)
	}
	if !contains(out, "ALLOWED=[ok]") {
		t.Fatalf("allowed var missing: %q", out)
	}
}

// TestExitCodePropagates: a failing command yields a non-zero exit code, no error.
func TestExitCodePropagates(t *testing.T) {
	sb := &LocalSandbox{cfg: Config{Workdir: t.TempDir()}}
	_, code, err := sb.Exec(context.Background(), "exit 7")
	if err != nil {
		t.Fatalf("clean nonzero exit should not be an error: %v", err)
	}
	if code != 7 {
		t.Fatalf("expected exit 7, got %d", code)
	}
}

// TestCopyTreeNoGit: the agent's working tree must not contain .git.
func TestCopyTreeNoGit(t *testing.T) {
	src := t.TempDir()
	mustWrite(t, filepath.Join(src, "main.go"), "package main")
	mustWrite(t, filepath.Join(src, ".git", "config"), "[core]")
	mustWrite(t, filepath.Join(src, "pkg", "a.go"), "package pkg")
	mustWrite(t, filepath.Join(src, "pkg", ".git", "hook"), "x") // nested .git too

	dst := filepath.Join(t.TempDir(), "work")
	if err := CopyTreeNoGit(src, dst); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, "main.go")); err != nil {
		t.Fatalf("main.go should be copied: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "pkg", "a.go")); err != nil {
		t.Fatalf("pkg/a.go should be copied: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, ".git")); !os.IsNotExist(err) {
		t.Fatalf(".git must NOT be copied into the working tree")
	}
	if _, err := os.Stat(filepath.Join(dst, "pkg", ".git")); !os.IsNotExist(err) {
		t.Fatalf("nested .git must NOT be copied")
	}
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

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
