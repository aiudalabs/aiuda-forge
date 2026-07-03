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

// Projector mirrors GitHub state into the ticket store.
type Projector struct {
	Tickets *tickets.Store
	GH      GitHubReader

	// serializes syncs per repo so a webhook burst doesn't stampede gh.
	mu     sync.Mutex
	inFly  map[string]bool
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

	issues, err := p.GH.ListIssueStates(ctx, repoURL)
	if err != nil {
		return res, err
	}
	prs, err := p.GH.ListOpenPRs(ctx, repoURL)
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
				if target == tickets.StatusBacklog && sessions[st.ID] != "" {
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
		}
	}
	return res, nil
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
