// Package api is the control plane: the HTTP API that fulfils the contract in
// docs/14_API_CONTRACT_v2.md (§A operations, §B events, §C internal) plus the
// event bus. GOLDEN RULE: every operation goes through here and every state
// transition emits an event (from the store, the single emit point). There is no
// privileged path — UI, Brain and CLI are equal clients.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"forge/internal/auth"
	"forge/internal/billing"
	"forge/internal/brain"
	"forge/internal/channels"
	"forge/internal/conductor"
	"forge/internal/ghapp"
	"forge/internal/httpx"
	"forge/internal/projects"
	"forge/internal/settings"
	"forge/internal/store"
	"forge/internal/tickets"
	"forge/internal/workflow"
)

// Server wires the store, the executor engine, the event bus, the registry,
// the settings store, the native ticket store, and the project store into one
// HTTP handler.
type Server struct {
	Store    *store.Store
	Engine   *workflow.Engine
	Bus      *Bus
	Registry *Registry
	Settings *settings.Store
	Tickets  *tickets.Store
	Projects *projects.Store // nil when ProjectsDB is not configured
	Auth     *auth.Store     // nil when AuthDB is not configured
	Brain    *brain.Brain    // nil when ANTHROPIC_API_KEY is not configured
	Billing  *billing.Store  // nil when billing is not configured
	// Channels is the connector registry, used by inbound webhooks to reply to a
	// channel (v1.3). Set by the app after construction; nil disables replies.
	Channels channels.Registry
	// Projector mirrors GitHub issue/PR state into the ticket store (F1 pivot).
	// nil disables the sync endpoint + webhook. GHWebhookSecret feeds the webhook
	// signature check, read per-request (hot-reloadable). Dispatcher fires ready
	// work to GitHub agents under the project's autonomy policy (F2).
	Projector  *conductor.Projector
	Dispatcher *conductor.Dispatcher
	// Resolver despacha resoluciones de conflicto de PR (claude_action). nil
	// deshabilita el endpoint POST /projects/{id}/prs/{number}/resolve-conflicts.
	Resolver        *conductor.ConflictResolver
	GHWebhookSecret func() string
	// PreviewsRoot is the directory the `release` step publishes static previews to;
	// GET /previews/{project_id}/{run_id}/... serves it. Empty disables the route.
	PreviewsRoot string
	// PreviewSecret is the HMAC key for minting preview capability tokens (must match
	// the AuthConfig.PreviewSecret the middleware verifies with). PreviewsBaseURL is
	// the public origin the minted preview URL is built on ("" → root-relative).
	PreviewSecret   []byte
	PreviewsBaseURL string
	linkCodes       *linkCodeStore // short-lived codes binding a channel user to an account
	mux             *http.ServeMux
	// exportMus serializa los exports a GitHub por proyecto (ver exportLock).
	exportMus sync.Map
}

// workspaceForProject resolves a project's billing workspace via its owner. Returns
// ok=false for an unowned/system project or when billing isn't wired — callers then
// skip billing (internal/demo runs never trip metering).
func (s *Server) workspaceForProject(projectID string) (string, bool) {
	if s.Billing == nil || s.Projects == nil || projectID == "" {
		return "", false
	}
	p, err := s.Projects.Get(projectID)
	if err != nil || p.OwnerID == "" {
		return "", false
	}
	ws, err := s.Billing.WorkspaceForOwner(p.OwnerID)
	if err != nil {
		return "", false
	}
	return ws.ID, true
}

// NewServer builds and routes a Server. The settings store lives next to the
// registry (registry/../settings.json) — config in the control plane, not the kernel.
// tix and proj may be nil; their routes return 503 until they are set
// (needTickets / needProjects guards).
func NewServer(st *store.Store, eng *workflow.Engine, bus *Bus, reg *Registry, tix *tickets.Store, proj *projects.Store, au *auth.Store) *Server {
	set, _ := settings.Open(filepath.Join(filepath.Dir(reg.Root), "settings.json"))
	s := &Server{Store: st, Engine: eng, Bus: bus, Registry: reg, Settings: set, Tickets: tix, Projects: proj, Auth: au, linkCodes: newLinkCodeStore(), mux: http.NewServeMux()}
	s.routes()
	return s
}

// ServeHTTP makes Server an http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

