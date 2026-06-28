package projects_test

import (
	"testing"

	"forge/internal/projects"
)

// Creating a project records its owner as a member with role owner.
func TestCreateRecordsOwnerMember(t *testing.T) {
	st := openTemp(t)
	if _, err := st.Create(projects.Project{ID: "p1", OwnerID: "usr-a"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	role, err := st.MemberRole("p1", "usr-a")
	if err != nil {
		t.Fatalf("MemberRole: %v", err)
	}
	if role != projects.RoleOwner {
		t.Fatalf("owner role = %q, want %q", role, projects.RoleOwner)
	}
	members, err := st.Members("p1")
	if err != nil {
		t.Fatalf("Members: %v", err)
	}
	if len(members) != 1 || members[0].UserID != "usr-a" || members[0].Role != projects.RoleOwner {
		t.Fatalf("Members = %+v, want one owner usr-a", members)
	}
}

// A non-member gets "" and back-compat: a legacy owner_id with no members row is owner.
func TestMemberRoleNonMemberAndLegacyOwner(t *testing.T) {
	st := openTemp(t)
	if _, err := st.Create(projects.Project{ID: "p1", OwnerID: "usr-a"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	// stranger is not a member.
	if role, _ := st.MemberRole("p1", "usr-stranger"); role != "" {
		t.Fatalf("stranger role = %q, want empty", role)
	}
	// Simulate a legacy project: owner_id set but the members row removed.
	if err := st.RemoveMember("p1", "usr-a"); err != nil {
		t.Fatalf("RemoveMember: %v", err)
	}
	if role, _ := st.MemberRole("p1", "usr-a"); role != projects.RoleOwner {
		t.Fatalf("legacy owner role = %q, want owner", role)
	}
}

// AddMember adds editors/viewers and upserts the role on repeat.
func TestAddMemberUpsert(t *testing.T) {
	st := openTemp(t)
	if _, err := st.Create(projects.Project{ID: "p1", OwnerID: "usr-a"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := st.AddMember("p1", "usr-b", projects.RoleViewer); err != nil {
		t.Fatalf("AddMember viewer: %v", err)
	}
	if role, _ := st.MemberRole("p1", "usr-b"); role != projects.RoleViewer {
		t.Fatalf("usr-b role = %q, want viewer", role)
	}
	// Promote to editor — upsert, not a duplicate row.
	if err := st.AddMember("p1", "usr-b", projects.RoleEditor); err != nil {
		t.Fatalf("AddMember editor: %v", err)
	}
	if role, _ := st.MemberRole("p1", "usr-b"); role != projects.RoleEditor {
		t.Fatalf("usr-b role = %q, want editor after promote", role)
	}
	members, _ := st.Members("p1")
	if len(members) != 2 {
		t.Fatalf("members = %d, want 2 (owner + b)", len(members))
	}
}

func TestRoleAtLeast(t *testing.T) {
	cases := []struct {
		have, min string
		want      bool
	}{
		{projects.RoleOwner, projects.RoleViewer, true},
		{projects.RoleOwner, projects.RoleEditor, true},
		{projects.RoleOwner, projects.RoleOwner, true},
		{projects.RoleEditor, projects.RoleViewer, true},
		{projects.RoleEditor, projects.RoleOwner, false},
		{projects.RoleViewer, projects.RoleEditor, false},
		{projects.RoleViewer, projects.RoleViewer, true},
		{"", projects.RoleViewer, false},
		{"garbage", projects.RoleViewer, false},
	}
	for _, c := range cases {
		if got := projects.RoleAtLeast(c.have, c.min); got != c.want {
			t.Errorf("RoleAtLeast(%q,%q) = %v, want %v", c.have, c.min, got, c.want)
		}
	}
}
