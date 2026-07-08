// Package digest builds a short daily "standup" summary of a project's state and
// activity since the last digest: what progressed (stories done), what's blocking
// (stories waiting on unfinished deps, runs that retried/timed out), what's next
// (running work), and what it cost (token spend for the period). It gathers the
// facts deterministically from the ticket + control stores and (optionally) hands
// them to the Brain's LLM to phrase as a crisp standup; with no LLM it renders a
// deterministic four-section text. Both the Brain's daily_digest tool and the
// control-plane scheduler drive it.
package digest

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"forge/internal/store"
	"forge/internal/tickets"
)

// StorySource is the ticket-store surface the digest reads (satisfied by
// *tickets.Store). Stories carry their deps, so blockers derive from one call.
type StorySource interface {
	ListStoriesByProject(projectID string) ([]tickets.Story, error)
}

// RunSource is the control-store surface the digest reads (satisfied by
// *store.Store). Runs give the window watermark (UpdatedAt); tasks give the
// retry/timeout/failure signals for the "what's blocking" section.
type RunSource interface {
	ListRunsByProject(status store.Status, projectID string) ([]*store.Run, error)
	TasksForRun(runID string) ([]*store.Task, error)
}

// SpendFn returns the real token cost (USD) a project incurred since `since`
// (unix millis). Wired by the caller to billing (project → owner → workspace);
// nil means spend is unavailable and reported as $0.
type SpendFn func(projectID string, since int64) (float64, error)

// SynthesizeFn turns the factual, deterministic digest into a natural-language
// standup via an LLM. nil (or an error/empty return) falls back to the
// deterministic render, so the digest never depends on the model being up.
type SynthesizeFn func(ctx context.Context, system, user string) (string, error)

// Digester gathers and renders the digest. Stories + Runs are required; Spend and
// Synthesize are optional.
type Digester struct {
	Stories    StorySource
	Runs       RunSource
	Spend      SpendFn
	Synthesize SynthesizeFn
	Now        func() time.Time
}

// StoryRef is a compact story reference for a digest line.
type StoryRef struct {
	ID     string
	Title  string
	Owner  string
	Sprint string
}

// Blocker is a story that cannot start because it depends on unfinished work.
type Blocker struct {
	Story     StoryRef
	OnStories []string // dep ids not yet done
	OnSprints []string // sprints those deps live in (the "waiting for SPn" signal)
}

// ProblemRun is a run that retried, timed out, or failed in the window.
type ProblemRun struct {
	RunID    string
	Workflow string
	Reason   string // short human phrase, e.g. "step implement timed out" / "retried 2×"
}

// Data is the gathered, deterministic snapshot the render/synthesis consume.
type Data struct {
	ProjectID   string
	ProjectName string
	Since       time.Time // window start (last digest); zero = all-time
	Now         time.Time
	Done        []StoryRef // progressed to done/in_review within the window
	Running     []StoryRef // currently running (snapshot, not windowed)
	Failed      []StoryRef // failed within the window
	Blockers    []Blocker
	ProblemRuns []ProblemRun
	SpendUSD    float64
}

func (d *Digester) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

// Build gathers the data and returns the standup text — LLM-synthesized when a
// Synthesize func is set and succeeds, otherwise the deterministic render.
func (d *Digester) Build(ctx context.Context, projectID, projectName string, since time.Time) (string, error) {
	data, err := d.Gather(projectID, projectName, since)
	if err != nil {
		return "", err
	}
	deterministic := Render(data)
	if d.Synthesize == nil {
		return deterministic, nil
	}
	out, serr := d.Synthesize(ctx, synthesisSystem, deterministic)
	if serr != nil || strings.TrimSpace(out) == "" {
		return deterministic, nil // model down/empty → the facts still ship
	}
	return strings.TrimSpace(out), nil
}

