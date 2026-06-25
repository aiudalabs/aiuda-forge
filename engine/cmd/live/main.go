// Command live is the Wave 7 live-validation harness: it runs a REAL factory
// flow with VIBEFORGE_ENGINE=claude against a disposable local target repo,
// streams events, and reports status + cost. It is an ops/validation tool, not
// part of the kernel runtime. Usage:
//
//	TARGET_REMOTE=/path/to/bare.git TICKET="..." \
//	  VIBEFORGE_REGISTRY=registry VIBEFORGE_DB=/tmp/live.db \
//	  VIBEFORGE_WORKDIR=/tmp/runs go run ./cmd/live
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"time"

	"vibeforge-kernel/internal/agent"
	"vibeforge-kernel/internal/app"
	"vibeforge-kernel/internal/gate"
	"vibeforge-kernel/internal/httpx"
	"vibeforge-kernel/internal/store"
)

func main() {
	remote := mustEnv("TARGET_REMOTE")
	// Validate the remote URL before accepting it — git accepts ext:: and file://
	// URLs that can execute arbitrary commands; reject those schemes up-front.
	if err := httpx.ValidateRemote(remote); err != nil {
		log.Fatalf("TARGET_REMOTE rejected: %v", err)
	}
	workflowID := envOr("WORKFLOW", "factory")
	ticket := envOr("TICKET", "Implement add(a, b) in calc.py and unit tests in test_calc.py (unittest).")
	sandboxRuntime := envOr("VIBEFORGE_SANDBOX", "local")

	// Wire the agent auth from env so the credential reaches the agent — required
	// in docker mode, where the container env is an allowlist (it does NOT inherit
	// the host's CLAUDE_CODE_OAUTH_TOKEN). oauth_token uses your Max subscription.
	agentAuth := agent.Auth{Mode: agent.AuthSubscription}
	if t := os.Getenv("CLAUDE_CODE_OAUTH_TOKEN"); t != "" {
		agentAuth = agent.Auth{Mode: agent.AuthOAuthToken, Token: t}
	} else if k := os.Getenv("ANTHROPIC_API_KEY"); k != "" {
		agentAuth = agent.Auth{Mode: agent.AuthAPIKey, Token: k}
	}

	a, err := app.Build(app.Config{
		DBPath:         envOr("VIBEFORGE_DB", "live.db"),
		RegistryRoot:   envOr("VIBEFORGE_REGISTRY", "registry"),
		WorkdirRoot:    envOr("VIBEFORGE_WORKDIR", ".vibeforge-live"),
		EngineMode:     "claude",
		SandboxRuntime: sandboxRuntime,
		AgentAuth:      agentAuth,
		AgentTimeout:   12 * time.Minute,
	})
	if err != nil {
		log.Fatalf("build: %v", err)
	}
	defer a.Close()

	// Seed each run by cloning the disposable target repo into the workdir and
	// sealing the gate BEFORE any agent runs.
	a.Engine.OnSeed = func(runID, workdir string) error {
		if out, err := run("git", "clone", "--quiet", remote, workdir); err != nil {
			return fmt.Errorf("clone target: %v: %s", err, out)
		}
		return gate.SealWorkdir(workdir)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.StartBackground(ctx)

	// Stream events live.
	go streamEvents(ctx, a.Store)

	runID, err := a.Engine.StartRun(workflowID, map[string]any{"ticket": ticket})
	if err != nil {
		log.Fatalf("start run: %v", err)
	}
	fmt.Printf("\n=== LIVE RUN %s (workflow=%s, engine=claude, sandbox=%s) ===\n", runID, workflowID, sandboxRuntime)
	fmt.Printf("ticket: %s\n\n", ticket)

	status := waitTerminal(a.Store, runID, 15*time.Minute)
	time.Sleep(150 * time.Millisecond) // let the event stream flush

	fmt.Printf("\n=== RESULT: %s ===\n", status)
	reportSteps(a.Store, runID)
	total := totalCost(a.Store, runID)
	fmt.Printf("\nTOTAL COST: $%.4f USD\n", total)

	if status != store.StatusDone {
		os.Exit(1)
	}
}

func streamEvents(ctx context.Context, st *store.Store) {
	var after int64
	t := time.NewTicker(150 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			evs, err := st.AllEventsAfter(after)
			if err != nil {
				continue
			}
			for _, e := range evs {
				after = e.Seq
				fmt.Printf("  [event] %-22s %s\n", e.Type, brief(e.Data))
			}
		}
	}
}

func waitTerminal(st *store.Store, runID string, timeout time.Duration) store.Status {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		run, err := st.GetRun(runID)
		if err == nil && store.IsTerminal(run.Status) {
			return run.Status
		}
		time.Sleep(300 * time.Millisecond)
	}
	return "TIMEOUT"
}

func reportSteps(st *store.Store, runID string) {
	tasks, _ := st.TasksForRun(runID)
	for _, t := range tasks {
		var res map[string]any
		_ = json.Unmarshal([]byte(t.Result), &res)
		detail := ""
		if d, ok := res["detail"].(string); ok {
			detail = trunc(d, 100)
		}
		fmt.Printf("  step %-10s %-9s %s\n", t.StepID, t.Status, detail)
	}
}

func totalCost(st *store.Store, runID string) float64 {
	tasks, _ := st.TasksForRun(runID)
	var total float64
	for _, t := range tasks {
		var res map[string]any
		if err := json.Unmarshal([]byte(t.Result), &res); err != nil {
			continue
		}
		if out, ok := res["output"].(map[string]any); ok {
			if c, ok := out["cost_usd"].(float64); ok {
				total += c
			}
		}
	}
	return total
}

func brief(data string) string { return trunc(data, 140) }

func trunc(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

func run(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func mustEnv(k string) string {
	v := os.Getenv(k)
	if v == "" {
		log.Fatalf("%s is required", k)
	}
	return v
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
