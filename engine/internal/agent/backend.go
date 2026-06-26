// Package agent is the multi-CLI adapter: a generic Backend interface that
// wraps an agent CLI (it does NOT build the agentic loop — the CLI does), plus
// the `agent` step runner. The real engine is claude.go (`claude -p`); tests use
// FakeBackend so the suite is fast, free and non-flaky.
package agent

import (
	"context"
	"time"

	"forge/internal/sandbox"
)

// AuthMode selects how the child agent process authenticates.
type AuthMode string

const (
	// AuthSubscription uses the logged-in Claude session/credentials (default).
	AuthSubscription AuthMode = "subscription"
	// AuthAPIKey passes an ANTHROPIC_API_KEY to the child.
	AuthAPIKey AuthMode = "api_key"
	// AuthOAuthToken passes a CLAUDE_CODE_OAUTH_TOKEN to the child.
	AuthOAuthToken AuthMode = "oauth_token"
)

// Auth carries the resolved auth choice + token (token unused for subscription).
type Auth struct {
	Mode  AuthMode
	Token string
}

// Options configure one agent run.
type Options struct {
	Model        string        // e.g. claude-opus-4-8; "" = CLI default
	AllowedTools []string      // claude tool names, e.g. Read, Edit, Write, Bash
	SystemPrompt string        // persona, appended via --append-system-prompt
	Workdir      string        // the agent's working tree (cwd of the child)
	Timeout      time.Duration // hard wall; 0 = no timeout
	Auth         Auth

	// Sandbox, when set, runs the agent INSIDE the per-task sandbox (docker exec/
	// run) instead of on the host. ContainerEnv is the egress/auth allowlist that
	// reaches the agent process (proxy + LLM credential; never daemon secrets).
	Sandbox      sandbox.Sandbox
	ContainerEnv []string

	// CIDFile, when set with a docker sandbox, is the path `docker run --cidfile`
	// writes the container id to. On cancel/timeout the backend reads it and runs
	// `docker rm -f <id>` so the container is killed by id, not just the host
	// docker client process group — otherwise the orphaned container keeps the
	// egress net + /work mount (M1). Must NOT exist before the run (docker errors).
	CIDFile string
}

// EventKind classifies a streamed event from the agent.
type EventKind string

const (
	KindText    EventKind = "text"     // assistant text chunk
	KindToolUse EventKind = "tool_use" // a tool invocation
	KindSystem  EventKind = "system"   // init / system notices
	KindResult  EventKind = "result"   // terminal result line
)

// Event is one streamed item (the live-log unit). Raw keeps the source object.
type Event struct {
	Kind EventKind
	Text string
	Tool string
	Raw  map[string]any
}

// Result is the terminal outcome of an agent run.
type Result struct {
	Text     string  // final assistant text / result
	Success  bool    // !is_error
	CostUSD  float64 // total_cost_usd
	NumTurns int
	Raw      map[string]any
}

// Backend wraps an agent CLI. Run streams events via onEvent (may be nil) and
// returns the terminal Result. ctx cancellation must kill the child process
// group (no orphaned agents).
type Backend interface {
	Run(ctx context.Context, prompt string, opts Options, onEvent func(Event)) (Result, error)
}

// FakeBackend is the deterministic test stub. It emits one text event and a
// result, optionally simulating failure or editing a file via the Script hook.
type FakeBackend struct {
	// Reply is the canned assistant text.
	Reply string
	// Fail makes the run report is_error=true.
	Fail bool
	// Cost is the reported cost (0 by default — tests stay free).
	Cost float64
	// Script, if set, is called with the workdir so a fake agent can stage files
	// (stand in for real edits) deterministically.
	Script func(workdir string, prompt string) error
}

// Run implements Backend deterministically.
func (f FakeBackend) Run(_ context.Context, prompt string, opts Options, onEvent func(Event)) (Result, error) {
	if f.Script != nil {
		if err := f.Script(opts.Workdir, prompt); err != nil {
			return Result{}, err
		}
	}
	reply := f.Reply
	if reply == "" {
		reply = "fake agent reply"
	}
	if onEvent != nil {
		onEvent(Event{Kind: KindText, Text: reply})
	}
	res := Result{Text: reply, Success: !f.Fail, CostUSD: f.Cost, NumTurns: 1, Raw: map[string]any{"fake": true}}
	if onEvent != nil {
		onEvent(Event{Kind: KindResult, Text: reply, Raw: res.Raw})
	}
	return res, nil
}
