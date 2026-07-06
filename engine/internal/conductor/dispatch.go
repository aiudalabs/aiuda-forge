package conductor

// Dispatch (F2 pivot): compute WHAT is ready to hand to a GitHub agent and FIRE
// it through the configured channel. The policy GitHub doesn't have lives here:
// per-story readiness (deps done), per-sprint gating (goal-mode order), model
// routing per lane, and the approve/auto/off autonomy switch. Mirrors the legacy
// scheduler's fireByProject/fireSprint (internal/orchestrator/native.go) with
// the store swapped for the GitHub projection.

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"

	"forge/internal/github"
	"forge/internal/tickets"
)

// Policy is the slice of project settings dispatch needs (adapted from
// projects.Settings by the API layer; conductor stays decoupled from that store).
type Policy struct {
	ExecutionUnit  string            // "sprint" | "story"
	DispatchMode   string            // "approve" | "auto" | "off"
	Executor       string            // "copilot" | "claude_action"
	ModelByLane    map[string]string // lane → model ("" / missing = auto)
	ExecutorByLane map[string]string // lane → executor; ausente = Executor del proyecto
	MaxConcurrency int               // stories con agente a la vez; 0 = sin límite
}

// executorFor resuelve el canal de una lane bajo la política.
func (p Policy) executorFor(lane string) string {
	if e, ok := p.ExecutorByLane[lane]; ok && e != "" {
		return e
	}
	return p.Executor
}

// otherExecutor es el canal alterno para el fallback automático.
func otherExecutor(e string) string {
	if e == "claude_action" {
		return "copilot"
	}
	return "claude_action"
}

// Candidate is one dispatchable unit under the current policy.
type Candidate struct {
	Kind     string   `json:"kind"` // "story" | "sprint"
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Stories  []string `json:"stories,omitempty"` // sprint kind: member story ids, dep order
	Lane     string   `json:"lane"`
	Model    string   `json:"model"`    // resolved from ModelByLane ("" = auto)
	Executor string   `json:"executor"` // channel the dispatch would use
}

// DispatchResult reports one dispatch.
type DispatchResult struct {
	Dispatched []string `json:"dispatched"` // story ids now running
	Channel    string   `json:"channel"`
	Model      string   `json:"model"`
	TaskURL    string   `json:"task_url,omitempty"`
}

// ErrNotCandidate: the requested unit isn't dispatchable anymore (raced by a
// merge/sync or never was). The API maps it to 409 so the console reloads.
var ErrNotCandidate = errors.New("not a dispatchable candidate")

// GitHubDispatcher is the write surface (implemented by *github.Client).
type GitHubDispatcher interface {
	CreateAgentTask(ctx context.Context, repoURL, prompt, model string) (string, error)
	DispatchWorkflow(ctx context.Context, repoURL, workflowFile, ref string, inputs map[string]string) error
}

// Dispatcher computes candidates and fires them.
type Dispatcher struct {
	Tickets *tickets.Store
	GH      GitHubDispatcher
	// ClientFor, si está presente, resuelve el cliente por PROYECTO (tenant).
	ClientFor func(ctx context.Context, projectID string) *github.Client
}

// ghFor resuelve el dispatcher de GitHub para un proyecto.
func (d *Dispatcher) ghFor(ctx context.Context, projectID string) GitHubDispatcher {
	if d.ClientFor != nil {
		if c := d.ClientFor(ctx, projectID); c != nil {
			return c
		}
	}
	return d.GH
}

// docsOnMain reports whether the project's canonical spec is on `main` — the invariant a
// sprint depends on (the agents read docs/ from main). Fail-OPEN on a missing tenant client
// or a transient API error: the design flow (docs_pr auto-merges before handoff) is the
// PRIMARY guard, so this conductor gate is defense-in-depth and must not deadlock dispatch
// on a GitHub blip.
func (d *Dispatcher) docsOnMain(ctx context.Context, projectID, repoURL string) bool {
	if d.ClientFor == nil {
		return true
	}
	c := d.ClientFor(ctx, projectID)
	if c == nil {
		return true
	}
	ok, err := c.FileOnBranch(ctx, repoURL, "main", "docs/PRD.md")
	if err != nil {
		return true
	}
	return ok
}

// gateOnDocs enforces the invariant "a story is dispatchable ⟺ its project's docs are on
// `main`". If there is work but the docs aren't merged yet, hold EVERYTHING (return no
// candidates) so no sprint runs against a docs-less main — the reservas-belleza incident,
// where a sprint fired before the design docs reached main and the agent worked blind.
func (d *Dispatcher) gateOnDocs(ctx context.Context, projectID, repoURL string, out []Candidate) []Candidate {
	if len(out) == 0 || d.docsOnMain(ctx, projectID, repoURL) {
		return out
	}
	log.Printf("conductor(%s): %d candidate(s) HELD — docs/ not on main yet; merge the design docs PR to unblock", projectID, len(out))
	return nil
}

