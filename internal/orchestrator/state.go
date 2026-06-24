package orchestrator

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

// stateData is the on-disk JSON structure.
type stateData struct {
	// Fired maps issue number → run ID (set when a run is fired).
	Fired map[string]string `json:"fired"`
	// Completed is the set of issue numbers whose runs have finished
	// successfully. Used by depsAllDone to unblock downstream issues.
	Completed map[string]bool `json:"completed,omitempty"`
	// Failed is the set of issue numbers whose runs ended in a terminal
	// non-DONE state. Stops re-polling and surfaces as StatusFailed.
	Failed map[string]bool `json:"failed,omitempty"`
}

// State persists the set of issues that have been fired (and their run IDs) to
// a JSON file so a restart does not double-fire.
type State struct {
	mu   sync.Mutex
	path string
	data stateData
}

// LoadState reads existing state from path (creates an empty state if the file
// does not exist yet).
func LoadState(path string) (*State, error) {
	s := &State{
		path: path,
		data: stateData{
			Fired:     make(map[string]string),
			Completed: make(map[string]bool),
			Failed:    make(map[string]bool),
		},
	}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read state file %s: %w", path, err)
	}
	if err := json.Unmarshal(b, &s.data); err != nil {
		return nil, fmt.Errorf("parse state file %s: %w", path, err)
	}
	if s.data.Fired == nil {
		s.data.Fired = make(map[string]string)
	}
	if s.data.Completed == nil {
		s.data.Completed = make(map[string]bool)
	}
	if s.data.Failed == nil {
		s.data.Failed = make(map[string]bool)
	}
	return s, nil
}

// IsFired returns true when the issue number has been fired.
func (s *State) IsFired(number int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.data.Fired[itoa(number)]
	return ok
}

// MarkFired records that number was fired with the given run ID and persists the
// state to disk.
func (s *State) MarkFired(number int, runID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Fired[itoa(number)] = runID
	return s.flush()
}

// RunID returns the run ID for a fired issue, or "" if not fired.
func (s *State) RunID(number int) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.Fired[itoa(number)]
}

// IsCompleted returns true when the issue number's run has been marked done.
func (s *State) IsCompleted(number int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.Completed[itoa(number)]
}

// MarkCompleted records that the run for number finished successfully and
// persists the state to disk. This unblocks downstream issues in depsAllDone.
func (s *State) MarkCompleted(number int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Completed[itoa(number)] = true
	return s.flush()
}

// IsFailed returns true when the issue number's run ended in a terminal non-DONE
// state and the ticket has been recorded as failed.
func (s *State) IsFailed(number int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.Failed[itoa(number)]
}

// MarkFailed records that the run for number ended in a terminal failure and
// persists the state to disk. The ticket will surface as StatusFailed in
// GET /tickets and will no longer be re-polled by reconcileCompletions.
func (s *State) MarkFailed(number int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Failed[itoa(number)] = true
	return s.flush()
}

// FiredCount returns how many issues have been fired (used for backoff logic).
func (s *State) FiredCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.data.Fired)
}

// flush writes the current state to disk (caller must hold mu).
func (s *State) flush() error {
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, b, 0o644)
}

func itoa(n int) string {
	return fmt.Sprintf("%d", n)
}
