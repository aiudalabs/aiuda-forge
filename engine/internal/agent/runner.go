package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"forge/internal/sandbox"
	"forge/internal/workflow"
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
	// Timeout is the ABSOLUTE backstop wall-clock per agent call (catches a
	// pathological infinite-but-active loop). IdleTimeout is the primary watchdog:
	// a healthy agent streams events continuously, so we kill on INACTIVITY (no
	// output for IdleTimeout) rather than total time — long-but-progressing tasks
	// survive while hung ones die fast.
	Timeout     time.Duration
	IdleTimeout time.Duration

	// Sandboxed runs the agent INSIDE a per-task sandbox (docker with egress
	// allowlist, or the local fallback) on a .git-less copy of the worktree, then
	// syncs edits back so the next step (the gate) sees them. When false, the
	// agent runs directly on the host workdir (legacy; used by pure unit tests).
	Sandboxed       bool
	SandboxTemplate sandbox.Config // Network = egress (NOT none); Image must contain claude
	Egress          EgressConfig

	// Emit, if set, receives streamed events for the live-log (step.event).
	Emit func(ev Event)

	// RouteModel, if set, is the cost-routing policy (billing): given the step type
	// and resolved agent id it returns a model id to use — consulted ABOVE the
	// manifest default and BELOW an explicit per-step model. "" = no opinion. Wired
	// by app.Build to billing.DefaultPolicy; keeps agent↔billing decoupled.
	RouteModel func(stepType, agentID string) string
}

// NewStepRunner builds an agent step runner.
func NewStepRunner(backend Backend, agents Loader) *StepRunner {
	// Absolute backstop generous (2h) so a long-but-progressing task isn't guillotined;
	// the idle watchdog (8m of no streamed output → stalled) is the real guard.
	return &StepRunner{Backend: backend, Agents: agents, Timeout: 2 * time.Hour, IdleTimeout: 8 * time.Minute}
}

