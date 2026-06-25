// Package orchestrator polls GitHub Issues for a target repo, computes the
// READY set (open issues whose dependencies are all DONE), fires runs on the
// control-plane for each READY issue, and exposes GET /tickets so the UI can
// mirror the status.
//
// Interfaces (GitHub, ControlPlane) keep all external I/O injectable so tests
// are fully hermetic: no network, no real gh, no LLM.
package orchestrator

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"
)

// TicketStatus is the computed state of a GitHub Issue from the orchestrator's
// point of view.
type TicketStatus string

const (
	StatusOpen    TicketStatus = "open"
	StatusBlocked TicketStatus = "blocked"
	StatusReady   TicketStatus = "ready"
	StatusFiring  TicketStatus = "firing"
	StatusDone    TicketStatus = "done"
	StatusFailed  TicketStatus = "failed"
)

// Issue is the orchestrator's view of a GitHub Issue.
type Issue struct {
	Number int      `json:"number"`
	Title  string   `json:"title"`
	Body   string   `json:"body"`
	State  string   `json:"state"`  // "open" | "closed"
	Labels []string `json:"labels"` // raw label names
}

// Ticket is an Issue enriched with computed orchestrator status.
type Ticket struct {
	ID     int          `json:"id"`
	Title  string       `json:"title"`
	Status TicketStatus `json:"status"`
	Deps   []int        `json:"deps"`
	RunID  string       `json:"run_id,omitempty"`
}

// GitHub is the interface through which the orchestrator reads issues.
// The real implementation shells out to `gh`; tests use a fake.
type GitHub interface {
	ListIssues(ctx context.Context) ([]Issue, error)
}

// ControlPlane is the interface through which the orchestrator fires runs and
// checks their progress. The real implementation talks HTTP to the
// control-plane; tests use a fake.
type ControlPlane interface {
	FireRun(ctx context.Context, workflow string, payload any) (runID string, err error)
	// RunStatus returns the current status of a run ("RUNNING", "DONE",
	// "FAILED", …) so the orchestrator can advance a ticket once its run finishes.
	RunStatus(ctx context.Context, runID string) (status string, err error)
	// ExecutionUnit returns the configured execution_unit ("sprint"|"story") from
	// the control-plane settings. The native scheduler reads it each cycle to pick
	// sprint-batched (goal mode) vs per-story execution.
	ExecutionUnit(ctx context.Context) (string, error)
	// MergeMode returns the configured merge_mode ("manual"|"auto") from the
	// control-plane settings. The native scheduler reads it each cycle to decide
	// whether to merge an in-review PR itself ("auto") or wait for a human merge.
	MergeMode(ctx context.Context) (string, error)
	// RunPRURL returns the pull-request URL a run opened (parsed from its pr step's
	// detail, "pr(github): opened <url>"). Empty (with nil error) when the run has
	// no GitHub PR yet — e.g. a local-mode pr step that only pushed a branch.
	RunPRURL(ctx context.Context, runID string) (string, error)
}

// Config holds tunables for the orchestrator loop.
type Config struct {
	Workflow     string        // workflow to fire (required)
	PollInterval time.Duration // default 10s
	StateFile    string        // default ./orchestrator-state.json
}

func (c *Config) setDefaults() {
	if c.PollInterval == 0 {
		c.PollInterval = 10 * time.Second
	}
	if c.StateFile == "" {
		c.StateFile = "./orchestrator-state.json"
	}
}

// Orchestrator is the core engine.
type Orchestrator struct {
	cfg   Config
	gh    GitHub
	cp    ControlPlane
	state *State
}

// New builds an Orchestrator. Call RunOnce or Run to start it.
func New(cfg Config, gh GitHub, cp ControlPlane) (*Orchestrator, error) {
	cfg.setDefaults()
	if cfg.Workflow == "" {
		return nil, fmt.Errorf("orchestrator: Workflow is required")
	}
	st, err := LoadState(cfg.StateFile)
	if err != nil {
		return nil, fmt.Errorf("orchestrator: load state: %w", err)
	}
	return &Orchestrator{cfg: cfg, gh: gh, cp: cp, state: st}, nil
}

