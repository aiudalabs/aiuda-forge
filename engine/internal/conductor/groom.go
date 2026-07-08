package conductor

// JIT grooming — recover BMAD's level-2 backlog on the GitHub-native path. The
// scrum-master emits a LIGHT skeleton per story (title + user-story + ACs); the
// dispatch used to ship that skeleton raw, so agents built generic UIs with no
// visual target and re-created components that already existed
// (docs/ANALYSIS-2026-07-08-calidad-ui-y-verificacion.md). Here, right before a
// story is dispatched, we run the story-detailer (the existing backend agent,
// on the host, NO sandbox — same wiring as the design runner) to expand the
// skeleton into a dev-ready spec, and enrich the issue body with three sections:
// the spec, a visual spec (mockup + UI_SCREENS extract, frontend only), and the
// sibling-component inventory. All of it is best-effort: a detailer timeout or a
// missing doc degrades the body (drops that section) and NEVER blocks the
// dispatch. VIBEFORGE_CONDUCTOR_GROOM=0 leaves the Groomer unwired → the body is
// byte-identical to the plain export.

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"forge/internal/agent"
	"forge/internal/export"
	"forge/internal/github"
	"forge/internal/tickets"
	"gopkg.in/yaml.v3"
)

// groomAgentID is the registry agent that expands a skeleton into a dev-ready
// spec (BMAD create-story, level 2). It already exists (registry/agents/
// story-detailer.{yaml,md}); until now it only ran in the legacy factory.
const groomAgentID = "story-detailer"

// groomRef is the branch the canonical design docs live on. Trunk-based: design
// merges docs/ to main before handoff (design.yaml docs_pr → main); `dev` is
// vestigial. The conductor already treats main as the doc source (docsOnMain).
const groomRef = "main"

// groomDocs are the design docs fetched into the detailer's temp working tree.
// The three the task mandates plus the two the frontend spec needs; each is
// best-effort (a missing doc is simply not written — the persona tolerates it).
var groomDocs = []string{
	"docs/PRD.md",
	"docs/ARCHITECTURE.md",
	"docs/DATA_MODEL.md",
	"docs/UI_SCREENS.md",
	"docs/DESIGN_SYSTEM.md",
}

// GroomGitHub is the tenant-scoped GitHub surface grooming needs: read the design
// docs + backlog, list the repo tree (existing components), and PATCH the issue
// body. *github.Client implements it; tests inject a fake.
type GroomGitHub interface {
	ReadFile(ctx context.Context, repoURL, path, ref string) (string, error)
	ListContents(ctx context.Context, repoURL, dir, ref string) ([]github.DocEntry, error)
	UpdateIssueBody(ctx context.Context, repoURL string, number int, body string) error
}

// Groomer performs the pre-dispatch enrichment. It is self-contained: it resolves
// its own tenant client and reads the ticket store, so the Dispatcher only has to
// call GroomStories. nil Groomer = grooming off.
type Groomer struct {
	Backend agent.Backend // the story-detailer engine (claude -p in prod, fake in tests)
	Agents  agent.Loader  // registry loader → story-detailer persona + model + tools
	Auth    agent.Auth
	// Timeout bounds ONE story-detailer call — the dispatch blocks on grooming so
	// the enriched body is live before the agent reads the issue, and this caps that
	// block. 0 → 10m.
	Timeout time.Duration
	// ClientFor resolves the per-project (tenant) GitHub surface. Injected by app;
	// nil → grooming is skipped. Returns a nil interface when there is no client.
	ClientFor func(ctx context.Context, projectID string) GroomGitHub
	// Tickets is the (read-only) ticket store: story bodies + the module map for
	// prior art. nil → grooming is skipped.
	Tickets *tickets.Store
}

func (g *Groomer) timeout() time.Duration {
	if g.Timeout > 0 {
		return g.Timeout
	}
	return 10 * time.Minute
}