func (s *Server) routes() {
	m := s.mux
	// §A — operations the UI uses.
	m.HandleFunc("POST /runs", s.createRun)
	m.HandleFunc("GET /runs", s.listRuns)
	m.HandleFunc("GET /runs/{id}", s.getRun)
	m.HandleFunc("POST /runs/{id}/cancel", s.cancelRun)
	m.HandleFunc("DELETE /runs/{id}", s.deleteRun)
	m.HandleFunc("POST /runs/{id}/retry", s.retryRun)
	// Requeue (R2): resurrect a failed run's stories back to backlog so the
	// orchestrator re-fires. In sprint mode this requeues the WHOLE sprint.
	m.HandleFunc("POST /runs/{id}/requeue", s.needTickets(s.requeueRun))
	m.HandleFunc("GET /control/status", s.controlStatus)
	m.HandleFunc("POST /control/pause", s.pause)
	m.HandleFunc("POST /control/resume", s.resume)
	m.HandleFunc("POST /runs/{id}/steps/{step}/approve", s.approveStep)
	m.HandleFunc("POST /runs/{id}/steps/{step}/reject", s.rejectStep)
	m.HandleFunc("POST /runs/{id}/steps/{step}/answer", s.answerStep)
	m.HandleFunc("POST /runs/{id}/steps/{step}/merge", s.mergeStep)
	m.HandleFunc("POST /runs/{id}/steps/{step}/rerun", s.rerunStep)
	m.HandleFunc("GET /runs/{id}/artifacts/{kind}", s.artifacts)
	m.HandleFunc("GET /metrics", s.metrics)
	m.HandleFunc("GET /analytics", s.metrics)
	m.HandleFunc("GET /healthz", s.health)
	m.HandleFunc("GET /readyz", s.health)
	// Local auth (email+password). POST /auth/login and POST /auth/register are the
	// unauthenticated endpoints (exempted in the httpx.Auth middleware); logout/me
	// require a token.
	m.HandleFunc("POST /auth/login", s.needAuth(s.login))
	m.HandleFunc("POST /auth/register", s.needAuth(s.register))
	m.HandleFunc("POST /auth/logout", s.needAuth(s.logout))
	m.HandleFunc("GET /auth/me", s.needAuth(s.me))
	// Onboarding: what the current user still needs to self-serve (GitHub connected?).
	m.HandleFunc("GET /me/capabilities", s.capabilities)
	m.HandleFunc("POST /auth/change-password", s.needAuth(s.changePassword))
	// GitHub OAuth (onboarding Forja): setup de la App vía manifest + login.
	m.HandleFunc("GET /setup/github-app", s.setupGitHubApp)
	m.HandleFunc("GET /setup/github-app/callback", s.setupGitHubAppCallback)
	m.HandleFunc("GET /auth/github/start", s.githubAuthStart)
	m.HandleFunc("GET /auth/github/callback", s.githubAuthCallback)
	m.HandleFunc("GET /auth/github/status", s.githubAuthStatus)
	// registry CRUD (compose/edit/list/delete agents, skills, workflows — no-code).
	// Generic by {kind}: workflows|agents|skills. PUT/POST validate against the
	// SAME parser the kernel uses, so a saved manifest is always runnable.
	// Templates github-native: la especialización repo-baked (ver registry.go).
	m.HandleFunc("GET /registry/templates", s.listTemplates)
	m.HandleFunc("GET /registry/templates/file", s.getTemplate)
	m.HandleFunc("PUT /registry/templates/file", s.putTemplate)
	m.HandleFunc("GET /registry/{kind}", s.listRegistry)
	m.HandleFunc("GET /registry/{kind}/{id}", s.getRegistry)
	m.HandleFunc("PUT /registry/{kind}/{id}", s.putRegistry)
	m.HandleFunc("POST /registry/{kind}/{id}", s.putRegistry)
	m.HandleFunc("DELETE /registry/{kind}/{id}", s.delRegistry)
	// Persona sidecar for agents: the <id>.md file next to the <id>.yaml manifest.
	m.HandleFunc("GET /registry/agents/{id}/persona", s.getAgentPersona)
	// settings (MCP connections, agent auth, sandbox, merge policy) — secrets masked.
	m.HandleFunc("GET /settings", s.getSettings)
	m.HandleFunc("PUT /settings", s.putSettings)

	// §B — events.
	m.HandleFunc("GET /runs/{id}/events", s.events)
	m.HandleFunc("GET /ws", s.websocket)

	// Static previews published by the `release` step, served under /pv/{token}/…
	// The path-embedded preview token is the sole credential (the auth middleware
	// validates it; a session/service token is never accepted here) — untrusted repo
	// JS must not be able to replay a broad token. Guarded to 404 when unconfigured.
	m.HandleFunc("GET /pv/{token}/{path...}", s.servePreview)
	// Mint a short-lived, path-scoped preview token (session-authenticated, member-
	// gated) so the console opens a preview without a session token in the URL.
	m.HandleFunc("POST /projects/{id}/previews/{run}/token", s.needProjects(s.mintPreviewToken))

	// §C — daemon-internal worker↔kernel.
	m.HandleFunc("POST /runs/claim", s.claim)
	m.HandleFunc("POST /steps/{id}/report", s.report)
	m.HandleFunc("POST /steps/{id}/heartbeat", s.heartbeat)
	m.HandleFunc("POST /steps/{id}/usage", s.usage)

	// Project store. Guarded per-request — Projects may be nil (no ProjectsDB configured).
	m.HandleFunc("POST /projects", s.needProjects(s.createProject))
	m.HandleFunc("GET /projects", s.needProjects(s.listProjects))
	// Per-project settings (audit A2). Registered before "/projects/{id}" patterns
	// are not needed — Go's ServeMux matches the more specific pattern first.
	m.HandleFunc("GET /projects/{id}/settings", s.needProjects(s.getProjectSettings))
	m.HandleFunc("PUT /projects/{id}/settings", s.needProjects(s.putProjectSettings))
	m.HandleFunc("GET /projects/{id}", s.needProjects(s.getProject))
	// Studio = Confluence (U1): the project's specs read straight from its repo
	// docs/ (source of truth that outlives an ephemeral design run).
	m.HandleFunc("GET /projects/{id}/docs", s.needProjects(s.listProjectDocs))
	m.HandleFunc("GET /projects/{id}/docs/file", s.needProjects(s.getProjectDoc))
	m.HandleFunc("GET /projects/{id}/docs/history", s.needProjects(s.getProjectDocHistory))
	m.HandleFunc("GET /projects/{id}/design/log", s.needProjects(s.getProjectDesignLog))
	m.HandleFunc("GET /github/orgs", s.needProjects(s.githubOrgs))
	// Billing: the budget gate the orchestrator consults before firing, and the
	// workspace billing/health view the dashboard reads.
	m.HandleFunc("GET /projects/{id}/entitlement", s.needProjects(s.projectEntitlement))
	m.HandleFunc("GET /projects/{id}/billing", s.needProjects(s.projectBilling))
	// Members & invitations (v1.2 roles): list is any-member; manage is owner-only.
	m.HandleFunc("GET /projects/{id}/members", s.needProjects(s.listMembers))
	m.HandleFunc("POST /projects/{id}/members", s.needProjects(s.inviteMember))
	m.HandleFunc("PUT /projects/{id}/members/{userId}", s.needProjects(s.updateMemberRole))
	m.HandleFunc("DELETE /projects/{id}/members/{userId}", s.needProjects(s.removeMember))
	m.HandleFunc("POST /invites/{token}/accept", s.needProjects(s.acceptInvite))
	// Ticket import (v1.3): pull a source's issues into the backlog. Editor+ only.
	m.HandleFunc("POST /projects/{id}/import/github", s.needProjects(s.importGitHub))
	// Backlog export (F0 GitHub-native pivot): stories → issues + native blocked_by
	// deps. Editor+ only; synchronous (minutes for big backlogs); idempotent.
	m.HandleFunc("POST /projects/{id}/export/github", s.needProjects(s.exportGitHub))
	// GitHub projection (F1): manual sync + inbound webhook (public route, request
	// verified by X-Hub-Signature-256 — patrón del webhook de Telegram).
	m.HandleFunc("POST /projects/{id}/sync/github", s.needProjects(s.syncGitHub))
	// Dispatch (F2): ready-set del conductor + despacho a agentes de GitHub.
	m.HandleFunc("GET /projects/{id}/dispatch/candidates", s.needProjects(s.dispatchCandidates))
	m.HandleFunc("POST /projects/{id}/dispatch", s.needProjects(s.dispatchWork))
	// Scaffold (F2): hornear la especialización github-native en el repo del proyecto.
	m.HandleFunc("POST /projects/{id}/scaffold/github", s.needProjects(s.scaffoldGitHub))
	// Cola de PRs + aprobación segura de workflows (F3).
	m.HandleFunc("GET /projects/{id}/prs", s.needProjects(s.listProjectPRs))
	m.HandleFunc("POST /projects/{id}/prs/{number}/resolve-conflicts", s.needProjects(s.resolvePRConflicts))
	m.HandleFunc("GET /projects/{id}/executors", s.needProjects(s.listExecutors))
	m.HandleFunc("PUT /projects/{id}/secrets/claude", s.needProjects(s.setClaudeSecret))
	// Spend desde GitHub (F4): gasto medido del repo (Copilot/Actions/LFS) del ciclo.
	m.HandleFunc("GET /projects/{id}/spend/github", s.needProjects(s.githubSpend))
	m.HandleFunc("POST /projects/{id}/workflows/{runId}/approve", s.needProjects(s.approveWorkflowRun))
	secret := s.GHWebhookSecret
	if secret == nil {
		secret = func() string { return "" }
	}
	m.Handle("POST /webhooks/github", ghapp.WebhookHandler(secret, webhookSync{s}))
	// Channels (v1.3): issue a link code (authenticated) + the inbound Telegram
	// webhook (public, verified by the bot secret header).
	m.HandleFunc("POST /channels/{connector}/link-code", s.issueLinkCode)
	m.HandleFunc("POST /webhooks/telegram", s.telegramWebhook)
	// Per-project channel links (list any-member; manage editor+).
	m.HandleFunc("GET /projects/{id}/channels", s.needProjects(s.listChannels))
	m.HandleFunc("POST /projects/{id}/channels", s.needProjects(s.linkChannel))
	m.HandleFunc("DELETE /projects/{id}/channels", s.needProjects(s.unlinkChannel))
	m.HandleFunc("POST /projects/{id}/channels/test", s.needProjects(s.testChannels))
	// Brain — the per-project conversational assistant (needBrain → 503 if no key).
	m.HandleFunc("POST /projects/{id}/assistant", s.needProjects(s.needBrain(s.assistantSend)))
	m.HandleFunc("GET /projects/{id}/assistant/history", s.needProjects(s.needBrain(s.assistantHistory)))
	m.HandleFunc("POST /projects/{id}/assistant/actions/{actionId}/approve", s.needProjects(s.needBrain(s.assistantApprove)))
	m.HandleFunc("POST /projects/{id}/assistant/actions/{actionId}/reject", s.needProjects(s.needBrain(s.assistantReject)))

	// Native ticket store. Registered unconditionally and guarded per-request:
	// Tickets may be nil (no TicketsDB configured) — needTickets returns 503 in that case.
	m.HandleFunc("POST /epics", s.needTickets(s.createEpic))
	m.HandleFunc("GET /epics", s.needTickets(s.listEpics))
	m.HandleFunc("GET /epics/{id}", s.needTickets(s.getEpic))
	m.HandleFunc("POST /sprints", s.needTickets(s.createSprint))
	m.HandleFunc("GET /sprints", s.needTickets(s.listSprints))
	// Sprint-batched (goal-mode) endpoints the orchestrator polls. "/sprints/ready"
	// is a more specific pattern than "/sprints/{id}/..." so it routes correctly.
	m.HandleFunc("GET /sprints/ready", s.needTickets(s.readySprints))
	m.HandleFunc("GET /sprints/awaiting-review", s.needTickets(s.awaitingReviewSprints))
	m.HandleFunc("GET /sprints/awaiting-retro", s.needTickets(s.awaitingRetroSprints))
	m.HandleFunc("GET /sprints/{id}/stories", s.needTickets(s.sprintStories))
	m.HandleFunc("GET /sprints/{id}/telemetry", s.needTickets(s.sprintTelemetry))
	m.HandleFunc("POST /sprints/{id}/claim", s.needTickets(s.claimSprint))
	m.HandleFunc("POST /sprints/{id}/planning-run", s.needTickets(s.setSprintPlanningRun))
	m.HandleFunc("POST /sprints/{id}/review-run", s.needTickets(s.setSprintReviewRun))
	m.HandleFunc("POST /sprints/{id}/retro-run", s.needTickets(s.setSprintRetroRun))
	m.HandleFunc("PUT /sprints/{id}/status", s.needTickets(s.updateSprintStatus))
	m.HandleFunc("POST /sprints/{id}/requeue", s.needTickets(s.requeueSprint))
	m.HandleFunc("POST /sprints/{id}/cancel", s.needTickets(s.cancelSprint))
	m.HandleFunc("POST /stories/{id}/requeue", s.needTickets(s.requeueStory))
	m.HandleFunc("POST /stories", s.needTickets(s.createStory))
	m.HandleFunc("GET /stories", s.needTickets(s.listStoriesHandler))
	m.HandleFunc("GET /stories/{id}", s.needTickets(s.getStory))
	m.HandleFunc("DELETE /stories/{id}", s.needTickets(s.deleteStory))
	// PATCH edits fields/deps in place; the mid-sprint moves cancel/move/split each
	// have their own guarded action endpoint (mirroring /requeue).
	m.HandleFunc("PATCH /stories/{id}", s.needTickets(s.editStory))
	m.HandleFunc("PUT /stories/{id}/status", s.needTickets(s.updateStoryStatus))
	m.HandleFunc("POST /stories/{id}/claim", s.needTickets(s.claimStory))
	m.HandleFunc("POST /stories/{id}/cancel", s.needTickets(s.cancelStory))
	m.HandleFunc("POST /stories/{id}/move", s.needTickets(s.moveStory))
	m.HandleFunc("POST /stories/{id}/split", s.needTickets(s.splitStory))
	m.HandleFunc("POST /stories/{id}/deps", s.needTickets(s.addStoryDeps))
	// Enviar a GitHub por-story (F2): export idempotente síncrono con resultado real.
	m.HandleFunc("POST /stories/{id}/export", s.needTickets(s.exportStory))
	// GET /tickets — compat endpoint matching the orchestrator's shape so the
	// existing UI can read the native store unchanged.
	m.HandleFunc("GET /tickets", s.needTickets(s.ticketsCompat))
}