// RunOnce executes a single poll-and-fire cycle and returns.
func (o *Orchestrator) RunOnce(ctx context.Context) error {
	issues, err := o.gh.ListIssues(ctx)
	if err != nil {
		return fmt.Errorf("list issues: %w", err)
	}

	byNumber := make(map[int]*Issue, len(issues))
	for i := range issues {
		byNumber[issues[i].Number] = &issues[i]
	}

	// Reconcile finished runs FIRST so a dependent can unblock in the same cycle
	// its dependency completes.
	o.reconcileCompletions(ctx, issues)

	for i := range issues {
		issue := &issues[i]
		if err := o.processIssue(ctx, issue, byNumber); err != nil {
			log.Printf("orchestrator: issue #%d: %v", issue.Number, err)
		}
	}
	return nil
}

// reconcileCompletions checks every fired-but-not-settled issue's run and
// advances the ticket based on the run's terminal state:
//   - DONE → mark completed (unblocks dependents)
//   - FAILED / CANCELLED → mark failed (surfaces in GET /tickets; stops re-polling)
//
// This advances a ticket from "firing" to "done" or "failed" without needing
// the GitHub issue to be closed by hand.
func (o *Orchestrator) reconcileCompletions(ctx context.Context, issues []Issue) {
	for i := range issues {
		n := issues[i].Number
		if !o.state.IsFired(n) || o.state.IsCompleted(n) || o.state.IsFailed(n) {
			continue
		}
		runID := o.state.RunID(n)
		if runID == "" {
			continue
		}
		status, err := o.cp.RunStatus(ctx, runID)
		if err != nil {
			log.Printf("orchestrator: run status #%d (%s): %v", n, runID, err)
			continue
		}
		switch status {
		case "DONE":
			if err := o.state.MarkCompleted(n); err != nil {
				log.Printf("orchestrator: mark completed #%d: %v", n, err)
				continue
			}
			log.Printf("orchestrator: issue #%d run %s DONE — ticket completed", n, runID)
		case "FAILED", "CANCELLED":
			if err := o.state.MarkFailed(n); err != nil {
				log.Printf("orchestrator: mark failed #%d: %v", n, err)
				continue
			}
			log.Printf("orchestrator: issue #%d run %s %s — ticket failed", n, runID, status)
		}
		// In-progress statuses (RUNNING, QUEUED, AWAITING, …): leave it alone.
	}
}

