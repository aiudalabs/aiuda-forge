// Package conductor is the thin orchestration layer of the GitHub-native pivot
// (docs/PLAN-2026-07-03-pivot-github-native.md §5): GitHub owns execution
// (issues, agents, PRs, checks); this package owns the POLICY GitHub doesn't
// have and the PROJECTION the console reads.
//
// F1 ships the projection: mirror each exported story's state from its GitHub
// issue back into the native ticket store (the store doubles as projection
// cache, so the console's Tickets UI needs no re-pointing). Trigger: a periodic
// poll — cheap, two REST calls per repo — plus the webhook receiver
// (internal/ghapp) for push-driven syncs when a webhook is configured.
package conductor

import (
	"context"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"forge/internal/github"
	"forge/internal/tickets"
)

// Dead-agent recovery thresholds (task: stuck Copilot/claude_action stories). Both
// are counted in projection TICKS (~25s each), so a threshold of 8 ≈ 3.3 min — long
// enough to ride out GitHub eventual consistency and a just-dispatched run that has
// not materialized yet, short enough that a genuinely-dead agent frees its story on
// its own in a few minutes instead of hanging `running` forever.
const (
	// notFoundDeathThreshold: consecutive 404 "not found" reads of a Copilot agent
	// task before its session is declared DEAD. A purged task 404s forever; without
	// this the sweep retried it every tick (2 log lines/25s, no verdict, story stuck).
	notFoundDeathThreshold = 8
	// staleLabelThreshold: consecutive ticks a story carries `agent:running` with NO
	// live claude.yml run before the label is declared STALE (the run died without its
	// if:always() cleanup — credits/kill). Guards the dispatch→run-materialize window.
	staleLabelThreshold = 8
)

// GitHubReader is the read surface the projection needs. *github.Client
// implements it; tests inject a fake.
type GitHubReader interface {
	ListIssueStates(ctx context.Context, repoURL string) ([]github.IssueState, error)
	ListOpenPRs(ctx context.Context, repoURL string) ([]github.OpenPR, error)
}

// IssueCloser cierra el loop de un PR mergeado cuyos closing keywords no
// auto-cerraron el issue (p.ej. escritos entre backticks). Opcional: nil lo
// desactiva. *github.Client lo implementa.
type IssueCloser interface {
	PRMerged(ctx context.Context, repoURL string, number int) (bool, error)
	CloseIssue(ctx context.Context, repoURL string, number int, comment string) error
}

// ProjectLister yields the projects to keep in sync. *projects.Store implements
// List() ([]projects.Project, error); we only need id+repo, decoupled here.
type SyncTarget struct {
	ProjectID string
	RepoURL   string
}

// Result reports one projection pass over one project.
type Result struct {
	ProjectID string `json:"project_id"`
	Repo      string `json:"repo"`
	Mirrored  int    `json:"mirrored"` // stories with an external_ref on this repo
	Changed   int    `json:"changed"`  // stories whose status was updated
}

// TaskStater reads the live state of a Copilot agent task. *github.Client
// implements it; nil disables the dead-session sweep.
type TaskStater interface {
	AgentTaskState(ctx context.Context, repoURL, taskID string) (string, error)
}

// RunLiveChecker reports whether a workflow has any run that is not terminal —
// the GitHub-observable liveness signal behind the stale-label recovery (a story
// stuck `running` on an `agent:running` label whose claude.yml run died without
// cleanup). *github.Client implements it. Recovery needs it AND LabelRemover set.
type RunLiveChecker interface {
	WorkflowRunsActive(ctx context.Context, repoURL, workflowFile string) (bool, error)
}

// LabelRemover drops the `agent:running` label off issues — the GitHub-observable
// cleanup the conductor performs when it declares a claude_action session dead.
// *github.Client implements it. Recovery needs it AND RunLiveChecker set.
type LabelRemover interface {
	RemoveIssueRunning(ctx context.Context, repoURL string, numbers []int) error
}

