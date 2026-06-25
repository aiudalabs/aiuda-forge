package studio

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// claudeEngine implements Engine by shelling out to `claude -p --resume`.
// The workdir is set as the command's working directory so that claude -p
// operates on the project's context directory (reading prior artifacts and
// session state kept by the claude CLI).
type claudeEngine struct{}

// NewClaudeEngine returns an Engine backed by the claude CLI.
func NewClaudeEngine() Engine {
	return &claudeEngine{}
}

// RunPhase runs `claude -p --resume "<prompt>"` in workdir and returns the
// combined stdout output. A non-zero exit code is returned as an error.
func (e *claudeEngine) RunPhase(ctx context.Context, workdir, phase, prompt string) (string, error) {
	cmd := exec.CommandContext(ctx, "claude", "-p", "--resume", prompt)
	cmd.Dir = workdir

	out, err := cmd.Output()
	if err != nil {
		detail := err.Error()
		if ee, ok := err.(*exec.ExitError); ok {
			if s := strings.TrimSpace(string(ee.Stderr)); s != "" {
				detail = s
			}
		}
		return "", fmt.Errorf("claude phase %q: %s", phase, detail)
	}
	return strings.TrimSpace(string(out)), nil
}
