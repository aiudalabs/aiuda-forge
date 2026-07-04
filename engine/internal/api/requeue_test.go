package api

// Requeue validation: only a `failed` story is requeued; `backlog` is an idempotent
// no-op (pressing the button twice is safe); running/in_review/done or a story whose
// kernel run is still live are blocked with a reason. The sprint sweep reencola solo
// las failed y reporta el resto. Cross-tenant is 404 (no existence leak).

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"forge/internal/auth"
	"forge/internal/httpx"
	"forge/internal/projects"
	"forge/internal/store"
	"forge/internal/tickets"
)

// requeueServer wires Auth+Projects+Tickets+kernel Store, one user A owning project
// pa, and a second user B owning pb (for the cross-tenant check).
func requeueServer(t *testing.T) (s *Server, aCtx context.Context, aID string) {
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
	st, err := store.Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { au.Close(); pr.Close(); tix.Close(); st.Close() })

	a, _ := au.CreateUser("a@example.com", "password123")
	b, _ := au.CreateUser("b@example.com", "password123")
	if _, err := pr.Create(projects.Project{ID: "pa", OwnerID: a.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := pr.Create(projects.Project{ID: "pb", OwnerID: b.ID}); err != nil {
		t.Fatal(err)
	}
	s = &Server{Auth: au, Projects: pr, Tickets: tix, Store: st, linkCodes: newLinkCodeStore()}
	return s, httpx.WithUserID(context.Background(), a.ID), a.ID
}

// mkStory creates a story in pa at the given status. When external, it carries an
// external_ref so requeue routes through SyncExternalStatus.
func mkStory(t *testing.T, s *Server, id, sprint string, status tickets.Status, external bool) {
	t.Helper()
	if err := s.Tickets.CreateStory(tickets.Story{ID: id, Title: id, SprintID: sprint, ProjectID: "pa"}); err != nil {
		t.Fatalf("CreateStory %s: %v", id, err)
	}
	if external {
		if err := s.Tickets.SetStoryExternalRef("pa", id, "github:o/pa#"+id); err != nil {
			t.Fatalf("SetStoryExternalRef %s: %v", id, err)
		}
	}
	switch status {
	case tickets.StatusBacklog:
		// already backlog
	case tickets.StatusFailed:
		if err := s.Tickets.MarkFailed(id); err != nil {
			t.Fatalf("MarkFailed %s: %v", id, err)
		}
	case tickets.StatusRunning:
		if _, err := s.Tickets.ClaimStory(id); err != nil {
			t.Fatalf("ClaimStory %s: %v", id, err)
		}
	case tickets.StatusInReview:
		if _, err := s.Tickets.ClaimStory(id); err != nil {
			t.Fatalf("ClaimStory %s: %v", id, err)
		}
		if err := s.Tickets.MarkInReview(id, "https://pr"); err != nil {
			t.Fatalf("MarkInReview %s: %v", id, err)
		}
	default:
		t.Fatalf("mkStory: unsupported status %q", status)
	}
}

func postRequeueStory(s *Server, ctx context.Context, id, project string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	target := "/stories/" + id + "/requeue"
	if project != "" {
		target += "?project=" + project
	}
	req := httptest.NewRequest("POST", target, nil)
	req.SetPathValue("id", id)
	s.requeueStory(rec, req.WithContext(ctx))
	return rec
}

func TestRequeueStoryValidation(t *testing.T) {
	s, aCtx, _ := requeueServer(t)

	// failed (external) → requeued:true, story back in backlog.
	mkStory(t, s, "s-failed", "", tickets.StatusFailed, true)
	rec := postRequeueStory(s, aCtx, "s-failed", "pa")
	if rec.Code != 200 {
		t.Fatalf("requeue failed story = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["requeued"] != true {
		t.Fatalf("requeue failed story requeued=%v, want true; body=%s", body["requeued"], rec.Body.String())
	}
	if st, _ := s.Tickets.GetStoryInProject("pa", "s-failed"); st.Status != tickets.StatusBacklog {
		t.Fatalf("s-failed after requeue = %q, want backlog", st.Status)
	}

	// Second press on the now-backlog story → 200 idempotent no-op, requeued:false + reason.
	rec = postRequeueStory(s, aCtx, "s-failed", "pa")
	if rec.Code != 200 {
		t.Fatalf("double requeue = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	body = map[string]any{}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["requeued"] != false || body["reason"] == nil {
		t.Fatalf("double requeue body = %s, want requeued:false + reason", rec.Body.String())
	}

	// running / in_review / done → 409 with a reason, story untouched.
	for _, tc := range []struct {
		id     string
		status tickets.Status
	}{
		{"s-running", tickets.StatusRunning},
		{"s-review", tickets.StatusInReview},
	} {
		mkStory(t, s, tc.id, "", tc.status, true)
		rec := postRequeueStory(s, aCtx, tc.id, "pa")
		if rec.Code != 409 {
			t.Fatalf("requeue %s story = %d, want 409; body=%s", tc.status, rec.Code, rec.Body.String())
		}
		if st, _ := s.Tickets.GetStoryInProject("pa", tc.id); st.Status != tc.status {
			t.Fatalf("%s after blocked requeue = %q, want unchanged %q", tc.id, st.Status, tc.status)
		}
	}
}

// A `failed` legacy story whose kernel run is still RUNNING must be blocked (R3
// desync): requeueing would race a live run and orphan its PR.
func TestRequeueStoryBlockedByLiveRun(t *testing.T) {
	s, aCtx, _ := requeueServer(t)

	run, err := s.Store.CreateRun("run-live", "factory", "pa", "{}")
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if err := s.Store.SetRunStatus(run.ID, store.StatusRunning); err != nil {
		t.Fatalf("SetRunStatus: %v", err)
	}
	// Legacy (non-external) story: run_id set while non-terminal, then failed.
	if err := s.Tickets.CreateStory(tickets.Story{ID: "s-live", Title: "s-live", ProjectID: "pa"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Tickets.SetStoryRun("s-live", run.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Tickets.MarkFailed("s-live"); err != nil {
		t.Fatal(err)
	}

	rec := postRequeueStory(s, aCtx, "s-live", "pa")
	if rec.Code != 409 {
		t.Fatalf("requeue with live run = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	if st, _ := s.Tickets.GetStoryInProject("pa", "s-live"); st.Status != tickets.StatusFailed {
		t.Fatalf("s-live after blocked requeue = %q, want failed", st.Status)
	}

	// Once the run finishes (DONE), the same story requeues cleanly.
	if err := s.Store.SetRunStatus(run.ID, store.StatusDone); err != nil {
		t.Fatalf("SetRunStatus DONE: %v", err)
	}
	rec = postRequeueStory(s, aCtx, "s-live", "pa")
	if rec.Code != 200 {
		t.Fatalf("requeue after run done = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if st, _ := s.Tickets.GetStoryInProject("pa", "s-live"); st.Status != tickets.StatusBacklog {
		t.Fatalf("s-live after requeue = %q, want backlog", st.Status)
	}
}

// Cross-tenant: A requeueing B's story is a 404 (no existence leak), and B's story
// is untouched.
func TestRequeueStoryCrossTenant(t *testing.T) {
	s, aCtx, _ := requeueServer(t)
	if err := s.Tickets.CreateStory(tickets.Story{ID: "sb1", Title: "B's", ProjectID: "pb"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Tickets.MarkFailed("sb1"); err != nil {
		t.Fatal(err)
	}
	rec := postRequeueStory(s, aCtx, "sb1", "pb")
	if rec.Code != 404 {
		t.Fatalf("A requeue B's story = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	if st, _ := s.Tickets.GetStoryInProject("pb", "sb1"); st.Status != tickets.StatusFailed {
		t.Fatalf("B's story wrongly mutated = %q, want failed", st.Status)
	}
}

// requeueSprint reencola solo las failed del sprint, reporta el resto en `skipped`
// con su razón, y es idempotente (segunda pasada no reencola nada nuevo).
func TestRequeueSprintMixed(t *testing.T) {
	s, aCtx, _ := requeueServer(t)
	if err := s.Tickets.CreateSprint(tickets.Sprint{ID: "sp1", Name: "sprint", ProjectID: "pa"}); err != nil {
		t.Fatal(err)
	}
	mkStory(t, s, "m-failed1", "sp1", tickets.StatusFailed, true)
	mkStory(t, s, "m-failed2", "sp1", tickets.StatusFailed, true)
	mkStory(t, s, "m-running", "sp1", tickets.StatusRunning, true)
	mkStory(t, s, "m-backlog", "sp1", tickets.StatusBacklog, true)

	post := func() map[string]any {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/sprints/sp1/requeue", nil)
		req.SetPathValue("id", "sp1")
		s.requeueSprint(rec, req.WithContext(aCtx))
		if rec.Code != 200 {
			t.Fatalf("requeueSprint = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out
	}

	out := post()
	requeued, _ := out["requeued"].([]any)
	skipped, _ := out["skipped"].([]any)
	if len(requeued) != 2 {
		t.Fatalf("requeued = %v, want the 2 failed stories", out["requeued"])
	}
	// Only the running story is a reported skip; backlog is a silent no-op.
	if len(skipped) != 1 {
		t.Fatalf("skipped = %v, want [m-running]", out["skipped"])
	}
	if sk := skipped[0].(map[string]any); sk["id"] != "m-running" || sk["reason"] == nil {
		t.Fatalf("skipped[0] = %v, want m-running + reason", skipped[0])
	}
	for _, id := range []string{"m-failed1", "m-failed2"} {
		if st, _ := s.Tickets.GetStoryInProject("pa", id); st.Status != tickets.StatusBacklog {
			t.Fatalf("%s after sprint requeue = %q, want backlog", id, st.Status)
		}
	}
	if st, _ := s.Tickets.GetStoryInProject("pa", "m-running"); st.Status != tickets.StatusRunning {
		t.Fatalf("m-running wrongly touched = %q, want running", st.Status)
	}

	// Idempotent: a second sweep has nothing failed left to requeue.
	out = post()
	if requeued, _ := out["requeued"].([]any); len(requeued) != 0 {
		t.Fatalf("second sweep requeued = %v, want none", out["requeued"])
	}
}

// Cross-tenant sprint requeue is a 404 (sprintDenied), no stories touched.
func TestRequeueSprintCrossTenant(t *testing.T) {
	s, aCtx, _ := requeueServer(t)
	if err := s.Tickets.CreateSprint(tickets.Sprint{ID: "spb", Name: "B sprint", ProjectID: "pb"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Tickets.CreateStory(tickets.Story{ID: "sb1", SprintID: "spb", ProjectID: "pb"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Tickets.MarkFailed("sb1"); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/sprints/spb/requeue", nil)
	req.SetPathValue("id", "spb")
	s.requeueSprint(rec, req.WithContext(aCtx))
	if rec.Code != 404 {
		t.Fatalf("A requeue B's sprint = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	if st, _ := s.Tickets.GetStoryInProject("pb", "sb1"); st.Status != tickets.StatusFailed {
		t.Fatalf("B's story wrongly requeued = %q, want failed", st.Status)
	}
}