// PRFileLister lista los archivos que un PR tocó. Alimenta el grafo
// producto↔código (task #5): cuando una story llega a done por un PR mergeado,
// sus rutas se registran (story→archivos). Opcional: nil desactiva la captura.
// *github.Client lo implementa (ListPRFiles).
type PRFileLister interface {
	ListPRFiles(ctx context.Context, repoURL string, number int) ([]string, error)
}

// Projector mirrors GitHub state into the ticket store.
type Projector struct {
	Tickets *tickets.Store
	GH      GitHubReader
	// TaskState, when set, lets the sweep detect dead agent sessions (task
	// failed/cancelled, or a purged task 404ing past the threshold) and return
	// their stories to backlog automatically.
	TaskState TaskStater
	// RunLive + Labels, when BOTH set, enable stale-label recovery for the
	// claude_action channel: a story stuck `running` on an `agent:running` label
	// whose claude.yml run is dead gets the label removed and returns to backlog.
	// Either nil disables it (the label pins running, as before).
	RunLive RunLiveChecker
	Labels  LabelRemover
	// Closer, when set, closes issues whose story PR merged without auto-close.
	Closer IssueCloser
	// Files, when set, records a merged story's changed paths into the code graph
	// (task #5). Best-effort — nil, or a failed listing, just skips the capture.
	Files PRFileLister
	// OnGraphChanged, when set, fires once per pass that captured new files into
	// the graph — the app wires it to regenerate docs/MODULE_MAP.md. nil disables.
	OnGraphChanged func(projectID, repoURL string)
	// ClientFor, si está presente, resuelve el cliente GitHub POR PROYECTO
	// (multi-tenant: token del dueño / installation token); nil = usar los
	// campos fijos GH/TaskState/Closer (auth del host).
	ClientFor func(ctx context.Context, projectID string) *github.Client

	// serializes syncs per repo so a webhook burst doesn't stampede gh.
	mu    sync.Mutex
	inFly map[string]bool

	// recMu guards the dead-agent recovery counters, which persist ACROSS ticks
	// (that is the whole point — a verdict needs N consecutive observations) and
	// are shared across concurrently-syncing projects.
	recMu sync.Mutex
	// notFound counts consecutive 404 reads per Copilot task id (uuid, globally
	// unique). Reset by any non-404 outcome (a live read or a transient 5xx).
	notFound map[string]int
	// staleLabel counts consecutive ticks a story's `agent:running` label had no
	// live claude.yml run, keyed by "projectID\x00issueNumber".
	staleLabel map[string]int
}

func NewProjector(store *tickets.Store, gh GitHubReader) *Projector {
	return &Projector{Tickets: store, GH: gh, inFly: map[string]bool{}}
}

// bumpNotFound increments and returns the consecutive-404 count for a task id.
func (p *Projector) bumpNotFound(taskID string) int {
	p.recMu.Lock()
	defer p.recMu.Unlock()
	if p.notFound == nil {
		p.notFound = map[string]int{}
	}
	p.notFound[taskID]++
	return p.notFound[taskID]
}

// resetNotFound clears a task id's 404 streak (a live read or a transient error).
func (p *Projector) resetNotFound(taskID string) {
	p.recMu.Lock()
	defer p.recMu.Unlock()
	delete(p.notFound, taskID)
}

// bumpStaleLabel increments and returns the consecutive stale-label count for a key.
func (p *Projector) bumpStaleLabel(key string) int {
	p.recMu.Lock()
	defer p.recMu.Unlock()
	if p.staleLabel == nil {
		p.staleLabel = map[string]int{}
	}
	p.staleLabel[key]++
	return p.staleLabel[key]
}

// resetStaleLabel clears a key's stale-label streak (a live run appeared, or the
// label is gone).
func (p *Projector) resetStaleLabel(key string) {
	p.recMu.Lock()
	defer p.recMu.Unlock()
	delete(p.staleLabel, key)
}

// agentLogins spots the coding agents: an issue assigned to one of these (or any
// [bot]) counts as "an agent is working" → running.
var agentLogins = map[string]bool{
	"copilot-swe-agent":    true,
	"copilot":              true,
	"anthropic-code-agent": true,
	"claude":               true,
	"codex":                true,
}