// claudeWorkflowFile is the conductor-dispatch workflow the scaffold bakes into
// each repo (templates github-native).
const claudeWorkflowFile = "claude.yml"

// Candidates returns what the policy allows dispatching RIGHT NOW. Empty when
// dispatch is off. Only mirrored stories (external_ref on this repo) qualify —
// an unexported backlog has nowhere to be dispatched to.
func (d *Dispatcher) Candidates(ctx context.Context, projectID, repoURL string, pol Policy) ([]Candidate, error) {
	if pol.DispatchMode == "off" || repoURL == "" {
		return nil, nil
	}
	slug := repoSlug(repoURL)
	stories, err := d.Tickets.ListStoriesByProject(projectID)
	if err != nil {
		return nil, err
	}
	byID := map[string]tickets.Story{}
	inFlight := 0
	for _, st := range stories {
		byID[st.ID] = st
		if st.Status == tickets.StatusRunning {
			inFlight++
		}
	}
	// Tope de concurrencia (F3): con el cupo lleno no hay candidatos — ni para
	// el botón ni para el auto-dispatch.
	if pol.MaxConcurrency > 0 && inFlight >= pol.MaxConcurrency {
		return nil, nil
	}
	mirrored := func(st tickets.Story) bool {
		return strings.HasPrefix(st.ExternalRef, "github:"+slug+"#")
	}
	done := func(id string) bool {
		s, ok := byID[id]
		return ok && s.Status == tickets.StatusDone
	}
	storyReady := func(st tickets.Story) bool {
		if st.Status != tickets.StatusBacklog || !mirrored(st) {
			return false
		}
		for _, dep := range st.Deps {
			if !done(dep) {
				return false
			}
		}
		return true
	}

	if pol.ExecutionUnit != "sprint" {
		var out []Candidate
		for _, st := range stories {
			if storyReady(st) {
				out = append(out, Candidate{
					Kind: "story", ID: st.ID, Title: st.Title, Lane: st.Owner,
					Model: pol.ModelByLane[st.Owner], Executor: pol.Executor,
				})
			}
		}
		sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
		return d.gateOnDocs(ctx, projectID, repoURL, out), nil
	}

	// Sprint mode: a sprint is dispatchable when it still has backlog work, nothing
	// of it is already in flight, and every cross-sprint dependency is done.
	bySprint := map[string][]tickets.Story{}
	for _, st := range stories {
		if st.SprintID != "" && mirrored(st) {
			bySprint[st.SprintID] = append(bySprint[st.SprintID], st)
		}
	}
	var out []Candidate
	for sid, members := range bySprint {
		pending, inFlight := 0, 0
		gated := false
		for _, st := range members {
			switch st.Status {
			case tickets.StatusBacklog:
				pending++
			case tickets.StatusRunning, tickets.StatusInReview:
				inFlight++
			}
			for _, dep := range st.Deps {
				depSt, ok := byID[dep]
				if ok && depSt.SprintID != sid && !done(dep) {
					gated = true
				}
			}
		}
		if pending == 0 || inFlight > 0 || gated {
			continue
		}
		sort.Slice(members, func(i, j int) bool { return members[i].ID < members[j].ID }) // dep order by convention
		ids := make([]string, 0, pending)
		for _, st := range members {
			if st.Status == tickets.StatusBacklog {
				ids = append(ids, st.ID)
			}
		}
		lane := dominantLane(members)
		out = append(out, Candidate{
			Kind: "sprint", ID: sid, Title: sprintTitle(sid, members), Stories: ids,
			Lane: lane, Model: pol.ModelByLane[lane], Executor: pol.Executor,
		})
	}
	sort.Slice(out, func(i, j int) bool { return sprintNum(out[i].ID) < sprintNum(out[j].ID) })
	return d.gateOnDocs(ctx, projectID, repoURL, out), nil
}

