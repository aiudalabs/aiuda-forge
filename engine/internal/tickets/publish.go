package tickets

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"forge/internal/workflow"
	"gopkg.in/yaml.v3"
)

// BacklogFile is the parsed representation of docs/backlog.yaml produced by the
// scrum-master design phase. All fields map 1:1 to the native ticket store.
type BacklogFile struct {
	Epic    backlogEpic     `yaml:"epic"`
	Sprints []backlogSprint `yaml:"sprints"`
	Stories []backlogStory  `yaml:"stories"`
}

type backlogEpic struct {
	ID          string `yaml:"id"`
	Title       string `yaml:"title"`
	Description string `yaml:"description"`
}

type backlogSprint struct {
	ID   string `yaml:"id"`
	Name string `yaml:"name"`
	Goal string `yaml:"goal"`
}

type backlogStory struct {
	ID         string   `yaml:"id"`
	Title      string   `yaml:"title"`
	Body       string   `yaml:"body"`
	Acceptance string   `yaml:"acceptance"`
	Owner      string   `yaml:"owner"`
	SprintID   string   `yaml:"sprint_id"`
	Deps       []string `yaml:"deps"`
}

// PublishRunner is the ticket_publish step type. It reads a structured backlog
// artifact (backlog.yaml) from the run's workdir and writes it into the native
// ticket store — closing the loop from design workflow → executable backlog.
type PublishRunner struct {
	Store *Store
}

// Run implements workflow.Runner. It reads the backlog artifact, creates the
// epic (idempotently), and creates each story (skipping existing ones).
// The optional inputs["repo"] value is recorded on every story so the factory
// scheduler knows which repository to clone when the story is fired.
// Returns a failed StepResult — never an error — so the run surfaces the
// problem rather than panicking.
func (r *PublishRunner) Run(_ context.Context, step workflow.Step, inputs map[string]any, workdir string) (workflow.StepResult, error) {
	backlogPath := "docs/backlog.yaml"
	if v, ok := inputs["backlog"].(string); ok && v != "" {
		backlogPath = v
	}

	repo := ""
	if v, ok := inputs["repo"].(string); ok {
		repo = v
	}

	full := filepath.Join(workdir, backlogPath)
	raw, err := os.ReadFile(full)
	if err != nil {
		return workflow.StepResult{
			Success: false,
			Detail:  fmt.Sprintf("ticket_publish: read %s: %v", backlogPath, err),
		}, nil
	}

	var bf BacklogFile
	if err := yaml.Unmarshal(raw, &bf); err != nil {
		return workflow.StepResult{
			Success: false,
			Detail:  fmt.Sprintf("ticket_publish: parse %s: %v", backlogPath, err),
		}, nil
	}

	// Create the epic if present; ignore "already exists" (UNIQUE constraint).
	if bf.Epic.ID != "" {
		if err := r.Store.CreateEpic(Epic{
			ID:          bf.Epic.ID,
			Title:       bf.Epic.Title,
			Description: bf.Epic.Description,
		}); err != nil && !isSQLiteConflict(err) {
			return workflow.StepResult{
				Success: false,
				Detail:  fmt.Sprintf("ticket_publish: create epic %s: %v", bf.Epic.ID, err),
			}, nil
		}
	}

	// Materialize sprints BEFORE stories: the sprint-batched scheduler fires by
	// sprint (ReadySprints iterates the sprints table), so a story's sprint_id is
	// inert unless the Sprint row exists. Create from the explicit `sprints:`
	// section first, then derive any sprint_id referenced by a story but not
	// declared (name defaults to the id). Idempotent — existing rows are skipped.
	declared := map[string]bool{}
	for _, sp := range bf.Sprints {
		if sp.ID == "" {
			continue
		}
		if err := r.createSprint(sp.ID, sp.Name, sp.Goal); err != nil {
			return workflow.StepResult{
				Success: false,
				Detail:  fmt.Sprintf("ticket_publish: create sprint %s: %v", sp.ID, err),
			}, nil
		}
		declared[sp.ID] = true
	}
	for _, s := range bf.Stories {
		if s.SprintID == "" || declared[s.SprintID] {
			continue
		}
		if err := r.createSprint(s.SprintID, s.SprintID, ""); err != nil {
			return workflow.StepResult{
				Success: false,
				Detail:  fmt.Sprintf("ticket_publish: derive sprint %s: %v", s.SprintID, err),
			}, nil
		}
		declared[s.SprintID] = true
	}

	created, skipped := 0, 0
	for _, s := range bf.Stories {
		if s.ID == "" {
			continue
		}
		err := r.Store.CreateStory(Story{
			ID:       s.ID,
			EpicID:   bf.Epic.ID,
			SprintID: s.SprintID,
			Title:    s.Title,
			Body:     s.Body,
			Accept:   s.Acceptance,
			Owner:    s.Owner,
			Deps:     s.Deps,
			Status:   StatusBacklog,
			Repo:     repo,
		})
		if err != nil {
			if isSQLiteConflict(err) {
				skipped++
				continue
			}
			return workflow.StepResult{
				Success: false,
				Detail:  fmt.Sprintf("ticket_publish: create story %s: %v", s.ID, err),
			}, nil
		}
		created++
	}

	return workflow.StepResult{
		Success: true,
		Output: map[string]any{
			"epic":    bf.Epic.ID,
			"sprints": len(declared),
			"created": created,
			"skipped": skipped,
		},
		Detail: fmt.Sprintf("ticket_publish: epic=%s sprints=%d created=%d skipped=%d", bf.Epic.ID, len(declared), created, skipped),
	}, nil
}

// createSprint inserts a sprint, treating an "already exists" UNIQUE conflict as
// success so republishing a backlog is idempotent.
func (r *PublishRunner) createSprint(id, name, goal string) error {
	if name == "" {
		name = id
	}
	if err := r.Store.CreateSprint(Sprint{ID: id, Name: name, Goal: goal}); err != nil && !isSQLiteConflict(err) {
		return err
	}
	return nil
}

// isSQLiteConflict reports whether err is a SQLite UNIQUE constraint violation
// (error code 1555 or 2067), which signals the row already exists.
func isSQLiteConflict(err error) bool {
	if err == nil {
		return false
	}
	// database/sql wraps the driver error; inspect the string as a fallback
	// because modernc.org/sqlite error types are not exported.
	msg := err.Error()
	return contains(msg, "UNIQUE constraint failed") ||
		contains(msg, "constraint failed") ||
		errors.Is(err, sql.ErrNoRows) // never true, but keeps the logic explicit
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