func isAgent(login string) bool {
	return agentLogins[strings.ToLower(login)] || strings.HasSuffix(login, "[bot]")
}

// prNum extrae el número de PR de su URL html.
func prNum(url string) int {
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

// taskIDRe extrae el id de task de una session_url de Copilot
// (…/tasks/<uuid>); las sesiones claude_action no matchean (página de runs).
var taskIDRe = regexp.MustCompile(`/tasks/([0-9a-f-]{8,})`)

// runningLabel es la señal, observable en GitHub, de que un agente está
// trabajando un issue AHORA. La pone el workflow claude.yml al arrancar y la
// quita en su paso if:always() al terminar (registry/templates … claude.yml).
// Reemplaza el ancla local de story_sessions para el canal claude_action, que
// no asigna el issue ni abre PR mientras corre y cuya sesión (página de runs,
// sin id consultable) el barrido no podía soltar → el ancla nunca se soltaba y
// la story flapeaba ready/running.
const runningLabel = "agent:running"

// isTaskSession reporta si la URL de sesión es una task de Copilot (…/tasks/<id>),
// la ÚNICA que el barrido de sesiones muertas puede consultar y por tanto soltar.
// Las sesiones claude_action no lo son: su liveness es runningLabel, no esta tabla.
func isTaskSession(url string) bool {
	return url != "" && taskIDRe.MatchString(url)
}

// hasLabel reporta si el issue lleva el label name.
func hasLabel(iss github.IssueState, name string) bool {
	for _, l := range iss.Labels {
		if l == name {
			return true
		}
	}
	return false
}

// closesRefs extracts the issue numbers a PR body claims to close
// ("Closes #7, fixes #12, resolves #3" — GitHub's closing keywords).
var closesRe = regexp.MustCompile(`(?i)(?:close[sd]?|fix(?:es|ed)?|resolve[sd]?)\s+#(\d+)`)

func closesRefs(body string) []int {
	var out []int
	for _, m := range closesRe.FindAllStringSubmatch(body, -1) {
		if n, err := strconv.Atoi(m[1]); err == nil {
			out = append(out, n)
		}
	}
	return out
}

// SyncProject mirrors one project's stories from repoURL. Derivation per story
// (GitHub is the source of truth for mirrored stories):
//
//	issue closed                    → done
//	open + non-draft PR closing it  → in_review (+pr_url)
//	open + draft PR closing it      → running   (+pr_url; agent still working)
//	open + agent assignee           → running
//	otherwise                       → backlog   ("ready" stays derived from deps)
func (p *Projector) SyncProject(ctx context.Context, projectID, repoURL string) (Result, error) {
	res := Result{ProjectID: projectID, Repo: repoURL}

	// One sync per repo at a time; concurrent triggers coalesce into a no-op.
	p.mu.Lock()
	if p.inFly[repoURL] {
		p.mu.Unlock()
		return res, nil
	}
	p.inFly[repoURL] = true
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		delete(p.inFly, repoURL)
		p.mu.Unlock()
	}()

	// Cliente por tenant cuando hay factory; si no, los fijos del host.
	ghR := p.GH
	taskState := p.TaskState
	closer := p.Closer
	filer := p.Files
	runLive := p.RunLive
	labels := p.Labels
	if p.ClientFor != nil {
		if c := p.ClientFor(ctx, projectID); c != nil {
			ghR = c
			taskState = c
			closer = c
			filer = c
			runLive = c
			labels = c
		}
	}
	// Stale-label recovery needs BOTH a liveness signal and the label-removal write.
	canRecoverLabel := runLive != nil && labels != nil

	slug, err := github.Slug(repoURL)
	if err != nil {
		return res, err
	}
	stories, err := p.Tickets.ListStoriesByProject(projectID)
	if err != nil {
		return res, err
	}
	prefix := "github:" + slug + "#"
	byNumber := map[int]tickets.Story{}
	for _, st := range stories {
		rest, ok := strings.CutPrefix(st.ExternalRef, prefix)
		if !ok {
			continue
		}
		n, err := strconv.Atoi(rest)
		if err != nil {
			continue
		}
		byNumber[n] = st
	}
	res.Mirrored = len(byNumber)
	if res.Mirrored == 0 {
		return res, nil // nothing exported to this repo yet
	}

	issues, err := ghR.ListIssueStates(ctx, repoURL)
	if err != nil {
		return res, err
	}
	prs, err := ghR.ListOpenPRs(ctx, repoURL)
	if err != nil {
		return res, err
	}
	// Sesiones de agente activas (F2 dispatch): una task de Copilot creada por
	// prompt NO asigna el issue ni tiene PR durante sus primeros minutos — sin
	// esto, el tick degradaría la story recién despachada a backlog (y en modo
	// auto la re-despacharía). La sesión ancla la story en running hasta que
	// aparezca su PR o el issue cierre. (F3: poll del estado real de la task.)
	sessions, err := p.Tickets.SessionURLs(projectID)
	if err != nil {
		sessions = map[string]string{}
	}

	// issue number → the open PR that closes it (non-draft wins over draft).
	prFor := map[int]github.OpenPR{}
	for _, pr := range prs {
		for _, n := range closesRefs(pr.Body) {
			if cur, seen := prFor[n]; seen && !cur.Draft {
				continue
			}
			prFor[n] = pr
		}
	}
	// Fallback: los agentes no siempre respetan "Closes #n" (cazado en vivo: el
	// PR de sprint de Copilot salió sin refs). Una mención del id de la story
	// (p.ej. "S1-01") en el título/body del PR también la liga — \b evita que
	// S1-1 matchee dentro de S1-11.
	for _, pr := range prs {
		text := pr.Title + "\n" + pr.Body
		for n, st := range byNumber {
			if cur, seen := prFor[n]; seen && !cur.Draft {
				continue
			}
			if regexp.MustCompile(`\b` + regexp.QuoteMeta(st.ID) + `\b`).MatchString(text) {
				prFor[n] = pr
			}
		}
	}

	// Barrido de sesiones muertas (F3): si la task de Copilot detrás de una
	// sesión terminó en failed/cancelled/error, O si su registro fue PURGADO (404
	// "not found" sostenido — una task que estaba corriendo y ya no existe está de
	// hecho muerta), el agente murió — la story vuelve a backlog (y su sesión se
	// limpia) para que el dispatch la re-sirva. Distingue el 404 (muerte) de un
	// 5xx/timeout (transitorio real: se mantiene el backoff, no se toca la story).
	deadSession := map[string]bool{} // story id → su task está muerta
	if taskState != nil {
		type verdict struct {
			dead      bool
			transient bool
		}
		checked := map[string]verdict{} // task id → veredicto (varias stories comparten task)
		for id, url := range sessions {
			m := taskIDRe.FindStringSubmatch(url)
			if m == nil {
				continue // sesión claude_action (página de runs) — sin estado consultable aún
			}
			taskID := m[1]
			v, ok := checked[taskID]
			if !ok {
				state, err := taskState.AgentTaskState(ctx, repoURL, taskID)
				switch {
				case err == nil:
					p.resetNotFound(taskID) // una lectura viva corta la racha de 404
					v.dead = state == "failed" || state == "cancelled" || state == "error"
				case errors.Is(err, github.ErrTaskNotFound):
					n := p.bumpNotFound(taskID)
					if n >= notFoundDeathThreshold {
						v.dead = true
						log.Printf("conductor: task %s — %d×404 not-found consecutivos → sesión declarada MUERTA (registro purgado); soltando ancla", taskID, n)
					} else {
						v.transient = true // aún no confirmado: sin veredicto no tocamos nada
					}
				default:
					// 5xx/timeout/red: transitorio de verdad — se mantiene el backoff y
					// NO se cuenta como 404 (se resetea la racha para no acumular ruido).
					p.resetNotFound(taskID)
					v.transient = true
					log.Printf("conductor: task state %s (transitorio, se reintenta): %v", taskID, err)
				}
				checked[taskID] = v
			}
			if v.dead {
				deadSession[id] = true
			}
		}
	}

	// Cierre de loop de PRs mergeados (cazado en vivo: closes entre backticks
	// no auto-cierran): una story con PR que YA no está abierto pero SÍ mergeó,
	// cierra su issue aquí — GitHub vuelve a ser la verdad y la derivación de
	// abajo la marca done en este mismo pase.
	if closer != nil {
		mergedPR := map[int]bool{} // pr number → merged (cache por pase)
		for i := range issues {
			iss := &issues[i]
			st, mirrored := byNumber[iss.Number]
			if !mirrored || iss.State != "open" || st.PRURL == "" {
				continue
			}
			n := prNum(st.PRURL)
			if n == 0 {
				continue
			}
			if _, isOpen := prFor[iss.Number]; isOpen {
				continue // su PR sigue abierto: nada que cerrar
			}
			merged, ok := mergedPR[n]
			if !ok {
				var err error
				merged, err = closer.PRMerged(ctx, repoURL, n)
				if err != nil {
					continue // best-effort
				}
				mergedPR[n] = merged
			}
			if !merged {
				continue
			}
			if err := closer.CloseIssue(ctx, repoURL, iss.Number,
				fmt.Sprintf("Cerrado por el conductor: el PR %s mergeó pero sus closing keywords no auto-cerraron este issue.", st.PRURL)); err != nil {
				log.Printf("conductor: close issue #%d: %v", iss.Number, err)
				continue
			}
			iss.State = "closed" // la derivación de este pase ya lo ve done
		}
	}

	// claudeActive: ¿hay ALGÚN run vivo de claude.yml en el repo? Se consulta como
	// mucho UNA vez por pase (memoizado, y solo si de verdad hay un label que
	// evaluar). Repo-level a propósito: si hay CUALQUIER run vivo no declaramos
	// stale ningún label (conservador — nunca soltamos una story que podría estar
	// corriendo; a cambio, un label muerto que coexiste con otro run vivo espera a
	// que ese run termine). Fail-CLOSED (true) ante error → nunca recupera a ciegas.
	claudeComputed, claudeVal := false, true
	claudeActive := func() bool {
		if claudeComputed {
			return claudeVal
		}
		claudeComputed = true
		if runLive == nil {
			claudeVal = true
			return true
		}
		live, err := runLive.WorkflowRunsActive(ctx, repoURL, claudeWorkflowFile)
		if err != nil {
			log.Printf("conductor(%s): liveness de %s no concluyente (%v) — no se recupera este pase", projectID, claudeWorkflowFile, err)
			claudeVal = true
			return true
		}
		claudeVal = live
		return live
	}

	graphChanged := false
	for _, iss := range issues {
		st, mirrored := byNumber[iss.Number]
		if !mirrored {
			continue
		}
		target := tickets.StatusBacklog
		prURL := ""
		switch {
		case iss.State == "closed":
			target = tickets.StatusDone
		default:
			if pr, ok := prFor[iss.Number]; ok {
				prURL = pr.URL
				if pr.Draft {
					target = tickets.StatusRunning
				} else {
					target = tickets.StatusInReview
				}
			} else {
				for _, a := range iss.Assignees {
					if isAgent(a) {
						target = tickets.StatusRunning
						break
					}
				}
				// Señal de "corriendo" de claude_action: el label agent:running que
				// el workflow pone al arrancar y quita al terminar. GitHub = verdad:
				// presente ⟺ hay un run vivo; ausente tras terminar → la story cae a
				// backlog (o a in_review si dejó PR) sin ancla local que la trabe.
				if target == tickets.StatusBacklog && hasLabel(iss, runningLabel) {
					target = p.deriveLabelAnchor(ctx, projectID, repoURL, st, iss, canRecoverLabel, labels, claudeActive)
				}
				// Ancla de sesión SOLO para tasks de Copilot: una task recién creada
				// aún no asignó el issue ni abrió PR, y el barrido de sesiones muertas
				// puede soltarla. Las sesiones claude_action NO anclan aquí (su liveness
				// es el label de arriba) — es lo que causaba el flap ready/running.
				if target == tickets.StatusBacklog && isTaskSession(sessions[st.ID]) {
					if deadSession[st.ID] {
						// Task muerta (failed/cancelled/404 sostenido): la story vuelve a
						// backlog. Registra "agente perdido" SOLO en la transición (estaba
						// running) para que la UI lo muestre; la sesión se limpia abajo.
						if st.Status == tickets.StatusRunning && st.AgentLost == "" {
							note := fmt.Sprintf("Copilot agent task perdida: %s", taskIDOf(sessions[st.ID]))
							if err := p.Tickets.MarkAgentLost(projectID, st.ID, note); err != nil {
								log.Printf("conductor: marcar agent_lost %s: %v", st.ID, err)
							}
							log.Printf("conductor: story %s — %s → devuelta a backlog para re-despacho", st.ID, note)
						}
					} else {
						target = tickets.StatusRunning // task despachada aún sin PR
					}
				}
			}
		}
		changed, err := p.Tickets.SyncExternalStatus(st.ID, target, prURL)
		if err != nil {
			return res, fmt.Errorf("sync %s: %w", st.ID, err)
		}
		if changed {
			res.Changed++
			// Grafo producto↔código (task #5): al MOMENTO en que la story llega a
			// done, registrar las rutas de su PR mergeado. Solo en la transición
			// (changed) → una llamada por story, no por tick. Best-effort: sin
			// filer o sin nº de PR conocido, se omite sin ensuciar el pase.
			if target == tickets.StatusDone && filer != nil {
				if n := prNum(st.PRURL); n > 0 {
					if p.captureFiles(ctx, filer, projectID, repoURL, st.ID, n) > 0 {
						graphChanged = true
					}
				}
			}
		}
	}
	// Limpieza de sesiones muertas: una task declarada muerta deja de poblar el
	// barrido (deja de consultarse → se acaba el 404 cada 25s). Cubre también las
	// stories YA done cuya sesión task quedó sin limpiar (el caso reservas-belleza:
	// stories done + tasks purgadas → 404 eterno). Una running→backlog ya la limpió
	// SyncExternalStatus; este DELETE extra es idempotente.
	for id := range deadSession {
		if err := p.Tickets.ClearStorySession(id); err != nil {
			log.Printf("conductor: limpiar sesión muerta de %s: %v", id, err)
		}
	}
	// El grafo cambió en este pase → regenerar docs/MODULE_MAP.md (Capa 1
	// mantenida). El callback hace su propio ctx/goroutine; aquí solo lo gatillamos.
	if graphChanged && p.OnGraphChanged != nil {
		p.OnGraphChanged(projectID, repoURL)
	}
	return res, nil
}

