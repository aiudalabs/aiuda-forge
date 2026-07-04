package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeClaude writes an executable shell script that stands in for the `claude` CLI:
// it ignores the args and emits whatever NDJSON the body prints, on whatever schedule.
func fakeClaude(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "fakeclaude")
	if err := os.WriteFile(p, []byte("#!/bin/bash\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// A silent (hung) agent is killed by the idle watchdog well before the absolute
// timeout: it emits one line, then goes quiet for longer than IdleTimeout.
func TestIdleTimeoutKillsStalledAgent(t *testing.T) {
	bin := fakeClaude(t, `echo '{"type":"text","text":"hi"}'; sleep 3`)
	be := CliBackend{BaseArgv: []string{bin}}

	start := time.Now()
	_, err := be.Run(context.Background(), "p",
		Options{Workdir: t.TempDir(), IdleTimeout: 400 * time.Millisecond, Timeout: 30 * time.Second}, nil)
	elapsed := time.Since(start)

	if err == nil || !strings.Contains(err.Error(), "stalled") {
		t.Fatalf("want a 'stalled' error from the idle watchdog, got %v", err)
	}
	// Killed on inactivity (~400ms), NOT after the 30s absolute wall.
	if elapsed > 3*time.Second {
		t.Fatalf("idle kill took %s — watchdog did not fire on inactivity", elapsed)
	}
}

// A steadily-progressing agent SURVIVES past the idle window: it streams a line
// every 150ms (< the 400ms idle) for ~1.2s total, then finishes. The total run
// exceeds IdleTimeout, proving we kill on inactivity, not on elapsed time.
func TestSteadyOutputSurvivesIdleTimeout(t *testing.T) {
	bin := fakeClaude(t, `for i in $(seq 1 8); do echo '{"type":"text","text":"work"}'; sleep 0.15; done; echo '{"type":"result","result":"done","is_error":false}'`)
	be := CliBackend{BaseArgv: []string{bin}}

	_, err := be.Run(context.Background(), "p",
		Options{Workdir: t.TempDir(), IdleTimeout: 400 * time.Millisecond, Timeout: 30 * time.Second}, nil)
	if err != nil {
		t.Fatalf("a steadily-streaming agent must survive the idle timeout, got %v", err)
	}
}
