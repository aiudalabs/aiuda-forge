package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"forge/internal/agent"
	"forge/internal/channels"
	"forge/internal/digest"
	"forge/internal/projects"
	"forge/internal/store"
	"forge/internal/tickets"
)

// TestBuildFailsOnInvalidDigestCron: a garbage VIBEFORGE_DIGEST_CRON must FAIL the
// boot with a clear, named error rather than silently never firing.
func TestBuildFailsOnInvalidDigestCron(t *testing.T) {
	t.Setenv("VIBEFORGE_DIGEST_CRON", "not a cron")
	t.Setenv("ANTHROPIC_API_KEY", "") // keep the brain out of this boot
	dir := t.TempDir()
	_, err := Build(Config{
		DBPath:       filepath.Join(dir, "control.db"),
		RegistryRoot: dir,
		WorkdirRoot:  dir,
		EngineMode:   "echo",
		Backend:      agent.FakeBackend{Reply: "ok"},
	})
	if err == nil {
		t.Fatal("expected Build to fail on invalid cron, got nil")
	}
	if !strings.Contains(err.Error(), "VIBEFORGE_DIGEST_CRON") {
		t.Fatalf("error should name the cron env var, got: %v", err)
	}
}

func TestBuildAcceptsDefaultDigestCron(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	dir := t.TempDir()
	a, err := Build(Config{
		DBPath:       filepath.Join(dir, "control.db"),
		RegistryRoot: dir,
		WorkdirRoot:  dir,
		EngineMode:   "echo",
		Backend:      agent.FakeBackend{Reply: "ok"},
	})
	if err != nil {
		t.Fatalf("Build with default cron: %v", err)
	}
	defer a.Close()
	if a.digestCron.String() != "0 13 * * *" {
		t.Fatalf("default cron = %q, want 0 13 * * *", a.digestCron.String())
	}
}

// recConn records the targets it was asked to notify.
type recConn struct{ targets []string }

func (c *recConn) Name() string { return "telegram" }
func (c *recConn) Notify(_ context.Context, target string, _ channels.Event) error {
	c.targets = append(c.targets, target)
	return nil
}

// TestSendDigestsRespectsChannel: a project with digest_channel set receives the
// digest and its watermark advances; a project with no channel (null = off) is a
// no-op.
func TestSendDigestsRespectsChannel(t *testing.T) {
	dir := t.TempDir()
	proj, err := projects.Open(filepath.Join(dir, "projects.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer proj.Close()
	tix, err := tickets.Open(filepath.Join(dir, "tickets.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer tix.Close()
	st, err := store.Open(filepath.Join(dir, "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if _, err := proj.Create(projects.Project{ID: "on", Name: "On"}); err != nil {
		t.Fatal(err)
	}
	if err := proj.SetDigestChannel("on", "telegram:12345"); err != nil {
		t.Fatal(err)
	}
	if _, err := proj.Create(projects.Project{ID: "off", Name: "Off"}); err != nil {
		t.Fatal(err)
	}

	conn := &recConn{}
	a := &App{
		Projects:       proj,
		digester:       &digest.Digester{Stories: tix, Runs: st},
		digestChannels: channels.Registry{"telegram": conn},
	}
	a.sendDigests(context.Background())

	if len(conn.targets) != 1 || conn.targets[0] != "12345" {
		t.Fatalf("notified targets = %v, want [12345]", conn.targets)
	}
	// The "on" project's watermark advanced; "off" stayed at 0.
	pon, _ := proj.Get("on")
	if pon.LastDigestAt == 0 {
		t.Fatal("expected last_digest_at to advance for the delivered project")
	}
	poff, _ := proj.Get("off")
	if poff.LastDigestAt != 0 {
		t.Fatalf("off project watermark moved to %d, want 0 (no-op)", poff.LastDigestAt)
	}
}

// TestReleaseRunnerRegistered proves app.Build wires the `release` step runner: a
// release run reaches a terminal state via the runner's own validation ("no repo")
// rather than the executor's "no runner for step type" — the latter would mean the
// step type was never registered.
func TestReleaseRunnerRegistered(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	dir := t.TempDir()
	wfDir := filepath.Join(dir, "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	wf := "id: release\nversion: 1.0.0\nsteps:\n  - id: release\n    type: release\n"
	if err := os.WriteFile(filepath.Join(wfDir, "release.yaml"), []byte(wf), 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := Build(Config{
		DBPath:       filepath.Join(dir, "control.db"),
		RegistryRoot: dir,
		WorkdirRoot:  filepath.Join(dir, "runs"),
		EngineMode:   "echo",
		Backend:      agent.FakeBackend{Reply: "ok"},
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	defer a.Close()

	runID, err := a.Engine.StartRun("release", map[string]any{}) // no repo → runner fails cleanly
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	status, err := a.Engine.RunToCompletion(context.Background(), runID)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if status != store.StatusFailed {
		t.Fatalf("run status = %s, want failed (no repo)", status)
	}
	tasks, _ := a.Store.TasksForRun(runID)
	if len(tasks) == 0 {
		t.Fatal("no tasks recorded")
	}
	last := tasks[len(tasks)-1]
	if strings.Contains(last.Error, "no runner for step type") {
		t.Fatalf("release runner not registered: %q", last.Error)
	}
	if !strings.Contains(last.Error, "no repo") {
		t.Fatalf("expected the release runner's own failure, got %q", last.Error)
	}
}
