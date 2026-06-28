package projects_test

import (
	"errors"
	"testing"

	"forge/internal/projects"
)

// An invite, once accepted by a user, creates the membership and is marked used;
// a second accept is rejected.
func TestInviteLifecycle(t *testing.T) {
	st := openTemp(t)
	if _, err := st.Create(projects.Project{ID: "p1", OwnerID: "usr-owner"}); err != nil {
		t.Fatal(err)
	}
	inv, err := st.CreateInvite("p1", "Invitee@Example.com", projects.RoleEditor)
	if err != nil {
		t.Fatalf("CreateInvite: %v", err)
	}
	if inv.Token == "" || inv.Email != "invitee@example.com" {
		t.Fatalf("invite = %+v, want token + normalized email", inv)
	}
	// Pending list shows it.
	pend, _ := st.PendingInvites("p1")
	if len(pend) != 1 {
		t.Fatalf("pending = %d, want 1", len(pend))
	}
	// Accept → membership created, role applied.
	if _, err := st.AcceptInvite(inv.Token, "usr-invitee"); err != nil {
		t.Fatalf("AcceptInvite: %v", err)
	}
	if role, _ := st.MemberRole("p1", "usr-invitee"); role != projects.RoleEditor {
		t.Fatalf("role after accept = %q, want editor", role)
	}
	// No longer pending.
	if pend, _ := st.PendingInvites("p1"); len(pend) != 0 {
		t.Fatalf("pending after accept = %d, want 0", len(pend))
	}
	// Second accept rejected.
	if _, err := st.AcceptInvite(inv.Token, "usr-invitee"); !errors.Is(err, projects.ErrInviteUsed) {
		t.Fatalf("second accept err = %v, want ErrInviteUsed", err)
	}
}

func TestGetInviteNotFound(t *testing.T) {
	st := openTemp(t)
	if _, err := st.GetInvite("nope"); !errors.Is(err, projects.ErrNotFound) {
		t.Fatalf("GetInvite(unknown) err = %v, want ErrNotFound", err)
	}
}

func TestRevokeInvite(t *testing.T) {
	st := openTemp(t)
	if _, err := st.Create(projects.Project{ID: "p1", OwnerID: "usr-owner"}); err != nil {
		t.Fatal(err)
	}
	inv, _ := st.CreateInvite("p1", "x@example.com", projects.RoleViewer)
	if err := st.RevokeInvite(inv.Token); err != nil {
		t.Fatalf("RevokeInvite: %v", err)
	}
	if _, err := st.GetInvite(inv.Token); !errors.Is(err, projects.ErrNotFound) {
		t.Fatalf("after revoke err = %v, want ErrNotFound", err)
	}
}
