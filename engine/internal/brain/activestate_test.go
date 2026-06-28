package brain

import (
	"path/filepath"
	"testing"

	"forge/internal/store"
)

// ActiveState must report only the ACTIVE runs (queued/running/awaiting) as state,
// and COUNT (not enumerate) the old terminal runs — so the Brain can't mistake a
// pile of old failed/cancelled runs for "the factory is idle/paused" (the bug that
// made it tell the user the engine was paused while a run was actively building).
func TestActiveStateSeparatesActiveFromTerminal(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	mk := func(id string, to ...store.Status) {
		if _, err := s.CreateRun(id, "factory", "p1", "{}"); err != nil {
			t.Fatal(err)
		}
		for _, st := range to {
			if err := s.SetRunStatus(id, st); err != nil {
				t.Fatalf("SetRunStatus %s→%s: %v", id, st, err)
			}
		}
	}
	// active
	mk("r-queued")                                              // QUEUED
	mk("r-running", store.StatusRunning)                        // RUNNING
	mk("r-awaiting", store.StatusRunning, store.StatusAwaiting) // AWAITING (parked at gate)
	// terminal (the noise the Brain used to summarize wrong)
	mk("r-done", store.StatusRunning, store.StatusDone)
	mk("r-cancel", store.StatusCancelled)
	mk("r-failed", store.StatusRunning, store.StatusFailed)
	// another project — must not leak
	if _, err := s.CreateRun("r-other", "factory", "p2", "{}"); err != nil {
		t.Fatal(err)
	}

	ops := EngineOps{Store: s} // Engine nil → not paused (nil-safe Status)
	st, err := ops.ActiveState("p1")
	if err != nil {
		t.Fatal(err)
	}

	if st["paused"] != false {
		t.Errorf("paused = %v, want false", st["paused"])
	}
	active := st["active_runs"].([]map[string]any)
	if len(active) != 3 {
		t.Fatalf("active_runs = %d, want 3 (queued/running/awaiting); got %v", len(active), active)
	}
	if st["terminal_runs"] != 3 {
		t.Errorf("terminal_runs = %v, want 3 (done/cancel/failed)", st["terminal_runs"])
	}
	// p2's run must not appear in p1's state.
	for _, r := range active {
		if r["id"] == "r-other" {
			t.Error("cross-project run leaked into active_runs")
		}
	}
}

// get_state is registered as a safe viewer-level read.
func TestGetStateToolRegistered(t *testing.T) {
	def, ok := registry["get_state"]
	if !ok {
		t.Fatal("get_state tool not registered")
	}
	if def.Kind != Reversible || def.MinRole != "viewer" {
		t.Fatalf("get_state should be Reversible/viewer, got kind=%v role=%q", def.Kind, def.MinRole)
	}
}
