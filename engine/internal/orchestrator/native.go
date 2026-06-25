package orchestrator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

// NativeTicket is the orchestrator's view of a control-plane story.
type NativeTicket struct {
	ID     string   `json:"id"`
	Title  string   `json:"title"`
	Status string   `json:"status"`
	RunID  string   `json:"run_id"`
	Deps   []string `json:"deps"`
}

// StoryProvider is the interface through which NativeScheduler reads stories
// and writes status back. Tests use a fake; production uses NativeHTTPProvider.
type StoryProvider interface {
	// Ready returns stories whose deps are all done (derived "ready" — status
	// "backlog" with every dep at "done" in the control-plane store).
	Ready(ctx context.Context) ([]NativeTicket, error)
	// Running returns stories currently executing.
	Running(ctx context.Context) ([]NativeTicket, error)
	// Claim atomically transitions a story from backlog → running.
	// Returns true if this caller claimed it, false if already taken.
	Claim(ctx context.Context, id string) (bool, error)
	// MarkRunning records the run_id on a story that was claimed.
	MarkRunning(ctx context.Context, id, runID string) error
	// MarkDone advances a story to done once its run finishes.
	MarkDone(ctx context.Context, id string) error
	// MarkFailed advances a story to failed when its run ends in a terminal
	// non-DONE state (FAILED, CANCELLED) so it does not stay stuck.
	MarkFailed(ctx context.Context, id string) error
	// GetStory fetches the full story (body, acceptance, owner) so the fired run
	// carries the complete context — not just the title.
	GetStory(ctx context.Context, id string) (NativeStory, error)
}

// NativeStory is the full story the scheduler passes to a run as context.
// Repo is the GitHub repository URL the factory agent clones to implement
// the story; it flows from the design run payload → story.repo → run payload.
type NativeStory struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	Accept string `json:"acceptance"`
	Owner  string `json:"owner"`
	Repo   string `json:"repo"`
}

// NativeHTTPProvider implements StoryProvider against the control-plane HTTP API.
type NativeHTTPProvider struct {
	baseURL string
	http    *http.Client
}

// NewNativeHTTPProvider returns a StoryProvider that talks to the control-plane
// at baseURL (e.g. "http://localhost:8080").
func NewNativeHTTPProvider(baseURL string) *NativeHTTPProvider {
	return &NativeHTTPProvider{baseURL: baseURL, http: &http.Client{}}
}

type ticketsListResp struct {
	Tickets []NativeTicket `json:"tickets"`
}

// Ready GETs /tickets and returns stories whose status is "ready" (the
// control-plane's compat endpoint already derives readiness from the dep graph).
func (p *NativeHTTPProvider) Ready(ctx context.Context) ([]NativeTicket, error) {
	return p.filterTickets(ctx, "ready")
}

// Running GETs /tickets and returns stories whose status is "running".
func (p *NativeHTTPProvider) Running(ctx context.Context) ([]NativeTicket, error) {
	return p.filterTickets(ctx, "running")
}

func (p *NativeHTTPProvider) filterTickets(ctx context.Context, status string) ([]NativeTicket, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/tickets", nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	resp, err := p.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get /tickets: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("get /tickets: status %d", resp.StatusCode)
	}

	var body ticketsListResp
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decode /tickets: %w", err)
	}

	var out []NativeTicket
	for _, t := range body.Tickets {
		if t.Status == status {
			out = append(out, t)
		}
	}
	return out, nil
}

type storyStatusReq struct {
	Status string `json:"status"`
	RunID  string `json:"run_id,omitempty"`
}

type claimResp struct {
	Claimed bool `json:"claimed"`
}

// Claim POSTs /stories/{id}/claim. Returns true if this caller claimed it.
func (p *NativeHTTPProvider) Claim(ctx context.Context, id string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.baseURL+"/stories/"+id+"/claim", nil)
	if err != nil {
		return false, fmt.Errorf("build request: %w", err)
	}
	resp, err := p.http.Do(req)
	if err != nil {
		return false, fmt.Errorf("post /stories/%s/claim: %w", id, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusConflict {
		return false, nil // already claimed by someone else
	}
	if resp.StatusCode >= 300 {
		return false, fmt.Errorf("post /stories/%s/claim: status %d", id, resp.StatusCode)
	}
	var body claimResp
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return false, fmt.Errorf("decode claim response: %w", err)
	}
	return body.Claimed, nil
}