// Dispatch fires one candidate (story or sprint id) after re-validating it.
func (d *Dispatcher) Dispatch(ctx context.Context, projectID, repoURL string, pol Policy, unitID string) (DispatchResult, error) {
	cands, err := d.Candidates(ctx, projectID, repoURL, pol)
	if err != nil {
		return DispatchResult{}, err
	}
	var cand *Candidate
	for i := range cands {
		if cands[i].ID == unitID {
			cand = &cands[i]
			break
		}
	}
	if cand == nil {
		return DispatchResult{}, fmt.Errorf("%w: %s", ErrNotCandidate, unitID)
	}

	storyIDs := cand.Stories
	if cand.Kind == "story" {
		storyIDs = []string{cand.ID}
	}
	prompt, err := d.buildPrompt(projectID, repoURL, cand.Kind, storyIDs)
	if err != nil {
		return DispatchResult{}, err
	}

	res := DispatchResult{Dispatched: storyIDs, Channel: cand.Executor, Model: cand.Model}
	ghd := d.ghFor(ctx, projectID)
	issues := d.issueNumbers(storyIDs)
	fire := func(executor string) (string, error) {
		return d.fireChannel(ctx, ghd, repoURL, executor, prompt, cand.Model, issues)
	}
	url, err := fire(cand.Executor)
	if err != nil {
		// Fallback automático de canal (F4 — la lección del outage de Copilot):
		// si el canal primario falla al DESPACHAR, se intenta el alterno.
		alt := otherExecutor(cand.Executor)
		log.Printf("conductor: canal %s falló (%v) — fallback a %s", cand.Executor, err, alt)
		url, err = fire(alt)
		if err != nil {
			return DispatchResult{}, fmt.Errorf("ambos canales fallaron (%s y %s): %w", cand.Executor, alt, err)
		}
		res.Channel = alt
		if alt == "claude_action" {
			res.Model = ""
		}
	}
	res.TaskURL = url
	// Reflejo inmediato: las stories despachadas pasan a running y quedan
	// ligadas a su sesión de agente (el link "ver sesión" de la consola); la
	// proyección las mantiene correctas a partir de aquí (PR/merge/reopen).
	for _, id := range storyIDs {
		if _, err := d.Tickets.SyncExternalStatus(id, tickets.StatusRunning, ""); err != nil {
			return res, fmt.Errorf("dispatched but marking %s running failed: %w", id, err)
		}
		if err := d.Tickets.SetStorySession(id, res.TaskURL); err != nil {
			return res, fmt.Errorf("dispatched but recording session for %s failed: %w", id, err)
		}
	}
	return res, nil
}

// fireChannel dispara el prompt por un canal concreto y devuelve la URL de la
// sesión. Es la unidad que el fallback automático reintenta por el canal alterno.
// issues son los números de issue del despacho (coma-separados): el workflow
// claude.yml los marca agent:running mientras corre — la señal de "corriendo"
// observable en GitHub que la proyección lee (projection.go runningLabel).
func (d *Dispatcher) fireChannel(ctx context.Context, gh GitHubDispatcher, repoURL, executor, prompt, model, issues string) (string, error) {
	switch executor {
	case "claude_action":
		// Disciplina de checkpoint para el runner efímero (R1 GitHub-edition +
		// el turno-que-espera-workers): refuerzo por prompt; el enforcement
		// real es el Rescue checkpoint del workflow (determinista).
		prompt = "IMPORTANT — ephemeral runner discipline: FIRST create your working branch and push it. " +
			"Commit AND push after completing EACH story (or any substantial unit of work) so progress survives " +
			"session limits. If you sense you are running out of session, push what is done and open the PR as " +
			"draft with a checklist of what remains. NEVER spawn background workers and end your turn waiting " +
			"for them — when your turn ends the session ENDS and unpushed work is lost. Open the PR BEFORE any " +
			"optional self-review pass.\n\n" + prompt
		inputs := map[string]string{"prompt": prompt}
		if issues != "" {
			inputs["issues"] = issues
		}
		if err := gh.DispatchWorkflow(ctx, repoURL, claudeWorkflowFile, "main", inputs); err != nil {
			return "", err
		}
		// El run concreto tarda en materializarse; el link estable es la página
		// de runs del workflow.
		return fmt.Sprintf("https://github.com/%s/actions/workflows/%s", repoSlug(repoURL), claudeWorkflowFile), nil
	default: // copilot
		url, err := gh.CreateAgentTask(ctx, repoURL, prompt, model)
		if err != nil {
			return "", err
		}
		if url == "" {
			url = "https://github.com/copilot/agents"
		}
		return url, nil
	}
}

