package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// ---- fakes ------------------------------------------------------------------

// fakeEngine is a hermetic Engine stub: returns deterministic output per phase.
type fakeEngine struct {
	mu      sync.Mutex
	outputs map[string]string // phase → output
	errors  map[string]error  // phase → forced error
	calls   []string          // phases called in order
}

func newFakeEngine() *fakeEngine {
	return &fakeEngine{
		outputs: map[string]string{
			"discovery":    "# Discovery\n\nProblem statement and target users.",
			"prd":          "# PRD\n\nProduct requirements.",
			"architecture": "# Architecture\n\nSystem design.",
			"ui":           "# UI Screens\n\nUser interface specs.",
			"backlog":      "# Backlog\n\nPrioritised issues.",
		},
		errors: make(map[string]error),
	}
}

func (f *fakeEngine) RunPhase(_ context.Context, _, phase, _ string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, phase)
	if err, ok := f.errors[phase]; ok {
		return "", err
	}
	out, ok := f.outputs[phase]
	if !ok {
		out = "# " + phase
	}
	return out, nil
}

func (f *fakeEngine) setCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make([]string, len(f.calls))
	copy(cp, f.calls)
	return cp
}

// fakeGitHub records created issues and assigns sequential numbers.
type fakeGitHub struct {
	mu     sync.Mutex
	issues []createdIssue
}

type createdIssue struct {
	Title  string
	Body   string
	Labels []string
	Number int
}

func (f *fakeGitHub) CreateIssue(_ context.Context, title, body string, labels []string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	num := len(f.issues) + 1
	f.issues = append(f.issues, createdIssue{
		Title:  title,
		Body:   body,
		Labels: labels,
		Number: num,
	})
	return num, nil
}

func (f *fakeGitHub) issueAt(i int) createdIssue {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.issues[i]
}

func (f *fakeGitHub) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.issues)
}

// ---- helper -----------------------------------------------------------------

func newTestStudio(t *testing.T) (*Studio, *fakeEngine, *fakeGitHub) {
	t.Helper()
	eng := newFakeEngine()
	gh := &fakeGitHub{}
	s, err := New(filepath.Join(t.TempDir(), "studio-data"), eng, gh)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s, eng, gh
}

// runApprovePhase runs phase and immediately approves it — shorthand for getting
// past a phase in multi-phase tests.
func runApprovePhase(t *testing.T, s *Studio, projectID, phase string) {
	t.Helper()
	if err := s.RunPhase(context.Background(), projectID, phase); err != nil {
		t.Fatalf("RunPhase(%q): %v", phase, err)
	}
	if err := s.ApprovePhase(projectID, phase); err != nil {
		t.Fatalf("ApprovePhase(%q): %v", phase, err)
	}
}

// ---- tests ------------------------------------------------------------------

// TestCreateAndGetProject: a project can be created and retrieved.
func TestCreateAndGetProject(t *testing.T) {
	s, _, _ := newTestStudio(t)

	proj, err := s.CreateProject("proj-1", "My Project")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if proj.ID != "proj-1" || proj.Name != "My Project" {
		t.Errorf("unexpected project: %+v", proj)
	}
	// All phases start as pending.
	for _, phase := range Phases {
		if proj.Phases[phase].Status != PhaseStatusPending {
			t.Errorf("phase %q: want pending, got %s", phase, proj.Phases[phase].Status)
		}
	}

	// GetProject round-trip.
	got, err := s.GetProject("proj-1")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if got.Name != "My Project" {
		t.Errorf("GetProject name: want My Project, got %s", got.Name)
	}
}

// TestRunPhaseProducesVersionedArtifact: running a phase produces a versioned
// artifact on disk. Re-running (after reject) produces v2.
func TestRunPhaseProducesVersionedArtifact(t *testing.T) {
	s, _, _ := newTestStudio(t)

	_, err := s.CreateProject("art-proj", "Artifact Test")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	// Run discovery.
	if err := s.RunPhase(context.Background(), "art-proj", "discovery"); err != nil {
		t.Fatalf("RunPhase discovery: %v", err)
	}

	arts, err := s.ListArtifacts("art-proj")
	if err != nil {
		t.Fatalf("ListArtifacts: %v", err)
	}
	if len(arts) != 1 {
		t.Fatalf("expected 1 artifact, got %d", len(arts))
	}
	if arts[0].Name != "discovery" || arts[0].Version != 1 {
		t.Errorf("artifact: want discovery v1, got %+v", arts[0])
	}

	// Reject and re-run — should produce v2.
	if err := s.RejectPhase("art-proj", "discovery", "needs more detail"); err != nil {
		t.Fatalf("RejectPhase: %v", err)
	}
	if err := s.RunPhase(context.Background(), "art-proj", "discovery"); err != nil {
		t.Fatalf("second RunPhase discovery: %v", err)
	}

	_, content, err := s.GetArtifact("art-proj", "discovery")
	if err != nil {
		t.Fatalf("GetArtifact: %v", err)
	}
	if content == "" {
		t.Error("artifact content should not be empty")
	}

	arts2, _ := s.ListArtifacts("art-proj")
	if arts2[0].Version != 2 {
		t.Errorf("after re-run: want version 2, got %d", arts2[0].Version)
	}
}

