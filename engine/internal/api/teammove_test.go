package api

// HTTP-level coverage for the mid-sprint board moves: cancel/move/split/edit. Reuses
// the requeueServer harness (user a owns project pa) and its mkStory helper.

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"forge/internal/tickets"
)

func doStory(s *Server, ctx context.Context, method, id, action, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	target := "/stories/" + id
	if action != "" {
		target += "/" + action
	}
	target += "?project=pa"
	var rdr *strings.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	} else {
		rdr = strings.NewReader("")
	}
	req := httptest.NewRequest(method, target, rdr).WithContext(ctx)
	req.SetPathValue("id", id)
	switch {
	case action == "cancel":
		s.cancelStory(rec, req)
	case action == "move":
		s.moveStory(rec, req)
	case action == "split":
		s.splitStory(rec, req)
	case action == "" && method == "PATCH":
		s.editStory(rec, req)
	}
	return rec
}

func TestCancelStoryEndpoint(t *testing.T) {
	s, aCtx, _ := requeueServer(t)
	mkStory(t, s, "C1", "", tickets.StatusFailed, false)
	rec := doStory(s, aCtx, "POST", "C1", "cancel", "")
	if rec.Code != 200 {
		t.Fatalf("cancel = %d, body %s", rec.Code, rec.Body.String())
	}
	if got, _ := s.Tickets.GetStoryInProject("pa", "C1"); got.Status != tickets.StatusCancelled {
		t.Fatalf("C1 status = %s, want cancelled", got.Status)
	}

	// Running is blocked with 409.
	mkStory(t, s, "C2", "", tickets.StatusRunning, false)
	if rec := doStory(s, aCtx, "POST", "C2", "cancel", ""); rec.Code != 409 {
		t.Fatalf("cancel running = %d, want 409", rec.Code)
	}
}

func TestMoveStoryEndpoint(t *testing.T) {
	s, aCtx, _ := requeueServer(t)
	for _, sp := range []string{"SP1", "SP2"} {
		if err := s.Tickets.CreateSprint(tickets.Sprint{ID: sp, ProjectID: "pa", Name: sp}); err != nil {
			t.Fatal(err)
		}
	}
	mkStory(t, s, "M1", "SP1", tickets.StatusBacklog, false)
	rec := doStory(s, aCtx, "POST", "M1", "move", `{"sprint_id":"SP2"}`)
	if rec.Code != 200 {
		t.Fatalf("move = %d, body %s", rec.Code, rec.Body.String())
	}
	if got, _ := s.Tickets.GetStoryInProject("pa", "M1"); got.SprintID != "SP2" {
		t.Fatalf("M1 sprint = %s, want SP2", got.SprintID)
	}
	// Unknown sprint → 404.
	if rec := doStory(s, aCtx, "POST", "M1", "move", `{"sprint_id":"SPX"}`); rec.Code != 404 {
		t.Fatalf("move to unknown = %d, want 404", rec.Code)
	}
}

func TestSplitStoryEndpoint(t *testing.T) {
	s, aCtx, _ := requeueServer(t)
	mkStory(t, s, "S1", "", tickets.StatusBacklog, false)
	rec := doStory(s, aCtx, "POST", "S1", "split", `{"parts":[{"title":"a"},{"title":"b"}]}`)
	if rec.Code != 200 {
		t.Fatalf("split = %d, body %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Parts []string `json:"parts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if strings.Join(resp.Parts, ",") != "S1-a,S1-b" {
		t.Fatalf("parts = %v", resp.Parts)
	}
	if got, _ := s.Tickets.GetStoryInProject("pa", "S1"); got.Status != tickets.StatusCancelled {
		t.Fatalf("S1 status = %s, want cancelled", got.Status)
	}
	// Too few parts → 400.
	mkStory(t, s, "S2", "", tickets.StatusBacklog, false)
	if rec := doStory(s, aCtx, "POST", "S2", "split", `{"parts":[{"title":"only"}]}`); rec.Code != 400 {
		t.Fatalf("split 1 part = %d, want 400", rec.Code)
	}
}

func TestEditStoryEndpoint(t *testing.T) {
	s, aCtx, _ := requeueServer(t)
	mkStory(t, s, "E1", "", tickets.StatusBacklog, false)
	rec := doStory(s, aCtx, "PATCH", "E1", "", `{"title":"renamed","owner":"backend"}`)
	if rec.Code != 200 {
		t.Fatalf("edit = %d, body %s", rec.Code, rec.Body.String())
	}
	got, _ := s.Tickets.GetStoryInProject("pa", "E1")
	if got.Title != "renamed" || got.Owner != "backend" {
		t.Fatalf("edit result = title %q owner %q", got.Title, got.Owner)
	}
	// Editing a running story → 409.
	mkStory(t, s, "E2", "", tickets.StatusRunning, false)
	if rec := doStory(s, aCtx, "PATCH", "E2", "", `{"title":"x"}`); rec.Code != 409 {
		t.Fatalf("edit running = %d, want 409", rec.Code)
	}
}
