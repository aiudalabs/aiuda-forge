package api

// GitHub-projection wiring (F1 of the GitHub-native pivot): the manual sync
// endpoint the console can hit, and the SyncTrigger the webhook receiver
// (internal/ghapp) drives. Both funnel into conductor.Projector, which mirrors
// issue/PR state into the native ticket store — the store doubles as the
// console's projection cache, so the Tickets UI reads GitHub state unchanged.

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"forge/internal/projects"
)

// syncGitHub handles POST /projects/{id}/sync/github: mirror now, return the
// result. Any project member may trigger it (it's a read of GitHub, not a write).
func (s *Server) syncGitHub(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.requireRole(r.Context(), id, projects.RoleViewer) {
		httpErr(w, http.StatusForbidden, "syncing requires project membership")
		return
	}
	if s.Projector == nil {
		httpErr(w, http.StatusServiceUnavailable, "github projection not configured")
		return
	}
	p, err := s.Projects.Get(id)
	if err != nil {
		if errors.Is(err, projects.ErrNotFound) {
			httpErr(w, http.StatusNotFound, "project not found: "+id)
			return
		}
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if p.Repo == "" {
		httpErr(w, http.StatusBadRequest, "project has no repo")
		return
	}
	res, err := s.Projector.SyncProject(r.Context(), id, p.Repo)
	if err != nil {
		httpErr(w, http.StatusBadGateway, "github sync failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// webhookSync adapts webhook deliveries to projection syncs: a delivery names a
// repo; every project mirroring that repo gets synced. Implements
// ghapp.SyncTrigger; called on a goroutine by the receiver, so it may block.
type webhookSync struct{ s *Server }

func (t webhookSync) SyncRepo(repoURL string) {
	if t.s.Projector == nil || t.s.Projects == nil {
		return
	}
	all, err := t.s.Projects.List()
	if err != nil {
		log.Printf("webhookSync: list projects: %v", err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	for _, p := range all {
		if p.Repo != repoURL {
			continue
		}
		if res, err := t.s.Projector.SyncProject(ctx, p.ID, p.Repo); err != nil {
			log.Printf("webhookSync %s: %v", p.ID, err)
		} else if res.Changed > 0 {
			log.Printf("webhookSync %s: %d/%d stories actualizadas", p.ID, res.Changed, res.Mirrored)
		}
	}
}
