package gate

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"vibeforge-kernel/internal/sandbox"
	"vibeforge-kernel/internal/workflow"
)

// HardenedRunner implements workflow.Runner for the `gate` step type with the
// full security treatment: the gate command runs inside a sandbox (egress-deny,
// env allowlist) and is checked against the sealed integrity snapshot
// (anti-tamper + suite-integrity). The factory workflow (Wave 5) uses this in
// place of the bare workflow.GateRunner.
type HardenedRunner struct {
	// SandboxTemplate is the base sandbox config; Workdir is filled per task.
	SandboxTemplate sandbox.Config
}

// NewHardenedRunner builds a hardened gate runner with sane defaults
// (egress-deny on, env allowlist = default secret-free set).
func NewHardenedRunner() *HardenedRunner {
	return &HardenedRunner{SandboxTemplate: sandbox.Config{EgressDeny: true}}
}

// Run implements workflow.Runner.
func (h *HardenedRunner) Run(ctx context.Context, step workflow.Step, _ map[string]any, workdir string) (workflow.StepResult, error) {
	command := step.Command
	if step.CommandFrom == "repo" {
		b, err := os.ReadFile(filepath.Join(workdir, GateFile))
		if err != nil {
			return workflow.StepResult{Success: false, Detail: "no " + GateFile + ": " + err.Error()}, nil
		}
		command = string(b)
	}
	if strings.TrimSpace(command) == "" {
		return workflow.StepResult{Success: false, Detail: "gate has no command"}, nil
	}

	cfg := h.SandboxTemplate
	cfg.Workdir = workdir
	sb := sandbox.New(cfg)

	res, err := Run(ctx, sb, workdir, MetaRootFor(workdir), RunIDFromWorkdir(workdir), command)
	if err != nil {
		return workflow.StepResult{Success: false, Detail: "gate run error: " + err.Error()}, nil
	}
	detail := strings.TrimSpace(res.Output)
	return workflow.StepResult{
		Success: res.Passed,
		Output:  map[string]any{"passed": res.Passed, "tamper": res.Tamper, "sandbox": res.Sandbox},
		Detail:  detail,
	}, nil
}

// MetaRootFor returns the daemon-side meta directory for a run workdir. It is a
// sibling of the runs root (WorkdirRoot/.vibeforge-meta), i.e. OUTSIDE the
// agent's working tree (WorkdirRoot/<runID>), so the seal can't be rewritten.
func MetaRootFor(workdir string) string {
	return filepath.Join(filepath.Dir(workdir), ".vibeforge-meta")
}

// RunIDFromWorkdir recovers the run id from the conventional workdir layout.
func RunIDFromWorkdir(workdir string) string { return filepath.Base(workdir) }

// SealWorkdir snapshots and seals the integrity of workdir. The kernel calls
// this when it seeds a run's working tree, BEFORE any agent runs.
func SealWorkdir(workdir string) error {
	integ, err := Snapshot(workdir, nil)
	if err != nil {
		return err
	}
	return Seal(MetaRootFor(workdir), RunIDFromWorkdir(workdir), integ)
}