// buildPrompt composes the agent prompt. Story mode points at the issue (its
// body already carries the full spec + ACs from the export); sprint mode is the
// goal-mode contract: every story, in order, one branch, ONE PR closing all.
func (d *Dispatcher) buildPrompt(projectID, repoURL, kind string, storyIDs []string) (string, error) {
	var b strings.Builder
	// Contexto del grafo producto↔código (task #5): el mapa de módulos ya
	// construidos, para que el agente reutilice en vez de crear estructura
	// paralela. Vacío en un proyecto nuevo → no ensucia el prompt.
	b.WriteString(d.projectContext(projectID))
	issueOf := func(st tickets.Story) int {
		if i := strings.LastIndex(st.ExternalRef, "#"); i >= 0 {
			if n, err := strconv.Atoi(st.ExternalRef[i+1:]); err == nil {
				return n
			}
		}
		return 0
	}
	if kind == "story" {
		st, err := d.Tickets.GetStory(storyIDs[0])
		if err != nil {
			return "", err
		}
		n := issueOf(st)
		fmt.Fprintf(&b, "Resolve issue #%d (%s — %s).\n\n", n, st.ID, st.Title)
		b.WriteString("Implement EXACTLY what the issue's acceptance criteria specify — every checkbox, nothing more. ")
		b.WriteString("Write honest tests that exercise each acceptance criterion. ")
		fmt.Fprintf(&b, "Open a pull request whose description includes `Closes #%d`.\n", n)
		return b.String(), nil
	}

	b.WriteString("# Goal mode — implement this entire sprint in ONE pass\n\n")
	b.WriteString("You are implementing a WHOLE SPRINT in a single run, on a single branch, that becomes ONE pull request. ")
	b.WriteString("Work through every story below IN THE ORDER GIVEN — they are in dependency order. ")
	b.WriteString("Do not open separate branches or PRs per story.\n\nStories (in order):\n\n")
	var closes []string
	for _, id := range storyIDs {
		st, err := d.Tickets.GetStory(id)
		if err != nil {
			return "", err
		}
		n := issueOf(st)
		closes = append(closes, fmt.Sprintf("Closes #%d", n))
		fmt.Fprintf(&b, "### %s — %s (issue #%d)\n%s\n", st.ID, st.Title, n, strings.TrimSpace(st.Body))
		if acs := strings.TrimSpace(st.Accept); acs != "" {
			fmt.Fprintf(&b, "\nAcceptance criteria:\n%s\n", acs)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "The pull request description MUST include: %s.\n", strings.Join(closes, ", "))
	b.WriteString("Every story's acceptance criteria must pass together; write honest tests per criterion.\n")
	return b.String(), nil
}

// issueNumbers devuelve los números de issue (del external_ref de cada story),
// coma-separados y en orden. El workflow claude.yml los recibe como input para
// marcarlos agent:running mientras corre (projection.go runningLabel). Una story
// sin external_ref resoluble se omite sin error.
func (d *Dispatcher) issueNumbers(storyIDs []string) string {
	var nums []string
	for _, id := range storyIDs {
		st, err := d.Tickets.GetStory(id)
		if err != nil {
			continue
		}
		if i := strings.LastIndex(st.ExternalRef, "#"); i >= 0 {
			if n, err := strconv.Atoi(st.ExternalRef[i+1:]); err == nil {
				nums = append(nums, strconv.Itoa(n))
			}
		}
	}
	return strings.Join(nums, ",")
}

// projectContext renderiza un mapa compacto de los módulos ya construidos en el
// proyecto (del grafo producto↔código, task #5), most-touched primero, para
// inyectarlo al inicio del prompt del agente. Es el "repo map" barato de Forja:
// no promete "los archivos exactos a tocar" (eso lo resuelve el agente con su
// búsqueda nativa), sino "esto ya existe y vive aquí — reutilízalo". Vacío en un
// proyecto sin merges todavía → "".
func (d *Dispatcher) projectContext(projectID string) string {
	if d.Tickets == nil {
		return ""
	}
	mods, err := d.Tickets.ModuleMap(projectID, 2)
	if err != nil || len(mods) == 0 {
		return ""
	}
	const maxMods = 12
	var b strings.Builder
	b.WriteString("## Existing modules (already shipped — reuse, don't duplicate)\n")
	b.WriteString("Paths prior stories already built, most-touched first. Prefer extending these over creating parallel structure; stay in your lane.\n\n")
	for i, m := range mods {
		if i >= maxMods {
			fmt.Fprintf(&b, "- …and %d more modules\n", len(mods)-maxMods)
			break
		}
		lanes := ""
		if len(m.Lanes) > 0 {
			lanes = " · " + strings.Join(m.Lanes, ", ")
		}
		fmt.Fprintf(&b, "- `%s/` — %d files%s\n", m.Dir, m.Files, lanes)
	}
	b.WriteString("\n")
	return b.String()
}

func dominantLane(members []tickets.Story) string {
	counts := map[string]int{}
	best, bestN := "", 0
	for _, st := range members {
		if st.Owner == "" {
			continue
		}
		counts[st.Owner]++
		if counts[st.Owner] > bestN {
			best, bestN = st.Owner, counts[st.Owner]
		}
	}
	return best
}

func sprintTitle(sid string, members []tickets.Story) string {
	return fmt.Sprintf("%s · %d stories", sid, len(members))
}

func sprintNum(id string) int {
	n, err := strconv.Atoi(strings.TrimFunc(id, func(r rune) bool { return r < '0' || r > '9' }))
	if err != nil {
		return 1 << 30
	}
	return n
}

func repoSlug(repoURL string) string {
	s := strings.TrimSuffix(repoURL, ".git")
	if i := strings.Index(s, "github.com/"); i >= 0 {
		return strings.Trim(s[i+len("github.com/"):], "/")
	}
	return s
}