// deriveLabelAnchor decides the status of a story whose issue carries the
// `agent:running` label. Normally the label pins `running` (claude_action's
// GitHub-observable liveness). But a run that DIES without its if:always() cleanup
// leaves the label stuck → the story hangs `running` forever. When recovery is
// enabled (RunLive+Labels) and NO live claude.yml run backs the label for
// staleLabelThreshold consecutive ticks, the label is declared STALE: it is
// removed (GitHub-observable), the story is flagged "agente perdido", and it
// returns to backlog. The threshold rides out the dispatch→run-materialize window.
func (p *Projector) deriveLabelAnchor(ctx context.Context, projectID, repoURL string, st tickets.Story, iss github.IssueState, canRecover bool, labels LabelRemover, claudeActive func() bool) tickets.Status {
	key := projectID + "\x00" + strconv.Itoa(iss.Number)

	// Ya declarada perdida en un pase anterior: el label es un fantasma que aún no
	// se refleja como quitado (consistencia eventual de GitHub). NO re-anclar a
	// running (deshacer la recuperación) ni re-emitir; re-intentar quitarlo (idempotente).
	if st.AgentLost != "" {
		if labels != nil {
			_ = labels.RemoveIssueRunning(ctx, repoURL, []int{iss.Number})
		}
		return tickets.StatusBacklog
	}

	// Sin capacidad de recuperación (host sin RunLive/Labels): el label ancla
	// running como siempre — comportamiento intacto.
	if !canRecover {
		return tickets.StatusRunning
	}

	// Hay un run vivo → el label es legítimo. Resetea la racha y ancla running.
	if claudeActive() {
		p.resetStaleLabel(key)
		return tickets.StatusRunning
	}

	// Label sin run vivo: acumula evidencia. Bajo el umbral, sigue siendo running
	// (todavía puede ser un run recién despachado que no materializó).
	if n := p.bumpStaleLabel(key); n < staleLabelThreshold {
		return tickets.StatusRunning
	}

	// Umbral alcanzado: label STALE. Quítalo, marca "agente perdido", vuelve a backlog.
	p.resetStaleLabel(key)
	if err := labels.RemoveIssueRunning(ctx, repoURL, []int{iss.Number}); err != nil {
		log.Printf("conductor: quitar %s de #%d: %v", runningLabel, iss.Number, err)
	}
	note := fmt.Sprintf("label %s sin workflow run vivo (claude_action murió sin limpiar)", runningLabel)
	if err := p.Tickets.MarkAgentLost(projectID, st.ID, note); err != nil {
		log.Printf("conductor: marcar agent_lost %s: %v", st.ID, err)
	}
	if err := p.Tickets.ClearStorySession(st.ID); err != nil {
		log.Printf("conductor: limpiar sesión de %s: %v", st.ID, err)
	}
	log.Printf("conductor: story %s (#%d) — %s → devuelta a backlog para re-despacho", st.ID, iss.Number, note)
	return tickets.StatusBacklog
}

