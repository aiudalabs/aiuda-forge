package api_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

// waitStatus polls a run until its top-level status reaches one of want.
func waitStatus(t *testing.T, base, id string, want ...string) string {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		resp, data := do(t, "GET", base+"/runs/"+id, nil)
		if resp.StatusCode == http.StatusOK {
			var run map[string]any
			_ = json.Unmarshal(data, &run)
			st, _ := run["status"].(string)
			for _, w := range want {
				if st == w {
					return st
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	_, evs := do(t, "GET", base+"/runs/"+id+"/events", nil)
	t.Fatalf("run %s never reached %v\nevents: %s", id, want, evs)
	return ""
}

// waitAwaitingStep polls until some step of the run is AWAITING (a human_gate
// parked). The board derives "needs approval" from this — the run keeps RUNNING
// while a step waits. Returns the awaiting step id.
func waitAwaitingStep(t *testing.T, base, id string) string {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		resp, data := do(t, "GET", base+"/runs/"+id, nil)
		if resp.StatusCode == http.StatusOK {
			var rv struct {
				Steps []struct {
					StepID string `json:"step_id"`
					Status string `json:"status"`
				} `json:"steps"`
			}
			_ = json.Unmarshal(data, &rv)
			for _, s := range rv.Steps {
				if s.Status == "AWAITING" {
					return s.StepID
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("run %s never had an AWAITING step", id)
	return ""
}

// TestRejectStep: a human_gate parks (AWAITING); rejecting it with a reason
// completes the gate as a failure → the run does NOT merge (here: FAILED, since
// the `gated` workflow declares no on_fail on the gate). Governance works.
func TestRejectStep(t *testing.T) {
	base, _, _ := testKernel(t)
	resp, data := do(t, "POST", base+"/runs", map[string]any{
		"workflow": "gated",
		"payload":  map[string]any{"ticket": "implement X"},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /runs gated = %d: %s", resp.StatusCode, data)
	}
	var run map[string]any
	_ = json.Unmarshal(data, &run)
	id := run["id"].(string)

	// It must PARK at the human_gate (proves governance pause).
	if step := waitAwaitingStep(t, base, id); step != "approve" {
		t.Fatalf("expected 'approve' step awaiting, got %q", step)
	}

	// Reject with a reason.
	resp, data = do(t, "POST", base+"/runs/"+id+"/steps/approve/reject",
		map[string]any{"reason": "review found a unicode bug — cover ø/ß"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST reject = %d: %s", resp.StatusCode, data)
	}

	// No on_fail on the gate → rejected run terminates FAILED (NOT merged/DONE).
	if st := waitStatus(t, base, id, "FAILED", "DONE"); st != "FAILED" {
		t.Fatalf("rejected run should be FAILED, got %s", st)
	}
}

// TestAnswerStepValidationAndNoTarget exercises the answer endpoint's guards: an
// empty body is a 400, and answering a gate that declares no on_fail.goto (the
// `gated` workflow's `approve` gate) maps ErrNoAnswerTarget to a 409. The happy
// path (phase re-run + re-park) is covered at the engine level with a fake run.
func TestAnswerStepValidationAndNoTarget(t *testing.T) {
	base, _, _ := testKernel(t)
	resp, data := do(t, "POST", base+"/runs", map[string]any{
		"workflow": "gated",
		"payload":  map[string]any{"ticket": "implement X"},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /runs gated = %d: %s", resp.StatusCode, data)
	}
	var run map[string]any
	_ = json.Unmarshal(data, &run)
	id := run["id"].(string)

	if step := waitAwaitingStep(t, base, id); step != "approve" {
		t.Fatalf("expected 'approve' step awaiting, got %q", step)
	}

	// Empty text → 400.
	resp, data = do(t, "POST", base+"/runs/"+id+"/steps/approve/answer", map[string]any{"text": "   "})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty answer should be 400, got %d: %s", resp.StatusCode, data)
	}

	// gated's approve gate has NO on_fail.goto → ErrNoAnswerTarget → 409.
	resp, data = do(t, "POST", base+"/runs/"+id+"/steps/approve/answer", map[string]any{"text": "use Postgres"})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("answer on a gate with no on_fail should be 409, got %d: %s", resp.StatusCode, data)
	}
}

// TestMetricsCost: /metrics exposes the cost-breakdown shape (total + by_workflow
// + by_step) and acceptance rate. Value is 0 with the free fake backend — the
// point is the aggregation plumbing exists and is correct.
func TestMetricsCost(t *testing.T) {
	base, _, _ := testKernel(t)
	id := startFactory(t, base)
	waitTerminal(t, base, id)

	resp, data := do(t, "GET", base+"/metrics", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /metrics = %d: %s", resp.StatusCode, data)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("bad metrics json: %s", data)
	}
	for _, k := range []string{"total_cost_usd", "cost_by_workflow", "cost_by_step", "acceptance_rate", "by_status"} {
		if _, ok := m[k]; !ok {
			t.Fatalf("metrics missing key %q: %s", k, data)
		}
	}
	bs, _ := m["by_status"].(map[string]any)
	if bs["DONE"] == nil {
		t.Fatalf("expected a DONE run in by_status: %s", data)
	}
}