// needTickets guards a handler that requires the native ticket store, returning
// 503 if it has not been wired in.
func (s *Server) needTickets(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.Tickets == nil {
			httpErr(w, http.StatusServiceUnavailable, "ticket store not configured")
			return
		}
		h(w, r)
	}
}

// ---- §A handlers ------------------------------------------------------------

type createRunReq struct {
	Workflow string         `json:"workflow"`
	Payload  map[string]any `json:"payload"`
}

func (s *Server) createRun(w http.ResponseWriter, r *http.Request) {
	var req createRunReq
	if !readJSON(w, r, &req) {
		return
	}
	if req.Workflow == "" {
		httpErr(w, http.StatusBadRequest, "workflow is required")
		return
	}
	runID, err := s.Engine.StartRun(req.Workflow, req.Payload)
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	run, _ := s.Store.GetRun(runID)
	writeJSON(w, http.StatusCreated, run)
}

func (s *Server) listRuns(w http.ResponseWriter, r *http.Request) {
	status := store.Status(r.URL.Query().Get("status"))
	// ?project=<id> scopes the list to one project (audit A1); absent = all
	// projects (back-compat / service-token admin).
	project := r.URL.Query().Get("project")
	// Multi-tenant (D1): a user session is confined to the projects it can access.
	//   · ?project=<id> given → must be a member of it, else empty list (no leak).
	//   · no ?project=        → return runs across ALL projects the user is a member
	//     of, NOT an empty list. This powers the cross-project views (Studio's design
	//     list, Overview) that legitimately need every project at once without a leak.
	// The service token (orchestrator) is unrestricted.
	if uid := httpx.UserIDFromContext(r.Context()); uid != "" {
		if project != "" {
			if !s.canAccessProject(r.Context(), project) {
				writeJSON(w, http.StatusOK, map[string]any{"runs": []*store.Run{}})
				return
			}
		} else {
			runs, err := s.listRunsForMember(uid, status)
			if err != nil {
				httpErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"runs": runs})
			return
		}
	}
	runs, err := s.Store.ListRunsByProject(status, project)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if runs == nil {
		runs = []*store.Run{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": runs})
}

// listRunsForMember returns every run (optionally status-filtered) that belongs to
// a project the user is a member of — the tenant-safe answer to an unscoped /runs
// from a user session (it powers Studio's cross-project design list). Returns an
// empty (non-nil) slice if the projects store is absent or the user has no projects.
func (s *Server) listRunsForMember(userID string, status store.Status) ([]*store.Run, error) {
	if s.Projects == nil {
		return []*store.Run{}, nil
	}
	projs, err := s.Projects.ListForMember(userID)
	if err != nil {
		return nil, err
	}
	allowed := make(map[string]bool, len(projs))
	for _, p := range projs {
		allowed[p.ID] = true
	}
	all, err := s.Store.ListRunsByProject(status, "")
	if err != nil {
		return nil, err
	}
	out := make([]*store.Run, 0, len(all))
	for _, run := range all {
		if allowed[run.ProjectID] {
			out = append(out, run)
		}
	}
	return out, nil
}

// runView is a run plus its steps (tasks) — what GET /runs/{id} returns.
type runView struct {
	*store.Run
	Steps []*store.Task `json:"steps"`
}

func (s *Server) getRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	run, err := s.Store.GetRun(id)
	if err != nil {
		notFound(w, err)
		return
	}
	if !s.canAccessProject(r.Context(), run.ProjectID) {
		httpErr(w, http.StatusNotFound, "run not found: "+id) // 404, not 403 (no existence leak)
		return
	}
	if run.DeletedAt != 0 {
		// Soft-deleted (D4): gone to clients, but the row + tasks + events are
		// retained in the DB for audit and so dependents never dangle.
		httpErr(w, http.StatusNotFound, "run not found: "+id)
		return
	}
	tasks, _ := s.Store.TasksForRun(id)
	if tasks == nil {
		tasks = []*store.Task{}
	}
	writeJSON(w, http.StatusOK, runView{Run: run, Steps: tasks})
}

