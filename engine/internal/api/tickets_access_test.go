package api

// C1 (cross-tenant en la superficie de tickets): un usuario NO puede leer ni
// mutar stories/sprints/epics de un proyecto ajeno (404/lista vacía — sin leak
// de existencia); el service token (uid vacío) sigue sin restricción; y un
// POST /stories de sesión de usuario sin project_id es 400, nunca cae al
// proyecto "default".

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"forge/internal/auth"
	"forge/internal/httpx"
	"forge/internal/projects"
	"forge/internal/tickets"
)

// ticketsAccessServer wires a Server with auth+projects+tickets stores, two
// users (a, b) each owning one project (pa, pb), and B's backlog: epic eb,
// sprint spb and story sb1 in pb.
func ticketsAccessServer(t *testing.T) (s *Server, aCtx, svcCtx context.Context) {
	t.Helper()
	au, err := auth.Open(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatalf("auth.Open: %v", err)
	}
	pr, err := projects.Open(filepath.Join(t.TempDir(), "projects.db"))
	if err != nil {
		t.Fatalf("projects.Open: %v", err)
	}
	tix, err := tickets.Open(filepath.Join(t.TempDir(), "tickets.db"))
	if err != nil {
		t.Fatalf("tickets.Open: %v", err)
	}
	t.Cleanup(func() { au.Close(); pr.Close(); tix.Close() })

	a, _ := au.CreateUser("a@example.com", "password123")
	b, _ := au.CreateUser("b@example.com", "password123")
	if _, err := pr.Create(projects.Project{ID: "pa", OwnerID: a.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := pr.Create(projects.Project{ID: "pb", OwnerID: b.ID}); err != nil {
		t.Fatal(err)
	}
	if err := tix.CreateEpic(tickets.Epic{ID: "eb", Title: "B's secret epic"}); err != nil {
		t.Fatal(err)
	}
	if err := tix.CreateSprint(tickets.Sprint{ID: "spb", Name: "B sprint", ProjectID: "pb"}); err != nil {
		t.Fatal(err)
	}
	if err := tix.CreateStory(tickets.Story{ID: "sb1", Title: "B's story", EpicID: "eb", SprintID: "spb", ProjectID: "pb"}); err != nil {
		t.Fatal(err)
	}
	_ = b
	s = &Server{Auth: au, Projects: pr, Tickets: tix, linkCodes: newLinkCodeStore()}
	aCtx = httpx.WithUserID(context.Background(), a.ID)
	svcCtx = context.Background() // service token: no user id on the context
	return s, aCtx, svcCtx
}

// (a) User A cannot read or mutate B's stories/sprints/epics — 404 or empty,
// never the data, never a 403 that confirms existence.
func TestTicketsCrossTenantDenied(t *testing.T) {
	s, aCtx, _ := ticketsAccessServer(t)

	deny404 := func(name string, h http.HandlerFunc, method, path, id, body string) {
		t.Helper()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.SetPathValue("id", id)
		req = req.WithContext(aCtx)
		h(rec, req)
		if rec.Code != 404 {
			t.Fatalf("%s = %d, want 404; body=%s", name, rec.Code, rec.Body.String())
		}
	}

	deny404("GET /stories/sb1", s.getStory, "GET", "/stories/sb1", "sb1", "")
	deny404("PUT /stories/sb1/status", s.updateStoryStatus, "PUT", "/stories/sb1/status", "sb1", `{"status":"failed"}`)
	deny404("POST /stories/sb1/deps", s.addStoryDeps, "POST", "/stories/sb1/deps", "sb1", `{"deps":[]}`)
	deny404("POST /stories/sb1/claim", s.claimStory, "POST", "/stories/sb1/claim", "sb1", "")
	deny404("PUT /sprints/spb/status", s.updateSprintStatus, "PUT", "/sprints/spb/status", "spb", `{"status":"failed"}`)
	deny404("POST /sprints/spb/claim", s.claimSprint, "POST", "/sprints/spb/claim", "spb", "")
	deny404("POST /sprints/spb/requeue", s.requeueSprint, "POST", "/sprints/spb/requeue", "spb", "")
	deny404("GET /epics/eb", s.getEpic, "GET", "/epics/eb", "eb", "")

	// The denied mutations must not have touched B's story.
	st, err := s.Tickets.GetStory("sb1")
	if err != nil || st.Status != tickets.StatusBacklog {
		t.Fatalf("sb1 after denied mutations = %v (%v), want backlog", st.Status, err)
	}
}

// (a) The list readers scope a user session to its own projects — B's backlog
// never appears, with or without ?project=.
func TestTicketsListsScopedToMember(t *testing.T) {
	s, aCtx, _ := ticketsAccessServer(t)
	if err := s.Tickets.CreateStory(tickets.Story{ID: "sa1", Title: "A's story", ProjectID: "pa"}); err != nil {
		t.Fatal(err)
	}

	get := func(h http.HandlerFunc, target, pathID string) map[string]any {
		t.Helper()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", target, nil)
		if pathID != "" {
			req.SetPathValue("id", pathID)
		}
		req = req.WithContext(aCtx)
		h(rec, req)
		if rec.Code != 200 {
			t.Fatalf("GET %s = %d; body=%s", target, rec.Code, rec.Body.String())
		}
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out
	}

	if got := get(s.listStoriesHandler, "/stories", "")["stories"].([]any); len(got) != 1 {
		t.Fatalf("GET /stories for A = %d stories, want 1 (only sa1)", len(got))
	}
	if got := get(s.listStoriesHandler, "/stories?project=pb", "")["stories"].([]any); len(got) != 0 {
		t.Fatalf("GET /stories?project=pb for A = %d stories, want 0", len(got))
	}
	if got := get(s.listSprints, "/sprints", "")["sprints"].([]any); len(got) != 0 {
		t.Fatalf("GET /sprints for A = %d sprints, want 0 (spb is B's)", len(got))
	}
	if got := get(s.readySprints, "/sprints/ready?project=pb", "")["sprints"].([]any); len(got) != 0 {
		t.Fatalf("GET /sprints/ready?project=pb for A = %d, want 0", len(got))
	}
	if got := get(s.sprintStories, "/sprints/spb/stories", "spb")["stories"].([]any); len(got) != 0 {
		t.Fatalf("GET /sprints/spb/stories for A = %d stories, want 0", len(got))
	}
	if got := get(s.listEpics, "/epics", "")["epics"].([]any); len(got) != 0 {
		t.Fatalf("GET /epics for A = %d epics, want 0 (eb belongs to B's stories)", len(got))
	}
}

// (b) The service token (no user id on the context) keeps full system access.
func TestTicketsServiceTokenUnrestricted(t *testing.T) {
	s, _, svcCtx := ticketsAccessServer(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/stories/sb1", nil)
	req.SetPathValue("id", "sb1")
	s.getStory(rec, req.WithContext(svcCtx))
	if rec.Code != 200 {
		t.Fatalf("service GET /stories/sb1 = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("PUT", "/stories/sb1/status", strings.NewReader(`{"status":"running","run_id":"r1"}`))
	req2.SetPathValue("id", "sb1")
	s.updateStoryStatus(rec2, req2.WithContext(svcCtx))
	if rec2.Code != 200 {
		t.Fatalf("service PUT /stories/sb1/status = %d, want 200; body=%s", rec2.Code, rec2.Body.String())
	}

	// createStory without project_id keeps the system contract (default project).
	rec3 := httptest.NewRecorder()
	req3 := httptest.NewRequest("POST", "/stories", strings.NewReader(`{"id":"sys1","title":"system story"}`))
	s.createStory(rec3, req3.WithContext(svcCtx))
	if rec3.Code != 201 {
		t.Fatalf("service POST /stories = %d, want 201; body=%s", rec3.Code, rec3.Body.String())
	}
	if st, _ := s.Tickets.GetStory("sys1"); st.ProjectID != tickets.DefaultProjectID {
		t.Fatalf("system story project = %q, want %q", st.ProjectID, tickets.DefaultProjectID)
	}
}

// (c) POST /stories from a user session: no project_id → 400 (never a silent
// fall-through to "default"); own project → 201; someone else's project → 404.
func TestCreateStoryUserSessionRequiresOwnProject(t *testing.T) {
	s, aCtx, _ := ticketsAccessServer(t)

	post := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/stories", strings.NewReader(body))
		s.createStory(rec, req.WithContext(aCtx))
		return rec
	}

	if rec := post(`{"id":"sa9","title":"no project"}`); rec.Code != 400 {
		t.Fatalf("POST /stories without project_id = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if rec := post(`{"id":"sa9","title":"cross tenant","project_id":"pb"}`); rec.Code != 404 {
		t.Fatalf("POST /stories into pb = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	if rec := post(`{"id":"sa9","title":"own project","project_id":"pa"}`); rec.Code != 201 {
		t.Fatalf("POST /stories into pa = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
}

// DELETE /stories/{id}: cross-tenant is 404 (no leak); a story with dependents is
// 409 + the blocking ids; the happy path removes the story of the CORRECT project
// (with duplicate ids) and reports its surviving external_ref.
func TestDeleteStoryEndpoint(t *testing.T) {
	s, aCtx, _ := ticketsAccessServer(t)

	del := func(target, id string, ctx context.Context) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("DELETE", target, nil)
		req.SetPathValue("id", id)
		s.deleteStory(rec, req.WithContext(ctx))
		return rec
	}

	// Cross-tenant: A cannot delete B's story — 404, and it survives.
	if rec := del("/stories/sb1", "sb1", aCtx); rec.Code != 404 {
		t.Fatalf("A DELETE /stories/sb1 = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	if _, err := s.Tickets.GetStory("sb1"); err != nil {
		t.Fatalf("sb1 wrongly deleted: %v", err)
	}

	// A's backlog: sa-dep is a dependency of sa-main (in pa); plus a same-id "sb1"
	// orphan in pa carrying an external_ref, to prove scoping + the ref echo.
	if err := s.Tickets.CreateStory(tickets.Story{ID: "sa-dep", ProjectID: "pa"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Tickets.CreateStory(tickets.Story{ID: "sa-main", ProjectID: "pa", Deps: []string{"sa-dep"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Tickets.CreateStory(tickets.Story{ID: "sb1", ProjectID: "pa"}); err != nil {
		t.Fatal(err) // same id as B's story, different project
	}
	if err := s.Tickets.SetStoryExternalRef("pa", "sb1", "github:o/pa#5"); err != nil {
		t.Fatal(err)
	}

	// sa-dep has a dependent (sa-main) → 409 with the blocking id.
	rec := del("/stories/sa-dep", "sa-dep", aCtx)
	if rec.Code != 409 {
		t.Fatalf("DELETE sa-dep with dependent = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	var conflict map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &conflict)
	deps, _ := conflict["dependents"].([]any)
	if len(deps) != 1 || deps[0] != "sa-main" {
		t.Fatalf("409 dependents = %v, want [sa-main]", conflict["dependents"])
	}

	// Happy path: delete A's own sb1 (scoped to pa) — B's sb1 must survive, and the
	// response echoes the external_ref so the UI can warn the GitHub issue remains.
	rec2 := del("/stories/sb1?project=pa", "sb1", aCtx)
	if rec2.Code != 200 {
		t.Fatalf("DELETE /stories/sb1?project=pa = %d, want 200; body=%s", rec2.Code, rec2.Body.String())
	}
	var ok map[string]any
	_ = json.Unmarshal(rec2.Body.Bytes(), &ok)
	if ok["deleted"] != true || ok["external_ref"] != "github:o/pa#5" {
		t.Fatalf("delete response = %v, want deleted:true + external_ref", ok)
	}
	if _, err := s.Tickets.GetStoryInProject("pa", "sb1"); err == nil {
		t.Fatal("pa/sb1 was not deleted")
	}
	if _, err := s.Tickets.GetStoryInProject("pb", "sb1"); err != nil {
		t.Fatalf("pb/sb1 (B's) was wrongly deleted: %v", err)
	}
}
