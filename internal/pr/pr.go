// Package pr implements the `pr` step type: it takes the agent's modified
// working tree and turns it into a pull request. PR_MODE=local commits to a
// branch (and pushes to a configured remote if present) without GitHub —
// exactly what the Wave 7 live validation uses. GitHub mode is a later add; the
// step type and risk-policy approval hook exist now.
package pr

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"vibeforge-kernel/internal/workflow"
)

// Runner implements workflow.Runner for `pr` steps.
type Runner struct {
	// Mode is "local" (default) or "github" (not in MVP).
	Mode string
}

// NewRunner builds a PR runner. Mode defaults to PR_MODE env or "local".
func NewRunner() *Runner {
	mode := os.Getenv("PR_MODE")
	if mode == "" {
		mode = "local"
	}
	return &Runner{Mode: mode}
}

// Run implements workflow.Runner.
func (r *Runner) Run(ctx context.Context, step workflow.Step, inputs map[string]any, workdir string) (workflow.StepResult, error) {
	runID := filepath.Base(workdir)
	branch := "vibeforge/" + runID

	if !isGitRepo(ctx, workdir) {
		// No repo seeded (e.g. stub flows): record a synthetic local PR so the
		// flow completes deterministically.
		return workflow.StepResult{
			Success: true,
			Output:  map[string]any{"pr": "local-synthetic", "branch": branch, "mode": r.Mode, "committed": false},
			Detail:  "pr(local): no git repo in workdir; recorded synthetic PR",
		}, nil
	}

	msg := commitMessage(inputs, runID)
	// gitRun prepends "git" — these are the args AFTER it.
	steps := [][]string{
		{"checkout", "-B", branch},
		{"add", "-A"},
		{"-c", "user.email=kernel@vibeforge", "-c", "user.name=vibeforge", "commit", "-m", msg, "--allow-empty"},
	}
	var log strings.Builder
	for _, s := range steps {
		out, err := gitRun(ctx, workdir, s...)
		log.WriteString(out)
		if err != nil {
			return workflow.StepResult{Success: false, Detail: "pr(local) failed at `git " + strings.Join(s, " ") + "`: " + err.Error() + "\n" + out}, nil
		}
	}

	pushed := false
	if hasRemote(ctx, workdir, "origin") {
		if out, err := gitRun(ctx, workdir, "push", "-f", "origin", branch); err != nil {
			log.WriteString(out)
			return workflow.StepResult{Success: false, Detail: "pr(local) push failed: " + err.Error() + "\n" + out}, nil
		}
		pushed = true
	}

	head, _ := gitRun(ctx, workdir, "rev-parse", "HEAD")
	return workflow.StepResult{
		Success: true,
		Output: map[string]any{
			"pr": branch, "branch": branch, "mode": r.Mode,
			"committed": true, "pushed": pushed, "head": strings.TrimSpace(head),
		},
		Detail: fmt.Sprintf("pr(local): committed to %s (pushed=%v)", branch, pushed),
	}, nil
}

func commitMessage(inputs map[string]any, runID string) string {
	if t, ok := inputs["ticket"].(string); ok && t != "" {
		first := t
		if i := strings.IndexByte(t, '\n'); i > 0 {
			first = t[:i]
		}
		return "vibeforge: " + strings.TrimSpace(first)
	}
	return "vibeforge: run " + runID
}

func isGitRepo(ctx context.Context, workdir string) bool {
	_, err := gitRun(ctx, workdir, "rev-parse", "--is-inside-work-tree")
	return err == nil
}

func hasRemote(ctx context.Context, workdir, name string) bool {
	out, err := gitRun(ctx, workdir, "remote")
	if err != nil {
		return false
	}
	for _, line := range strings.Fields(out) {
		if line == name {
			return true
		}
	}
	return false
}

func gitRun(ctx context.Context, workdir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = workdir
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	return buf.String(), err
}
