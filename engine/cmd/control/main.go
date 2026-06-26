// Command control is the VibeForge v2 control plane: HTTP API + event bus +
// in-process worker + reaper. Single binary for the MVP; cmd/worker can run
// standalone against the API for horizontal scale.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
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
		AuthDB:         envOr("VIBEFORGE_AUTH_DB", "auth.db"),
		RegistryRoot:   envOr("VIBEFORGE_REGISTRY", "registry"),
		WorkdirRoot:    envOr("VIBEFORGE_WORKDIR", ".vibeforge-runs"),
		EngineMode:     envOr("VIBEFORGE_ENGINE", "echo"),
		SandboxRuntime: os.Getenv("VIBEFORGE_SANDBOX"),
		AgentAuth:      agentAuth,
		// Per-agent wall clock. Goal-mode sprints build several stories in ONE pass,
		// so a whole-sprint run can easily exceed the old fixed 20m. Configurable via
		// VIBEFORGE_AGENT_TIMEOUT_MIN (minutes); default keeps the historical 20m.
		AgentTimeout:   time.Duration(envInt("VIBEFORGE_AGENT_TIMEOUT_MIN", 20)) * time.Minute,
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

	// ---- mandatory auth (audit C1) -----------------------------------------
	// Auth is active when EITHER a service token is set OR at least one user
	// exists in the auth store. When active, every request needs a session token
	// or the service token (httpx.Auth), and CORS reflects a single origin with
	// credentials (httpx.CORS, never "*"). When NEITHER is configured the server
	// is auth-less and MUST bind to loopback only — we rewrite a non-loopback
	// addr down to 127.0.0.1 and log loudly rather than serving open to the world.
	apiToken := os.Getenv("VIBEFORGE_API_TOKEN")
	userCount := 0
	if a.Auth != nil {
		if n, cErr := a.Auth.CountUsers(); cErr == nil {
			userCount = n
		}
	}
	var sessions httpx.SessionValidator
	if a.Auth != nil {
		sessions = a.Auth
	}
	authCfg := httpx.AuthConfig{ServiceToken: apiToken, Sessions: sessions}
	authActive := apiToken != "" || userCount > 0

	if authActive {
		log.Printf("vibeforge control auth: ENABLED (session token or VIBEFORGE_API_TOKEN required; %d user(s))", userCount)
	} else {
		log.Printf("vibeforge control auth: OPEN — no users and no VIBEFORGE_API_TOKEN. Binding to loopback ONLY.")
		addr = loopbackAddr(addr)
		// Force the open-mode middleware to truly open (no session/token configured
		// means cfg.Enabled() is already false, so Auth is a pass-through).
		authCfg = httpx.AuthConfig{}
	}

	// CORS posture: locked to a single origin (with credentials) when auth is
	// active; "*" only when explicitly opted into open dev mode (VIBEFORGE_CORS=open).
	corsCfg := httpx.CORSConfig{
		Origin:   os.Getenv("VIBEFORGE_CORS_ORIGIN"),
		AllowAny: os.Getenv("VIBEFORGE_CORS") == "open",
	}
	if corsCfg.AllowAny && authActive {
		log.Printf("vibeforge control: WARNING VIBEFORGE_CORS=open with auth active — credentials disabled for cross-origin requests")
	}

	// CORS outermost (handles OPTIONS preflight before Auth sees it), then Auth,
	// then the mux.
	handler := httpx.CORS(corsCfg, httpx.Auth(authCfg, a.Server))
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

// loopbackAddr rewrites a bind address to loopback so an auth-less server is
// never reachable off-host. ":8080" or "0.0.0.0:8080" → "127.0.0.1:8080"; an
// addr already on 127.0.0.1/localhost is returned unchanged.
func loopbackAddr(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		// No host part (shouldn't happen for ":8080" which splits fine) — be safe.
		return "127.0.0.1" + addr
	}
	switch host {
	case "127.0.0.1", "localhost", "::1":
		return addr // already loopback
	}
	return net.JoinHostPort("127.0.0.1", port)
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