// GroomStories enriches the issue body of each story about to be dispatched. Fully
// best-effort: any failure is logged and skipped, never returned — grooming must
// not block or fail a dispatch.
func (g *Groomer) GroomStories(ctx context.Context, projectID, repoURL string, storyIDs []string) {
	if g == nil || g.ClientFor == nil || g.Tickets == nil || repoURL == "" {
		return
	}
	repo := g.ClientFor(ctx, projectID)
	if repo == nil {
		return
	}
	keys := g.screenKeys(ctx, repo, repoURL)
	mods, _ := g.Tickets.ModuleMap(projectID, 2)
	for _, id := range storyIDs {
		st, err := g.Tickets.GetStory(id)
		if err != nil {
			log.Printf("conductor groom(%s): load story %s: %v", projectID, id, err)
			continue
		}
		num := issueNumberOf(st)
		if num == 0 {
			continue // not mirrored to a GitHub issue → nothing to enrich
		}
		if err := g.GroomIssue(ctx, repo, repoURL, st, num, keys[st.ID], export.PriorArtForLane(st.Owner, mods)); err != nil {
			log.Printf("conductor groom(%s): story %s: %v", projectID, id, err)
		}
	}
}

// GroomIssue computes the three enrichment sections for ONE story and PATCHes them
// onto its issue body. Each section is independent and best-effort: the spec is
// dropped if the detailer fails (the degrade path), the visual section only exists
// for a story with a screen_key, and components only when the lane has a known dir
// with entries. Returns an error only for the PATCH itself (so the caller logs it);
// a detailer failure is logged here and degrades rather than erroring out.
func (g *Groomer) GroomIssue(ctx context.Context, repo GroomGitHub, repoURL string, st tickets.Story, issueNum int, screenKey, priorArt string) error {
	spec, err := g.detailStory(ctx, repo, repoURL, st)
	if err != nil {
		// Transient/timeout/limit: degrade — keep the other sections, drop Spec, warn.
		log.Printf("conductor groom: story-detailer failed for %s (issue #%d) — dispatching without the Spec section: %v", st.ID, issueNum, err)
	}

	enrich := export.Enrichment{
		SpecDevReady: export.SpecSection(spec),
		VisualSpec:   export.VisualSpecSection(screenKey, mockupRawURL(repoURL, screenKey), g.uiExtract(ctx, repo, repoURL, screenKey)),
		Components:   export.ComponentsSection(g.componentPaths(ctx, repo, repoURL, st.Owner)),
	}
	if (export.Enrichment{}) == enrich {
		return nil // nothing to add (backend story, no spec, no components) → leave the issue as exported
	}
	return repo.UpdateIssueBody(ctx, repoURL, issueNum, export.EnrichedBody(st, priorArt, enrich))
}

// detailStory runs the story-detailer over a temp working tree holding the fetched
// design docs (paths under docs/, like a checkout) and returns its dev-ready spec.
// The agent runs on the host with its Read tool — NO sandbox (design-phase wiring).
func (g *Groomer) detailStory(ctx context.Context, repo GroomGitHub, repoURL string, st tickets.Story) (string, error) {
	if g.Backend == nil || g.Agents == nil {
		return "", fmt.Errorf("groomer not fully wired (backend/loader)")
	}
	m, err := g.Agents.Load(groomAgentID)
	if err != nil {
		return "", fmt.Errorf("load %s: %w", groomAgentID, err)
	}

	dir, err := os.MkdirTemp("", "groom-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	for _, path := range groomDocs {
		content, rerr := repo.ReadFile(ctx, repoURL, path, groomRef)
		if rerr != nil {
			continue // best-effort: a missing doc is fine (persona degrades on it)
		}
		full := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			continue
		}
		_ = os.WriteFile(full, []byte(content), 0o644)
	}

	cctx, cancel := context.WithTimeout(ctx, g.timeout())
	defer cancel()
	res, err := g.Backend.Run(cctx, groomPrompt(st), agent.Options{
		Model:        m.Model,
		AllowedTools: m.AllowedTools(),
		SystemPrompt: m.Persona,
		Workdir:      dir,
		Timeout:      g.timeout(),
		Auth:         g.Auth,
	}, nil)
	if err != nil {
		return "", err
	}
	if !res.Success {
		return "", fmt.Errorf("story-detailer reported failure")
	}
	if strings.TrimSpace(res.Text) == "" {
		return "", fmt.Errorf("story-detailer returned empty spec")
	}
	return res.Text, nil
}

