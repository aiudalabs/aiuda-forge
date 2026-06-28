package api

import (
	"net/http"

	"forge/internal/channels"
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

// testChannels handles POST /projects/{id}/channels/test: send a test message to all
// the project's linked channels THROUGH the real connector (same token/path the live
// delivery uses), so a user can verify the wiring end-to-end. Editor+ only.
func (s *Server) testChannels(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.requireRole(r.Context(), id, projects.RoleEditor) {
		httpErr(w, http.StatusForbidden, "testing channels requires editor or owner")
		return
	}
	if s.Channels == nil {
		httpErr(w, http.StatusServiceUnavailable, "channels not configured")
		return
	}
	chans, err := s.Projects.Channels(id)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	ev := channels.Event{
		Type:      "channel.test",
		ProjectID: id,
		Title:     "✅ Aiuda Factory · prueba de canal",
		Detail:    "Si ves esto, las notificaciones de este proyecto funcionan. 🚀",
	}
	var sent, failed int
	var firstErr string
	for _, ch := range chans {
		conn := s.Channels.Get(ch.Connector)
		if conn == nil {
			continue
		}
		if err := conn.Notify(r.Context(), ch.Target, ev); err != nil {
			failed++
			if firstErr == "" {
				firstErr = err.Error()
			}
			continue
		}
		sent++
	}
	writeJSON(w, http.StatusOK, map[string]any{"sent": sent, "failed": failed, "error": firstErr})
}
