package api

// Multi-tenant access control (audit D1). The project_id columns (A1) scoped the
// DATA, but the read/act-by-id routes never checked OWNERSHIP — any valid session
// could read or mutate another tenant's runs by passing ?project= or a run id.
// These helpers close that: a user session may only touch projects it owns; the
// service token (the orchestrator/system) is unrestricted.

import (
	"context"
	"net/http"

	"forge/internal/httpx"
	"forge/internal/projects"
)

// roleForProject resolves the caller's role on projectID: owner·editor·viewer for a
// member, or "" if they are not a member. A service-token caller (the orchestrator)
// is the system → owner. With no projects store, enforcement is off → owner. The
// second return is false only for a user session that is not a member.
func (s *Server) roleForProject(ctx context.Context, projectID string) (string, bool) {
	uid := httpx.UserIDFromContext(ctx)
	if uid == "" {
		return projects.RoleOwner, true // service token / system (orchestrator)
	}
	if s.Projects == nil {
		return projects.RoleOwner, true // no multi-tenant store → no enforcement
	}
	role, err := s.Projects.MemberRole(projectID, uid)
	if err != nil || role == "" {
		return "", false
	}
	return role, true
}

// canAccessProject reports whether the caller (identified on ctx) may access
// projectID. A service-token caller (UserIDFromContext == "") is the system and may
// access anything; a user session may access a project on which it holds ANY role
// (owner·editor·viewer). With no projects store configured, enforcement is off
// (single-tenant deployments).
func (s *Server) canAccessProject(ctx context.Context, projectID string) bool {
	_, ok := s.roleForProject(ctx, projectID)
	return ok
}

// requireRole reports whether the caller meets a minimum role on projectID (e.g.
// requireRole(ctx, id, projects.RoleEditor) for a write). Read-access already implies
// viewer; this gates writes (editor) and admin actions (owner).
func (s *Server) requireRole(ctx context.Context, projectID, min string) bool {
	role, ok := s.roleForProject(ctx, projectID)
	if !ok {
		return false
	}
	return projects.RoleAtLeast(role, min)
}

// runAccessible reports whether the caller may act on run id, by ownership of the
// run's project. False also when the run is missing — a caller learns nothing
// about runs they do not own (no existence leak: routes return 404, not 403).
func (s *Server) runAccessible(ctx context.Context, runID string) bool {
	run, err := s.Store.GetRun(runID)
	if err != nil {
		return false
	}
	return s.canAccessProject(ctx, run.ProjectID)
}

// crossTenantDenied guards a project-scoped LIST reader (tickets, metrics, sprints).
// For a user session asking for a project it does not own (or no project at all) it
// writes `empty` as a 200 and returns true — no cross-tenant data, no existence
// signal. A service-token caller (the orchestrator) is never denied.
func (s *Server) crossTenantDenied(w http.ResponseWriter, ctx context.Context, project string, empty any) bool {
	if httpx.UserIDFromContext(ctx) == "" {
		return false // service token / system
	}
	if project == "" || !s.canAccessProject(ctx, project) {
		writeJSON(w, 200, empty)
		return true
	}
	return false
}