// Run loops forever, calling RunOnce at the configured interval until ctx is
// cancelled. It backs off (doubles the sleep, up to 5× base) when an idle
// cycle fires nothing new.
func (o *Orchestrator) Run(ctx context.Context) {
	interval := o.cfg.PollInterval
	maxInterval := o.cfg.PollInterval * 5
	for {
		firedBefore := o.state.FiredCount()
		if err := o.RunOnce(ctx); err != nil {
			log.Printf("orchestrator: poll error: %v", err)
		}
		// Back off when idle; reset when something was fired.
		if o.state.FiredCount() > firedBefore {
			interval = o.cfg.PollInterval
		} else if interval < maxInterval {
			interval *= 2
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

// Tickets returns the current enriched ticket list for GET /tickets.
func (o *Orchestrator) Tickets(ctx context.Context) ([]Ticket, error) {
	issues, err := o.gh.ListIssues(ctx)
	if err != nil {
		return nil, err
	}
	byNumber := make(map[int]*Issue, len(issues))
	for i := range issues {
		byNumber[issues[i].Number] = &issues[i]
	}

	tickets := make([]Ticket, 0, len(issues))
	for i := range issues {
		issue := &issues[i]
		deps := parseDeps(issue)
		if deps == nil {
			deps = []int{} // emit [] not null — arrays stay arrays in the API
		}
		status := o.computeStatus(issue, deps, byNumber)
		t := Ticket{
			ID:     issue.Number,
			Title:  issue.Title,
			Status: status,
			Deps:   deps,
		}
		if runID := o.state.RunID(issue.Number); runID != "" {
			t.RunID = runID
		}
		tickets = append(tickets, t)
	}
	return tickets, nil
}

// processIssue fires a run for issue if it is READY (open, all deps DONE) and
// has not already been fired.
func (o *Orchestrator) processIssue(ctx context.Context, issue *Issue, byNumber map[int]*Issue) error {
	if issue.State != "open" {
		return nil
	}
	if o.state.IsFired(issue.Number) {
		return nil
	}

	deps := parseDeps(issue)
	if !o.depsAllDone(deps, byNumber) {
		return nil
	}

	// All deps are DONE — fire.
	log.Printf("orchestrator: firing run for issue #%d: %s", issue.Number, issue.Title)
	payload := map[string]any{
		"ticket": issue.Title + "\n\n" + issue.Body,
		"issue":  issue.Number,
	}
	runID, err := o.cp.FireRun(ctx, o.cfg.Workflow, payload)
	if err != nil {
		return fmt.Errorf("fire run: %w", err)
	}

	if err := o.state.MarkFired(issue.Number, runID); err != nil {
		return fmt.Errorf("save state: %w", err)
	}
	log.Printf("orchestrator: issue #%d fired — run %s", issue.Number, runID)
	return nil
}

// depsAllDone returns true when every dep number is DONE.
//
// A dep is DONE when:
//   - GitHub reports its state as "closed", OR
//   - This orchestrator has marked it as completed (run finished).
//
// NOTE: "fired" alone is not sufficient — the run may still be in progress.
// Only a GitHub close or an explicit completion mark counts. This keeps
// within-cycle ordering safe: firing #1 in the same cycle as evaluating #2
// does not prematurely unblock #2.
func (o *Orchestrator) depsAllDone(deps []int, byNumber map[int]*Issue) bool {
	for _, dep := range deps {
		issue, ok := byNumber[dep]
		if !ok {
			// Unknown dep — treat as not done (conservative).
			return false
		}
		if issue.State == "closed" || o.state.IsCompleted(dep) {
			continue
		}
		return false
	}
	return true
}

// computeStatus derives the TicketStatus for a ticket from its issue state and
// the dep graph.
func (o *Orchestrator) computeStatus(issue *Issue, deps []int, byNumber map[int]*Issue) TicketStatus {
	if issue.State == "closed" || o.state.IsCompleted(issue.Number) {
		return StatusDone
	}
	if o.state.IsFailed(issue.Number) {
		return StatusFailed
	}
	if o.state.IsFired(issue.Number) {
		return StatusFiring
	}
	// open and not yet fired
	if o.depsAllDone(deps, byNumber) {
		return StatusReady
	}
	return StatusBlocked
}

// parseDeps extracts dep issue numbers from an Issue, supporting:
//   - labels of the form "depends:ENG-1" (numeric suffix) or "depends:#12"
//   - a body line like "Depends-on: #12, #13" or "Depends-on: 12, 13"
func parseDeps(issue *Issue) []int {
	seen := map[int]bool{}
	var deps []int

	add := func(n int) {
		if n > 0 && !seen[n] {
			seen[n] = true
			deps = append(deps, n)
		}
	}

	// Labels: "depends:ENG-1", "depends:#12", "depends:12"
	for _, label := range issue.Labels {
		lower := strings.ToLower(label)
		prefix := "depends:"
		if !strings.HasPrefix(lower, prefix) {
			continue
		}
		raw := label[len(prefix):]
		if n := extractNumber(raw); n > 0 {
			add(n)
		}
	}

	// Body: scan for a line starting with "Depends-on:" (case-insensitive)
	for _, line := range strings.Split(issue.Body, "\n") {
		stripped := strings.TrimSpace(line)
		lower := strings.ToLower(stripped)
		if !strings.HasPrefix(lower, "depends-on:") {
			continue
		}
		rest := stripped[len("depends-on:"):]
		for _, part := range strings.Split(rest, ",") {
			if n := extractNumber(strings.TrimSpace(part)); n > 0 {
				add(n)
			}
		}
	}

	return deps
}

// extractNumber pulls the trailing integer from strings like "#12", "ENG-12",
// "12". Returns 0 if no integer is found.
func extractNumber(s string) int {
	s = strings.TrimSpace(s)
	// Strip leading "#"
	s = strings.TrimPrefix(s, "#")
	// Take the suffix after the last "-" (handles "ENG-12")
	if i := strings.LastIndex(s, "-"); i >= 0 {
		s = s[i+1:]
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}
