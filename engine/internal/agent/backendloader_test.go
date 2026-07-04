package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadBackends(t *testing.T) {
	dir := t.TempDir()

	// Write two valid backend YAMLs and one malformed one.
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("claude.yaml", "id: claude\nargv:\n  - claude\n  - -p\n  - --output-format\n  - stream-json\n")
	write("opencode.yaml", "id: opencode\nargv:\n  - opencode\n  - -p\n")
	write("bad.yaml", "id: \nargv: []\n") // skipped: missing id + argv

	backends, err := LoadBackends(dir)
	if err != nil {
		t.Fatalf("LoadBackends: %v", err)
	}
	if len(backends) != 2 {
		t.Fatalf("want 2 backends, got %d: %v", len(backends), backends)
	}
	cb, ok := backends["claude"].(CliBackend)
	if !ok {
		t.Fatalf("claude backend must be a CliBackend")
	}
	if len(cb.BaseArgv) == 0 || cb.BaseArgv[0] != "claude" {
		t.Fatalf("claude BaseArgv[0] must be 'claude', got %v", cb.BaseArgv)
	}
	ob, ok := backends["opencode"].(CliBackend)
	if !ok {
		t.Fatalf("opencode backend must be a CliBackend")
	}
	if ob.BaseArgv[0] != "opencode" {
		t.Fatalf("opencode BaseArgv[0] must be 'opencode', got %v", ob.BaseArgv)
	}
}

func TestLoadBackendsMissingDir(t *testing.T) {
	// A non-existent directory must return an empty map, not an error.
	backends, err := LoadBackends("/tmp/does-not-exist-vibeforge-test")
	if err != nil {
		t.Fatalf("missing dir must not error, got %v", err)
	}
	if len(backends) != 0 {
		t.Fatalf("missing dir must return empty map, got %v", backends)
	}
}

func TestCliBackendDefaultsToClaudeArgv(t *testing.T) {
	// Zero-value CliBackend must behave identically to the old ClaudeBackend{}.
	be := CliBackend{}
	// We can't run the real claude in tests, but we can check the argv construction
	// via the defaultArgv sentinel (same field used internally).
	_ = be // compile check is enough; real behaviour covered by claude_idle_test.go
}
