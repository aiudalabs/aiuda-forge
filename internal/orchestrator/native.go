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
	// MarkRunning flips a story to running and records the run that drives it.
	MarkRunning(ctx context.Context, id, runID string) error
	// MarkDone advances a story to done once its run finishes.
	MarkDone(ctx context.Context, id string) error
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

// MarkRunning PUTs /stories/{id}/status with status=running and the run_id.
func (p *NativeHTTPProvider) MarkRunning(ctx context.Context, id, runID string) error {
	return p.putStatus(ctx, id, storyStatusReq{Status: "running", RunID: runID})
}

// MarkDone PUTs /stories/{id}/status with status=done.
func (p *NativeHTTPProvider) MarkDone(ctx context.Context, id string) error {
	return p.putStatus(ctx, id, storyStatusReq{Status: "done"})
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

// RunOnce executes a single poll-and-fire cycle:
//
//  1. For each ready story: fire a run; on success, mark the story running.
//  2. For each running story: check its run's status; if DONE, mark the story done.
//
// Completing a story advances its status to "done", which unblocks any
// dependent stories — they will appear in Ready() on the next cycle.
func (s *NativeScheduler) RunOnce(ctx context.Context) error {
	// Step (a) — advance completions before firing so a dep can unblock in the
	// same cycle that its run finishes (matches the GitHub orchestrator's order).
	running, err := s.provider.Running(ctx)
	if err != nil {
		return fmt.Errorf("list running stories: %w", err)
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
		if status != "DONE" {
			continue
		}
		if err := s.provider.MarkDone(ctx, t.ID); err != nil {
			log.Printf("native-scheduler: mark done story=%s: %v", t.ID, err)
			continue
		}
		log.Printf("native-scheduler: story %s run %s DONE — marked done", t.ID, t.RunID)
	}

	// Step (b) — fire ready stories.
	ready, err := s.provider.Ready(ctx)
	if err != nil {
		return fmt.Errorf("list ready stories: %w", err)
	}
	for _, t := range ready {
		payload := map[string]any{
			"story_id": t.ID,
			"title":    t.Title,
			"body":     t.Title, // body comes from title for the HTTP provider; extended payloads via richer providers
		}
		runID, err := s.cp.FireRun(ctx, s.workflow, payload)
		if err != nil {
			log.Printf("native-scheduler: fire run story=%s: %v", t.ID, err)
			continue
		}
		if err := s.provider.MarkRunning(ctx, t.ID, runID); err != nil {
			log.Printf("native-scheduler: mark running story=%s run=%s: %v", t.ID, runID, err)
			continue
		}
		log.Printf("native-scheduler: story %s fired — run %s", t.ID, runID)
	}
	return nil
}

// Run loops forever, calling RunOnce at interval until ctx is cancelled.
// It backs off (doubles the sleep, up to 5× base) when an idle cycle fires
// nothing new, matching the Orchestrator.Run backoff pattern.
func (s *NativeScheduler) Run(ctx context.Context, interval time.Duration) {
	maxInterval := interval * 5
	for {
		if err := s.RunOnce(ctx); err != nil {
			log.Printf("native-scheduler: poll error: %v", err)
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}

		// Simple backoff: next sleep doubles up to max; reset when context is
		// still live (we don't have a fired-count here, so we just let it grow
		// to max and stay there — steady-state is one poll per maxInterval when
		// the board is quiet).
		if interval < maxInterval {
			interval *= 2
		}
	}
}
