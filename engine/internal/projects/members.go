package projects

import "database/sql"

// Roles a user can hold on a project. owner > editor > viewer.
const (
	RoleOwner  = "owner"
	RoleEditor = "editor"
	RoleViewer = "viewer"
)

var roleRank = map[string]int{RoleViewer: 1, RoleEditor: 2, RoleOwner: 3}

// ValidRole reports whether r is a known role.
func ValidRole(r string) bool { _, ok := roleRank[r]; return ok }

// RoleAtLeast reports whether `have` meets the minimum `min` (e.g. an editor meets
// "viewer", an owner meets "editor"). An unknown/empty `have` never meets anything.
func RoleAtLeast(have, min string) bool { return roleRank[have] >= roleRank[min] }

// Member is a user's membership on a project.
type Member struct {
	ProjectID string `json:"project_id"`
	UserID    string `json:"user_id"`
	Role      string `json:"role"`
	CreatedAt int64  `json:"created_at"`
}

// AddMember adds a member or updates their role (upsert).
func (s *Store) AddMember(projectID, userID, role string) error {
	_, err := s.db.Exec(
		`INSERT INTO project_members(project_id, user_id, role, created_at) VALUES(?,?,?,?)
		 ON CONFLICT(project_id, user_id) DO UPDATE SET role=excluded.role`,
		projectID, userID, role, s.now())
	return err
}

// MemberRole returns a user's role on a project, or "" if they are not a member. The
// project's legacy owner_id is always treated as owner even without a members row, so
// projects that predate this table keep working.
func (s *Store) MemberRole(projectID, userID string) (string, error) {
	if userID == "" {
		return "", nil
	}
	var role string
	err := s.db.QueryRow(`SELECT role FROM project_members WHERE project_id=? AND user_id=?`, projectID, userID).Scan(&role)
	if err == nil {
		return role, nil
	}
	if err != sql.ErrNoRows {
		return "", err
	}
	// Back-compat fallback: the project's owner_id is the owner.
	p, gerr := s.Get(projectID)
	if gerr == nil && p.OwnerID != "" && p.OwnerID == userID {
		return RoleOwner, nil
	}
	return "", nil
}

// Members lists a project's members (newest first).
func (s *Store) Members(projectID string) ([]Member, error) {
	rows, err := s.db.Query(`SELECT project_id, user_id, role, created_at FROM project_members WHERE project_id=? ORDER BY created_at ASC`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Member
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.ProjectID, &m.UserID, &m.Role, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// RemoveMember removes a member. Removing the owner is rejected by the caller (the
// owner is the billing/account anchor); this is the raw delete.
func (s *Store) RemoveMember(projectID, userID string) error {
	_, err := s.db.Exec(`DELETE FROM project_members WHERE project_id=? AND user_id=?`, projectID, userID)
	return err
}
