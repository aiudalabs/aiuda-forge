package brain

import (
	"context"
	"fmt"
	"os/exec"
	"time"
)

const (
	execTimeout   = 2 * time.Minute
	execMaxOutput = 16000 // cap combined output so one command can't blow the LLM context
)

// Exec runs a shell command on the control host and returns its combined output.
// This is the escape hatch — invoked only through the `exec` tool, which is mutating
// (human-approved per command), owner-only, and disabled unless VIBEFORGE_BRAIN_EXEC=1.
func (o EngineOps) Exec(command string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), execTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "bash", "-lc", command).CombinedOutput()
	s := string(out)
	if len(s) > execMaxOutput {
		s = s[:execMaxOutput] + "\n...[truncated]"
	}
	if err != nil {
		return s, fmt.Errorf("exec failed: %w", err)
	}
	return s, nil
}
