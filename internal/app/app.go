// Package app assembles the kernel: store + executor engine + all step runners +
// the selected agent backend + event bus + HTTP API. Both cmd/control and the
// contract tests build through here, so the wiring is exercised exactly once and
// there is no separate test-only path.
package app

import (
	"context"
	"os"
	"time"

	"vibeforge-kernel/internal/agent"
	"vibeforge-kernel/internal/api"
	"vibeforge-kernel/internal/gate"
	"vibeforge-kernel/internal/pr"
	"vibeforge-kernel/internal/sandbox"
	"vibeforge-kernel/internal/store"
	"vibeforge-kernel/internal/workflow"
)

// Config configures an assembled kernel.
type Config struct {
	DBPath         string         // sqlite path
	RegistryRoot   string         // registry/
	WorkdirRoot    string         // where per-run working trees live
	EngineMode     string         // "echo" (FakeBackend) | "claude" (real)
	SandboxRuntime string         // "", "docker", "local"; "runsc" via env
	Backend        agent.Backend  // optional override (tests inject a fake)
	AgentAuth      agent.Auth     // auth for the real backend
	AgentTimeout   time.Duration  // per-agent wall clock
}

// App is the assembled kernel.
type App struct {
	Store  *store.Store
	Engine *workflow.Engine
	Bus    *api.Bus
	Server *api.Server
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
	eng.Register("agent", agentRunner)

	verifyRunner := agent.NewVerifyRunner(backend, agentLoader)
	verifyRunner.Auth = cfg.AgentAuth
	if cfg.AgentTimeout > 0 {
		verifyRunner.Timeout = cfg.AgentTimeout
	}
	eng.Register("agentic_verify", verifyRunner)

	eng.Register("human_gate", agent.HumanGateRunner{})
	eng.Register("pr", pr.NewRunner())

	bus := api.NewBus(st)
	reg := api.NewRegistry(cfg.RegistryRoot)
	srv := api.NewServer(st, eng, bus, reg)

	return &App{Store: st, Engine: eng, Bus: bus, Server: srv}, nil
}

// ClaudeBackend builds the real claude -p backend.
func ClaudeBackend() agent.Backend { return agent.ClaudeBackend{} }

// StartBackground launches the in-process worker, reaper and event bus.
func (a *App) StartBackground(ctx context.Context) {
	go a.Engine.WorkerLoop(ctx, "inproc-worker")
	go a.Engine.ReaperLoop(ctx, 60_000, 10*time.Second)
	go a.Bus.Run(ctx)
}

// Close releases resources.
func (a *App) Close() error { return a.Store.Close() }
