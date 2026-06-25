// Package app assembles the kernel: store + executor engine + all step runners +
// the selected agent backend + event bus + HTTP API. Both cmd/control and the
// contract tests build through here, so the wiring is exercised exactly once and
// there is no separate test-only path.
package app

import (
	"context"
	"fmt"
	"os"
	"time"

	"forge/internal/agent"
	"forge/internal/api"
	"forge/internal/gate"
	"forge/internal/pr"
	"forge/internal/projects"
	"forge/internal/sandbox"
	"forge/internal/store"
	"forge/internal/tickets"
	"forge/internal/workflow"
)

// Config configures an assembled kernel.
type Config struct {
	DBPath         string         // sqlite path
	TicketsDB      string         // tickets sqlite path; "" disables the ticket store
	ProjectsDB     string         // projects sqlite path; "" disables the project store
	RegistryRoot   string         // registry/
	WorkdirRoot    string         // where per-run working trees live
	EngineMode     string         // "echo" (FakeBackend) | "claude" (real)
	SandboxRuntime string         // "", "docker", "local"; "runsc" via env
	Backend        agent.Backend  // optional override (tests inject a fake)
	AgentAuth      agent.Auth     // auth for the real backend
	AgentTimeout   time.Duration  // per-agent wall clock
	Workers        int            // in-process worker pool size (<=0 → 1)
}

// App is the assembled kernel.
type App struct {
	Store    *store.Store
	Tickets  *tickets.Store  // nil when TicketsDB is not configured
	Projects *projects.Store // nil when ProjectsDB is not configured
	Engine   *workflow.Engine
	Bus      *api.Bus
	Server   *api.Server
	workers  int
}

