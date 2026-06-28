package api_test

import (
	"encoding/json"
	"testing"
	"time"

	"forge/internal/store"
)

// TestArtifactPicksLatestDone reproduces #18: a retried step leaves an older
// FAILED task plus a newer DONE one. GET /runs/{id}/artifacts/{step} must serve
// the DONE artifact, not the stale FAILED one that happens to come first by
// created_at.
func TestArtifactPicksLatestDone(t *testing.T) {
	base, a, _ := testKernel(t)

	if _, err := a.Store.CreateRun("run_artifact_t", "design", "", "{}"); err != nil {
		t.Fatalf("create run: %v", err)
	}

	// Older FAILED instance of the step.
	if err := a.Store.EnqueueTask(&store.Task{
		ID: "task_old", RunID: "run_artifact_t", WorkflowID: "design",
		StepID: "prd", Type: "design", Status: store.StatusFailed,
		Result: `{"text":"OLD-FAILED"}`,
	}); err != nil {
		t.Fatalf("enqueue old: %v", err)
	}
	// Ensure a strictly later created_at so ordering is deterministic.
	time.Sleep(2 * time.Millisecond)
	// Newer DONE instance from the retry.
	if err := a.Store.EnqueueTask(&store.Task{
		ID: "task_new", RunID: "run_artifact_t", WorkflowID: "design",
		StepID: "prd", Type: "design", Status: store.StatusDone,
		Result: `{"text":"NEW-DONE"}`,
	}); err != nil {
		t.Fatalf("enqueue new: %v", err)
	}

	resp, body := do(t, "GET", base+"/runs/run_artifact_t/artifacts/prd", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, body)
	}
	var got struct {
		Result struct {
			Text string `json:"text"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, body)
	}
	if got.Result.Text != "NEW-DONE" {
		t.Fatalf("artifact text = %q, want %q (served the stale FAILED instance)", got.Result.Text, "NEW-DONE")
	}
}