func (s *Server) cancelRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.runAccessible(r.Context(), id) {
		httpErr(w, http.StatusNotFound, "run not found: "+id)
		return
	}
	if err := s.Store.CancelRun(id); err != nil {
		notFound(w, err)
		return
	}
	run, _ := s.Store.GetRun(id)
	writeJSON(w, http.StatusOK, run)
}

func (s *Server) deleteRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.runAccessible(r.Context(), id) {
		httpErr(w, http.StatusNotFound, "run not found: "+id)
		return
	}
	if err := s.Store.DeleteRun(id); err != nil {
		notFound(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": id})
}

func (s *Server) retryRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.runAccessible(r.Context(), id) {
		httpErr(w, http.StatusNotFound, "run not found: "+id)
		return
	}
	if err := s.Engine.RetryRun(id); err != nil {
		notFound(w, err)
		return
	}
	run, _ := s.Store.GetRun(id)
	writeJSON(w, http.StatusOK, run)
}

// requeueRun (R2) resurrects a failed run's stories back to backlog so the
// orchestrator re-fires them. Because a goal-mode sprint's stories share the
// run_id, this requeues the WHOLE sprint; in story mode it requeues the one story.
func (s *Server) requeueRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.runAccessible(r.Context(), id) {
		httpErr(w, http.StatusNotFound, "run not found: "+id)
		return
	}
	n, err := s.Tickets.RequeueByRun(id)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"requeued": n})
}

// requeueVerdict is the outcome of attempting to requeue ONE story. It lets the
// single-story and sprint handlers share the same validation while shaping their
// own response (a blocked story is a 409 for the single endpoint but a reported
// "skipped" entry for the sprint sweep).
type requeueVerdict int

const (
	reqDone    requeueVerdict = iota // failed → backlog applied
	reqNoop                          // already in backlog: idempotent no-op
	reqBlocked                       // not requeueable (wrong state, or live execution)
)

// requeueStoryUnit validates and, when legal, requeues a single story failed→backlog.
// It is the shared unit behind POST /stories/{id}/requeue and requeueSprint. Only a
// `failed` story is requeueable; `backlog` is an idempotent no-op (pressing the
// button twice must not error or double-fire); any other state (running/in_review/
// done) or a still-live execution is blocked with a human-readable reason. Callers
// have already authorized the mutation on st.ProjectID.
func (s *Server) requeueStoryUnit(st tickets.Story) (requeueVerdict, string, error) {
	switch st.Status {
	case tickets.StatusBacklog:
		// The conductor already auto-recovered a lost-agent story to backlog; the
		// operator's requeue click ACKNOWLEDGES it — clear the "agente perdido"
		// badge so it returns to a clean, dispatchable backlog. Only a mirrored,
		// still-flagged story acts; a plain backlog story stays an idempotent no-op.
		if st.ExternalRef != "" && st.AgentLost != "" {
			if err := s.Tickets.MarkAgentLost(st.ProjectID, st.ID, ""); err != nil {
				return reqBlocked, "", err
			}
			return reqDone, "", nil
		}
		return reqNoop, "la story ya está en el backlog", nil
	case tickets.StatusFailed:
		// requeueable — fall through to the live-execution guard.
	default:
		return reqBlocked, fmt.Sprintf("la story está en %s; solo se pueden reencolar stories en failed", st.Status), nil
	}
	if reason := s.liveExecutionReason(st); reason != "" {
		return reqBlocked, reason, nil
	}
	// Mirrored stories go through SyncExternalStatus (it also clears the agent
	// session so the projection won't re-anchor the story to running); legacy,
	// non-mirrored stories use the guarded single-story requeue.
	if st.ExternalRef != "" {
		if _, err := s.Tickets.SyncExternalStatus(st.ID, tickets.StatusBacklog, ""); err != nil {
			return reqBlocked, "", err
		}
		return reqDone, "", nil
	}
	changed, err := s.Tickets.RequeueStory(st.ProjectID, st.ID)
	if err != nil {
		return reqBlocked, "", err
	}
	if !changed {
		// Lost a race: the story left `failed` between our read and this write.
		return reqBlocked, "la story cambió de estado; recarga e inténtalo de nuevo", nil
	}
	return reqDone, "", nil
}

