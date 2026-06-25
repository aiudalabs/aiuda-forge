package orchestrator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// cpClient implements ControlPlane by POSTing to a real control-plane server.
type cpClient struct {
	baseURL string
	http    *http.Client
}

// NewControlPlaneClient returns a ControlPlane that fires runs against baseURL
// (e.g. "http://localhost:8080").
func NewControlPlaneClient(baseURL string) ControlPlane {
	return &cpClient{
		baseURL: baseURL,
		http:    &http.Client{},
	}
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

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/runs", bytes.NewReader(body))
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/runs/"+runID, nil)
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/settings", nil)
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/settings", nil)
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/runs/"+runID, nil)
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
