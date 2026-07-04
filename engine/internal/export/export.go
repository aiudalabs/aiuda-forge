// Package export publishes a project's native backlog to GitHub as first-class
// issues with native dependencies (F0 of the GitHub-native pivot, see
// docs/PLAN-2026-07-03-pivot-github-native.md). It is the mirror image of
// internal/imports: stories flow OUT, keyed by external_ref ("github:owner/repo#N")
// so re-exporting is idempotent — already-exported stories are skipped and a
// mid-run failure can simply be retried.
//
// The dependency edges use GitHub's native issue dependencies (blocked_by), which
// the console's kanban/graph and the conductor's ready-set read back. Labels carry
// the methodology vocabulary (sprint SPn, lane:<owner>, epic En) instead of Go code
// — a step toward the "kernel methodology-free" cleanup (D8).
package export

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"forge/internal/github"
	"forge/internal/tickets"
)

// GitHubWriter is the write surface the exporter needs. *github.Client implements
// it; tests inject a fake.
type GitHubWriter interface {
	EnsureLabel(ctx context.Context, repoURL, name, color string) (bool, error)
	CreateIssue(ctx context.Context, repoURL, title, body string, labels []string) (github.CreatedIssue, error)
	IssueID(ctx context.Context, repoURL string, number int) (int64, error)
	AddIssueBlockedBy(ctx context.Context, repoURL string, issueNumber int, blockedByID int64) (bool, error)
}

// Result reports what an export did. JSON tags are the console contract.
type Result struct {
	Repo          string `json:"repo"`
	IssuesCreated int    `json:"issues_created"`
	IssuesSkipped int    `json:"issues_skipped"` // already exported (external_ref set)
	LabelsCreated int    `json:"labels_created"`
	DepsCreated   int    `json:"deps_created"`
	DepsSkipped   int    `json:"deps_skipped"` // edge already existed, or endpoint missing
}

// Label colors: sprint blue, lane green, epic purple (mirrors the PoC).
const (
	colorSprint = "1d76db"
	colorLane   = "0e8a16"
	colorEpic   = "5319e7"
)

// writeDelay spaces GitHub writes to stay clear of secondary rate limits (the PoC
// used 300-400ms for 44 issues + 73 edges without incident).
var writeDelay = 250 * time.Millisecond

// GitHubBacklog exports projectID's stories to repoURL. Idempotent per story via
// external_ref. Returns the summary AND an error when it stopped midway — the
// partial counts are meaningful (retry continues where it left off).
func GitHubBacklog(ctx context.Context, store *tickets.Store, gh GitHubWriter, projectID, repoURL string) (Result, error) {
	res := Result{Repo: repoURL}
	slug, err := github.Slug(repoURL)
	if err != nil {
		return res, fmt.Errorf("resolve repo slug: %w", err)
	}
	stories, err := store.ListStoriesByProject(projectID)
	if err != nil {
		return res, err
	}
	if len(stories) == 0 {
		return res, fmt.Errorf("project %s has no stories to export", projectID)
	}
	sort.Slice(stories, func(i, j int) bool { return stories[i].ID < stories[j].ID })

	// Grafo producto↔código (task #5, hook F2): prior art por lane para enriquecer
	// el cuerpo del issue. En el PRIMER export el grafo está vacío → nil → los
	// cuerpos salen idénticos (sin sección de prior art); en iteraciones ya trae
	// los módulos que la lane construyó, para reutilizarlos.
	mods, _ := store.ModuleMap(projectID, 2)

	// Pass 0 — labels (sprint / lane / epic), deduped.
	seen := map[string]bool{}
	for _, st := range stories {
		for name, color := range labelsFor(st) {
			if seen[name] {
				continue
			}
			seen[name] = true
			created, err := gh.EnsureLabel(ctx, repoURL, name, color)
			if err != nil {
				return res, fmt.Errorf("label %q: %w", name, err)
			}
			if created {
				res.LabelsCreated++
			}
			pause(ctx)
		}
	}

	// Pass 1 — issues. Track number+database-id per story for the deps pass.
	type ref struct {
		number int
		id     int64 // 0 = not fetched yet (pre-existing export)
	}
	byStory := make(map[string]*ref, len(stories))
	for _, st := range stories {
		if st.ExternalRef != "" {
			// Already exported: recover the issue number from the ref; the database
			// id is fetched lazily only if a dependency needs it.
			if n, ok := numberFromRef(st.ExternalRef, slug); ok {
				byStory[st.ID] = &ref{number: n}
			}
			res.IssuesSkipped++
			continue
		}
		created, err := gh.CreateIssue(ctx, repoURL, issueTitle(st), issueBody(st, priorArtForLane(st.Owner, mods)), labelNames(st))
		if err != nil {
			return res, fmt.Errorf("issue %s: %w", st.ID, err)
		}
		// Record the mirror BEFORE moving on: if this write fails we stop, because
		// continuing without the ref would duplicate the issue on retry.
		extRef := fmt.Sprintf("github:%s#%d", slug, created.Number)
		if err := store.SetStoryExternalRef(st.ID, extRef); err != nil {
			return res, fmt.Errorf("issue %s created as #%d but recording external_ref failed: %w", st.ID, created.Number, err)
		}
		byStory[st.ID] = &ref{number: created.Number, id: created.ID}
		res.IssuesCreated++
		pause(ctx)
	}

	// Pass 2 — native dependency edges. A story is BLOCKED BY its deps; the
	// endpoint wants the blocker's database id.
	for _, st := range stories {
		target := byStory[st.ID]
		if target == nil {
			continue
		}
		for _, dep := range st.Deps {
			blocker := byStory[dep]
			if blocker == nil {
				res.DepsSkipped++ // dep outside this project/export — nothing to wire
				continue
			}
			if blocker.id == 0 {
				id, err := gh.IssueID(ctx, repoURL, blocker.number)
				if err != nil {
					return res, fmt.Errorf("dep %s<-%s: fetch blocker id: %w", st.ID, dep, err)
				}
				blocker.id = id
			}
			created, err := gh.AddIssueBlockedBy(ctx, repoURL, target.number, blocker.id)
			if err != nil {
				return res, fmt.Errorf("dep %s<-%s: %w", st.ID, dep, err)
			}
			if created {
				res.DepsCreated++
			} else {
				res.DepsSkipped++
			}
			pause(ctx)
		}
	}
	return res, nil
}