// liveExecutionReason returns a human-readable reason when the story looks like it is
// still executing, or "" when it is safe to requeue. It uses only CHEAP LOCAL signals
// — it never calls GitHub on the request path.
//
// Reliable local signal: a legacy factory story whose run_id points to a store run
// still QUEUED/RUNNING (the R3 desync — a story got marked failed while the kernel run
// driving it actually revived). Requeueing then would race a live run and orphan its PR.
//
// LIMIT: a GitHub-native story (external_ref, empty run_id) dispatched to a
// claude_action workflow has NO cheap local liveness signal on THIS request path — its
// state lives on GitHub. But it self-corrects via the projection: the claude.yml workflow
// carries an `agent:running` label for the run's whole life (put at start, removed in its
// if:always() step), so a wrongly-requeued but still-live story is re-derived to running
// on the next projection tick, and a genuinely-dead one stays in backlog. Requeue here is
// thus safe on operator judgement (it clears the session; the label — not the session —
// is now what anchors a live claude_action run). The Copilot-task sweep is unchanged.
func (s *Server) liveExecutionReason(st tickets.Story) string {
	if st.RunID == "" || s.Store == nil {
		return ""
	}
	run, err := s.Store.GetRun(st.RunID)
	if err != nil || run == nil {
		return "" // unknown/legacy run record — don't block on a missing row
	}
	if run.Status == store.StatusRunning || run.Status == store.StatusQueued {
		return fmt.Sprintf("su run %s sigue activo (%s); espera a que termine o cancélalo antes de reencolar", st.RunID, run.Status)
	}
	return ""
}