// Gather collects the digest facts for a project over the window since `since`.
func (d *Digester) Gather(projectID, projectName string, since time.Time) (Data, error) {
	data := Data{ProjectID: projectID, ProjectName: projectName, Since: since, Now: d.now()}

	stories, err := d.Stories.ListStoriesByProject(projectID)
	if err != nil {
		return Data{}, fmt.Errorf("list stories: %w", err)
	}

	// Watermark map: run id → last transition time, so a done/failed story counts
	// as "in the window" when its run changed since the last digest.
	updatedByRun := map[string]int64{}
	if d.Runs != nil {
		runs, rerr := d.Runs.ListRunsByProject("", projectID)
		if rerr != nil {
			return Data{}, fmt.Errorf("list runs: %w", rerr)
		}
		for _, r := range runs {
			updatedByRun[r.ID] = r.UpdatedAt
		}
	}
	sinceMs := since.UnixMilli()
	inWindow := func(runID string) bool {
		if since.IsZero() {
			return true
		}
		return updatedByRun[runID] >= sinceMs
	}

	statusByID := map[string]tickets.Status{}
	sprintByID := map[string]string{}
	for _, s := range stories {
		statusByID[s.ID] = s.Status
		sprintByID[s.ID] = s.SprintID
	}

	for _, s := range stories {
		ref := StoryRef{ID: s.ID, Title: s.Title, Owner: s.Owner, Sprint: s.SprintID}
		switch s.Status {
		case tickets.StatusDone, tickets.StatusInReview:
			if inWindow(s.RunID) {
				data.Done = append(data.Done, ref)
			}
		case tickets.StatusRunning:
			data.Running = append(data.Running, ref) // snapshot: what's live now
		case tickets.StatusFailed:
			if inWindow(s.RunID) {
				data.Failed = append(data.Failed, ref)
			}
		case tickets.StatusBacklog:
			// A blocker is a not-yet-started story waiting on deps that aren't done —
			// the same derivation the board's waitingBySprint uses (cross-sprint deps
			// not done gate the dependent).
			if b, blocked := blocker(ref, s.Deps, statusByID, sprintByID); blocked {
				data.Blockers = append(data.Blockers, b)
			}
		}
	}

	data.ProblemRuns = d.problemRuns(projectID, sinceMs, since.IsZero())

	if d.Spend != nil {
		spend, serr := d.Spend(projectID, sinceMs)
		if serr == nil {
			data.SpendUSD = spend
		}
	}
	return data, nil
}

// blocker returns the Blocker for a story if any of its deps are not done.
func blocker(ref StoryRef, deps []string, statusByID map[string]tickets.Status, sprintByID map[string]string) (Blocker, bool) {
	b := Blocker{Story: ref}
	sprintSet := map[string]bool{}
	for _, dep := range deps {
		if statusByID[dep] == tickets.StatusDone {
			continue
		}
		b.OnStories = append(b.OnStories, dep)
		if sp := sprintByID[dep]; sp != "" && sp != ref.Sprint {
			sprintSet[sp] = true
		}
	}
	if len(b.OnStories) == 0 {
		return Blocker{}, false
	}
	for sp := range sprintSet {
		b.OnSprints = append(b.OnSprints, sp)
	}
	sort.Strings(b.OnSprints)
	return b, true
}

// problemRuns finds runs updated in the window whose tasks retried, timed out, or
// failed — the "what's blocking" operational signal.
func (d *Digester) problemRuns(projectID string, sinceMs int64, allTime bool) []ProblemRun {
	if d.Runs == nil {
		return nil
	}
	runs, err := d.Runs.ListRunsByProject("", projectID)
	if err != nil {
		return nil
	}
	var out []ProblemRun
	for _, r := range runs {
		if !allTime && r.UpdatedAt < sinceMs {
			continue
		}
		tasks, err := d.Runs.TasksForRun(r.ID)
		if err != nil {
			continue
		}
		if reason := diagnose(tasks); reason != "" {
			out = append(out, ProblemRun{RunID: r.ID, Workflow: r.WorkflowID, Reason: reason})
		}
	}
	return out
}