// TestApprovalGating: the next phase cannot run until the current is approved.
func TestApprovalGating(t *testing.T) {
	s, _, _ := newTestStudio(t)

	_, err := s.CreateProject("gate-proj", "Gating Test")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	// Run discovery — puts it in awaiting_approval.
	if err := s.RunPhase(context.Background(), "gate-proj", "discovery"); err != nil {
		t.Fatalf("RunPhase discovery: %v", err)
	}

	// Trying to run prd without approving discovery must fail.
	err = s.RunPhase(context.Background(), "gate-proj", "prd")
	if err == nil {
		t.Fatal("expected error running prd before discovery approved, got nil")
	}
	if !strings.Contains(err.Error(), "blocked") {
		t.Errorf("expected blocking error, got: %v", err)
	}

	// Approve discovery.
	if err := s.ApprovePhase("gate-proj", "discovery"); err != nil {
		t.Fatalf("ApprovePhase discovery: %v", err)
	}

	// Now prd can run.
	if err := s.RunPhase(context.Background(), "gate-proj", "prd"); err != nil {
		t.Fatalf("RunPhase prd after approval: %v", err)
	}
}

// TestRejectAllowsRerun: a rejected phase can be run again.
func TestRejectAllowsRerun(t *testing.T) {
	s, _, _ := newTestStudio(t)

	_, err := s.CreateProject("reject-proj", "Reject Test")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	if err := s.RunPhase(context.Background(), "reject-proj", "discovery"); err != nil {
		t.Fatalf("RunPhase: %v", err)
	}

	if err := s.RejectPhase("reject-proj", "discovery", "needs work"); err != nil {
		t.Fatalf("RejectPhase: %v", err)
	}

	proj, _ := s.GetProject("reject-proj")
	if proj.Phases["discovery"].Status != PhaseStatusRejected {
		t.Errorf("want rejected, got %s", proj.Phases["discovery"].Status)
	}
	if proj.Phases["discovery"].Feedback != "needs work" {
		t.Errorf("feedback not saved: %s", proj.Phases["discovery"].Feedback)
	}

	// Re-run must succeed.
	if err := s.RunPhase(context.Background(), "reject-proj", "discovery"); err != nil {
		t.Fatalf("re-run after rejection: %v", err)
	}
}

// TestApproveBlockedByStatus: approving a pending or running phase returns an
// error with a clear message.
func TestApproveBlockedByStatus(t *testing.T) {
	s, _, _ := newTestStudio(t)
	_, _ = s.CreateProject("status-proj", "Status Test")

	// Can't approve a pending phase.
	err := s.ApprovePhase("status-proj", "discovery")
	if err == nil || !strings.Contains(err.Error(), "cannot be approved") {
		t.Errorf("expected approval error for pending phase, got: %v", err)
	}
}

// TestFullPipelineApproval: run all 5 phases through the full pipeline, each
// approved, reaching the backlog.
func TestFullPipelineApproval(t *testing.T) {
	s, _, _ := newTestStudio(t)
	_, err := s.CreateProject("full-proj", "Full Pipeline")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	for _, phase := range Phases {
		runApprovePhase(t, s, "full-proj", phase)
	}

	proj, _ := s.GetProject("full-proj")
	for _, phase := range Phases {
		if proj.Phases[phase].Status != PhaseStatusApproved {
			t.Errorf("phase %q: want approved, got %s", phase, proj.Phases[phase].Status)
		}
	}

	// All phases approved — currentPhase() returns "".
	if cur := proj.currentPhase(); cur != "" {
		t.Errorf("currentPhase: want '', got %q", cur)
	}
}