// requeueSprint (R2) reencola TODAS las stories `failed` de un sprint, aplicando la
// misma validación por story que el endpoint individual. Devuelve las ids reencoladas
// y las saltadas con su razón (una story running/in_review/done o con run vivo NO se
// toca) — el sprint mixto es lo normal, así que las saltadas se reportan, no fallan.
func (s *Server) requeueSprint(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	// C1: requeueing mutates the sprint's stories — a user session must be
	// editor on the sprint's project(s); 404 for outsiders (no existence leak).
	if s.sprintDenied(w, r.Context(), id, projects.RoleEditor) {
		return
	}
	// sprintDenied verified editor on every project holding this sprint id, so the
	// unscoped enumeration (across those projects) reveals nothing unauthorized.
	stories, err := s.Tickets.StoriesBySprint(id)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	requeued := []string{}
	skipped := []map[string]string{}
	for _, st := range stories {
		verdict, reason, err := s.requeueStoryUnit(st)
		if err != nil {
			httpErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		switch verdict {
		case reqDone:
			requeued = append(requeued, st.ID)
		case reqBlocked:
			skipped = append(skipped, map[string]string{"id": st.ID, "reason": reason})
		}
		// reqNoop (already backlog) is neither requeued nor a problem — omit silently.
	}
	writeJSON(w, http.StatusOK, map[string]any{"requeued": requeued, "skipped": skipped})
}

// requeueStory (R2 + pivote F2) devuelve UNA story al backlog para que el dispatch la
// re-sirva. Valida el estado: solo una story `failed` se reencola; `backlog` es un
// no-op idempotente (200, requeued:false); cualquier otro estado o una ejecución viva
// da 409 con la razón. Para stories espejadas en GitHub va por SyncExternalStatus (que
// además limpia la sesión de agente ligada); las legacy usan RequeueStory.
func (s *Server) requeueStory(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	// C1: requeueing mutates the story — editor on its project or 404 (no leak).
	st, err := s.storyForRoute(r, id)
	if err != nil {
		ticketNotFound(w, err)
		return
	}
	if s.ticketDenied(w, r.Context(), st.ProjectID, projects.RoleEditor) {
		return
	}
	verdict, reason, err := s.requeueStoryUnit(st)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	switch verdict {
	case reqNoop:
		writeJSON(w, http.StatusOK, map[string]any{"requeued": false, "reason": reason})
	case reqBlocked:
		httpErr(w, http.StatusConflict, reason)
	default:
		writeJSON(w, http.StatusOK, map[string]any{"requeued": true})
	}
}

// ---- Mid-sprint "team-move" operations (cancel/move/split/edit) -------------
// These mirror the requeue handlers: read+authorize the story, then delegate to the
// guarded store operation. Every state change still flows through the tickets state
// machine (the single point that guards a story's status), so the API stays a thin,
// non-privileged client (kernel constitution §4/§5).

// cancelVerdict is the outcome of attempting to cancel ONE story — the cancel analogue
// of requeueVerdict, so the single-story and sprint handlers share one validation.
type cancelVerdict int

const (
	cancelDone    cancelVerdict = iota // → cancelled applied
	cancelNoop                         // already cancelled: idempotent no-op
	cancelBlocked                      // running (cancel its run first) or terminal done
)

// cancelStoryUnit validates and, when legal, cancels a single story. Only a
// backlog/failed/in_review story is cancellable; `cancelled` is an idempotent no-op
// (pressing the button twice must not error); a `running` story is blocked (its run
// must be cancelled first — that parks it failed, from where cancel is legal); a `done`
// story is blocked (shipped work is not cancelled). Callers have already authorized the
// mutation on st.ProjectID.
func (s *Server) cancelStoryUnit(st tickets.Story) (cancelVerdict, string, error) {
	switch st.Status {
	case tickets.StatusCancelled:
		return cancelNoop, "la story ya está cancelada", nil
	case tickets.StatusRunning:
		return cancelBlocked, "la story está corriendo; cancela primero su run y luego cancélala", nil
	case tickets.StatusDone:
		return cancelBlocked, "la story está en done; no se puede cancelar trabajo ya entregado", nil
	}
	if err := s.Tickets.CancelStory(st.ProjectID, st.ID); err != nil {
		if errors.Is(err, tickets.ErrIllegalTransition) {
			// Raced out of a cancellable state between our read and this write.
			return cancelBlocked, "la story cambió de estado; recarga e inténtalo de nuevo", nil
		}
		return cancelBlocked, "", err
	}
	return cancelDone, "", nil
}

// cancelStory handles POST /stories/{id}/cancel. Editor+ on the story's project.
func (s *Server) cancelStory(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	st, err := s.storyForRoute(r, id)
	if err != nil {
		ticketNotFound(w, err)
		return
	}
	if s.ticketDenied(w, r.Context(), st.ProjectID, projects.RoleEditor) {
		return
	}
	verdict, reason, err := s.cancelStoryUnit(st)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	switch verdict {
	case cancelNoop:
		writeJSON(w, http.StatusOK, map[string]any{"cancelled": false, "reason": reason})
	case cancelBlocked:
		httpErr(w, http.StatusConflict, reason)
	default:
		writeJSON(w, http.StatusOK, map[string]any{"cancelled": true})
	}
}

// cancelSprint handles POST /sprints/{id}/cancel — cancels every one of a sprint's
// non-running, non-terminal stories, reporting which were cancelled and which were
// skipped (a running/done story is left untouched, mirroring requeueSprint).
func (s *Server) cancelSprint(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.sprintDenied(w, r.Context(), id, projects.RoleEditor) {
		return
	}
	stories, err := s.Tickets.StoriesBySprint(id)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	cancelled := []string{}
	skipped := []map[string]string{}
	for _, st := range stories {
		verdict, reason, err := s.cancelStoryUnit(st)
		if err != nil {
			httpErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		switch verdict {
		case cancelDone:
			cancelled = append(cancelled, st.ID)
		case cancelBlocked:
			skipped = append(skipped, map[string]string{"id": st.ID, "reason": reason})
		}
		// cancelNoop (already cancelled) is neither cancelled nor a problem — omit.
	}
	writeJSON(w, http.StatusOK, map[string]any{"cancelled": cancelled, "skipped": skipped})
}

type moveStoryReq struct {
	SprintID string `json:"sprint_id"`
}

// moveStory handles POST /stories/{id}/move — reassigns a story to another sprint.
// The store rejects a move that breaks dependency ordering (409) or leaves the
// planning-mutable states (409); an unknown target sprint is 404. Editor+.
func (s *Server) moveStory(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	st, err := s.storyForRoute(r, id)
	if err != nil {
		ticketNotFound(w, err)
		return
	}
	if s.ticketDenied(w, r.Context(), st.ProjectID, projects.RoleEditor) {
		return
	}
	var req moveStoryReq
	if !readJSON(w, r, &req) {
		return
	}
	if err := s.Tickets.MoveStory(st.ProjectID, id, req.SprintID); err != nil {
		switch {
		case errors.Is(err, tickets.ErrNotFound):
			httpErr(w, http.StatusNotFound, err.Error())
		case errors.Is(err, tickets.ErrInvalidState), errors.Is(err, tickets.ErrDepOrder):
			httpErr(w, http.StatusConflict, err.Error())
		default:
			httpErr(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	got, err := s.Tickets.GetStoryInProject(st.ProjectID, id)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, got)
}

type splitStoryReq struct {
	Parts []tickets.StoryDraft `json:"parts"`
}

// splitStory handles POST /stories/{id}/split — replaces a story with N new ones and
// cancels the original. Bad input (too few parts, id collision) is 400; a story not in
// backlog/failed is 409. Editor+.
func (s *Server) splitStory(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	st, err := s.storyForRoute(r, id)
	if err != nil {
		ticketNotFound(w, err)
		return
	}
	if s.ticketDenied(w, r.Context(), st.ProjectID, projects.RoleEditor) {
		return
	}
	var req splitStoryReq
	if !readJSON(w, r, &req) {
		return
	}
	ids, err := s.Tickets.SplitStory(st.ProjectID, id, req.Parts)
	if err != nil {
		switch {
		case errors.Is(err, tickets.ErrInvalidInput):
			httpErr(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, tickets.ErrInvalidState):
			httpErr(w, http.StatusConflict, err.Error())
		case errors.Is(err, tickets.ErrNotFound):
			httpErr(w, http.StatusNotFound, err.Error())
		default:
			httpErr(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"cancelled": id, "parts": ids})
}

// editStory handles PATCH /stories/{id} — a sparse edit of title/body/acceptance/
// owner/deps. A dep replacement that is malformed (self-dep, missing dep, cycle) is
// 400; editing a running/terminal story is 409. Editor+.
func (s *Server) editStory(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	st, err := s.storyForRoute(r, id)
	if err != nil {
		ticketNotFound(w, err)
		return
	}
	if s.ticketDenied(w, r.Context(), st.ProjectID, projects.RoleEditor) {
		return
	}
	var patch tickets.StoryPatch
	if !readJSON(w, r, &patch) {
		return
	}
	if err := s.Tickets.EditStory(st.ProjectID, id, patch); err != nil {
		if depError(w, err) {
			return
		}
		switch {
		case errors.Is(err, tickets.ErrInvalidState):
			httpErr(w, http.StatusConflict, err.Error())
		case errors.Is(err, tickets.ErrNotFound):
			httpErr(w, http.StatusNotFound, err.Error())
		default:
			httpErr(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	got, err := s.Tickets.GetStoryInProject(st.ProjectID, id)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, got)
}

func (s *Server) controlStatus(w http.ResponseWriter, r *http.Request) {
	// paused_until > 0 means the pause is the credit circuit-breaker (auto-resume at
	// that unix-millis time); 0 with paused=true means a manual/indefinite pause.
	writeJSON(w, http.StatusOK, map[string]any{
		"paused":       s.Engine.IsPaused(),
		"paused_until": s.Engine.PausedUntil(),
	})
}

func (s *Server) pause(w http.ResponseWriter, r *http.Request) {
	s.Engine.Pause()
	writeJSON(w, http.StatusOK, map[string]any{"paused": true})
}

func (s *Server) resume(w http.ResponseWriter, r *http.Request) {
	s.Engine.Resume()
	writeJSON(w, http.StatusOK, map[string]any{"paused": false})
}

func (s *Server) approveStep(w http.ResponseWriter, r *http.Request) {
	id, step := r.PathValue("id"), r.PathValue("step")
	if !s.runAccessible(r.Context(), id) {
		httpErr(w, http.StatusNotFound, "run not found: "+id)
		return
	}
	if err := s.Engine.ApproveStep(id, step); err != nil {
		httpErr(w, http.StatusConflict, err.Error())
		return
	}
	// Persist the just-approved design docs to the repo's dev branch so the
	// repo-backed Especificación view reflects approved specs immediately — not only
	// after the final docs PR. Best effort: the gate is already approved, so a commit
	// failure must never turn the approval into an error (it is logged, not returned).
	s.persistDesignDocs(r.Context(), id, step)
	writeJSON(w, http.StatusOK, map[string]any{"approved": step, "run": id})
}

// persistDesignDocs commits the design run's docs/ tree to the project repo's `dev`
// branch when a design phase gate is approved. It walks the run workspace's docs/
// directory and upserts each file via the GitHub contents API (WriteFile skips
// unchanged files), so each approval incrementally publishes exactly the approved
// specs. No-op for non-design runs, non-gate steps, or when the projects store /
// repo is absent. Errors are logged, never surfaced — the workflow already advanced.
func (s *Server) persistDesignDocs(ctx context.Context, runID, step string) {
	if s.Projects == nil || s.Engine == nil || !strings.HasSuffix(step, "_gate") {
		return
	}
	run, err := s.Store.GetRun(runID)
	if err != nil || run == nil || (run.WorkflowID != "design" && run.WorkflowID != "iterate") {
		return
	}
	proj, err := s.Projects.Get(run.ProjectID)
	if err != nil || proj.Repo == "" {
		return
	}
	root := s.Engine.Workdir(runID)
	docsDir := filepath.Join(root, "docs")
	gh := s.ghFor(ctx, proj.ID)
	// The per-project `design` branch is the APPROVED design history: every gate
	// approval (initial phase, re-run, or change-request) commits its docs here, so
	// `git log` on `design` is the full design evolution — separate from the code/
	// implementation history on main. `main` gets the released spec via the docs PR.
	const designBranch = "design"
	if err := gh.EnsureBranch(ctx, proj.Repo, designBranch, "main"); err != nil {
		log.Printf("persistDesignDocs %s ensure branch: %v", runID, err)
		return
	}
	err = filepath.WalkDir(docsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path) // e.g. docs/PRD.md, docs/mockups/index.html
		if err != nil {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		repoPath := filepath.ToSlash(rel)
		msg := "design: publish " + repoPath + " (" + step + " approved)"
		if _, err := gh.WriteFile(ctx, proj.Repo, designBranch, repoPath, string(content), msg); err != nil {
			log.Printf("persistDesignDocs %s %s: %v", runID, repoPath, err)
		}
		return nil
	})
	if err != nil {
		log.Printf("persistDesignDocs %s walk: %v", runID, err)
	}
}

type rejectReq struct {
	Reason string `json:"reason"`
}

func (s *Server) rejectStep(w http.ResponseWriter, r *http.Request) {
	id, step := r.PathValue("id"), r.PathValue("step")
	if !s.runAccessible(r.Context(), id) {
		httpErr(w, http.StatusNotFound, "run not found: "+id)
		return
	}
	var req rejectReq
	if !readJSON(w, r, &req) {
		return
	}
	if err := s.Engine.RejectStep(id, step, req.Reason); err != nil {
		httpErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rejected": step, "run": id, "reason": req.Reason})
}

type answerReq struct {
	Text string `json:"text"`
}

// answerStep is the THIRD gate verb (next to approve/reject): the human answers the
// phase's open questions. The phase re-runs incorporating the answers into the
// existing doc and re-parks at the same gate — it is NOT a rejection and does NOT
// consume the reject on_fail budget (see Engine.AnswerStep). Empty text is a 400;
// nothing awaiting / no on_fail.goto / answer cap reached are 409s.
func (s *Server) answerStep(w http.ResponseWriter, r *http.Request) {
	id, step := r.PathValue("id"), r.PathValue("step")
	if !s.runAccessible(r.Context(), id) {
		httpErr(w, http.StatusNotFound, "run not found: "+id)
		return
	}
	var req answerReq
	if !readJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Text) == "" {
		httpErr(w, http.StatusBadRequest, "answer text is required")
		return
	}
	if err := s.Engine.AnswerStep(id, step, req.Text); err != nil {
		switch {
		case errors.Is(err, workflow.ErrNoAwaitingStep),
			errors.Is(err, workflow.ErrNoAnswerTarget),
			errors.Is(err, workflow.ErrAnswerCapReached):
			httpErr(w, http.StatusConflict, err.Error())
		default:
			httpErr(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"answered": step, "run": id})
}

func (s *Server) mergeStep(w http.ResponseWriter, r *http.Request) {
	id, step := r.PathValue("id"), r.PathValue("step")
	if !s.runAccessible(r.Context(), id) {
		httpErr(w, http.StatusNotFound, "run not found: "+id)
		return
	}
	// In the MVP the pr step already commits/pushes (PR_MODE=local); merge is an
	// explicit ack endpoint so the contract surface exists and is exercisable.
	writeJSON(w, http.StatusOK, map[string]any{"merged": step, "run": id})
}

// rerunStep re-runs a SINGLE step of an existing run in place (reusing its workdir,
// no cascade to downstream) — e.g. regenerate `mockups` after switching the designer
// to Opus, without re-running the whole design or re-publishing to GitHub.
func (s *Server) rerunStep(w http.ResponseWriter, r *http.Request) {
	id, step := r.PathValue("id"), r.PathValue("step")
	if !s.runAccessible(r.Context(), id) {
		httpErr(w, http.StatusNotFound, "run not found: "+id)
		return
	}
	// Optional body {"feedback": "..."} — the conversational refine; injected into the
	// phase so the persona addresses it. Empty body = a plain regenerate.
	var req struct {
		Feedback string `json:"feedback"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if err := s.Engine.RerunStep(id, step, req.Feedback); err != nil {
		httpErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rerun": step, "run": id})
}

func (s *Server) artifacts(w http.ResponseWriter, r *http.Request) {
	id, kind := r.PathValue("id"), r.PathValue("kind")
	if !s.runAccessible(r.Context(), id) {
		httpErr(w, http.StatusNotFound, "run not found: "+id)
		return
	}
	tasks, err := s.Store.TasksForRun(id)
	if err != nil {
		notFound(w, err)
		return
	}
	// Pick the LATEST instance of this step, preferring a DONE one (#18). A retry
	// leaves an older FAILED task plus a newer DONE; tasks come back created_at ASC,
	// so returning the first match would serve the stale/empty FAILED artifact even
	// though the phase re-completed. Iterate keeping the best: a DONE always beats a
	// non-DONE, and among equals the later (more recent) wins.
	var best *store.Task
	for _, t := range tasks {
		if t.StepID != kind {
			continue
		}
		if best == nil || t.Status == store.StatusDone || best.Status != store.StatusDone {
			best = t
		}
	}
	if best == nil {
		httpErr(w, http.StatusNotFound, "no artifact for step "+kind)
		return
	}
	var result map[string]any
	_ = json.Unmarshal([]byte(best.Result), &result)
	writeJSON(w, http.StatusOK, map[string]any{"run": id, "kind": kind, "result": result})
}

func (s *Server) metrics(w http.ResponseWriter, r *http.Request) {
	// ?project=<id> scopes the metrics to one project (absent = all projects).
	project := r.URL.Query().Get("project")
	if s.crossTenantDenied(w, r.Context(), project, map[string]any{
		"runs_total": 0, "by_status": map[string]int{}, "total_cost_usd": 0,
		"cost_by_workflow": map[string]float64{}, "cost_by_step": map[string]float64{}, "acceptance_rate": 0,
	}) {
		return // D1: a user session may only read its own project's spend
	}
	runs, _ := s.Store.ListRunsByProject("", project)
	byStatus := map[string]int{}
	costByWorkflow := map[string]float64{}
	costByStep := map[string]float64{}
	var totalCost float64
	var tokensIn, tokensOut, turns, agentCalls int
	done := 0
	for _, run := range runs {
		byStatus[string(run.Status)]++
		if run.Status == store.StatusDone {
			done++
		}
		// Usage lives in each agent step's Result.output (cost_usd / tokens / turns).
		// Aggregate by workflow and by step. No kernel change — read what's stored.
		tasks, _ := s.Store.TasksForRun(run.ID)
		for _, t := range tasks {
			u := usageOf(t.Result)
			if u.cost == 0 && u.tokensIn == 0 && u.tokensOut == 0 {
				continue
			}
			agentCalls++
			totalCost += u.cost
			tokensIn += u.tokensIn
			tokensOut += u.tokensOut
			turns += u.turns
			costByWorkflow[run.WorkflowID] += u.cost
			costByStep[t.StepID] += u.cost
		}
	}
	acceptance := 0.0
	if len(runs) > 0 {
		acceptance = float64(done) / float64(len(runs))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"runs_total":       len(runs),
		"by_status":        byStatus,
		"paused":           s.Engine.IsPaused(),
		"total_cost_usd":   totalCost,
		"cost_by_workflow": costByWorkflow,
		"cost_by_step":     costByStep,
		"acceptance_rate":  acceptance,
		"total_tokens_in":  tokensIn,
		"total_tokens_out": tokensOut,
		"total_turns":      turns,
		"agent_calls":      agentCalls,
	})
}

// stepUsage is the per-step agent usage read from a stored Result.
type stepUsage struct {
	cost                       float64
	tokensIn, tokensOut, turns int
}

// usageOf extracts agent usage from a step's Result JSON. The agent runner stores
// these under Result.output (cost_usd / tokens_in / tokens_out / num_turns); a
// top-level fallback keeps older rows working.
func usageOf(result string) stepUsage {
	if result == "" {
		return stepUsage{}
	}
	var m map[string]any
	if json.Unmarshal([]byte(result), &m) != nil {
		return stepUsage{}
	}
	src := m
	if out, ok := m["output"].(map[string]any); ok {
		src = out
	}
	f := func(k string) float64 {
		if v, ok := src[k].(float64); ok {
			return v
		}
		return 0
	}
	return stepUsage{
		cost:      f("cost_usd"),
		tokensIn:  int(f("tokens_in")),
		tokensOut: int(f("tokens_out")),
		turns:     int(f("num_turns")),
	}
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---- registry CRUD ----------------------------------------------------------

func (s *Server) listRegistry(w http.ResponseWriter, r *http.Request) {
	kind := r.PathValue("kind")
	if _, ok := s.Registry.pathFor(kind, "_"); !ok {
		httpErr(w, http.StatusNotFound, "unknown registry kind: "+kind)
		return
	}
	ids, err := s.Registry.list(kind)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"kind": kind, "ids": ids})
}

func (s *Server) getRegistry(w http.ResponseWriter, r *http.Request) {
	path, ok := s.Registry.pathFor(r.PathValue("kind"), r.PathValue("id"))
	if !ok {
		httpErr(w, http.StatusNotFound, "unknown registry kind")
		return
	}
	s.getRegistryFile(w, path)
}

func (s *Server) putRegistry(w http.ResponseWriter, r *http.Request) {
	kind := r.PathValue("kind")
	id := r.PathValue("id")
	path, ok := s.Registry.pathFor(kind, id)
	if !ok {
		httpErr(w, http.StatusNotFound, "unknown registry kind")
		return
	}
	s.putRegistryFile(w, r, path, func(b []byte) error { return validateRegistry(kind, b) })
	// Invalidate the workflow cache so the next run re-reads the new manifest.
	if kind == "workflows" && s.Engine != nil {
		s.Engine.InvalidateWorkflow(id)
	}
}

func (s *Server) delRegistry(w http.ResponseWriter, r *http.Request) {
	path, ok := s.Registry.pathFor(r.PathValue("kind"), r.PathValue("id"))
	if !ok {
		httpErr(w, http.StatusNotFound, "unknown registry kind")
		return
	}
	s.deleteRegistryFile(w, path)
}

// getAgentPersona serves the markdown persona sidecar (<id>.md) for an agent.
// Returns 200 with the file body (text/plain) or 404 if the sidecar is absent.
// Uses the same path-traversal guard as pathFor so id can never escape the registry.
func (s *Server) getAgentPersona(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	// Resolve and validate via pathFor (agents kind) to get the traversal-safe
	// path to the .yaml manifest, then swap the extension.
	yamlPath, ok := s.Registry.pathFor("agents", id)
	if !ok {
		httpErr(w, http.StatusNotFound, "unknown agent id")
		return
	}
	mdPath := yamlPath[:len(yamlPath)-len(".yaml")] + ".md"
	b, err := os.ReadFile(mdPath)
	if err != nil {
		httpErr(w, http.StatusNotFound, "persona not found")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(b)
}

// ---- settings ---------------------------------------------------------------

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.Settings.Get()) // secrets masked
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	var in settings.Settings
	if !readJSON(w, r, &in) {
		return
	}
	out, err := s.Settings.Put(in)
	if err != nil {
		// A rejected execution_unit (or any validation failure) is a client error,
		// not a server fault — surface it as 400 so the UI can show the reason.
		if errors.Is(err, settings.ErrInvalid) {
			httpErr(w, http.StatusBadRequest, err.Error())
			return
		}
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// ---- §B events --------------------------------------------------------------

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.runAccessible(r.Context(), id) {
		httpErr(w, http.StatusNotFound, "run not found: "+id)
		return
	}
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	evs, err := s.Store.EventsAfter(id, after)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if evs == nil {
		evs = []*store.Event{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": evs})
}

// ---- §C internal ------------------------------------------------------------

type claimReq struct {
	Worker string `json:"worker"`
}

func (s *Server) claim(w http.ResponseWriter, r *http.Request) {
	var req claimReq
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.Worker == "" {
		req.Worker = "worker"
	}
	task, err := s.Store.Claim(req.Worker)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if task == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, task)
}

type reportReq struct {
	Fence   int64          `json:"fence"`
	Success bool           `json:"success"`
	Output  map[string]any `json:"output"`
	Detail  string         `json:"detail"`
}

func (s *Server) report(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req reportReq
	if !readJSON(w, r, &req) {
		return
	}
	err := s.Engine.ReportStep(id, req.Fence, workflow.StepResult{Success: req.Success, Output: req.Output, Detail: req.Detail})
	if err != nil {
		httpErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"reported": id})
}

type heartbeatReq struct {
	Fence int64 `json:"fence"`
}

func (s *Server) heartbeat(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req heartbeatReq
	if !readJSON(w, r, &req) {
		return
	}
	if err := s.Store.Heartbeat(id, req.Fence); err != nil {
		httpErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) usage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	task, err := s.Store.GetTask(id)
	if err != nil {
		notFound(w, err)
		return
	}
	_, _ = s.Store.AppendEvent(task.RunID, id, "step.usage", body)

	// Billing meter (a): accrue this task's REAL token cost to the workspace's cycle
	// — including failed tasks (every task reports usage). Resolve the workspace from
	// the run's project → owner; skip silently for internal/unowned runs.
	if cost, ok := body["cost_usd"].(float64); ok && cost > 0 {
		if run, err := s.Store.GetRun(task.RunID); err == nil {
			if wsID, ok := s.workspaceForProject(run.ProjectID); ok {
				if total, tripped, _ := s.Billing.AddCost(wsID, id, cost); tripped {
					// Admin alert (step 4): this workspace crossed our hard spend cap and
					// is now paused — the entitlement gate will stop its further runs.
					log.Printf("billing: workspace %s spend cap tripped at $%.2f — paused", wsID, total)
					_, _ = s.Store.AppendEvent(task.RunID, id, "billing.spend_cap_tripped",
						map[string]any{"workspace": wsID, "cost_usd_incurred": total})
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