// Build assembles a kernel per cfg.
func Build(cfg Config) (*App, error) {
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return nil, err
	}

	wfLoader := workflow.NewDirLoader(cfg.RegistryRoot + "/workflows")
	agentLoader := agent.NewDirLoader(cfg.RegistryRoot + "/agents")

	eng := workflow.NewEngine(st, wfLoader, cfg.WorkdirRoot)

	// Step runners — registered by type, the only place runners are wired.
	eng.Register("echo", workflow.EchoRunner{})

	hardGate := gate.NewHardenedRunner()
	hardGate.SandboxTemplate = sandbox.Config{
		Runtime:    cfg.SandboxRuntime,
		OCIRuntime: os.Getenv("VIBEFORGE_SANDBOX_RUNTIME"),
		Image:      os.Getenv("VIBEFORGE_SANDBOX_IMAGE"), // "" -> alpine; set to e.g. python:3.12-slim for a real gate
		EgressDeny: true,
	}
	eng.Register("gate", hardGate)

	backend := cfg.Backend
	if backend == nil {
		if cfg.EngineMode == "claude" {
			backend = ClaudeBackend()
		} else {
			backend = agent.FakeBackend{Reply: "stub: implemented"}
		}
	}
	agentRunner := agent.NewStepRunner(backend, agentLoader)
	agentRunner.Auth = cfg.AgentAuth
	if cfg.AgentTimeout > 0 {
		agentRunner.Timeout = cfg.AgentTimeout
	}
	// Run the agent INSIDE the per-task sandbox (docker + egress allowlist), on a
	// .git-less worktree. The agent's image must contain the `claude` CLI
	// (VIBEFORGE_AGENT_IMAGE); the egress network (VIBEFORGE_SANDBOX_NETWORK)
	// has no internet gateway — only the egress-proxy is reachable.
	agentRunner.Sandboxed = true
	agentRunner.SandboxTemplate = sandbox.Config{
		Runtime:    cfg.SandboxRuntime,
		OCIRuntime: os.Getenv("VIBEFORGE_SANDBOX_RUNTIME"),
		Image:      os.Getenv("VIBEFORGE_AGENT_IMAGE"),
		Network:    envOr("VIBEFORGE_SANDBOX_NETWORK", sandbox.DefaultEgressNetwork),
		UID:        os.Getenv("VIBEFORGE_SANDBOX_UID"),
		// EgressDeny stays false: the agent NEEDS the LLM API (via the proxy).
	}
	agentRunner.Egress = agent.EgressConfig{
		ProxyURL:      os.Getenv("VIBEFORGE_EGRESS_PROXY_URL"),
		AnthropicBase: os.Getenv("VIBEFORGE_EGRESS_ANTHROPIC_URL"),
		Open:          os.Getenv("VIBEFORGE_EGRESS") == "open",
	}
	eng.Register("agent", agentRunner)

	// Design step — same agent runner wiring but Sandboxed=false: design turns
	// produce documents, not code, so there is no need for a container worktree.
	// Inputs may carry `output: <relpath>` to persist the doc to disk.
	designRunner := agent.NewStepRunner(backend, agentLoader)
	designRunner.Auth = cfg.AgentAuth
	if cfg.AgentTimeout > 0 {
		designRunner.Timeout = cfg.AgentTimeout
	}
	designRunner.Sandboxed = false // design phases run on the host; no sandbox needed
	eng.Register("design", designRunner)

	verifyRunner := agent.NewVerifyRunner(backend, agentLoader)
	verifyRunner.Auth = cfg.AgentAuth
	if cfg.AgentTimeout > 0 {
		verifyRunner.Timeout = cfg.AgentTimeout
	}
	eng.Register("agentic_verify", verifyRunner)

	eng.Register("human_gate", agent.HumanGateRunner{})
	eng.Register("pr", pr.NewRunner())

	// Ticket store — optional. When TicketsDB is set, open the store, register
	// the ticket_publish step runner, and pass the store to the API server so
	// the HTTP ticket routes become active. When empty, the runner is not
	// registered and the API routes return 503 (needTickets guard).
	var tix *tickets.Store
	if cfg.TicketsDB != "" {
		var tixErr error
		tix, tixErr = tickets.Open(cfg.TicketsDB)
		if tixErr != nil {
			_ = st.Close()
			return nil, fmt.Errorf("open tickets db: %w", tixErr)
		}
		eng.Register("ticket_publish", &tickets.PublishRunner{Store: tix})
	}

	// Project store — optional. When ProjectsDB is set, open the store and pass
	// it to the API server so POST/GET /projects routes become active.
	var proj *projects.Store
	if cfg.ProjectsDB != "" {
		var projErr error
		proj, projErr = projects.Open(cfg.ProjectsDB)
		if projErr != nil {
			_ = st.Close()
			if tix != nil {
				_ = tix.Close()
			}
			return nil, fmt.Errorf("open projects db: %w", projErr)
		}
	}

	bus := api.NewBus(st)
	reg := api.NewRegistry(cfg.RegistryRoot)
	srv := api.NewServer(st, eng, bus, reg, tix, proj)

	workers := cfg.Workers
	if workers <= 0 {
		workers = 1
	}
	return &App{Store: st, Tickets: tix, Projects: proj, Engine: eng, Bus: bus, Server: srv, workers: workers}, nil
}

// ClaudeBackend builds the real claude -p backend.
func ClaudeBackend() agent.Backend { return agent.ClaudeBackend{} }

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// StartBackground launches the in-process worker POOL, reaper and event bus.
// The pool gives parallel execution across independent runs/tasks: the atomic
// claim (BEGIN IMMEDIATE + fence) guarantees no two workers ever claim the same
// task, so N workers drain the ready queue concurrently. Steps within one run
// stay serial (step N+1 is enqueued only after N completes).
func (a *App) StartBackground(ctx context.Context) {
	for i := 0; i < a.workers; i++ {
		go a.Engine.WorkerLoop(ctx, fmt.Sprintf("inproc-worker-%d", i))
	}
	go a.Engine.ReaperLoop(ctx, 60_000, 10*time.Second)
	go a.Bus.Run(ctx)
}

// Close releases resources.
func (a *App) Close() error {
	if a.Tickets != nil {
		_ = a.Tickets.Close()
	}
	if a.Projects != nil {
		_ = a.Projects.Close()
	}
	return a.Store.Close()
}