// diagnose returns a short reason if a run's tasks show trouble (a timeout, a
// retry, or a hard failure), or "" if the run is healthy. Timeouts and retries
// are called out specifically because they're the transient signals a standup
// cares about ("is something stuck?").
func diagnose(tasks []*store.Task) string {
	for _, t := range tasks {
		low := strings.ToLower(t.Error)
		if strings.Contains(low, "timeout") || strings.Contains(low, "timed out") || strings.Contains(low, "idle") {
			return "step " + t.StepID + " timed out"
		}
	}
	for _, t := range tasks {
		if t.Attempts > 1 {
			return fmt.Sprintf("step %s retried %d×", t.StepID, t.Attempts)
		}
	}
	for _, t := range tasks {
		if t.Status == store.StatusFailed {
			return "step " + t.StepID + " failed"
		}
	}
	return ""
}

const synthesisSystem = `You are a scrum master writing a SHORT daily standup for a software project.
You are given the factual digest below. Rewrite it as a crisp, friendly standup in Spanish.
Keep EXACTLY these four sections with these headers, in this order:
📈 Qué avanzó
🚧 Qué bloquea
⏭️ Qué sigue
💰 Cuánto costó
Be concise (a few bullet points per section max). Do not invent facts not present in the digest.`

// Render produces the deterministic four-section standup text from Data. It always
// emits all four sections (empty ones say "—") so the digest shape is stable.
func Render(d Data) string {
	var b strings.Builder
	name := d.ProjectName
	if name == "" {
		name = d.ProjectID
	}
	fmt.Fprintf(&b, "🗓️ Standup — %s\n", name)
	if !d.Since.IsZero() {
		fmt.Fprintf(&b, "Periodo: desde %s\n", d.Since.UTC().Format("2006-01-02 15:04 MST"))
	}
	b.WriteString("\n")

	b.WriteString("📈 Qué avanzó\n")
	if len(d.Done) == 0 {
		b.WriteString("— sin historias completadas en el periodo\n")
	} else {
		for _, s := range d.Done {
			b.WriteString(bullet(s))
		}
	}

	b.WriteString("\n🚧 Qué bloquea\n")
	if len(d.Blockers) == 0 && len(d.Failed) == 0 && len(d.ProblemRuns) == 0 {
		b.WriteString("— nada bloqueado\n")
	} else {
		for _, s := range d.Failed {
			fmt.Fprintf(&b, "• ❌ %s (%s) — falló\n", storyLabel(s), orDash(s.Owner))
		}
		for _, bl := range d.Blockers {
			wait := strings.Join(bl.OnSprints, ", ")
			if wait == "" {
				wait = strings.Join(bl.OnStories, ", ")
			}
			fmt.Fprintf(&b, "• ⧗ %s — esperando a %s\n", storyLabel(bl.Story), wait)
		}
		for _, pr := range d.ProblemRuns {
			fmt.Fprintf(&b, "• ⚠️ run %s (%s) — %s\n", pr.RunID, pr.Workflow, pr.Reason)
		}
	}

	b.WriteString("\n⏭️ Qué sigue\n")
	if len(d.Running) == 0 {
		b.WriteString("— nada en ejecución\n")
	} else {
		for _, s := range d.Running {
			b.WriteString(bullet(s))
		}
	}

	fmt.Fprintf(&b, "\n💰 Cuánto costó\n$%.2f en el periodo\n", d.SpendUSD)
	return b.String()
}

func bullet(s StoryRef) string {
	return fmt.Sprintf("• %s (%s)\n", storyLabel(s), orDash(s.Owner))
}

func storyLabel(s StoryRef) string {
	if s.Title == "" {
		return s.ID
	}
	return s.ID + " " + s.Title
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