// TestHandoffPublishesIssuesWithDeps: handoff after backlog approval publishes
// GitHub Issues with correct Depends-on body lines and depends: labels.
func TestHandoffPublishesIssuesWithDeps(t *testing.T) {
	s, _, gh := newTestStudio(t)
	_, err := s.CreateProject("handoff-proj", "Handoff Test")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	// Approve all phases.
	for _, phase := range Phases {
		runApprovePhase(t, s, "handoff-proj", phase)
	}

	items := []BacklogItem{
		{Title: "Bootstrap", Body: "Set up the repo."},
		{Title: "API layer", Body: "Build the API.", DepsOn: []int{1}},
		{Title: "Frontend", Body: "Build the UI.", DepsOn: []int{1, 2}},
	}

	issued, err := s.Handoff(context.Background(), "handoff-proj", items)
	if err != nil {
		t.Fatalf("Handoff: %v", err)
	}

	if len(issued) != 3 {
		t.Fatalf("expected 3 issued numbers, got %d", len(issued))
	}
	if issued[0] != 1 || issued[1] != 2 || issued[2] != 3 {
		t.Errorf("issued numbers: want [1,2,3], got %v", issued)
	}

	if gh.count() != 3 {
		t.Fatalf("expected 3 created issues, got %d", gh.count())
	}

	// Issue #1 — no deps, no Depends-on line.
	iss1 := gh.issueAt(0)
	if strings.Contains(iss1.Body, "Depends-on") {
		t.Errorf("issue 1 should not have Depends-on, got: %q", iss1.Body)
	}

	// Issue #2 — depends on #1; body must contain "Depends-on: #1".
	iss2 := gh.issueAt(1)
	if !strings.Contains(iss2.Body, "Depends-on: #1") {
		t.Errorf("issue 2 body: want Depends-on: #1, got: %q", iss2.Body)
	}
	// Must also have a "depends:#1" label.
	found := false
	for _, l := range iss2.Labels {
		if l == "depends:#1" {
			found = true
		}
	}
	if !found {
		t.Errorf("issue 2 labels: want depends:#1, got: %v", iss2.Labels)
	}

	// Issue #3 — depends on #1 and #2.
	iss3 := gh.issueAt(2)
	if !strings.Contains(iss3.Body, "Depends-on: #1, #2") {
		t.Errorf("issue 3 body: want Depends-on: #1, #2, got: %q", iss3.Body)
	}
}

// TestHandoffRequiresBacklogApproved: handoff before approving backlog fails.
func TestHandoffRequiresBacklogApproved(t *testing.T) {
	s, _, _ := newTestStudio(t)
	_, _ = s.CreateProject("hgate-proj", "Handoff Gate Test")

	_, err := s.Handoff(context.Background(), "hgate-proj", []BacklogItem{
		{Title: "thing", Body: "body"},
	})
	if err == nil {
		t.Fatal("expected error for handoff before backlog approved")
	}
	if !strings.Contains(err.Error(), "backlog") {
		t.Errorf("error should mention backlog: %v", err)
	}
}

// TestDepsBodyOrchestratorCompat: the "Depends-on:" lines emitted by handoff
// must be parseable by the orchestrator's parseDeps logic.
// We replicate parseDeps inline here to confirm compatibility.
func TestDepsBodyOrchestratorCompat(t *testing.T) {
	s, _, gh := newTestStudio(t)
	_, _ = s.CreateProject("compat-proj", "Compat Test")
	for _, phase := range Phases {
		runApprovePhase(t, s, "compat-proj", phase)
	}

	items := []BacklogItem{
		{Title: "First", Body: "First issue."},
		{Title: "Second", Body: "Depends on first.", DepsOn: []int{1}},
	}
	issued, err := s.Handoff(context.Background(), "compat-proj", items)
	if err != nil {
		t.Fatalf("Handoff: %v", err)
	}

	iss2 := gh.issueAt(1)
	// Parse the body using the same logic as the orchestrator.
	deps := parseDepsFromBody(iss2.Body)
	if len(deps) != 1 || deps[0] != issued[0] {
		t.Errorf("orchestrator compat: parseDeps got %v, want [%d]", deps, issued[0])
	}
}

// parseDepsFromBody is a replica of orchestrator.parseDeps for the "Depends-on:"
// body line, to verify cross-service compatibility without importing the package.
func parseDepsFromBody(body string) []int {
	var deps []int
	seen := map[int]bool{}
	for _, line := range strings.Split(body, "\n") {
		stripped := strings.TrimSpace(line)
		lower := strings.ToLower(stripped)
		if !strings.HasPrefix(lower, "depends-on:") {
			continue
		}
		rest := stripped[len("depends-on:"):]
		for _, part := range strings.Split(rest, ",") {
			p := strings.TrimSpace(part)
			p = strings.TrimPrefix(p, "#")
			var n int
			if _, err := fmt.Sscanf(p, "%d", &n); err == nil && n > 0 && !seen[n] {
				seen[n] = true
				deps = append(deps, n)
			}
		}
	}
	return deps
}

