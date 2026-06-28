package api

import (
	"net/http"

	"forge/internal/projects"
)

// ---- project channels API (v1.3) --------------------------------------------
//
// Manage which external channels a project notifies (and receives commands from).
// Listing is any-member; linking/unlinking is editor+ (it's project config). The
// connector credential (bot token) is global config in settings, not here.

// listChannels handles GET /projects/{id}/channels.
func (s *Server) listChannels(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.canAccessProject(r.Context(), id) {
		httpErr(w, http.StatusNotFound, "project not found: "+id)
		return
	}
	chans, err := s.Projects.Channels(id)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if chans == nil {
		chans = []projects.Channel{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"channels": chans})
}

type linkChannelReq struct {
	Connector string `json:"connector"`
	Target    string `json:"target"`
	Events    string `json:"events"`
}

// linkChannel handles POST /projects/{id}/channels: editor+ links a connector+target
// (e.g. a Telegram chat id) to the project, optionally scoped to specific events.
func (s *Server) linkChannel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.requireRole(r.Context(), id, projects.RoleEditor) {
		httpErr(w, http.StatusForbidden, "managing channels requires editor or owner")
		return
	}
	var req linkChannelReq
	if !readJSON(w, r, &req) {
		return
	}
	if req.Connector == "" || req.Target == "" {
		httpErr(w, http.StatusBadRequest, "connector and target are required")
		return
	}
	if err := s.Projects.LinkChannel(id, req.Connector, req.Target, req.Events); err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"linked": true})
}

// unlinkChannel handles DELETE /projects/{id}/channels?connector=&target=.
func (s *Server) unlinkChannel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.requireRole(r.Context(), id, projects.RoleEditor) {
		httpErr(w, http.StatusForbidden, "managing channels requires editor or owner")
		return
	}
	connector := r.URL.Query().Get("connector")
	target := r.URL.Query().Get("target")
	if connector == "" || target == "" {
		httpErr(w, http.StatusBadRequest, "connector and target query params are required")
		return
	}
	if err := s.Projects.UnlinkChannel(id, connector, target); err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"unlinked": true})
}
