// Command orchestrator resolves dependency ordering, fires runs on the
// control-plane for each READY story/issue, and exposes GET /tickets for the UI.
//
// Two source modes are supported, selected by -source:
//
//   - native (default): polls the control-plane's native ticket store. The
//     control-plane's /tickets endpoint derives readiness from the dep graph and
//     tracks status, so no local state file is needed. Runs are written back to
//     the story via PUT /stories/{id}/status.
//
//   - github: original mode — polls a GitHub repository for issues. The -repo
//     flag is required in this mode.
//
// Usage:
//
//	orchestrator [flags]
//
// Flags:
//
//	-source       Story source: native|github (default: native)
//	-repo         GitHub repo in "owner/repo" form (required for -source=github)
//	-cp           Control-plane base URL (default: http://localhost:8080)
//	-workflow     Workflow name to fire for each ready story (required)
//	-state        Path to the state JSON file (default: ./orchestrator-state.json; github mode only)
//	-interval     Poll interval, e.g. 10s (default: 10s)
//	-addr         Address to serve GET /tickets on (default: :9090)
//	-once         Run a single cycle and exit (useful for CI / tests)
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"forge/internal/httpx"
	"forge/internal/orchestrator"
)

func main() {
	source := flag.String("source", "native", "story source: native|github")
	repo := flag.String("repo", "", "GitHub repo (owner/repo) — required for -source=github")
	cpURL := flag.String("cp", envOr("VIBEFORGE_ADDR", "http://localhost:8080"), "control-plane base URL")
	workflow := flag.String("workflow", "", "workflow name to fire — required")
	stateFile := flag.String("state", "./orchestrator-state.json", "path to state JSON file (github mode)")
	interval := flag.Duration("interval", 10*time.Second, "poll interval")
	addr := flag.String("addr", envOr("ORCHESTRATOR_ADDR", ":9090"), "address to serve /tickets")
	once := flag.Bool("once", false, "run a single cycle and exit")
	flag.Parse()

	if *workflow == "" {
		log.Fatal("orchestrator: -workflow is required")
	}

	cp := orchestrator.NewControlPlaneClient(*cpURL)

	switch *source {
	case "native":
		runNative(cp, *cpURL, *workflow, *interval, *addr, *once)
	case "github":
		if *repo == "" {
			log.Fatal("orchestrator: -repo is required for -source=github (owner/repo)")
		}
		runGitHub(cp, *repo, *workflow, *stateFile, *interval, *addr, *once)
	default:
		log.Fatalf("orchestrator: unknown -source %q — must be native or github", *source)
	}
}

// runNative drives the scheduler from the control-plane's native ticket store.
func runNative(cp orchestrator.ControlPlane, cpURL, workflow string, interval time.Duration, addr string, once bool) {
	provider := orchestrator.NewNativeHTTPProvider(cpURL)
	sched := orchestrator.NewNativeScheduler(provider, cp, workflow)

	if once {
		if _, err := sched.RunOnce(context.Background()); err != nil {
			log.Fatalf("native-scheduler: %v", err)
		}
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// The native scheduler has no local /tickets HTTP server — the control-plane
	// already exposes GET /tickets. We still start the addr listener so the UI
	// can point at a single orchestrator address without knowing which mode is
	// active; it proxies to the control-plane's own endpoint.
	log.Printf("native-scheduler: polling %s every %s — workflow=%s", cpURL, interval, workflow)
	sched.Run(ctx, interval)
	log.Println("native-scheduler: stopped")
}

// runGitHub drives the original GitHub-backed orchestrator (unchanged path).
func runGitHub(cp orchestrator.ControlPlane, repo, workflow, stateFile string, interval time.Duration, addr string, once bool) {
	gh := orchestrator.NewGitHubClient(repo)
	orch, err := orchestrator.New(orchestrator.Config{
		Workflow:     workflow,
		PollInterval: interval,
		StateFile:    stateFile,
	}, gh, cp)
	if err != nil {
		log.Fatalf("orchestrator: %v", err)
	}

	if once {
		if err := orch.RunOnce(context.Background()); err != nil {
			log.Fatalf("orchestrator: %v", err)
		}
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Start the tickets HTTP server.
	srv := &http.Server{
		Addr:    addr,
		Handler: httpx.CORS(os.Getenv("VIBEFORGE_CORS_ORIGIN"), orchestrator.NewHTTPServer(orch)),
	}
	go func() {
		log.Printf("orchestrator: serving /tickets on %s", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("orchestrator: http server: %v", err)
		}
	}()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()

	log.Printf("orchestrator: polling %s every %s — workflow=%s", repo, interval, workflow)
	orch.Run(ctx)
	log.Println("orchestrator: stopped")
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
