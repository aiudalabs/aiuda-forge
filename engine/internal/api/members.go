package api

import (
	"errors"
	"net/http"

	"forge/internal/auth"
	"forge/internal/projects"
)

// ---- members & invitations API (v1.2 roles) ---------------------------------
//
// owner·editor·viewer membership per project. Reading the member list needs any
// role (canAccessProject); managing members (invite/role-change/remove) is owner
// -only (requireRole owner). Inviting an email that already has an account adds it
// immediately; an unknown email gets a pending invite + link the inviter shares.

// memberView is a member row projected with the user's email for display.
type memberView struct {
	UserID string `json:"user_id"`
	Email  string `json:"email"`
	Role   string `json:"role"`
}

// listMembers handles GET /projects/{id}/members: the project's members (with
// emails resolved) plus its pending invites. Any member may read.
func (s *Server) listMembers(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.canAccessProject(r.Context(), id) {
		httpErr(w, http.StatusNotFound, "project not found: "+id)
		return
	}
	members, err := s.Projects.Members(id)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	views := make([]memberView, 0, len(members))
	for _, m := range members {
		mv := memberView{UserID: m.UserID, Role: m.Role}
		if s.Auth != nil {
			if u, err := s.Auth.UserByID(m.UserID); err == nil {
				mv.Email = u.Email
			}
		}
		views = append(views, mv)
	}
	invites, err := s.Projects.PendingInvites(id)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if invites == nil {
		invites = []projects.Invite{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": views, "invites": invites})
}

type inviteReq struct {
	Email string `json:"email"`
	Role  string `json:"role"`
}

// inviteMember handles POST /projects/{id}/members: owner-only. An email with an
// existing account is added immediately (status "added"); an unknown email gets a
// pending invite + link (status "invited"). Roles are editor or viewer — ownership
// is not transferable via invite. Idempotent-ish: re-adding upserts the role.
func (s *Server) inviteMember(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.requireRole(r.Context(), id, projects.RoleOwner) {
		httpErr(w, http.StatusForbidden, "only the project owner can manage members")
		return
	}
	var req inviteReq
	if !readJSON(w, r, &req) {
		return
	}
	if req.Email == "" {
		httpErr(w, http.StatusBadRequest, "email is required")
		return
	}
	if req.Role != projects.RoleEditor && req.Role != projects.RoleViewer {
		httpErr(w, http.StatusBadRequest, "role must be editor or viewer")
		return
	}
	if s.Auth == nil {
		httpErr(w, http.StatusServiceUnavailable, "auth not configured")
		return
	}
	// Existing account → add now.
	if u, err := s.Auth.UserByEmail(req.Email); err == nil {
		if err := s.Projects.AddMember(id, u.ID, req.Role); err != nil {
			httpErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{
			"status": "added", "user_id": u.ID, "email": u.Email, "role": req.Role,
		})
		return
	} else if !errors.Is(err, auth.ErrNotFound) {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// No account yet → pending invite + link.
	inv, err := s.Projects.CreateInvite(id, req.Email, req.Role)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"status": "invited", "email": inv.Email, "role": inv.Role,
		"token": inv.Token, "invite_path": "/invite/" + inv.Token,
	})
}

type roleReq struct {
	Role string `json:"role"`
}

// updateMemberRole handles PUT /projects/{id}/members/{userId}: owner-only role
// change. The project owner's own row cannot be demoted (the account anchor).
func (s *Server) updateMemberRole(w http.ResponseWriter, r *http.Request) {
	id, userID := r.PathValue("id"), r.PathValue("userId")
	if !s.requireRole(r.Context(), id, projects.RoleOwner) {
		httpErr(w, http.StatusForbidden, "only the project owner can manage members")
		return
	}
	var req roleReq
	if !readJSON(w, r, &req) {
		return
	}
	if req.Role != projects.RoleEditor && req.Role != projects.RoleViewer {
		httpErr(w, http.StatusBadRequest, "role must be editor or viewer")
		return
	}
	if s.isProjectOwner(id, userID) {
		httpErr(w, http.StatusBadRequest, "cannot change the project owner's role")
		return
	}
	if err := s.Projects.AddMember(id, userID, req.Role); err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user_id": userID, "role": req.Role})
}

// removeMember handles DELETE /projects/{id}/members/{userId}: owner-only. The
// project owner cannot be removed.
func (s *Server) removeMember(w http.ResponseWriter, r *http.Request) {
	id, userID := r.PathValue("id"), r.PathValue("userId")
	if !s.requireRole(r.Context(), id, projects.RoleOwner) {
		httpErr(w, http.StatusForbidden, "only the project owner can manage members")
		return
	}
	if s.isProjectOwner(id, userID) {
		httpErr(w, http.StatusBadRequest, "cannot remove the project owner")
		return
	}
	if err := s.Projects.RemoveMember(id, userID); err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"removed": userID})
}

// acceptInvite handles POST /invites/{token}/accept: the invited user (a session is
// required) joins the project. The session user's email must match the invite (so a
// link can't be redeemed by a different account). The membership is created and the
// invite marked accepted.
func (s *Server) acceptInvite(w http.ResponseWriter, r *http.Request) {
	if s.Auth == nil {
		httpErr(w, http.StatusServiceUnavailable, "auth not configured")
		return
	}
	u, err := s.Auth.UserForToken(bearer(r))
	if err != nil {
		httpErr(w, http.StatusForbidden, "accepting an invite requires a user session")
		return
	}
	token := r.PathValue("token")
	inv, err := s.Projects.GetInvite(token)
	if err != nil {
		httpErr(w, http.StatusNotFound, "invite not found")
		return
	}
	if inv.Email != u.Email {
		httpErr(w, http.StatusForbidden, "this invite was issued to a different email")
		return
	}
	accepted, err := s.Projects.AcceptInvite(token, u.ID)
	if err != nil {
		if errors.Is(err, projects.ErrInviteUsed) {
			httpErr(w, http.StatusConflict, "invite already accepted")
			return
		}
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"project_id": accepted.ProjectID, "role": accepted.Role,
	})
}

// isProjectOwner reports whether userID is the project's anchor owner (its
// owner_id), which the member-management endpoints protect from demotion/removal.
func (s *Server) isProjectOwner(projectID, userID string) bool {
	p, err := s.Projects.Get(projectID)
	return err == nil && p.OwnerID != "" && p.OwnerID == userID
}
