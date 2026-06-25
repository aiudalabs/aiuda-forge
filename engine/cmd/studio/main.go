// Command studio runs the Studio service — the design/spec plane (BMAD-like)
// that turns an idea into a versioned spec and a ready backlog of GitHub Issues.
//
// Usage:
//
//	studio [flags]
//
// Flags:
//
//	-data    Root directory for project state and artifacts (default: ./studio-data)
//	-repo    GitHub repo in "owner/repo" form for handoff (required for real handoff)
//	-addr    Address to serve the Studio HTTP API (default: :9091)
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"forge/internal/httpx"
	"forge/internal/studio"
)

func main() {
	dataRoot := flag.String("data", envOr("STUDIO_DATA", "./studio-data"), "root dir for studio state and artifacts")
	repo := flag.String("repo", envOr("STUDIO_REPO", ""), "GitHub repo (owner/repo) for handoff issue creation")
	addr := flag.String("addr", envOr("STUDIO_ADDR", ":9091"), "address to serve the Studio API")
	flag.Parse()

	// Real implementations: claude CLI engine, gh CLI GitHub.
	// If no repo is given, the GitHub client will fail only at handoff time —
	// all other operations (phases, artifacts) work without a repo.
	var gh studio.GitHub
	if *repo != "" {
		gh = studio.NewGitHubClient(*repo)
	} else {
		gh = &noopGitHub{}
		log.Printf("studio: -repo not set — handoff will fail; set STUDIO_REPO or pass -repo")
	}

	eng := studio.NewClaudeEngine()

	s, err := studio.New(*dataRoot, eng, gh)
	if err != nil {
		log.Fatalf("studio: %v", err)
	}

	srv := &http.Server{
		Addr:         *addr,
		Handler:      httpx.CORS(os.Getenv("VIBEFORGE_CORS_ORIGIN"), studio.NewHTTPServer(s)),
		ReadTimeout:  5 * time.Minute, // phases can be long-running
		WriteTimeout: 5 * time.Minute,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Printf("studio: serving on %s — data=%s", *addr, *dataRoot)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("studio: http server: %v", err)
		}
	}()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()

	<-ctx.Done()
	log.Println("studio: stopped")
}

// noopGitHub is a placeholder GitHub that fails loudly — used when -repo is
// not provided so developers get a clear error at handoff time.
type noopGitHub struct{}

func (n *noopGitHub) CreateIssue(_ context.Context, title, _ string, _ []string) (int, error) {
	return 0, fmt.Errorf("studio: no GitHub repo configured (-repo flag or STUDIO_REPO env); cannot create issue %q", title)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
