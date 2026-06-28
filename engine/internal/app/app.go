// Package app assembles the kernel: store + executor engine + all step runners +
// the selected agent backend + event bus + HTTP API. Both cmd/control and the
// contract tests build through here, so the wiring is exercised exactly once and
// there is no separate test-only path.
package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"forge/internal/agent"
	"forge/internal/api"
	"forge/internal/auth"
	"forge/internal/billing"
	"forge/internal/brain"
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
	DBPath         string        // sqlite path
	TicketsDB      string        // tickets sqlite path; "" disables the ticket store
	ProjectsDB     string        // projects sqlite path; "" disables the project store
	AuthDB         string        // auth sqlite path; "" disables local auth
	RegistryRoot   string        // registry/
	WorkdirRoot    string        // where per-run working trees live
	EngineMode     string        // "echo" (FakeBackend) | "claude" (real)
	SandboxRuntime string        // "", "docker", "local"; "runsc" via env
	Backend        agent.Backend // optional override (tests inject a fake)
	AgentAuth      agent.Auth    // auth for the real backend
	AgentTimeout   time.Duration // per-agent wall clock
	Workers        int           // in-process worker pool size (<=0 → 1)
}

// App is the assembled kernel.
type App struct {
	Store    *store.Store
	Tickets  *tickets.Store  // nil when TicketsDB is not configured
	Projects *projects.Store // nil when ProjectsDB is not configured
	Auth     *auth.Store     // nil when AuthDB is not configured
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

	// requireDocker: when set, the agent and gate hard-fail (no LocalSandbox
	// fallback) if real docker isolation is unavailable (audit C2/C2b). It is ON
	// whenever the operator asked for docker (VIBEFORGE_SANDBOX=docker) or set
	// VIBEFORGE_REQUIRE_SANDBOX=1, UNLESS explicitly opted out for dev/test with
	// VIBEFORGE_ALLOW_LOCAL_SANDBOX=1. Untrusted repo code must never run on the host.
	requireDocker := dockerRequired(cfg.SandboxRuntime)

	hardGate := gate.NewHardenedRunner()
	hardGate.SandboxTemplate = sandbox.Config{
		Runtime:       cfg.SandboxRuntime,
		OCIRuntime:    os.Getenv("VIBEFORGE_SANDBOX_RUNTIME"),
		Image:         os.Getenv("VIBEFORGE_SANDBOX_IMAGE"), // "" -> alpine; set to e.g. python:3.12-slim for a real gate
		EgressDeny:    true,
		RequireDocker: requireDocker,
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
	// Register configured secrets for live-log redaction (audit C4): the agent's
	// auth token and the control-plane service token must never surface in a
	// persisted step.event. Token-prefix shapes are handled by the redactor itself.
	agent.RegisterSecret(cfg.AgentAuth.Token)
	agent.RegisterSecret(os.Getenv("VIBEFORGE_API_TOKEN"))

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
		Runtime:       cfg.SandboxRuntime,
		OCIRuntime:    os.Getenv("VIBEFORGE_SANDBOX_RUNTIME"),
		Image:         os.Getenv("VIBEFORGE_AGENT_IMAGE"),
		Network:       envOr("VIBEFORGE_SANDBOX_NETWORK", sandbox.DefaultEgressNetwork),
		UID:           os.Getenv("VIBEFORGE_SANDBOX_UID"),
		RequireDocker: requireDocker,
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

	// Auth store — optional. When AuthDB is set, open it and seed the first admin
	// from VIBEFORGE_ADMIN_EMAIL/PASSWORD if the users table is empty. The store
	// is passed to the API server so /auth/* routes activate and to the
	// mandatory-auth middleware (wired in cmd/control).
	var au *auth.Store
	if cfg.AuthDB != "" {
		var auErr error
		au, auErr = auth.Open(cfg.AuthDB)
		if auErr != nil {
			_ = st.Close()
			if tix != nil {
				_ = tix.Close()
			}
			if proj != nil {
				_ = proj.Close()
			}
			return nil, fmt.Errorf("open auth db: %w", auErr)
		}
		if _, seedErr := auth.SeedAdmin(au); seedErr != nil {
			_ = au.Close()
			_ = st.Close()
			if tix != nil {
				_ = tix.Close()
			}
			if proj != nil {
				_ = proj.Close()
			}
			return nil, fmt.Errorf("seed admin: %w", seedErr)
		}
	}

	bus := api.NewBus(st)
	reg := api.NewRegistry(cfg.RegistryRoot)
	srv := api.NewServer(st, eng, bus, reg, tix, proj, au)

	// Brain (optional): the per-project conversational assistant. Wired only when a
	// dedicated ANTHROPIC_API_KEY is present; otherwise its routes return 503. It
	// runs the tool-use loop in-process against the engine/store, and streams over
	// the same event bus (emit → AppendEvent scoped to a conversation id).
	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		bst, bErr := brain.Open(filepath.Join(filepath.Dir(cfg.DBPath), "brain.db"))
		if bErr != nil {
			return nil, fmt.Errorf("brain store: %w", bErr)
		}
		ops := brain.EngineOps{Engine: eng, Store: st, Tickets: tix}
		llm := brain.NewClient(key, os.Getenv("BRAIN_MODEL"))
		emit := func(convID, typ string, data map[string]any) { _, _ = st.AppendEvent(convID, "", typ, data) }
		srv.Brain = brain.New(llm, ops, bst, emit)
	}

	// Billing (SaaS metering + entitlements). Always on: it auto-creates a free
	// workspace per owner. Meter (a) real token cost is accrued in the usage handler;
	// meter (b) billable features via the ticket store's done hook (catches story- AND
	// sprint-mode merges), resolving the workspace from the project's owner.
	bill, billErr := billing.Open(filepath.Join(filepath.Dir(cfg.DBPath), "billing.db"))
	if billErr != nil {
		return nil, fmt.Errorf("billing store: %w", billErr)
	}
	srv.Billing = bill
	if tix != nil && proj != nil {
		tix.OnStoryDone = func(projectID, storyID, runID string) {
			p, err := proj.Get(projectID)
			if err != nil || p.OwnerID == "" {
				return // unowned/system run → not billable
			}
			ws, err := bill.WorkspaceForOwner(p.OwnerID)
			if err != nil {
				return
			}
			_, _ = bill.CountFeature(ws.ID, storyID, runID)
		}
	}

	workers := cfg.Workers
	if workers <= 0 {
		workers = 1
	}
	return &App{Store: st, Tickets: tix, Projects: proj, Auth: au, Engine: eng, Bus: bus, Server: srv, workers: workers}, nil
}

// ClaudeBackend builds the real claude -p backend.
func ClaudeBackend() agent.Backend { return agent.ClaudeBackend{} }

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// dockerRequired decides whether the agent/gate must hard-fail when docker
// isolation is unavailable (audit C2/C2b). It is true when the operator selected
// the docker sandbox runtime or set VIBEFORGE_REQUIRE_SANDBOX=1 — UNLESS they
// explicitly opted into the host fallback with VIBEFORGE_ALLOW_LOCAL_SANDBOX=1
// (dev/test only). The result is the secure default: an unconfigured docker-mode
// deployment refuses to degrade to the host.
func dockerRequired(sandboxRuntime string) bool {
	if os.Getenv("VIBEFORGE_ALLOW_LOCAL_SANDBOX") == "1" {
		return false // explicit dev/test opt-out
	}
	if os.Getenv("VIBEFORGE_REQUIRE_SANDBOX") == "1" {
		return true
	}
	return sandboxRuntime == "docker"
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
	if a.Auth != nil {
		_ = a.Auth.Close()
	}
	return a.Store.Close()
}
