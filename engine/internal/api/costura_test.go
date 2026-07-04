package api

// Tests de la costura "story manual → GitHub Issues": crear una story vía
// POST /stories debe encadenar el mismo export idempotente que el publish del
// backlog de diseño (OnStoryCreated). El fake replica el de export_test.go.

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"forge/internal/github"
	"forge/internal/projects"
	"forge/internal/tickets"
)

func newTicketsStore(t *testing.T) *tickets.Store {
	t.Helper()
	st, err := tickets.Open(filepath.Join(t.TempDir(), "tickets.db"))
	if err != nil {
		t.Fatalf("tickets.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// costuraFakeGH implementa export.GitHubWriter registrando las escrituras.
type costuraFakeGH struct {
	issues  []string // títulos creados, en orden
	deps    []string // "target<-blockerID"
	nextNum int
}

func (f *costuraFakeGH) EnsureLabel(_ context.Context, _, _, _ string) (bool, error) {
	return false, nil
}

func (f *costuraFakeGH) CreateIssue(_ context.Context, _, title, _ string, _ []string) (github.CreatedIssue, error) {
	f.nextNum++
	f.issues = append(f.issues, title)
	return github.CreatedIssue{Number: f.nextNum, ID: int64(f.nextNum) * 10}, nil
}

func (f *costuraFakeGH) IssueID(_ context.Context, _ string, number int) (int64, error) {
	return int64(number) * 10, nil
}

func (f *costuraFakeGH) AddIssueBlockedBy(_ context.Context, _ string, issueNumber int, blockedByID int64) (bool, error) {
	f.deps = append(f.deps, fmt.Sprintf("%d<-%d", issueNumber, blockedByID))
	return true, nil
}

// La story creada a mano se exporta como issue y su dep contra una story YA
// exportada se cablea sin duplicar el issue existente (idempotencia por
// external_ref — el mismo contrato que el export del publish).
func TestExportStoryBacklogCreatesOnlyTheNewIssue(t *testing.T) {
	store := newTicketsStore(t)
	srv := &Server{Tickets: store}

	prev := tickets.Story{ID: "S1-01", Title: "Schema", ProjectID: "p1"}
	if err := store.CreateStory(prev); err != nil {
		t.Fatalf("seed S1-01: %v", err)
	}
	if err := store.SetStoryExternalRef("p1", "S1-01", "github:o/r#7"); err != nil {
		t.Fatalf("seed external_ref: %v", err)
	}

	manual := tickets.Story{ID: "S9-01", Title: "Manual", Body: "As a user…", ProjectID: "p1", Deps: []string{"S1-01"}}
	if err := store.CreateStory(manual); err != nil {
		t.Fatalf("create manual story: %v", err)
	}

	gh := &costuraFakeGH{nextNum: 100}
	srv.exportStoryBacklog(context.Background(), gh, manual, "https://github.com/o/r")

	if len(gh.issues) != 1 || gh.issues[0] != "S9-01 — Manual" {
		t.Fatalf("issues created = %v, want only [S9-01 — Manual]", gh.issues)
	}
	got, err := store.GetStory("S9-01")
	if err != nil {
		t.Fatal(err)
	}
	if got.ExternalRef != "github:o/r#101" {
		t.Fatalf("external_ref = %q, want github:o/r#101", got.ExternalRef)
	}
	// La dep se cablea contra el database-id del blocker (number*10 en el fake).
	if len(gh.deps) != 1 || gh.deps[0] != "101<-70" {
		t.Fatalf("dep edges = %v, want [101<-70]", gh.deps)
	}
}

// Con dos stories del MISMO id en proyectos distintos (el incidente S11-01), el
// export por-story viaja SOLO la del proyecto pedido — export.GitHubBacklog lista
// por proyecto, así que el huérfano de "default" nunca se toca.
func TestExportStoryBacklogScopesToProjectWithDuplicateIDs(t *testing.T) {
	store := newTicketsStore(t)
	srv := &Server{Tickets: store}

	// Huérfano en default (sin repo) + la buena en p1 (con repo), mismo id.
	if err := store.CreateStory(tickets.Story{ID: "S1-01", Title: "orphan", ProjectID: "default"}); err != nil {
		t.Fatalf("seed default: %v", err)
	}
	good := tickets.Story{ID: "S1-01", Title: "real", ProjectID: "p1", Repo: "https://github.com/o/r"}
	if err := store.CreateStory(good); err != nil {
		t.Fatalf("seed p1: %v", err)
	}

	gh := &costuraFakeGH{nextNum: 200}
	if _, err := srv.exportStoryBacklog(context.Background(), gh, good, "https://github.com/o/r"); err != nil {
		t.Fatalf("export: %v", err)
	}

	if len(gh.issues) != 1 || gh.issues[0] != "S1-01 — real" {
		t.Fatalf("issues = %v, want only [S1-01 — real]", gh.issues)
	}
	// La de p1 quedó espejada; la de default sigue sin external_ref.
	p1, _ := store.GetStoryInProject("p1", "S1-01")
	if p1.ExternalRef == "" {
		t.Fatalf("p1 story not mirrored: %+v", p1)
	}
	def, _ := store.GetStoryInProject("default", "S1-01")
	if def.ExternalRef != "" {
		t.Fatalf("default orphan was wrongly exported: %q", def.ExternalRef)
	}
}

// exportRepoFor decide si una story manual tiene a dónde exportarse: nunca para
// el proyecto "default", el repo de la story gana, y si no, el del proyecto.
func TestExportRepoFor(t *testing.T) {
	pr, err := projects.Open(filepath.Join(t.TempDir(), "projects.db"))
	if err != nil {
		t.Fatalf("projects.Open: %v", err)
	}
	t.Cleanup(func() { pr.Close() })
	if _, err := pr.Create(projects.Project{ID: "p1", Name: "P1", Repo: "https://github.com/o/r"}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	srv := &Server{Projects: pr}

	cases := []struct {
		name string
		st   tickets.Story
		want string
	}{
		{"default project never exports", tickets.Story{ID: "s", ProjectID: tickets.DefaultProjectID, Repo: "https://github.com/o/r"}, ""},
		{"empty project never exports", tickets.Story{ID: "s"}, ""},
		{"story repo wins", tickets.Story{ID: "s", ProjectID: "p1", Repo: "https://github.com/o/other"}, "https://github.com/o/other"},
		{"falls back to project repo", tickets.Story{ID: "s", ProjectID: "p1"}, "https://github.com/o/r"},
		{"unknown project exports nothing", tickets.Story{ID: "s", ProjectID: "ghost"}, ""},
	}
	for _, tc := range cases {
		if got := srv.exportRepoFor(tc.st); got != tc.want {
			t.Errorf("%s: exportRepoFor = %q, want %q", tc.name, got, tc.want)
		}
	}
}
