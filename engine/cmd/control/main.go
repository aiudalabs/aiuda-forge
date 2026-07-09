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
	// allowlist and does NOT inherit the host's token). oauth_token = Max/Pro sub.
	//
	// VIBEFORGE_AGENT_AUTH forces a specific mode regardless of which tokens are
	// present — the failover lever (Phase 0): when the subscription hits its weekly
	// limit, set it to "api_key" so the factory runs on the pay-per-token key instead
	// of waiting days for the plan to reset. Unset = auto: prefer the subscription
	// (oauth_token) and only fall back to the API key when no oauth token is present.
	agentAuth := agent.Auth{Mode: agent.AuthSubscription}
	oauth, apikey := os.Getenv("CLAUDE_CODE_OAUTH_TOKEN"), os.Getenv("ANTHROPIC_API_KEY")
	switch os.Getenv("VIBEFORGE_AGENT_AUTH") {
	case "api_key":
		if apikey != "" {
			agentAuth = agent.Auth{Mode: agent.AuthAPIKey, Token: apikey}
		}
	case "oauth_token":
		if oauth != "" {
			agentAuth = agent.Auth{Mode: agent.AuthOAuthToken, Token: oauth}
		}
	default: // auto: subscription first, API key as fallback
		if oauth != "" {
			agentAuth = agent.Auth{Mode: agent.AuthOAuthToken, Token: oauth}
		} else if apikey != "" {
			agentAuth = agent.Auth{Mode: agent.AuthAPIKey, Token: apikey}
		}
	}
	log.Printf("agent auth mode: %s", agentAuth.Mode)
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
		sprintID := ""
		projectID := ""

		// Prefer the repo from the run's trigger payload when present; capture the
		// sprint id so the seed can resume that sprint's branch (resumable sprints),
		// and the project id so a repo-less trigger can resolve it from the project.
		// The workflow id decides the base branch below (iterate → trunk `main`).
		workflowID := ""
		run, err := a.Store.GetRun(runID)
		if err == nil {
			workflowID = run.WorkflowID
			if run.Payload != "" {
				var payload map[string]any
				if json.Unmarshal([]byte(run.Payload), &payload) == nil {
					if r, ok := payload["repo"].(string); ok && r != "" {
						remote = r
					}
					if s, ok := payload["sprint_id"].(string); ok {
						sprintID = s
					}
					if p, ok := payload["project_id"].(string); ok {
						projectID = p
					}
				}
			}
		}

		// Resolve the repo from the project when the trigger didn't carry it — e.g. a
		// Brain-launched `iterate` run passes only project_id + instructions. This lets
		// any project-scoped run clone the right repo without threading it through.
		if remote == "" && projectID != "" && a.Projects != nil {
			if p, perr := a.Projects.Get(projectID); perr == nil && p.Repo != "" {
				remote = p.Repo
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
		// An `iterate` run plans a delta ON TOP of the SHIPPED product. The pivot is
		// trunk-based: merged sprints land on `main`, and the vestigial `dev` branch
		// (legacy factory) may still exist but is stale. Base the iteration on `main`
		// so the iteration-planner reads the current PRD/architecture/backlog, not an
		// outdated dev. Factory runs keep the `dev` base via seedSprintBranch.
		if workflowID == "iterate" {
			checkoutBranch(workdir, "main", remote)
		} else {
			// GitHub Flow: base the run's work on `dev` (prior MERGED sprints) — or
			// RESUME this sprint's own branch if it has unmerged work from a prior run,
			// rebased onto current dev (resumable sprints).
			seedSprintBranch(workdir, sprintID)
		}
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
	authCfg := httpx.AuthConfig{ServiceToken: apiToken, Sessions: sessions, PreviewSecret: a.PreviewSecret}
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

// checkoutBranch switches the freshly cloned workdir to branch so the agent's
// work is based on it. If the branch does not exist on the remote, the default
// branch stays checked out and we log the fallback rather than failing the seed.
func checkoutBranch(workdir, branch, remote string) {
	chk := exec.Command("git", "-C", workdir, "checkout", branch)
	if out, err := chk.CombinedOutput(); err != nil {
		log.Printf("seed: repo %s has no %s branch (%s) — using default branch", remote, branch, exitText(out, err))
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

// gitC runs a git command in workdir, returning combined output.
func gitC(workdir string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", workdir}, args...)...).CombinedOutput()
	return string(out), err
}

// seedSprintBranch makes a run CONTINUE its sprint's branch instead of redoing the
// sprint from scratch (resumable sprints — a human team doesn't restart on a failed
// review, it keeps the branch). The branch is deterministic per sprint
// (vibeforge/sprint_<id>):
//   - exists on origin → check it out and REBASE onto current dev (so it picks up
//     newly-merged sprints); on a rebase conflict, abort and start FRESH from dev
//     (the stale branch is overwritten by the pr step's push -f, one branch/sprint).
//   - no sprint id (story mode) or no prior branch → a fresh branch off dev, as before.
func seedSprintBranch(workdir, sprintID string) {
	if sprintID == "" {
		checkoutBranch(workdir, "dev", "") // story mode: base on dev (prior merged work)
		return
	}
	branch := "vibeforge/sprint_" + sprintID
	if ls, _ := gitC(workdir, "ls-remote", "--heads", "origin", branch); strings.Contains(ls, "refs/heads/"+branch) {
		if _, err := gitC(workdir, "checkout", "-B", branch, "origin/"+branch); err == nil {
			if out, err := gitC(workdir, "-c", "user.email=kernel@vibeforge", "-c", "user.name=vibeforge", "rebase", "origin/dev"); err == nil {
				log.Printf("seed: sprint %s RESUMED from existing branch (rebased onto dev)", sprintID)
				return
			} else {
				_, _ = gitC(workdir, "rebase", "--abort")
				log.Printf("seed: sprint %s rebase onto dev conflicted (%s) — starting FRESH from dev", sprintID, exitText([]byte(out), err))
			}
		}
	}
	if _, err := gitC(workdir, "checkout", "-B", branch, "origin/dev"); err != nil {
		_, _ = gitC(workdir, "checkout", "-B", branch) // no origin/dev → name off the default branch
	}
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
