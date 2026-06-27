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
)

// canAccessProject reports whether the caller (identified on ctx) may access
// projectID. A service-token caller (UserIDFromContext == "") is the system and
// may access anything; a user session may access only a project it owns. With no
// projects store configured, enforcement is off (single-tenant deployments).
func (s *Server) canAccessProject(ctx context.Context, projectID string) bool {
	uid := httpx.UserIDFromContext(ctx)
	if uid == "" {
		return true // service token / system (orchestrator)
	}
	if s.Projects == nil {
		return true // no multi-tenant store → no enforcement
	}
	p, err := s.Projects.Get(projectID)
	if err != nil {
		return false
	}
	return p.OwnerID == uid
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
