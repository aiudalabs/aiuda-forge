package workflow

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// StepResult is the deterministic outcome of executing one step. Output becomes
// addressable as $<stepid>.output.<key>; Detail is human-facing (gate logs, etc.)
// and addressable as $<stepid>.detail; Success drives on_fail.
type StepResult struct {
	Success bool           `json:"success"`
	Output  map[string]any `json:"output"`
	Detail  string         `json:"detail"`
}

// Runner executes a single resolved step in workdir. Implementations are keyed
// by step Type and registered on the Engine. The executor never inspects step
// ids — it dispatches purely on Type, so flows stay data-defined.
type Runner interface {
	Run(ctx context.Context, step Step, inputs map[string]any, workdir string) (StepResult, error)
}

// EchoRunner is the deterministic test stub (the EchoEngine analog from v1): it
// succeeds and echoes its inputs. No LLM, fast, free, non-flaky. It also writes
// any `inputs.write_file`/`inputs.write_content` to disk so tests can stage
// files for a downstream gate without an agent.
type EchoRunner struct{}

func (EchoRunner) Run(_ context.Context, step Step, inputs map[string]any, workdir string) (StepResult, error) {
	if name, ok := inputs["write_file"].(string); ok && name != "" {
		content := asString(inputs["write_content"])
		if workdir != "" {
			_ = os.WriteFile(filepath.Join(workdir, name), []byte(content), 0o644)
		}
	}
	return StepResult{
		Success: true,
		Output:  map[string]any{"echoed": inputs, "step": step.ID},
		Detail:  "echo: " + step.ID,
	}, nil
}

// GateRunner runs a declared command in workdir and reports pass/fail by exit
// code. The command is either inline (step.Command, for tests) or read from the
// repo's .vibeforge-gate when command_from: repo. Anti-tamper/isolation hardening
// lands in Wave 4 (internal/gate); this is the deterministic core.
type GateRunner struct{}

func (GateRunner) Run(ctx context.Context, step Step, _ map[string]any, workdir string) (StepResult, error) {
	command := step.Command
	if step.CommandFrom == "repo" {
		b, err := os.ReadFile(filepath.Join(workdir, ".vibeforge-gate"))
		if err != nil {
			return StepResult{Success: false, Detail: "no .vibeforge-gate: " + err.Error()}, nil
		}
		command = string(b)
	}
	if strings.TrimSpace(command) == "" {
		return StepResult{Success: false, Detail: "gate has no command"}, nil
	}
	cmd := exec.CommandContext(ctx, "bash", "-c", command)
	cmd.Dir = workdir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	detail := strings.TrimSpace(out.String())
	if err != nil {
		if detail == "" {
			detail = err.Error()
		}
		return StepResult{Success: false, Output: map[string]any{"exit_error": err.Error()}, Detail: detail}, nil
	}
	return StepResult{Success: true, Output: map[string]any{"passed": true}, Detail: detail}, nil
}
