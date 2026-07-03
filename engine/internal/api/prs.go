package api

// Cola de PRs (F3): la bandeja diaria del humano — los PRs abiertos del repo
// del proyecto con sus stories ligadas y los workflow runs esperando su
// aprobación, más la acción de aprobar (con la política de seguridad SIEMPRE:
// un PR que toca .github/workflows/** no se auto-aprueba ni con click).

import (
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"forge/internal/conductor"
	"forge/internal/github"
	"forge/internal/projects"
)

type prView struct {
	Number             int              `json:"number"`
	Title              string           `json:"title"`
	URL                string           `json:"url"`
	Draft              bool             `json:"draft"`
	Author             string           `json:"author"`
	Stories            []string         `json:"stories"`
	ActionRequiredRuns []pendingRunView `json:"action_required_runs"`
}

type pendingRunView struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// GET /projects/{id}/prs
func (s *Server) listProjectPRs(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.requireRole(r.Context(), id, projects.RoleViewer) {
		httpErr(w, http.StatusForbidden, "listing PRs requires project membership")
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
		writeJSON(w, http.StatusOK, map[string]any{"prs": []prView{}})
		return
	}
	gh := github.New()
	prs, err := gh.ListOpenPRsDetailed(r.Context(), p.Repo)
	if err != nil {
		httpErr(w, http.StatusBadGateway, "github: "+err.Error())
		return
	}
	pending, err := gh.ListActionRequiredRuns(r.Context(), p.Repo)
	if err != nil {
		pending = nil // best-effort: la cola sirve aunque este fetch falle
	}
	// runs pendientes por PR (vía los PR numbers que trae cada run)
	pendingByPR := map[int][]pendingRunView{}
	for _, run := range pending {
		for _, n := range run.PRNumbers {
			pendingByPR[n] = append(pendingByPR[n], pendingRunView{ID: run.ID, Name: run.Name})
		}
	}
	// stories ligadas: mención del id (\bS1-01\b) o external_ref por Closes —
	// reusamos el vocabulario de la proyección: id de story presente en title/body.
	stories, _ := s.Tickets.ListStoriesByProject(id)
	views := make([]prView, 0, len(prs))
	for _, pr := range prs {
		v := prView{
			Number: pr.Number, Title: pr.Title, URL: pr.URL, Draft: pr.Draft, Author: pr.Author,
			Stories:            []string{},
			ActionRequiredRuns: pendingByPR[pr.Number],
		}
		if v.ActionRequiredRuns == nil {
			v.ActionRequiredRuns = []pendingRunView{}
		}
		text := pr.Title + "\n" + pr.Body
		for _, st := range stories {
			if st.ExternalRef == "" {
				continue
			}
			if regexp.MustCompile(`\b` + regexp.QuoteMeta(st.ID) + `\b`).MatchString(text) {
				v.Stories = append(v.Stories, st.ID)
				continue
			}
			// Closes #N con el número del issue espejado
			if i := strings.LastIndex(st.ExternalRef, "#"); i >= 0 {
				if strings.Contains(text, "#"+st.ExternalRef[i+1:]) {
					v.Stories = append(v.Stories, st.ID)
				}
			}
		}
		views = append(views, v)
	}
	writeJSON(w, http.StatusOK, map[string]any{"prs": views})
}

// POST /projects/{id}/workflows/{runId}/approve — aprueba UN run pendiente con
// la política de seguridad aplicada aunque sea un humano quien clickea.
func (s *Server) approveWorkflowRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.requireRole(r.Context(), id, projects.RoleEditor) {
		httpErr(w, http.StatusForbidden, "approving workflows requires editor or owner")
		return
	}
	runID, err := strconv.ParseInt(r.PathValue("runId"), 10, 64)
	if err != nil {
		httpErr(w, http.StatusBadRequest, "invalid run id")
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
	approver := &conductor.Approver{GH: github.New()}
	ok, err := approver.ApproveOne(r.Context(), p.Repo, runID)
	if err != nil {
		httpErr(w, http.StatusBadGateway, err.Error())
		return
	}
	if !ok {
		writeJSON(w, http.StatusConflict, map[string]any{
			"approved": false, "safe": false,
			"reason": "el PR toca .github/workflows/** — la política de seguridad exige aprobación manual en GitHub",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"approved": true, "safe": true})
}
