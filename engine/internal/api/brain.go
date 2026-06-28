package api

import (
	"encoding/json"
	"net/http"
)

// needBrain guards the assistant routes — 503 until a Brain is wired (the control
// plane was started without ANTHROPIC_API_KEY).
func (s *Server) needBrain(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.Brain == nil {
			httpErr(w, http.StatusServiceUnavailable, "brain not configured (set ANTHROPIC_API_KEY)")
			return
		}
		h(w, r)
	}
}

// assistantSend starts/continues the project's Brain conversation with a user
// message. The turn runs async and streams over the WS bus; this returns the
// conversation id immediately.
func (s *Server) assistantSend(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.canAccessProject(r.Context(), id) {
		httpErr(w, http.StatusNotFound, "project not found: "+id)
		return
	}
	var req struct {
		Message string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Message == "" {
		httpErr(w, http.StatusBadRequest, "message required")
		return
	}
	// v1.1: access is owner-only (canAccessProject), so the caller is the owner.
	// The role string is the hook v1.2's owner/editor/viewer gating plugs into.
	convID, err := s.Brain.Send(id, "owner", req.Message)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"conversation_id": convID})
}

// assistantHistory returns the project's conversation messages for the UI.
func (s *Server) assistantHistory(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.canAccessProject(r.Context(), id) {
		httpErr(w, http.StatusNotFound, "project not found: "+id)
		return
	}
	msgs, convID, err := s.Brain.History(id)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if msgs == nil {
		msgs = []map[string]any{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"conversation_id": convID, "messages": msgs})
}

func (s *Server) assistantApprove(w http.ResponseWriter, r *http.Request) {
	s.assistantResolve(w, r, true)
}
func (s *Server) assistantReject(w http.ResponseWriter, r *http.Request) {
	s.assistantResolve(w, r, false)
}

// assistantResolve answers a proposed mutating action (approve/reject).
func (s *Server) assistantResolve(w http.ResponseWriter, r *http.Request, approved bool) {
	id, actionID := r.PathValue("id"), r.PathValue("actionId")
	if !s.canAccessProject(r.Context(), id) {
		httpErr(w, http.StatusNotFound, "project not found: "+id)
		return
	}
	if err := s.Brain.Resolve(actionID, approved); err != nil {
		httpErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"action": actionID, "approved": approved})
}
