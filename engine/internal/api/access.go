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

// memberProjects returns the set of project ids the ctx caller is a member of,
// and whether tenant filtering applies at all. It reports scoped=false for the
// service token (uid == "") and when no projects store is configured —
// enforcement off, exactly like roleForProject. A failed membership lookup
// filters everything out (fail-closed) rather than leaking cross-tenant data.
func (s *Server) memberProjects(ctx context.Context) (map[string]bool, bool) {
	uid := httpx.UserIDFromContext(ctx)
	if uid == "" || s.Projects == nil {
		return nil, false
	}
	projs, err := s.Projects.ListForMember(uid)
	if err != nil {
		return map[string]bool{}, true
	}
	allowed := make(map[string]bool, len(projs))
	for _, p := range projs {
		allowed[p.ID] = true
	}
	return allowed, true
}

// anyAllowed reports whether any of pids is in the allowed set.
func anyAllowed(pids []string, allowed map[string]bool) bool {
	for _, pid := range pids {
		if allowed[pid] {
			return true
		}
	}
	return false
}

// ticketDenied gates a per-id ticket route (story/epic) on its project: it
// writes a 404 — not 403, so a non-member learns nothing about ids it does not
// own (same no-existence-leak contract as the runs routes) — when the caller
// lacks the min role on projectID, and reports whether it denied. The service
// token is never denied (requireRole treats it as the system).
func (s *Server) ticketDenied(w http.ResponseWriter, ctx context.Context, projectID, min string) bool {
	if s.requireRole(ctx, projectID, min) {
		return false
	}
	httpErr(w, http.StatusNotFound, "not found")
	return true
}

// sprintDenied gates a per-id sprint mutation (C1). Sprints are keyed
// (id, project_id) and the sprint mutators update by id ACROSS projects, so a
// user session must hold the min role on every project containing that sprint
// id. 404 (not 403) — no existence leak. The service token is never denied.
func (s *Server) sprintDenied(w http.ResponseWriter, ctx context.Context, sprintID, min string) bool {
	if httpx.UserIDFromContext(ctx) == "" || s.Projects == nil {
		return false // service token / enforcement off
	}
	pids, err := s.Tickets.SprintProjects(sprintID)
	if err != nil || len(pids) == 0 {
		httpErr(w, http.StatusNotFound, "sprint not found: "+sprintID)
		return true
	}
	for _, pid := range pids {
		if !s.requireRole(ctx, pid, min) {
			httpErr(w, http.StatusNotFound, "sprint not found: "+sprintID)
			return true
		}
	}
	return false
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
