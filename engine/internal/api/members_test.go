package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"forge/internal/auth"
	"forge/internal/httpx"
	"forge/internal/projects"
)

func membersServer(t *testing.T) (*Server, *auth.Store, *projects.Store) {
	t.Helper()
	au, err := auth.Open(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatalf("auth.Open: %v", err)
	}
	pr, err := projects.Open(filepath.Join(t.TempDir(), "projects.db"))
	if err != nil {
		t.Fatalf("projects.Open: %v", err)
	}
	t.Cleanup(func() { au.Close(); pr.Close() })
	return &Server{Auth: au, Projects: pr}, au, pr
}

// Inviting an existing user adds them immediately; a non-owner cannot manage.
func TestInviteExistingUserAndOwnerGate(t *testing.T) {
	s, au, pr := membersServer(t)
	owner, _ := au.CreateUser("owner@example.com", "password123")
	bob, _ := au.CreateUser("bob@example.com", "password123")
	if _, err := pr.Create(projects.Project{ID: "p1", OwnerID: owner.ID}); err != nil {
		t.Fatal(err)
	}

	// Owner invites an existing user → added immediately.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/projects/p1/members", strings.NewReader(`{"email":"bob@example.com","role":"editor"}`))
	req.SetPathValue("id", "p1")
	req = req.WithContext(httpx.WithUserID(context.Background(), owner.ID))
	s.inviteMember(rec, req)
	if rec.Code != 201 {
		t.Fatalf("invite existing status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out["status"] != "added" {
		t.Fatalf("status = %v, want added", out["status"])
	}
	if role, _ := pr.MemberRole("p1", bob.ID); role != projects.RoleEditor {
		t.Fatalf("bob role = %q, want editor", role)
	}

	// Bob (now an editor, not owner) cannot invite → 403.
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("POST", "/projects/p1/members", strings.NewReader(`{"email":"x@example.com","role":"viewer"}`))
	req2.SetPathValue("id", "p1")
	req2 = req2.WithContext(httpx.WithUserID(context.Background(), bob.ID))
	s.inviteMember(rec2, req2)
	if rec2.Code != 403 {
		t.Fatalf("editor invite status = %d, want 403", rec2.Code)
	}
}

// Inviting an unknown email returns a pending invite + token the inviter shares.
func TestInviteUnknownEmailIssuesLink(t *testing.T) {
	s, au, pr := membersServer(t)
	owner, _ := au.CreateUser("owner@example.com", "password123")
	if _, err := pr.Create(projects.Project{ID: "p1", OwnerID: owner.ID}); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/projects/p1/members", strings.NewReader(`{"email":"new@example.com","role":"viewer"}`))
	req.SetPathValue("id", "p1")
	req = req.WithContext(httpx.WithUserID(context.Background(), owner.ID))
	s.inviteMember(rec, req)
	if rec.Code != 201 {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out["status"] != "invited" || out["token"] == "" {
		t.Fatalf("expected invited + token, got %v", out)
	}
}

// The invited user accepts their link → membership created; a mismatched email is denied.
func TestAcceptInviteEmailMustMatch(t *testing.T) {
	s, au, pr := membersServer(t)
	owner, _ := au.CreateUser("owner@example.com", "password123")
	if _, err := pr.Create(projects.Project{ID: "p1", OwnerID: owner.ID}); err != nil {
		t.Fatal(err)
	}
	inv, _ := pr.CreateInvite("p1", "invitee@example.com", projects.RoleEditor)

	// A different user (wrong email) cannot redeem it.
	wrong, _ := au.CreateUser("wrong@example.com", "password123")
	_, wrongSess, _ := au.Authenticate("wrong@example.com", "password123")
	_ = wrong
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/invites/"+inv.Token+"/accept", nil)
	req.SetPathValue("token", inv.Token)
	req.Header.Set("Authorization", "Bearer "+wrongSess.Token)
	s.acceptInvite(rec, req)
	if rec.Code != 403 {
		t.Fatalf("wrong-email accept status = %d, want 403", rec.Code)
	}

	// The right user (now registered) redeems it.
	invitee, _ := au.CreateUser("invitee@example.com", "password123")
	_, sess, _ := au.Authenticate("invitee@example.com", "password123")
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("POST", "/invites/"+inv.Token+"/accept", nil)
	req2.SetPathValue("token", inv.Token)
	req2.Header.Set("Authorization", "Bearer "+sess.Token)
	s.acceptInvite(rec2, req2)
	if rec2.Code != 200 {
		t.Fatalf("accept status = %d, want 200; body=%s", rec2.Code, rec2.Body.String())
	}
	if role, _ := pr.MemberRole("p1", invitee.ID); role != projects.RoleEditor {
		t.Fatalf("invitee role = %q, want editor", role)
	}
}

// A viewer is read-only: changing settings requires editor+ (write-gating).
func TestViewerCannotChangeSettings(t *testing.T) {
	s, au, pr := membersServer(t)
	owner, _ := au.CreateUser("owner@example.com", "password123")
	viewer, _ := au.CreateUser("viewer@example.com", "password123")
	if _, err := pr.Create(projects.Project{ID: "p1", OwnerID: owner.ID}); err != nil {
		t.Fatal(err)
	}
	if err := pr.AddMember("p1", viewer.ID, projects.RoleViewer); err != nil {
		t.Fatal(err)
	}
	// Viewer attempts to change settings → 403.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/projects/p1/settings", strings.NewReader(`{"merge_mode":"auto"}`))
	req.SetPathValue("id", "p1")
	req = req.WithContext(httpx.WithUserID(context.Background(), viewer.ID))
	s.putProjectSettings(rec, req)
	if rec.Code != 403 {
		t.Fatalf("viewer settings status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	// An editor on the same project may.
	editor, _ := au.CreateUser("editor@example.com", "password123")
	_ = pr.AddMember("p1", editor.ID, projects.RoleEditor)
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("PUT", "/projects/p1/settings", strings.NewReader(`{"merge_mode":"auto"}`))
	req2.SetPathValue("id", "p1")
	req2 = req2.WithContext(httpx.WithUserID(context.Background(), editor.ID))
	s.putProjectSettings(rec2, req2)
	if rec2.Code != 200 {
		t.Fatalf("editor settings status = %d, want 200; body=%s", rec2.Code, rec2.Body.String())
	}
}

// The project owner cannot be removed or demoted.
func TestOwnerProtected(t *testing.T) {
	s, au, pr := membersServer(t)
	owner, _ := au.CreateUser("owner@example.com", "password123")
	if _, err := pr.Create(projects.Project{ID: "p1", OwnerID: owner.ID}); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/projects/p1/members/"+owner.ID, nil)
	req.SetPathValue("id", "p1")
	req.SetPathValue("userId", owner.ID)
	req = req.WithContext(httpx.WithUserID(context.Background(), owner.ID))
	s.removeMember(rec, req)
	if rec.Code != 400 {
		t.Fatalf("remove-owner status = %d, want 400", rec.Code)
	}
}
