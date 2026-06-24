// Command orchestrator polls a GitHub repository for issues, resolves
// dependency ordering, fires runs on the control-plane for each READY issue,
// and exposes GET /tickets for the UI.
//
// Usage:
//
//	orchestrator [flags]
//
// Flags:
//
//	-repo         GitHub repo in "owner/repo" form (required)
//	-cp           Control-plane base URL (default: http://localhost:8080)
//	-workflow     Workflow name to fire for each ready issue (required)
//	-state        Path to the state JSON file (default: ./orchestrator-state.json)
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

	"vibeforge-kernel/internal/orchestrator"
)

func main() {
	repo := flag.String("repo", "", "GitHub repo (owner/repo) — required")
	cpURL := flag.String("cp", envOr("VIBEFORGE_ADDR", "http://localhost:8080"), "control-plane base URL")
	workflow := flag.String("workflow", "", "workflow name to fire — required")
	stateFile := flag.String("state", "./orchestrator-state.json", "path to state JSON file")
	interval := flag.Duration("interval", 10*time.Second, "poll interval")
	addr := flag.String("addr", envOr("ORCHESTRATOR_ADDR", ":9090"), "address to serve /tickets")
	once := flag.Bool("once", false, "run a single cycle and exit")
	flag.Parse()

	if *repo == "" {
		log.Fatal("orchestrator: -repo is required (owner/repo)")
	}
	if *workflow == "" {
		log.Fatal("orchestrator: -workflow is required")
	}

	gh := orchestrator.NewGitHubClient(*repo)
	cp := orchestrator.NewControlPlaneClient(*cpURL)

	orch, err := orchestrator.New(orchestrator.Config{
		Workflow:     *workflow,
		PollInterval: *interval,
		StateFile:    *stateFile,
	}, gh, cp)
	if err != nil {
		log.Fatalf("orchestrator: %v", err)
	}

	if *once {
		if err := orch.RunOnce(context.Background()); err != nil {
			log.Fatalf("orchestrator: %v", err)
		}
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Start the tickets HTTP server.
	srv := &http.Server{
		Addr:    *addr,
		Handler: orchestrator.NewHTTPServer(orch),
	}
	go func() {
		log.Printf("orchestrator: serving /tickets on %s", *addr)
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

	log.Printf("orchestrator: polling %s every %s — workflow=%s", *repo, *interval, *workflow)
	orch.Run(ctx)
	log.Println("orchestrator: stopped")
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