// groomPrompt is the user turn for the detailer: the story skeleton + a pointer to
// the docs in the working tree. The persona (system prompt) owns the method; this
// only supplies the skeleton and the lane.
func groomPrompt(st tickets.Story) string {
	var b strings.Builder
	b.WriteString("Expand this ONE backlog story into a complete, dev-ready specification.\n\n")
	b.WriteString("skeleton:\n")
	fmt.Fprintf(&b, "- id: %s\n", st.ID)
	fmt.Fprintf(&b, "- title: %s\n", st.Title)
	if st.Owner != "" {
		fmt.Fprintf(&b, "- lane/owner: %s\n", st.Owner)
	}
	if body := strings.TrimSpace(st.Body); body != "" {
		fmt.Fprintf(&b, "- user story:\n%s\n", body)
	}
	if acc := strings.TrimSpace(st.Accept); acc != "" {
		fmt.Fprintf(&b, "- acceptance criteria:\n%s\n", acc)
	}
	b.WriteString("\nThe project's design docs are in the working tree — read what you need with your read tool: ")
	b.WriteString("docs/PRD.md, docs/ARCHITECTURE.md, docs/DATA_MODEL.md")
	b.WriteString(" (and docs/UI_SCREENS.md + docs/DESIGN_SYSTEM.md for a frontend story).\n")
	b.WriteString("Your reply IS the ticket: output ONLY the dev-ready spec for THIS story.\n")
	return b.String()
}

// screenKeys maps story id → screen_key by parsing docs/backlog.yaml from the repo.
// The store does not persist screen_key (the scrum-master emits it but nothing
// consumed it, and tickets/ is out of scope), so the committed backlog is the
// source. Empty map on any failure (no visual sections → graceful).
func (g *Groomer) screenKeys(ctx context.Context, repo GroomGitHub, repoURL string) map[string]string {
	out := map[string]string{}
	raw, err := repo.ReadFile(ctx, repoURL, "docs/backlog.yaml", groomRef)
	if err != nil {
		return out
	}
	var bf struct {
		Stories []struct {
			ID        string `yaml:"id"`
			ScreenKey string `yaml:"screen_key"`
		} `yaml:"stories"`
	}
	if err := yaml.Unmarshal([]byte(raw), &bf); err != nil {
		return out
	}
	for _, s := range bf.Stories {
		if s.ID != "" && s.ScreenKey != "" {
			out[s.ID] = s.ScreenKey
		}
	}
	return out
}

// uiExtract returns the docs/UI_SCREENS.md section for screenKey (matched by
// heading), or "" (no screen_key, no doc, or no matching heading).
func (g *Groomer) uiExtract(ctx context.Context, repo GroomGitHub, repoURL, screenKey string) string {
	if screenKey == "" {
		return ""
	}
	raw, err := repo.ReadFile(ctx, repoURL, "docs/UI_SCREENS.md", groomRef)
	if err != nil {
		return ""
	}
	return extractHeadingSection(raw, screenKey)
}

// componentPaths lists the components already built in the destination repo for
// this lane (lib/widgets/ for flutter, src/components/ for react), one level deep,
// capped. Empty for other lanes or when the dir is absent/empty. The rendered
// section tells the agent to REUSE before creating.
func (g *Groomer) componentPaths(ctx context.Context, repo GroomGitHub, repoURL, owner string) []string {
	dir := laneComponentDir(owner)
	if dir == "" {
		return nil
	}
	const maxPaths = 40
	var out []string
	entries, err := repo.ListContents(ctx, repoURL, dir, groomRef)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if len(out) >= maxPaths {
			break
		}
		if e.Type == "dir" {
			// Recurse ONE level so grouped components (e.g. src/components/ui/*) show up.
			sub, serr := repo.ListContents(ctx, repoURL, e.Path, groomRef)
			if serr != nil {
				continue
			}
			for _, s := range sub {
				if len(out) >= maxPaths {
					break
				}
				if s.Type == "file" {
					out = append(out, s.Path)
				}
			}
			continue
		}
		out = append(out, e.Path)
	}
	return out
}

