// Command control is the VibeForge v2 control plane: HTTP API + event bus +
// in-process worker + reaper. Single binary for the MVP; cmd/worker can run
// standalone against the API for horizontal scale.
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
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

	// Per-run target seeding: clone TARGET_REMOTE into each run's workdir and seal
	// the gate BEFORE any agent runs. (A real deployment would take the repo from
	// the trigger payload; a fixed remote is enough for single-project / demo.)
	if remote := os.Getenv("TARGET_REMOTE"); remote != "" {
		// Validate the remote URL before accepting it — git accepts ext:: and file://
		// URLs that can execute arbitrary commands; reject those schemes up-front.
		if err := httpx.ValidateRemote(remote); err != nil {
			log.Fatalf("TARGET_REMOTE rejected: %v", err)
		}
		a.Engine.OnSeed = func(runID, workdir string) error {
			if out, err := exec.Command("git", "clone", "--quiet", remote, workdir).CombinedOutput(); err != nil {
				return fmt.Errorf("clone target: %v: %s", err, out)
			}
			return gate.SealWorkdir(workdir)
		}
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