// Run implements workflow.Runner.
func (r *StepRunner) Run(ctx context.Context, step workflow.Step, inputs map[string]any, workdir string) (workflow.StepResult, error) {
	// Lane-aware routing: a non-empty inputs["agent"] (the story's owner, plumbed
	// by the orchestrator) overrides the workflow's static step.Agent so a per-lane
	// specialist (python-dev, react-dev, …) implements the story. An empty value
	// falls back to step.Agent, which itself defaults the runner to "dev".
	// TODO(B2): the sandbox IMAGE per lane is a Phase B2 concern — today every lane
	// shares the control's global image (r.SandboxTemplate). When B2 lands, pick the
	// image here from the resolved agentID (e.g. a flutter lane needs the Flutter SDK).
	agentID := step.Agent
	if v := asString(inputs["agent"]); v != "" {
		agentID = v
	}
	if agentID == "" {
		return workflow.StepResult{Success: false, Detail: "agent step has no agent id"}, nil
	}
	manifest, err := r.Agents.Load(agentID)
	if err != nil {
		return workflow.StepResult{Success: false, Detail: "load agent: " + err.Error()}, nil
	}

	model := step.Model // explicit per-step override from the workflow YAML wins
	if model == "" && r.RouteModel != nil {
		model = r.RouteModel(string(step.Type), agentID) // cost routing (billing policy)
	}
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
		IdleTimeout:  r.IdleTimeout,
		Auth:         r.Auth,
	}

	// agentWorkdir is where the agent's edits actually land — agentDir in
	// sandboxed mode (synced back to workdir on return), or workdir directly. The
	// output-doc capture below writes here so SyncBack carries the artifact (M4).
	agentWorkdir := workdir

	// Sandbox the agent: it works on a .git-less copy; edits sync back to the run
	// worktree (so the gate sees them) but the agent never touches .git / the remote.
	if r.Sandboxed {
		agentDir := workdir + ".agent"
		_ = os.RemoveAll(agentDir)
		if err := sandbox.CopyTreeNoGit(workdir, agentDir); err != nil {
			return workflow.StepResult{Success: false, Detail: "stage agent worktree: " + err.Error()}, nil
		}
		defer os.RemoveAll(agentDir)
		// Fence-guard the SyncBack (B5): a multi-minute agent step can be reaped
		// (RequeueStale bumps the fence; a second worker re-claims and re-runs on the
		// SAME workdir). The fence correctly discards this stale worker's DB result,
		// but SyncBack is a filesystem side effect the fence cannot undo — an
		// unconditional RemoveAll+recopy here would clobber the worktree the live
		// worker now owns mid-write. Skip it unless we still own the claim.
		defer func() {
			if !workflow.StillOwnsWorkdir(ctx) {
				return // reaped + re-claimed: another worker owns workdir — do not touch it
			}
			_ = sandbox.SyncBack(agentDir, workdir) // propagate edits, keep .git
		}()

		cfg := r.SandboxTemplate
		cfg.Workdir = agentDir
		if cfg.Network == "" {
			cfg.Network = sandbox.DefaultEgressNetwork
		}
		// Container-id sink (outside the /work mount): lets the backend kill the
		// container by id on cancel/timeout (M1). docker refuses a pre-existing
		// cidfile, so clear any stale one from a prior reaped attempt first.
		cidFile := agentDir + ".cid"
		_ = os.Remove(cidFile)
		cfg.CIDFile = cidFile
		opts.CIDFile = cidFile
		defer os.Remove(cidFile)
		sb := sandbox.New(cfg)
		// Hard-fail rather than silently run the agent on the host (audit C2b):
		// in docker-required mode an unavailable runtime must FAIL the step, not
		// degrade to LocalSandbox with the operator's creds and no egress proxy.
		if err := cfg.MustDocker(sb); err != nil {
			return workflow.StepResult{Success: false, Detail: "agent step: " + err.Error()}, nil
		}
		opts.Sandbox = sb
		opts.Workdir = agentDir
		agentWorkdir = agentDir
		if sb.Kind() == "docker" {
			opts.ContainerEnv = EgressEnv(r.Auth, r.Egress) // sentinel/oauth + HTTPS_PROXY allowlist
		} else {
			opts.ContainerEnv = localAgentEnv(r.Auth) // host fallback: scrubbed env, no daemon secrets
		}
	}

	res, err := r.Backend.Run(ctx, prompt, opts, eventSink(ctx, r.Emit))
	if err != nil {
		// A provider session/rate limit is TRANSIENT — not a defect in the work.
		// Signal Retry so the engine requeues the step with a backoff instead of
		// failing the run; it re-runs once the limit clears.
		if isTransientErr(err) {
			return workflow.StepResult{Retry: true, Detail: "transient (will retry): " + err.Error()}, nil
		}
		return workflow.StepResult{Success: false, Detail: "agent error: " + err.Error()}, nil
	}

	out := map[string]any{
		"text":       res.Text,
		"cost_usd":   res.CostUSD,
		"num_turns":  res.NumTurns,
		"tokens_in":  res.TokensIn,
		"tokens_out": res.TokensOut,
		"agent":      manifest.ID,
		"model":      model,
	}

	// If the step declares an output path (e.g. "docs/PRD.md"), capture the
	// produced document. Design steps use this. An agent may produce the doc two
	// ways: (a) write the file itself via its write tool (Claude often does this
	// for documents, then replies with a summary) — keep that full file; or
	// (b) return the document as its response text — persist that. We prefer the
	// agent-written file so the artifact is the full document, not a summary.
	if outRel := asString(inputs["output"]); outRel != "" {
		// Read/write against the agent's own working dir (agentDir in sandboxed
		// mode). Writing the fallback doc here — BEFORE the deferred SyncBack runs —
		// lets SyncBack carry it into workdir; writing to workdir directly would be
		// clobbered by that same SyncBack (M4). out["output"] still points at the
		// final workdir location the gate/pr will read after SyncBack.
		agentAbs := filepath.Join(agentWorkdir, outRel)
		if existing, rerr := os.ReadFile(agentAbs); rerr == nil && len(strings.TrimSpace(string(existing))) > 0 {
			out["text"] = string(existing) // the agent wrote the full doc — use it
		} else if mkErr := os.MkdirAll(filepath.Dir(agentAbs), 0o755); mkErr == nil {
			_ = os.WriteFile(agentAbs, []byte(res.Text), 0o644)
		}
		out["output"] = filepath.Join(workdir, outRel)
	}

	return workflow.StepResult{
		Success: res.Success,
		Output:  out,
		Detail:  res.Text,
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
		case "ticket", "instructions", "feedback", "agent":
			// "agent" is a routing key (selects the specialist), not prompt content.
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

// transientMarkers are substrings (lower-cased) that identify a provider-side
// limit/overload — a TRANSIENT condition that clears on its own (the session
// quota resets, the rate window passes). These must NOT fail the run; the engine
// requeues and retries. Anything else (a real agent/tool error) fails normally.
var transientMarkers = []string{
	"session limit",      // "You've hit your session limit · resets ..."
	"rate limit",         // generic rate limiting
	"rate_limit",         // API error code form
	"429",                // Too Many Requests
	"overloaded",         // provider overloaded
	"529",                // provider overloaded (Anthropic)
	"usage limit",        // plan usage cap
	"weekly limit",       // "You've hit your weekly limit · resets ..."
	"hit your limit",     // generic plan-cap phrasing
}

// isTransientErr reports whether err looks like a provider limit/overload that
// will clear without code changes — i.e. worth retrying rather than failing.
func isTransientErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, m := range transientMarkers {
		if strings.Contains(msg, m) {
			return true
		}
	}
	return false
}
