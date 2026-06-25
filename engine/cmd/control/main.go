// Command control is the VibeForge v2 control plane: HTTP API + event bus +
// in-process worker + reaper. Single binary for the MVP; cmd/worker can run
// standalone against the API for horizontal scale.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"forge/internal/agent"
	"forge/internal/app"
	"forge/internal/gate"
	"forge/internal/httpx"
)

func main() {
	addr := envOr("VIBEFORGE_ADDR", ":8080")
	// Wire agent auth from env (required in docker mode: the container env is an
	// allowlist and does NOT inherit the host's token). oauth_token = Max sub.
	agentAuth := agent.Auth{Mode: agent.AuthSubscription}
	if t := os.Getenv("CLAUDE_CODE_OAUTH_TOKEN"); t != "" {
		agentAuth = agent.Auth{Mode: agent.AuthOAuthToken, Token: t}
	} else if k := os.Getenv("ANTHROPIC_API_KEY"); k != "" {
		agentAuth = agent.Auth{Mode: agent.AuthAPIKey, Token: k}
	}
	a, err := app.Build(app.Config{
		DBPath:         envOr("VIBEFORGE_DB", "vibeforge.db"),
		TicketsDB:      envOr("VIBEFORGE_TICKETS_DB", "tickets.db"),
		ProjectsDB:     envOr("VIBEFORGE_PROJECTS_DB", "projects.db"),
		RegistryRoot:   envOr("VIBEFORGE_REGISTRY", "registry"),
		WorkdirRoot:    envOr("VIBEFORGE_WORKDIR", ".vibeforge-runs"),
		EngineMode:     envOr("VIBEFORGE_ENGINE", "echo"),
		SandboxRuntime: os.Getenv("VIBEFORGE_SANDBOX"),
		AgentAuth:      agentAuth,
		AgentTimeout:   20 * time.Minute,
		Workers:        envInt("VIBEFORGE_WORKERS", 4),
	})
	if err != nil {
		log.Fatalf("build kernel: %v", err)
	}
	defer a.Close()

	// Per-run target seeding: clone the project repo (from trigger payload.repo)
	// into each run's workdir and seal the gate BEFORE any agent runs.
	// Falls back to TARGET_REMOTE for backwards compat with single-project / demo setups.
	fallbackRemote := os.Getenv("TARGET_REMOTE")
	if fallbackRemote != "" {
		if err := httpx.ValidateRemote(fallbackRemote); err != nil {
			log.Fatalf("TARGET_REMOTE rejected: %v", err)
		}
	}
	// OnSeed is always registered so that payload.repo (set by POST /runs for a
	// project) takes effect; TARGET_REMOTE is the fallback.
	a.Engine.OnSeed = func(runID, workdir string) error {
		remote := fallbackRemote

		// Prefer the repo from the run's trigger payload when present.
		run, err := a.Store.GetRun(runID)
		if err == nil && run.Payload != "" {
			var payload map[string]any
			if json.Unmarshal([]byte(run.Payload), &payload) == nil {
				if r, ok := payload["repo"].(string); ok && r != "" {
					remote = r
				}
			}
		}

		if remote == "" {
			// Nothing to seed; gate-less flows (stub/echo) are fine with an empty workdir.
			return nil
		}
		if err := httpx.ValidateRemote(remote); err != nil {
			return fmt.Errorf("payload.repo rejected: %w", err)
		}
		if out, err := exec.Command("git", "clone", "--quiet", remote, workdir).CombinedOutput(); err != nil {
			return fmt.Errorf("clone target: %v: %s", err, out)
		}
		// GitHub Flow: base the run's work on `dev` so it includes prior MERGED
		// sprints (merge-gated deps). If `dev` doesn't exist (older/foreign repos),
		// fall back to the default branch already checked out and log it.
		checkoutDev(workdir, remote)
		return gate.SealWorkdir(workdir)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	a.StartBackground(ctx)

	apiToken := os.Getenv("VIBEFORGE_API_TOKEN")
	if apiToken != "" {
		log.Printf("vibeforge control auth: ENABLED (Bearer token required)")
	} else {
		log.Printf("vibeforge control auth: OPEN (no VIBEFORGE_API_TOKEN set)")
	}

	// CORS outermost (handles OPTIONS preflight before Auth sees it), then Auth,
	// then the mux. Auth is a no-op when apiToken is empty — demo works unchanged.
	handler := httpx.CORS(os.Getenv("VIBEFORGE_CORS_ORIGIN"), httpx.Auth(apiToken, a.Server))
	srv := &http.Server{Addr: addr, Handler: handler}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()

	log.Printf("vibeforge control listening on %s (engine=%s, workers=%d)", addr, envOr("VIBEFORGE_ENGINE", "echo"), envInt("VIBEFORGE_WORKERS", 4))
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("serve: %v", err)
	}
}

// checkoutDev switches the freshly cloned workdir to the `dev` branch so the
// agent's work is based on dev (which includes prior MERGED sprints). If `dev`
// does not exist on the remote, the default branch stays checked out and we log
// the fallback rather than failing the seed.
func checkoutDev(workdir, remote string) {
	chk := exec.Command("git", "-C", workdir, "checkout", "dev")
	if out, err := chk.CombinedOutput(); err != nil {
		log.Printf("seed: repo %s has no dev branch (%s) — using default branch", remote, exitText(out, err))
	}
}

// exitText returns a compact one-line description of a git failure for logging.
func exitText(out []byte, err error) string {
	s := strings.TrimSpace(string(out))
	if s == "" {
		return err.Error()
	}
	return s
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