func labelsFor(st tickets.Story) map[string]string {
	out := map[string]string{}
	if st.SprintID != "" {
		out[st.SprintID] = colorSprint
	}
	if st.Owner != "" {
		out["lane:"+st.Owner] = colorLane
	}
	if st.EpicID != "" {
		out[st.EpicID] = colorEpic
	}
	return out
}

func labelNames(st tickets.Story) []string {
	var out []string
	for name := range labelsFor(st) {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func issueTitle(st tickets.Story) string {
	return fmt.Sprintf("%s — %s", st.ID, st.Title)
}

// issueBody composes the issue body: user story + acceptance criteria as a
// checklist + optional "prior art" (task #5, the F2 enrichment hook) + a
// metadata footer. priorArt is the pre-rendered block of modules the story's
// lane already built (empty on a fresh project → nothing extra emitted).
func issueBody(st tickets.Story, priorArt string) string {
	var b strings.Builder
	if body := strings.TrimSpace(st.Body); body != "" {
		b.WriteString(body)
		b.WriteString("\n\n")
	}
	if acs := acceptanceLines(st.Accept); len(acs) > 0 {
		b.WriteString("### Acceptance criteria\n")
		for _, ac := range acs {
			fmt.Fprintf(&b, "- [ ] %s\n", ac)
		}
		b.WriteString("\n")
	}
	if priorArt != "" {
		b.WriteString(priorArt)
	}
	b.WriteString("---\n")
	meta := []string{"`" + st.ID + "`"}
	if st.SprintID != "" {
		meta = append(meta, "sprint `"+st.SprintID+"`")
	}
	if st.EpicID != "" {
		meta = append(meta, "epic `"+st.EpicID+"`")
	}
	if st.Owner != "" {
		meta = append(meta, "lane `"+st.Owner+"`")
	}
	b.WriteString(strings.Join(meta, " · "))
	b.WriteString("\n\n_Exported by aiuda-forge Studio_\n")
	return b.String()
}

// priorArtForLane renderiza el bloque "Prior art" del cuerpo del issue: los
// módulos que la lane de la story YA construyó (del grafo producto↔código), para
// que el agente los extienda en vez de crear estructura paralela. Devuelve "" si
// la lane no ha tocado nada todavía (proyecto nuevo o primera story de la lane),
// así el primer export no cambia. Máximo 8 módulos para no inflar el cuerpo.
func priorArtForLane(owner string, mods []tickets.ModuleHit) string {
	if owner == "" || len(mods) == 0 {
		return ""
	}
	var rows []string
	for _, m := range mods {
		mine := false
		for _, l := range m.Lanes {
			if l == owner {
				mine = true
				break
			}
		}
		if !mine {
			continue
		}
		rows = append(rows, fmt.Sprintf("- `%s/` — %d files (%s)", m.Dir, m.Files, strings.Join(m.Stories, ", ")))
		if len(rows) >= 8 {
			break
		}
	}
	if len(rows) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("### Prior art (your lane already built these — extend, don't duplicate)\n")
	for _, r := range rows {
		b.WriteString(r)
		b.WriteString("\n")
	}
	b.WriteString("\n")
	return b.String()
}

func acceptanceLines(accept string) []string {
	var out []string
	for _, l := range strings.Split(accept, "\n") {
		l = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(l), "-*• "))
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

// numberFromRef parses "github:owner/repo#N" and checks it points at slug.
func numberFromRef(ref, slug string) (int, bool) {
	rest, ok := strings.CutPrefix(ref, "github:")
	if !ok {
		return 0, false
	}
	repoPart, numPart, ok := strings.Cut(rest, "#")
	if !ok || !strings.EqualFold(repoPart, slug) {
		return 0, false
	}
	var n int
	if _, err := fmt.Sscanf(numPart, "%d", &n); err != nil {
		return 0, false
	}
	return n, true
}

func pause(ctx context.Context) {
	select {
	case <-ctx.Done():
	case <-time.After(writeDelay):
	}
}