// TestStatePersistence: projects survive a Studio restart (reload from disk).
func TestStatePersistence(t *testing.T) {
	dir := t.TempDir()
	eng := newFakeEngine()
	gh := &fakeGitHub{}

	s1, err := New(filepath.Join(dir, "studio-data"), eng, gh)
	if err != nil {
		t.Fatalf("New s1: %v", err)
	}
	_, err = s1.CreateProject("persist-proj", "Persist Test")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if err := s1.RunPhase(context.Background(), "persist-proj", "discovery"); err != nil {
		t.Fatalf("RunPhase: %v", err)
	}
	if err := s1.ApprovePhase("persist-proj", "discovery"); err != nil {
		t.Fatalf("ApprovePhase: %v", err)
	}

	// Re-load from the same root.
	s2, err := New(filepath.Join(dir, "studio-data"), eng, gh)
	if err != nil {
		t.Fatalf("New s2: %v", err)
	}
	proj, err := s2.GetProject("persist-proj")
	if err != nil {
		t.Fatalf("GetProject after reload: %v", err)
	}
	if proj.Phases["discovery"].Status != PhaseStatusApproved {
		t.Errorf("after reload, discovery: want approved, got %s", proj.Phases["discovery"].Status)
	}
}

// TestEngineErrorMarksPhasePendingForRerun: when the engine returns an error,
// the phase transitions to rejected so the user can re-run.
func TestEngineErrorMarksPhasePendingForRerun(t *testing.T) {
	s, eng, _ := newTestStudio(t)
	eng.errors["discovery"] = errors.New("claude quota exceeded")
	_, _ = s.CreateProject("err-proj", "Error Test")

	err := s.RunPhase(context.Background(), "err-proj", "discovery")
	if err == nil {
		t.Fatal("expected error from engine, got nil")
	}

	proj, _ := s.GetProject("err-proj")
	if proj.Phases["discovery"].Status != PhaseStatusRejected {
		t.Errorf("want rejected after engine error, got %s", proj.Phases["discovery"].Status)
	}
	// Re-run must succeed after clearing the error.
	delete(eng.errors, "discovery")
	if err := s.RunPhase(context.Background(), "err-proj", "discovery"); err != nil {
		t.Fatalf("re-run after engine error cleared: %v", err)
	}
}

// ---- HTTP endpoint tests ----------------------------------------------------

func newTestServer(t *testing.T) (*httptest.Server, *Studio, *fakeEngine, *fakeGitHub) {
	t.Helper()
	st, eng, gh := newTestStudio(t)
	srv := httptest.NewServer(NewHTTPServer(st))
	t.Cleanup(srv.Close)
	return srv, st, eng, gh
}

func httpGet(t *testing.T, url string) (*http.Response, map[string]any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp, body
}

func httpPost(t *testing.T, url, jsonBody string) (*http.Response, map[string]any) {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(jsonBody))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp, body
}