// taskIDOf extrae el uuid de una session_url de task de Copilot (…/tasks/<uuid>),
// o "" si no matchea.
func taskIDOf(url string) string {
	if m := taskIDRe.FindStringSubmatch(url); m != nil {
		return m[1]
	}
	return ""
}

// captureFiles registra en el grafo las rutas que el PR n de storyID tocó y
// devuelve cuántas insertó (0 = nada nuevo). Best-effort y silencioso salvo log:
// un fallo de listado no debe abortar el pase de proyección (que mantiene la UI).
func (p *Projector) captureFiles(ctx context.Context, filer PRFileLister, projectID, repoURL, storyID string, n int) int {
	files, err := filer.ListPRFiles(ctx, repoURL, n)
	if err != nil {
		log.Printf("conductor: grafo — listar archivos del PR #%d (%s): %v", n, storyID, err)
		return 0
	}
	inserted, err := p.Tickets.RecordStoryFiles(projectID, storyID, files)
	if err != nil {
		log.Printf("conductor: grafo — registrar archivos de %s: %v", storyID, err)
		return 0
	}
	if inserted > 0 {
		log.Printf("conductor: grafo — %s tocó %d archivos (PR #%d)", storyID, inserted, n)
	}
	return inserted
}

// Loop polls every interval, mirroring every target that has a repo. It is the
// fallback when no webhook reaches us (local/dev); webhook deliveries call
// SyncProject directly for a faster reaction.
func (p *Projector) Loop(ctx context.Context, interval time.Duration, targets func() []SyncTarget) {
	if interval <= 0 {
		return
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			for _, tg := range targets() {
				if tg.RepoURL == "" {
					continue
				}
				res, err := p.SyncProject(ctx, tg.ProjectID, tg.RepoURL)
				if err != nil {
					log.Printf("conductor: sync %s (%s): %v", tg.ProjectID, tg.RepoURL, err)
					continue
				}
				if res.Changed > 0 {
					log.Printf("conductor: sync %s — %d/%d stories actualizadas desde GitHub", tg.ProjectID, res.Changed, res.Mirrored)
				}
			}
		}
	}
}
