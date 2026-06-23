package agent

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"vibeforge-kernel/internal/sandbox"
	"vibeforge-kernel/internal/workflow"
)

// StepRunner implements workflow.Runner for the `agent` step type. It loads the
// agent manifest (data), builds a prompt from the step inputs, runs the Backend
// (claude -p in prod, FakeBackend in tests), and records the agent's output.
//
// Per-step model override (step.Model) beats the manifest model — that is how a
// reviewer step runs a DIFFERENT model than the implementer, declared in YAML.
type StepRunner struct {
	Backend Backend
	Agents  Loader
	Auth    Auth
	Timeout time.Duration

	// Sandboxed runs the agent INSIDE a per-task sandbox (docker with egress
	// allowlist, or the local fallback) on a .git-less copy of the worktree, then
	// syncs edits back so the next step (the gate) sees them. When false, the
	// agent runs directly on the host workdir (legacy; used by pure unit tests).
	Sandboxed       bool
	SandboxTemplate sandbox.Config // Network = egress (NOT none); Image must contain claude
	Egress          EgressConfig

	// Emit, if set, receives streamed events for the live-log (step.event).
	Emit func(ev Event)
}

// NewStepRunner builds an agent step runner.
func NewStepRunner(backend Backend, agents Loader) *StepRunner {
	return &StepRunner{Backend: backend, Agents: agents, Timeout: 20 * time.Minute}
}

// Run implements workflow.Runner.
func (r *StepRunner) Run(ctx context.Context, step workflow.Step, inputs map[string]any, workdir string) (workflow.StepResult, error) {
	if step.Agent == "" {
		return workflow.StepResult{Success: false, Detail: "agent step has no agent id"}, nil
	}
	manifest, err := r.Agents.Load(step.Agent)
	if err != nil {
		return workflow.StepResult{Success: false, Detail: "load agent: " + err.Error()}, nil
	}

	model := step.Model // cross-model override from the workflow YAML
	if model == "" {
		model = manifest.Model
	}

	prompt := buildPrompt(manifest, step, inputs)
	opts := Options{
		Model:        model,
		AllowedTools: manifest.AllowedTools(),
		SystemPrompt: manifest.Persona,
		Workdir:      workdir,
		Timeout:      r.Timeout,
		Auth:         r.Auth,
	}

	// Sandbox the agent: it works on a .git-less copy; edits sync back to the run
	// worktree (so the gate sees them) but the agent never touches .git / the remote.
	if r.Sandboxed {
		agentDir := workdir + ".agent"
		_ = os.RemoveAll(agentDir)
		if err := sandbox.CopyTreeNoGit(workdir, agentDir); err != nil {
			return workflow.StepResult{Success: false, Detail: "stage agent worktree: " + err.Error()}, nil
		}
		defer os.RemoveAll(agentDir)
		defer func() { _ = sandbox.SyncBack(agentDir, workdir) }() // propagate edits, keep .git

		cfg := r.SandboxTemplate
		cfg.Workdir = agentDir
		if cfg.Network == "" {
			cfg.Network = sandbox.DefaultEgressNetwork
		}
		sb := sandbox.New(cfg)
		opts.Sandbox = sb
		opts.Workdir = agentDir
		if sb.Kind() == "docker" {
			opts.ContainerEnv = EgressEnv(r.Auth, r.Egress) // sentinel/oauth + HTTPS_PROXY allowlist
		} else {
			opts.ContainerEnv = localAgentEnv(r.Auth) // host fallback: scrubbed env, no daemon secrets
		}
	}

	res, err := r.Backend.Run(ctx, prompt, opts, r.Emit)
	if err != nil {
		return workflow.StepResult{Success: false, Detail: "agent error: " + err.Error()}, nil
	}
	return workflow.StepResult{
		Success: res.Success,
		Output: map[string]any{
			"text":      res.Text,
			"cost_usd":  res.CostUSD,
			"num_turns": res.NumTurns,
			"agent":     manifest.ID,
			"model":     model,
		},
		Detail: res.Text,
	}, nil
}

// buildPrompt assembles the user prompt from the agent role + step inputs. It is
// intentionally simple data-plumbing; the JUDGMENT (how to do the work) lives in
// the persona markdown and skills, not here.
func buildPrompt(m *Manifest, step workflow.Step, inputs map[string]any) string {
	var b strings.Builder
	if m.Role != "" {
		b.WriteString(m.Role)
		b.WriteString("\n\n")
	}
	if style := step.Prompt; style != "" {
		fmt.Fprintf(&b, "Prompt style: %s\n\n", style)
	}
	// Common, well-known input keys plumbed in a stable order.
	if v := asString(inputs["ticket"]); v != "" {
		b.WriteString("## Ticket\n")
		b.WriteString(v)
		b.WriteString("\n\n")
	}
	if v := asString(inputs["instructions"]); v != "" {
		b.WriteString("## Instructions\n")
		b.WriteString(v)
		b.WriteString("\n\n")
	}
	if v := asString(inputs["feedback"]); v != "" {
		b.WriteString("## Feedback from a previous attempt (address this)\n")
		b.WriteString(v)
		b.WriteString("\n\n")
	}
	// Any other inputs appended generically so nothing is silently dropped.
	for k, val := range inputs {
		switch k {
		case "ticket", "instructions", "feedback":
			continue
		}
		s := asString(val)
		if s == "" {
			continue
		}
		fmt.Fprintf(&b, "## %s\n%s\n\n", k, s)
	}
	return strings.TrimSpace(b.String())
}

func asString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	default:
		return fmt.Sprintf("%v", t)
	}
}
