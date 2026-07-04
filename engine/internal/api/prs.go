package api

// Cola de PRs (F3): la bandeja diaria del humano — los PRs abiertos del repo
// del proyecto con sus stories ligadas y los workflow runs esperando su
// aprobación, más la acción de aprobar (con la política de seguridad SIEMPRE:
// un PR que toca .github/workflows/** no se auto-aprueba ni con click).

import (
	"context"
	"errors"
	"forge/internal/tickets"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"forge/internal/conductor"
	"forge/internal/projects"
)

type prView struct {
	Number             int              `json:"number"`
	Title              string           `json:"title"`
	URL                string           `json:"url"`
	Draft              bool             `json:"draft"`
	Author             string           `json:"author"`
	MergeState         string           `json:"merge_state"` // "conflicting" | "clean" | "" (desconocido)
	Stories            []string         `json:"stories"`
	ActionRequiredRuns []pendingRunView `json:"action_required_runs"`
}

// mergeState normaliza el enum `mergeable` de GitHub (MERGEABLE|CONFLICTING|
// UNKNOWN) al vocabulario de la consola. UNKNOWN (GitHub aún computa) → "".
func mergeState(mergeable string) string {
	switch mergeable {
	case "CONFLICTING":
		return "conflicting"
	case "MERGEABLE":
		return "clean"
	default:
		return ""
	}
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
	gh := s.ghFor(r.Context(), id)
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
			MergeState:         mergeState(pr.Mergeable),
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
	// Auto-consistencia de relojes (cazado por el usuario): la cola de PRs es
	// dato VIVO de GitHub, pero las stories son la proyección (tick de 60s).
	// Si una story in_review/running apunta a un PR que ya NO está abierto
	// (recién mergeado/cerrado), disparamos el sync aquí mismo para que ambas
	// superficies converjan en un ciclo de UI en vez de esperar al tick.
	if s.Projector != nil {
		open := map[int]bool{}
		for _, pr := range prs {
			open[pr.Number] = true
		}
		for _, st := range stories {
			if (st.Status == tickets.StatusInReview || st.Status == tickets.StatusRunning) && st.PRURL != "" {
				if n := prNumberFrom(st.PRURL); n > 0 && !open[n] {
					go func(pid, repo string) {
						ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
						defer cancel()
						_, _ = s.Projector.SyncProject(ctx, pid, repo)
					}(id, p.Repo)
					break // un sync cubre todo el proyecto
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"prs": views})
}

// prNumberFrom extrae el número de PR de su URL html.
func prNumberFrom(url string) int {
	i := strings.LastIndex(url, "/")
	if i < 0 {
		return 0
	}
	n, err := strconv.Atoi(url[i+1:])
	if err != nil {
		return 0
	}
	return n
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
	approver := &conductor.Approver{GH: s.ghFor(r.Context(), id)}
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

// POST /projects/{id}/prs/{number}/resolve-conflicts — despacha al canal
// claude_action un agente que resuelve el conflicto del PR contra main y pushea
// a la misma rama (incidente PR #83). El guard anti-loop del resolver responde
// 409 (sin re-despachar) si ya hay una resolución en vuelo para ese PR.
func (s *Server) resolvePRConflicts(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.requireRole(r.Context(), id, projects.RoleEditor) {
		httpErr(w, http.StatusForbidden, "resolving conflicts requires editor or owner")
		return
	}
	number, err := strconv.Atoi(r.PathValue("number"))
	if err != nil || number <= 0 {
		httpErr(w, http.StatusBadRequest, "invalid PR number")
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
	if s.Resolver == nil {
		httpErr(w, http.StatusServiceUnavailable, "conflict resolution is not configured")
		return
	}
	if err := s.Resolver.Resolve(r.Context(), id, p.Repo, number); err != nil {
		if errors.Is(err, conductor.ErrResolveInFlight) {
			writeJSON(w, http.StatusConflict, map[string]any{
				"dispatched": false,
				"reason":     "ya hay una resolución en curso para este PR — espera a que termine",
			})
			return
		}
		httpErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"dispatched": true})
}
