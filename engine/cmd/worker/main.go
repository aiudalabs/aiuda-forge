// Command worker is a standalone executor. It shares the kernel store with the
// control plane and claims/executes/reports steps via the same atomic-claim
// queue (proven safe for N concurrent workers in Wave 1). Run as many as you
// like for horizontal scale. The control plane also runs an in-process worker;
// disable that (or point this at a separate store) to avoid contention if needed.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"vibeforge-kernel/internal/app"
)

func main() {
	a, err := app.Build(app.Config{
		DBPath:         envOr("VIBEFORGE_DB", "vibeforge.db"),
		RegistryRoot:   envOr("VIBEFORGE_REGISTRY", "registry"),
		WorkdirRoot:    envOr("VIBEFORGE_WORKDIR", ".vibeforge-runs"),
		EngineMode:     envOr("VIBEFORGE_ENGINE", "echo"),
		SandboxRuntime: os.Getenv("VIBEFORGE_SANDBOX"),
		AgentTimeout:   20 * time.Minute,
	})
	if err != nil {
		log.Fatalf("build worker: %v", err)
	}
	defer a.Close()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	id := envOr("VIBEFORGE_WORKER_ID", "worker-1")
	log.Printf("vibeforge worker %s started (engine=%s)", id, envOr("VIBEFORGE_ENGINE", "echo"))
	go a.Engine.ReaperLoop(ctx, 60_000, 10*time.Second)
	a.Engine.WorkerLoop(ctx, id)
	log.Printf("vibeforge worker %s stopped", id)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
