package orchestrator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// cpClient implements ControlPlane by POSTing to a real control-plane server.
type cpClient struct {
	baseURL string
	http    *http.Client
	token   string // service token sent as "Authorization: Bearer <token>" (C1)
}

// NewControlPlaneClient returns a ControlPlane that fires runs against baseURL
// (e.g. "http://localhost:8080"). It reads VIBEFORGE_API_TOKEN and, when set,
// presents it as a Bearer token on every control-plane call so the orchestrator
// authenticates against the mandatory-auth middleware (audit C1).
func NewControlPlaneClient(baseURL string) ControlPlane {
	return &cpClient{
		baseURL: baseURL,
		http:    &http.Client{Timeout: 30 * time.Second},
		token:   os.Getenv("VIBEFORGE_API_TOKEN"),
	}
}

// authReq builds an http.Request with the Bearer auth header attached when a
// service token is configured. All control-plane calls go through here.
func (c *cpClient) authReq(ctx context.Context, method, url string, body []byte) (*http.Request, error) {
	var rdr *bytes.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rdr)
	if err != nil {
		return nil, err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	return req, nil
}

type fireRunReq struct {
	Workflow string `json:"workflow"`
	Payload  any    `json:"payload"`
}

type fireRunResp struct {
	ID string `json:"id"`
}

// FireRun POSTs to POST /runs and returns the run ID from the response.
func (c *cpClient) FireRun(ctx context.Context, workflow string, payload any) (string, error) {
	body, err := json.Marshal(fireRunReq{Workflow: workflow, Payload: payload})
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	req, err := c.authReq(ctx, http.MethodPost, c.baseURL+"/runs", body)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("post /runs: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		var errBody map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&errBody)
		return "", fmt.Errorf("post /runs: status %d: %v", resp.StatusCode, errBody)
	}

	var result fireRunResp
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	if result.ID == "" {
		return "", fmt.Errorf("post /runs: empty run ID in response")
	}
	return result.ID, nil
}

// RunStatus GETs /runs/{id} and returns its status field.
func (c *cpClient) RunStatus(ctx context.Context, runID string) (string, error) {
	req, err := c.authReq(ctx, http.MethodGet, c.baseURL+"/runs/"+runID, nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("get /runs/%s: %w", runID, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("get /runs/%s: status %d", runID, resp.StatusCode)
	}

	var run struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&run); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	return run.Status, nil
}

// ExecutionUnit GETs /settings and returns the execution_unit field
// ("sprint"|"story"). An empty/missing value is returned as "" so the caller can
// apply its own default.
func (c *cpClient) ExecutionUnit(ctx context.Context) (string, error) {
	req, err := c.authReq(ctx, http.MethodGet, c.baseURL+"/settings", nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("get /settings: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("get /settings: status %d", resp.StatusCode)
	}
	var s struct {
		ExecutionUnit string `json:"execution_unit"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		return "", fmt.Errorf("decode /settings: %w", err)
	}
	return s.ExecutionUnit, nil
}

// MergeMode GETs /settings and returns the merge_mode field ("manual"|"auto").
// An empty/missing value is returned as "" so the caller applies its own default.
func (c *cpClient) MergeMode(ctx context.Context) (string, error) {
	req, err := c.authReq(ctx, http.MethodGet, c.baseURL+"/settings", nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("get /settings: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("get /settings: status %d", resp.StatusCode)
	}
	var s struct {
		MergeMode string `json:"merge_mode"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		return "", fmt.Errorf("decode /settings: %w", err)
	}
	return s.MergeMode, nil
}

// RunPRURL GETs /runs/{id} and extracts the PR URL the run's `pr` step opened.
// The pr step records "pr(github): opened <url>" in its result detail; this finds
// the pr-type step and parses that URL. Returns "" (nil error) when no pr step
// reported a GitHub PR (local mode, or the run hasn't reached the pr step yet).
func (c *cpClient) RunPRURL(ctx context.Context, runID string) (string, error) {
	req, err := c.authReq(ctx, http.MethodGet, c.baseURL+"/runs/"+runID, nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("get /runs/%s: %w", runID, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("get /runs/%s: status %d", runID, resp.StatusCode)
	}
	var rv struct {
		Steps []struct {
			Type   string `json:"type"`
			Result string `json:"result"` // JSON: {"success":..,"output":..,"detail":".."}
		} `json:"steps"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rv); err != nil {
		return "", fmt.Errorf("decode /runs/%s: %w", runID, err)
	}
	for _, st := range rv.Steps {
		if st.Type != "pr" {
			continue
		}
		var res struct {
			Detail string `json:"detail"`
		}
		if json.Unmarshal([]byte(st.Result), &res) != nil {
			continue
		}
		if url := prURLFromDetail(res.Detail); url != "" {
			return url, nil
		}
	}
	return "", nil
}

// RunPRResult GETs /runs/{id} and inspects the pr STEP itself (status + detail),
// not just the run's top-level DONE. This lets the scheduler tell a DONE run that
// opened a PR apart from a DONE run whose pr step FAILED or recorded no URL (H1) —
// the latter must NOT be parked in_review with an empty PR (which hangs forever).
//   - hasPR=false: the run has no pr step (e.g. not reached yet).
//   - prStepOK=false: a pr step exists but did not reach a successful terminal
//     state (FAILED, or still running) → the caller should treat the run as failed.
//   - prStepOK=true, url=="": the pr step succeeded but opened no GitHub PR (local
//     mode pushed a branch). The caller decides whether that's acceptable.
func (c *cpClient) RunPRResult(ctx context.Context, runID string) (url string, prStepOK bool, hasPR bool, err error) {
	req, err := c.authReq(ctx, http.MethodGet, c.baseURL+"/runs/"+runID, nil)
	if err != nil {
		return "", false, false, fmt.Errorf("build request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", false, false, fmt.Errorf("get /runs/%s: %w", runID, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return "", false, false, fmt.Errorf("get /runs/%s: status %d", runID, resp.StatusCode)
	}
	var rv struct {
		Steps []struct {
			Type   string `json:"type"`
			Status string `json:"status"`
			Result string `json:"result"`
		} `json:"steps"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rv); err != nil {
		return "", false, false, fmt.Errorf("decode /runs/%s: %w", runID, err)
	}
	for _, st := range rv.Steps {
		if st.Type != "pr" {
			continue
		}
		// The pr step must have reached DONE for a PR to be real; a FAILED (or
		// still-running) pr step means no usable PR was produced.
		if st.Status != "DONE" {
			return "", false, true, nil
		}
		var res struct {
			Detail string `json:"detail"`
		}
		if json.Unmarshal([]byte(st.Result), &res) != nil {
			return "", true, true, nil
		}
		return prURLFromDetail(res.Detail), true, true, nil
	}
	return "", false, false, nil
}

// prURLFromDetail parses the PR URL from a pr step's detail string, which the pr
// runner formats as "pr(github): opened <url>". Returns "" if the detail is not
// the GitHub-opened form (e.g. a local-mode "pr(local): committed …" detail).
func prURLFromDetail(detail string) string {
	const marker = "pr(github): opened "
	i := strings.Index(detail, marker)
	if i < 0 {
		return ""
	}
	return strings.TrimSpace(detail[i+len(marker):])
}
