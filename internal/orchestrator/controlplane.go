package orchestrator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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