// laneComponentDir maps a story's lane to the repo dir where its components live.
// "" for lanes without a component convention (backend, shared primitives).
func laneComponentDir(owner string) string {
	l := strings.ToLower(owner)
	switch {
	case strings.Contains(l, "flutter"):
		return "lib/widgets"
	case strings.Contains(l, "react"):
		return "src/components"
	default:
		return ""
	}
}

// mockupRawURL is the raw link to the story's mockup, per the task:
// docs/mockups/<screen_key>.html on the default branch. "" when there is no
// screen_key or the repo slug can't be resolved.
func mockupRawURL(repoURL, screenKey string) string {
	if screenKey == "" {
		return ""
	}
	slug := repoSlug(repoURL)
	if slug == "" || !strings.Contains(slug, "/") {
		return ""
	}
	return fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/docs/mockups/%s.html", slug, groomRef, screenKey)
}

// extractHeadingSection returns the markdown block under the first ATX heading
// whose title matches key (case-insensitive), up to the next heading of the same
// or shallower level. Matching is fuzzy — key ("customer.catalog"), its dotted
// form as words ("customer catalog"), and its last segment ("catalog") are all
// tried — because UI_SCREENS headings carry titles/ids, not the raw screen_key.
// Capped so a huge section can't bloat the issue body.
func extractHeadingSection(md, key string) string {
	const maxLen = 3000
	lines := strings.Split(md, "\n")
	needles := headingNeedles(key)
	start, level := -1, 0
	for i, ln := range lines {
		lv, title := atxHeading(ln)
		if lv == 0 {
			continue
		}
		lower := strings.ToLower(title)
		for _, n := range needles {
			if strings.Contains(lower, n) {
				start, level = i, lv
				break
			}
		}
		if start >= 0 {
			break
		}
	}
	if start < 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(strings.TrimSpace(lines[start]))
	b.WriteString("\n")
	for _, ln := range lines[start+1:] {
		if lv, _ := atxHeading(ln); lv > 0 && lv <= level {
			break
		}
		b.WriteString(ln)
		b.WriteString("\n")
	}
	out := strings.TrimSpace(b.String())
	if len(out) > maxLen {
		out = out[:maxLen] + "\n…(recortado)"
	}
	return out
}

// headingNeedles derives the fuzzy match terms for a screen_key, longest first so
// the most specific match wins.
func headingNeedles(key string) []string {
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "" {
		return nil
	}
	needles := []string{key}
	if strings.Contains(key, ".") {
		needles = append(needles, strings.ReplaceAll(key, ".", " "))
		if segs := strings.Split(key, "."); len(segs) > 0 {
			needles = append(needles, segs[len(segs)-1])
		}
	}
	return needles
}

// atxHeading reports the level (1-6) and title of an ATX markdown heading line, or
// (0, "") if the line is not a heading.
func atxHeading(line string) (int, string) {
	s := strings.TrimSpace(line)
	if !strings.HasPrefix(s, "#") {
		return 0, ""
	}
	n := 0
	for n < len(s) && s[n] == '#' {
		n++
	}
	if n == 0 || n > 6 || n >= len(s) || s[n] != ' ' {
		return 0, ""
	}
	return n, strings.TrimSpace(s[n+1:])
}

// issueNumberOf parses the GitHub issue number from a story's external_ref
// ("github:owner/repo#N"). 0 when absent/unparseable.
func issueNumberOf(st tickets.Story) int {
	if i := strings.LastIndex(st.ExternalRef, "#"); i >= 0 {
		if n, err := strconv.Atoi(st.ExternalRef[i+1:]); err == nil {
			return n
		}
	}
	return 0
}
