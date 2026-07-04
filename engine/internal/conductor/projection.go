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
	// failed/cancelled) and return their stories to backlog automatically.
	TaskState TaskStater
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
}

func NewProjector(store *tickets.Store, gh GitHubReader) *Projector {
	return &Projector{Tickets: store, GH: gh, inFly: map[string]bool{}}
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
	if p.ClientFor != nil {
		if c := p.ClientFor(ctx, projectID); c != nil {
			ghR = c
			taskState = c
			closer = c
			filer = c
		}
	}

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
	// sesión terminó en failed/cancelled y la story sigue sin PR, el agente
	// murió — la story vuelve a backlog (y su sesión se limpia) para que el
	// dispatch la re-sirva. Hoy esto era un reset manual contra la DB.
	deadSession := map[string]bool{}
	if taskState != nil {
		checked := map[string]string{} // task id → state (varias stories comparten task)
		for id, url := range sessions {
			m := taskIDRe.FindStringSubmatch(url)
			if m == nil {
				continue // sesión claude_action (página de runs) — sin estado consultable aún
			}
			state, ok := checked[m[1]]
			if !ok {
				var err error
				state, err = taskState.AgentTaskState(ctx, repoURL, m[1])
				if err != nil {
					log.Printf("conductor: task state %s: %v", m[1], err)
					state = "" // best-effort: sin veredicto no tocamos nada
				}
				checked[m[1]] = state
			}
			if state == "failed" || state == "cancelled" || state == "error" {
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
				if target == tickets.StatusBacklog && sessions[st.ID] != "" && !deadSession[st.ID] {
					target = tickets.StatusRunning // sesión despachada aún sin PR
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
	// El grafo cambió en este pase → regenerar docs/MODULE_MAP.md (Capa 1
	// mantenida). El callback hace su propio ctx/goroutine; aquí solo lo gatillamos.
	if graphChanged && p.OnGraphChanged != nil {
		p.OnGraphChanged(projectID, repoURL)
	}
	return res, nil
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
