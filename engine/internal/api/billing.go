package api

import "net/http"

// projectEntitlement is the billing budget gate the orchestrator consults before
// firing a feature run (GET /projects/{id}/entitlement). FAIL-OPEN: an unowned/system
// project, missing billing, or an error returns allowed=true — a billing problem must
// never wedge the factory. A user caller is gated by project access.
func (s *Server) projectEntitlement(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.canAccessProject(r.Context(), id) {
		httpErr(w, http.StatusNotFound, "project not found: "+id)
		return
	}
	wsID, ok := s.workspaceForProject(id)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"allowed": true, "mode": "included", "reason": "no billing workspace for this project"})
		return
	}
	d, err := s.Billing.Entitlement(wsID)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"allowed": true, "reason": "billing error (fail-open): " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"allowed": d.Allowed, "mode": d.Mode, "reason": d.Reason})
}