// TestHTTPCreateProject: POST /projects returns 201 with project JSON.
func TestHTTPCreateProject(t *testing.T) {
	srv, _, _, _ := newTestServer(t)

	resp, body := httpPost(t, srv.URL+"/projects", `{"id":"http-proj","name":"HTTP Test"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /projects: want 201, got %d — body: %v", resp.StatusCode, body)
	}
	if body["id"] != "http-proj" {
		t.Errorf("id: want http-proj, got %v", body["id"])
	}
}

// TestHTTPListProjects: GET /projects returns the project list.
func TestHTTPListProjects(t *testing.T) {
	srv, st, _, _ := newTestServer(t)
	_, _ = st.CreateProject("list-a", "Alpha")
	_, _ = st.CreateProject("list-b", "Beta")

	resp, body := httpGet(t, srv.URL+"/projects")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /projects: %d", resp.StatusCode)
	}
	projects, ok := body["projects"].([]any)
	if !ok {
		t.Fatalf("projects key missing or wrong type: %v", body)
	}
	if len(projects) != 2 {
		t.Errorf("want 2 projects, got %d", len(projects))
	}
}

// TestHTTPRunApprovePhase: POST /run then /approve transitions the phase
// through awaiting_approval → approved.
func TestHTTPRunApprovePhase(t *testing.T) {
	srv, st, _, _ := newTestServer(t)
	_, _ = st.CreateProject("http-phase", "Phase Test")

	// Run discovery.
	resp, _ := httpPost(t, srv.URL+"/projects/http-phase/phases/discovery/run", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("run discovery: want 200, got %d", resp.StatusCode)
	}

	// Approve discovery.
	resp, body := httpPost(t, srv.URL+"/projects/http-phase/phases/discovery/approve", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("approve discovery: want 200, got %d — %v", resp.StatusCode, body)
	}

	// Verify status via GET.
	resp2, body2 := httpGet(t, srv.URL+"/projects/http-phase")
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("GET project: %d", resp2.StatusCode)
	}
	phases, _ := body2["phases"].(map[string]any)
	disc, _ := phases["discovery"].(map[string]any)
	if disc["status"] != "approved" {
		t.Errorf("discovery status: want approved, got %v", disc["status"])
	}
}

// TestHTTPRejectPhase: POST /reject transitions to rejected and stores feedback.
func TestHTTPRejectPhase(t *testing.T) {
	srv, st, _, _ := newTestServer(t)
	_, _ = st.CreateProject("http-reject", "Reject Test")

	httpPost(t, srv.URL+"/projects/http-reject/phases/discovery/run", "")
	resp, body := httpPost(t, srv.URL+"/projects/http-reject/phases/discovery/reject",
		`{"feedback":"Not detailed enough"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reject: want 200, got %d — %v", resp.StatusCode, body)
	}

	phases, _ := body["phases"].(map[string]any)
	disc, _ := phases["discovery"].(map[string]any)
	if disc["status"] != "rejected" {
		t.Errorf("status: want rejected, got %v", disc["status"])
	}
	if disc["feedback"] != "Not detailed enough" {
		t.Errorf("feedback: want 'Not detailed enough', got %v", disc["feedback"])
	}
}

// TestHTTPListArtifacts: GET /projects/{id}/artifacts returns artifact list.
func TestHTTPListArtifacts(t *testing.T) {
	srv, st, _, _ := newTestServer(t)
	_, _ = st.CreateProject("http-arts", "Artifacts Test")

	httpPost(t, srv.URL+"/projects/http-arts/phases/discovery/run", "")

	resp, body := httpGet(t, srv.URL+"/projects/http-arts/artifacts")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET artifacts: %d", resp.StatusCode)
	}
	arts, _ := body["artifacts"].([]any)
	if len(arts) != 1 {
		t.Errorf("want 1 artifact, got %d", len(arts))
	}
}

// TestHTTPGetArtifact: GET /projects/{id}/artifacts/{name} returns content.
func TestHTTPGetArtifact(t *testing.T) {
	srv, st, _, _ := newTestServer(t)
	_, _ = st.CreateProject("http-art1", "Artifact Get Test")
	httpPost(t, srv.URL+"/projects/http-art1/phases/discovery/run", "")

	resp, body := httpGet(t, srv.URL+"/projects/http-art1/artifacts/discovery")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET artifact: %d — %v", resp.StatusCode, body)
	}
	if body["content"] == "" || body["content"] == nil {
		t.Errorf("content should not be empty: %v", body)
	}
}

// TestHTTPHandoff: POST /projects/{id}/handoff publishes issues and returns
// issued numbers.
func TestHTTPHandoff(t *testing.T) {
	srv, st, _, gh := newTestServer(t)
	_, _ = st.CreateProject("http-handoff", "Handoff HTTP Test")
	for _, phase := range Phases {
		runApprovePhase(t, st, "http-handoff", phase)
	}

	reqBody := `{"items":[{"title":"Task 1","body":"First task"},{"title":"Task 2","body":"Second task","deps_on":[1]}]}`
	resp, body := httpPost(t, srv.URL+"/projects/http-handoff/handoff", reqBody)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("handoff: want 200, got %d — %v", resp.StatusCode, body)
	}

	issued, _ := body["issued"].([]any)
	if len(issued) != 2 {
		t.Errorf("want 2 issued, got %d — %v", len(issued), body)
	}
	if gh.count() != 2 {
		t.Errorf("want 2 github issues, got %d", gh.count())
	}
}

// TestHTTPNotFound: GET /projects/nonexistent returns 404.
func TestHTTPNotFound(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	resp, _ := httpGet(t, srv.URL+"/projects/nonexistent")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("want 404, got %d", resp.StatusCode)
	}
}

// TestHTTPHealthz: GET /healthz returns 200 {"ok":true}.
func TestHTTPHealthz(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	resp, body := httpGet(t, srv.URL+"/healthz")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz: %d", resp.StatusCode)
	}
	if body["ok"] != true {
		t.Errorf("healthz ok: want true, got %v", body["ok"])
	}
}