// MarkRunning PUTs /stories/{id}/status with status=running and the run_id.
// The story must already be claimed (status=running from Claim) before this is
// called; this call only records the run_id on an already-running story.
func (p *NativeHTTPProvider) MarkRunning(ctx context.Context, id, runID string) error {
	return p.putStatus(ctx, id, storyStatusReq{Status: "running", RunID: runID})
}

// MarkDone PUTs /stories/{id}/status with status=done.
func (p *NativeHTTPProvider) MarkDone(ctx context.Context, id string) error {
	return p.putStatus(ctx, id, storyStatusReq{Status: "done"})
}

// MarkFailed PUTs /stories/{id}/status with status=failed.
func (p *NativeHTTPProvider) MarkFailed(ctx context.Context, id string) error {
	return p.putStatus(ctx, id, storyStatusReq{Status: "failed"})
}

// GetStory GETs /stories/{id} and returns the full story.
func (p *NativeHTTPProvider) GetStory(ctx context.Context, id string) (NativeStory, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/stories/"+id, nil)
	if err != nil {
		return NativeStory{}, fmt.Errorf("build request: %w", err)
	}
	resp, err := p.http.Do(req)
	if err != nil {
		return NativeStory{}, fmt.Errorf("get /stories/%s: %w", id, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return NativeStory{}, fmt.Errorf("get /stories/%s: status %d", id, resp.StatusCode)
	}
	var st NativeStory
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return NativeStory{}, fmt.Errorf("decode story: %w", err)
	}
	return st, nil
}

func (p *NativeHTTPProvider) putStatus(ctx context.Context, id string, payload storyStatusReq) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut,
		p.baseURL+"/stories/"+id+"/status", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.http.Do(req)
	if err != nil {
		return fmt.Errorf("put /stories/%s/status: %w", id, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("put /stories/%s/status: status %d", id, resp.StatusCode)
	}
	return nil
}

// NativeScheduler polls the native ticket store (via StoryProvider), fires
// control-plane runs for ready stories, and marks them done when their runs
// complete. It shares the same ControlPlane interface as Orchestrator so both
// paths fire runs the same way.
//
// Idempotency: no state file is needed — MarkRunning flips the story status to
// "running", which removes it from Ready() on the next cycle. Restarting the
// scheduler simply re-polls; stories already running are tracked via Running().
type NativeScheduler struct {
	provider StoryProvider
	cp       ControlPlane
	workflow string
}

// NewNativeScheduler builds a NativeScheduler.
func NewNativeScheduler(provider StoryProvider, cp ControlPlane, workflow string) *NativeScheduler {
	return &NativeScheduler{provider: provider, cp: cp, workflow: workflow}
}

// terminalFailed reports whether a run status is a terminal failure — the run
// has ended and will never succeed. Distinct from still-in-progress statuses.
func terminalFailed(status string) bool {
	switch status {
	case "FAILED", "CANCELLED":
		return true
	}
	return false
}

