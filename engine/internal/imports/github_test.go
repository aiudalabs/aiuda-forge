package imports

import (
	"context"
	"path/filepath"
	"testing"

	"forge/internal/github"
	"forge/internal/tickets"
)

type fakeLister struct {
	issues []github.Issue
	calls  int
}

func (f *fakeLister) ListIssues(_ context.Context, _ string) ([]github.Issue, error) {
	f.calls++
	return f.issues, nil
}

func openStore(t *testing.T) *tickets.Store {
	t.Helper()
	st, err := tickets.Open(filepath.Join(t.TempDir(), "tickets.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

const repo = "https://github.com/acme/widgets"

func TestGitHubImportCreatesStories(t *testing.T) {
	st := openStore(t)
	lister := &fakeLister{issues: []github.Issue{
		{Number: 1, Title: "Add login", Body: "users can log in"},
		{Number: 2, Title: "Add logout", Body: "users can log out"},
	}}

	res, err := GitHub(context.Background(), st, lister, "proj1", repo)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if res.Imported != 2 || res.Skipped != 0 {
		t.Fatalf("res = %+v, want imported=2 skipped=0", res)
	}
	// The stories exist, scoped to the project, carrying external_ref + repo.
	id, ok, err := st.StoryIDByExternalRef("github:acme/widgets#1")
	if err != nil || !ok {
		t.Fatalf("issue #1 should be imported: ok=%v err=%v", ok, err)
	}
	story, err := st.GetStory(id)
	if err != nil {
		t.Fatal(err)
	}
	if story.Title != "Add login" || story.ProjectID != "proj1" || story.Repo != repo {
		t.Fatalf("story = %+v, want title/proj/repo set", story)
	}
	if story.Status != tickets.StatusBacklog {
		t.Fatalf("imported story status = %q, want backlog", story.Status)
	}
}

func TestGitHubImportIsIdempotent(t *testing.T) {
	st := openStore(t)
	lister := &fakeLister{issues: []github.Issue{
		{Number: 1, Title: "Add login", Body: "x"},
		{Number: 2, Title: "Add logout", Body: "y"},
	}}

	if _, err := GitHub(context.Background(), st, lister, "proj1", repo); err != nil {
		t.Fatal(err)
	}
	// Second import of the same issues → all skipped, none duplicated.
	res2, err := GitHub(context.Background(), st, lister, "proj1", repo)
	if err != nil {
		t.Fatal(err)
	}
	if res2.Imported != 0 || res2.Skipped != 2 {
		t.Fatalf("re-import res = %+v, want imported=0 skipped=2", res2)
	}
	all, _ := st.ListStoriesByProject("proj1")
	if len(all) != 2 {
		t.Fatalf("project should have exactly 2 stories after re-import, got %d", len(all))
	}
}

// A new issue appearing on a re-import is added; existing ones stay skipped.
func TestGitHubImportPicksUpNewIssues(t *testing.T) {
	st := openStore(t)
	lister := &fakeLister{issues: []github.Issue{{Number: 1, Title: "one", Body: ""}}}
	if _, err := GitHub(context.Background(), st, lister, "proj1", repo); err != nil {
		t.Fatal(err)
	}
	lister.issues = append(lister.issues, github.Issue{Number: 2, Title: "two", Body: ""})
	res, err := GitHub(context.Background(), st, lister, "proj1", repo)
	if err != nil {
		t.Fatal(err)
	}
	if res.Imported != 1 || res.Skipped != 1 {
		t.Fatalf("res = %+v, want imported=1 skipped=1", res)
	}
}
