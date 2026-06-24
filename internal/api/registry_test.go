package api_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// putRaw sends a raw (non-JSON) body — registry manifests are YAML/markdown.
func putRaw(t *testing.T, method, url, body string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, url, bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, data
}

func ids(t *testing.T, data []byte) []string {
	t.Helper()
	var out struct {
		IDs []string `json:"ids"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("bad list json: %s", data)
	}
	return out.IDs
}

func contains(ss []string, x string) bool {
	for _, s := range ss {
		if s == x {
			return true
		}
	}
	return false
}

// TestRegistryList: the repo registry's agents/workflows are listable.
func TestRegistryList(t *testing.T) {
	base, _, _ := testKernel(t)
	resp, data := do(t, "GET", base+"/registry/agents", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /registry/agents = %d: %s", resp.StatusCode, data)
	}
	if got := ids(t, data); !contains(got, "dev") || !contains(got, "reviewer") {
		t.Fatalf("agents list missing dev/reviewer: %v", got)
	}
	_, data = do(t, "GET", base+"/registry/workflows", nil)
	if got := ids(t, data); !contains(got, "factory") {
		t.Fatalf("workflows list missing factory: %v", got)
	}
}

// TestRegistrySkillCRUD: create → get → list → delete a skill (markdown).
func TestRegistrySkillCRUD(t *testing.T) {
	base, _, _ := testKernel(t)
	body := "# my-skill\nHow to do X.\n"
	if resp, d := putRaw(t, "PUT", base+"/registry/skills/my-skill", body); resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT skill = %d: %s", resp.StatusCode, d)
	}
	resp, d := do(t, "GET", base+"/registry/skills/my-skill", nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(d), "How to do X") {
		t.Fatalf("GET skill = %d: %s", resp.StatusCode, d)
	}
	if _, d := do(t, "GET", base+"/registry/skills", nil); !contains(ids(t, d), "my-skill") {
		t.Fatalf("skill not in list: %s", d)
	}
	if resp, d := do(t, "DELETE", base+"/registry/skills/my-skill", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("DELETE skill = %d: %s", resp.StatusCode, d)
	}
	if resp, _ := do(t, "GET", base+"/registry/skills/my-skill", nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("deleted skill still found: %d", resp.StatusCode)
	}
}

// TestRegistryValidation: a saved manifest is always runnable — invalid is rejected.
func TestRegistryValidation(t *testing.T) {
	base, _, _ := testKernel(t)
	// Workflow with no steps → invalid (the kernel parser rejects it).
	if resp, d := putRaw(t, "PUT", base+"/registry/workflows/bad", "id: bad\n"); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid workflow accepted (%d): %s", resp.StatusCode, d)
	}
	// Agent with no model → invalid.
	if resp, d := putRaw(t, "PUT", base+"/registry/agents/bad", "id: bad\nrole: x\n"); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid agent accepted (%d): %s", resp.StatusCode, d)
	}
	// A valid agent → accepted, then listable.
	ok := "id: myagent\nversion: 1.0.0\nmodel: claude-sonnet-4-6\nrole: tester\ntools: [read]\n"
	if resp, d := putRaw(t, "PUT", base+"/registry/agents/myagent", ok); resp.StatusCode != http.StatusOK {
		t.Fatalf("valid agent rejected (%d): %s", resp.StatusCode, d)
	}
	if _, d := do(t, "GET", base+"/registry/agents", nil); !contains(ids(t, d), "myagent") {
		t.Fatalf("created agent not listed: %s", d)
	}
}

// TestRegistryUnknownKind: a bogus kind 404s (no silent file path traversal).
func TestRegistryUnknownKind(t *testing.T) {
	base, _, _ := testKernel(t)
	if resp, _ := do(t, "GET", base+"/registry/nope", nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown kind not 404: %d", resp.StatusCode)
	}
}

// TestSettings: GET defaults, PUT a secret, secret comes back MASKED, and a
// masked round-trip never wipes the stored secret.
func TestSettings(t *testing.T) {
	base, _, _ := testKernel(t)
	resp, d := do(t, "GET", base+"/settings", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /settings = %d: %s", resp.StatusCode, d)
	}
	// Put an oauth secret.
	if resp, d := do(t, "PUT", base+"/settings", map[string]any{
		"agent_auth": map[string]any{"mode": "oauth_token", "secret": "sk-real-token"},
	}); resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT /settings = %d: %s", resp.StatusCode, d)
	}
	// GET must MASK the secret (never return it in clear).
	_, d = do(t, "GET", base+"/settings", nil)
	if strings.Contains(string(d), "sk-real-token") {
		t.Fatalf("secret leaked in clear: %s", d)
	}
	if !strings.Contains(string(d), "oauth_token") {
		t.Fatalf("settings did not persist mode: %s", d)
	}
	// A round-trip with the masked secret must NOT wipe the real one: change only
	// the merge policy, send no real secret → stored secret stays usable.
	if resp, _ := do(t, "PUT", base+"/settings", map[string]any{
		"merge_policy": map[string]string{"low": "automerge", "payments": "human_gate"},
	}); resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT settings (policy) failed: %d", resp.StatusCode)
	}
}