// RunOnce executes a single poll-and-fire cycle:
//
//  1. For each running story: check its run's status; if DONE, mark the story
//     done; if terminal-failed (FAILED/CANCELLED), mark it failed. Stories whose
//     run is still in progress (RUNNING/QUEUED/AWAITING) are skipped.
//  2. For each ready story: CLAIM it first (atomic backlog→running), then fire a
//     run. If the claim returns false (another scheduler got it), skip — this
//     prevents double-fire under concurrent schedulers.
//
// Completing a story advances its status to "done", which unblocks any
// dependent stories — they will appear in Ready() on the next cycle.
// RunOnce returns the number of actions taken (stories marked done/failed +
// stories fired) so the loop can reset its backoff when there was activity.
func (s *NativeScheduler) RunOnce(ctx context.Context) (int, error) {
	actions := 0
	// Step (a) — advance completions before firing so a dep can unblock in the
	// same cycle that its run finishes (matches the GitHub orchestrator's order).
	running, err := s.provider.Running(ctx)
	if err != nil {
		return actions, fmt.Errorf("list running stories: %w", err)
	}
	for _, t := range running {
		if t.RunID == "" {
			continue
		}
		status, err := s.cp.RunStatus(ctx, t.RunID)
		if err != nil {
			log.Printf("native-scheduler: run status story=%s run=%s: %v", t.ID, t.RunID, err)
			continue
		}
		if status == "DONE" {
			if err := s.provider.MarkDone(ctx, t.ID); err != nil {
				log.Printf("native-scheduler: mark done story=%s: %v", t.ID, err)
				continue
			}
			actions++
			log.Printf("native-scheduler: story %s run %s DONE — marked done", t.ID, t.RunID)
			continue
		}
		if terminalFailed(status) {
			if err := s.provider.MarkFailed(ctx, t.ID); err != nil {
				log.Printf("native-scheduler: mark failed story=%s: %v", t.ID, err)
				continue
			}
			actions++
			log.Printf("native-scheduler: story %s run %s %s — marked failed", t.ID, t.RunID, status)
		}
		// In-progress statuses (RUNNING, QUEUED, AWAITING, …): leave it alone.
	}

	// Step (b) — fire ready stories. Claim-then-fire to prevent double-fire.
	ready, err := s.provider.Ready(ctx)
	if err != nil {
		return actions, fmt.Errorf("list ready stories: %w", err)
	}
	for _, t := range ready {
		// Atomically claim backlog → running before firing. If another scheduler
		// (or a previous cycle that hasn't flushed yet) already claimed it, skip.
		claimed, err := s.provider.Claim(ctx, t.ID)
		if err != nil {
			log.Printf("native-scheduler: claim story=%s: %v", t.ID, err)
			continue
		}
		if !claimed {
			log.Printf("native-scheduler: story %s already claimed — skipping", t.ID)
			continue
		}

		// Build a rich ticket from the full story (title + body + acceptance) so the
		// implementing agent has the complete context, and a short `title` so the UI
		// doesn't show the whole description as the run title.
		// repo is passed through so OnSeed can clone the project repository.
		title, ticket, repo := t.Title, t.Title, ""
		if st, gerr := s.provider.GetStory(ctx, t.ID); gerr == nil {
			title = st.Title
			ticket = st.Title
			if st.Body != "" {
				ticket += "\n\n" + st.Body
			}
			if st.Accept != "" {
				ticket += "\n\nAcceptance criteria:\n" + st.Accept
			}
			repo = st.Repo
		}
		payload := map[string]any{
			"story_id": t.ID,
			"title":    title,
			"ticket":   ticket,
			"repo":     repo,
		}
		runID, err := s.cp.FireRun(ctx, s.workflow, payload)
		if err != nil {
			log.Printf("native-scheduler: fire run story=%s: %v", t.ID, err)
			continue
		}
		// Record the run_id on the (already-running) story.
		if err := s.provider.MarkRunning(ctx, t.ID, runID); err != nil {
			log.Printf("native-scheduler: mark running story=%s run=%s: %v", t.ID, runID, err)
			continue
		}
		actions++
		log.Printf("native-scheduler: story %s claimed and fired — run %s", t.ID, runID)
	}
	return actions, nil
}

// Run loops forever, calling RunOnce at interval until ctx is cancelled.
// It backs off (doubles the sleep, up to 5× base) when an idle cycle fires
// nothing new, matching the Orchestrator.Run backoff pattern.
func (s *NativeScheduler) Run(ctx context.Context, base time.Duration) {
	maxInterval := base * 5
	interval := base
	for {
		actions, err := s.RunOnce(ctx)
		if err != nil {
			log.Printf("native-scheduler: poll error: %v", err)
		}
		// Reset to the base interval whenever there was activity (a story fired or
		// completed) so follow-up work — completions, unblocked dependents — is
		// picked up promptly; back off only when idle.
		if actions > 0 {
			interval = base
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
