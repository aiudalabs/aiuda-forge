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

// TestAgentPersona: GET /registry/agents/{id}/persona returns the .md sidecar
// (200 with content) for a known agent and 404 for an unknown id.
// Also validates that a traversal id is rejected before any file I/O.
func TestAgentPersona(t *testing.T) {
	base, _, _ := testKernel(t)

	// Known agent with a persona sidecar.
	resp, data := do(t, "GET", base+"/registry/agents/dev/persona", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /registry/agents/dev/persona = %d: %s", resp.StatusCode, data)
	}
	if !strings.Contains(string(data), "Persona") && !strings.Contains(string(data), "dev") {
		t.Errorf("persona body looks empty or wrong: %s", data)
	}

	// Non-existent agent id → 404.
	resp, _ = do(t, "GET", base+"/registry/agents/no-such-agent/persona", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown agent persona expected 404, got %d", resp.StatusCode)
	}

	// Traversal id must not escape the registry root.
	resp, _ = putRaw(t, "GET", base+"/registry/agents/..%2f..%2fetc%2fpasswd/persona", "")
	if resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusBadRequest {
		t.Errorf("traversal persona expected 404/400, got %d", resp.StatusCode)
	}
}

// TestRegistryUnknownKind: a bogus kind 404s (no silent file path traversal).
func TestRegistryUnknownKind(t *testing.T) {
	base, _, _ := testKernel(t)
	if resp, _ := do(t, "GET", base+"/registry/nope", nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown kind not 404: %d", resp.StatusCode)
	}
}

// TestRegistryPathTraversal: traversal ids must never escape the registry root.
//
// The Go net/http mux path-cleans raw ../ sequences and issues a 301 redirect
// before the handler is invoked (raw "../../settings" → redirect → /settings),
// so those forms never reach pathFor at all.
//
// The dangerous form is the URL-encoded variant (%2f) which the mux passes through
// as-is, causing PathValue("id") to return "../../etc/passwd". pathFor must reject
// that before doing any file I/O.
func TestRegistryPathTraversal(t *testing.T) {
	base, _, _ := testKernel(t)

	// URL-encoded dot-dot — the mux delivers the decoded path to PathValue so
	// pathFor sees id = "../../etc/passwd" and must reject it.
	traversals := []string{
		base + "/registry/workflows/..%2f..%2f..%2fetc%2fpasswd",
		base + "/registry/agents/..%2f..%2fetc%2fpasswd",
		base + "/registry/skills/..%2f..%2fsettings.json",
	}
	for _, url := range traversals {
		resp, _ := putRaw(t, "GET", url, "")
		if resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusBadRequest {
			t.Errorf("traversal GET %s: expected 404/400, got %d", url, resp.StatusCode)
		}
		resp, _ = putRaw(t, "PUT", url, "id: x\nversion: 1.0.0\nmodel: claude-sonnet-4-6\nrole: t\n")
		if resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusBadRequest {
			t.Errorf("traversal PUT %s: expected 404/400, got %d", url, resp.StatusCode)
		}
		resp, _ = putRaw(t, "DELETE", url, "")
		if resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusBadRequest {
			t.Errorf("traversal DELETE %s: expected 404/400, got %d", url, resp.StatusCode)
		}
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
