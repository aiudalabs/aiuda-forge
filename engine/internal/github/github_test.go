package github

import (
	"context"
	"strings"
	"testing"
)

// recorder captures every call to the fake runner.
type call struct {
	workdir string
	name    string
	args    []string
}

func fakeRunner(replies map[string]string, calls *[]call) func(ctx context.Context, workdir string, name string, args ...string) (string, error) {
	return func(ctx context.Context, workdir string, name string, args ...string) (string, error) {
		*calls = append(*calls, call{workdir: workdir, name: name, args: args})
		key := name + " " + strings.Join(args, " ")
		// Match by prefix for flexibility.
		for prefix, out := range replies {
			if strings.HasPrefix(key, prefix) {
				return out, nil
			}
		}
		return "", nil
	}
}

// TestCreateRepoArgs checks that CreateRepo constructs the right gh args.
func TestCreateRepoArgs(t *testing.T) {
	var calls []call
	replies := map[string]string{
		"gh repo create": "https://github.com/myorg/my-repo\n",
	}
	c := withRunner(fakeRunner(replies, &calls))

	url, err := c.CreateRepo(context.Background(), "myorg", "my-repo", "test repo", true)
	if err != nil {
		t.Fatalf("CreateRepo: %v", err)
	}
	if url != "https://github.com/myorg/my-repo" {
		t.Errorf("url: got %q, want https://github.com/myorg/my-repo", url)
	}

	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	args := calls[0].args
	// Must contain: repo create myorg/my-repo --private --description ...
	mustContain(t, args, "repo")
	mustContain(t, args, "create")
	mustContain(t, args, "myorg/my-repo")
	mustContain(t, args, "--private")
	mustContain(t, args, "--description")
}

// TestCreateRepoPublic checks that public repos use --public not --private.
func TestCreateRepoPublic(t *testing.T) {
	var calls []call
	c := withRunner(fakeRunner(map[string]string{
		"gh repo create": "https://github.com/org/repo\n",
	}, &calls))

	_, err := c.CreateRepo(context.Background(), "org", "repo", "", false)
	if err != nil {
		t.Fatal(err)
	}
	args := calls[0].args
	mustContain(t, args, "--public")
	mustNotContain(t, args, "--private")
}

// TestCreateRepoAlreadyExists maps the "already exists" message to ErrRepoExists.
func TestCreateRepoAlreadyExists(t *testing.T) {
	c := withRunner(func(ctx context.Context, workdir string, name string, args ...string) (string, error) {
		return "GraphQL: Name already exists on this account (createRepository)", errExitCode1{}
	})

	_, err := c.CreateRepo(context.Background(), "org", "dup", "", true)
	if err == nil {
		t.Fatal("expected error")
	}
	// The returned error must wrap or be ErrRepoExists.
	if !strings.Contains(err.Error(), ErrRepoExists.Error()) {
		t.Errorf("expected ErrRepoExists in error, got %v", err)
	}
}

// TestEnsureDevBranchArgs verifies the git commands used to seed the dev branch.
func TestEnsureDevBranchArgs(t *testing.T) {
	var calls []call
	replies := map[string]string{
		"git clone":     "", // clone succeeds, no output
		"git ls-remote": "", // dev branch does NOT exist on remote
		"git checkout":  "",
		"git push":      "",
	}
	c := withRunner(fakeRunner(replies, &calls))

	err := c.EnsureDevBranch(context.Background(), "https://github.com/org/repo")
	if err != nil {
		t.Fatalf("EnsureDevBranch: %v", err)
	}

	// Expect: clone, ls-remote, checkout -b dev, push origin dev.
	names := make([]string, len(calls))
	for i, cl := range calls {
		names[i] = cl.name + " " + strings.Join(cl.args, " ")
	}

	if !containsPrefix(names, "git clone") {
		t.Errorf("expected git clone; calls: %v", names)
	}
	if !containsPrefix(names, "git ls-remote") {
		t.Errorf("expected git ls-remote; calls: %v", names)
	}
	if !containsPrefix(names, "git checkout -b dev") {
		t.Errorf("expected git checkout -b dev; calls: %v", names)
	}
	if !containsPrefix(names, "git push origin dev") {
		t.Errorf("expected git push origin dev; calls: %v", names)
	}
}

// TestEnsureDevBranchIdempotent checks that when dev already exists on the
// remote, we skip checkout+push (no unnecessary work).
func TestEnsureDevBranchIdempotent(t *testing.T) {
	var calls []call
	replies := map[string]string{
		"git clone":     "",
		"git ls-remote": "abc123\trefs/heads/dev\n", // dev already exists
	}
	c := withRunner(fakeRunner(replies, &calls))

	if err := c.EnsureDevBranch(context.Background(), "https://github.com/org/repo"); err != nil {
		t.Fatalf("EnsureDevBranch: %v", err)
	}
	// No checkout or push should have been issued.
	for _, cl := range calls {
		if cl.name == "git" && len(cl.args) > 0 && cl.args[0] == "push" {
			t.Error("unexpected git push when dev already exists")
		}
	}
}

// TestPRMerged covers the state/mergedAt combinations gh can return.
func TestPRMerged(t *testing.T) {
	cases := []struct {
		name  string
		reply string
		want  bool
	}{
		{"merged-by-state", `{"state":"MERGED","mergedAt":"2026-06-20T10:00:00Z"}`, true},
		{"merged-by-time-only", `{"state":"OPEN","mergedAt":"2026-06-20T10:00:00Z"}`, true},
		{"open", `{"state":"OPEN","mergedAt":null}`, false},
		{"closed-not-merged", `{"state":"CLOSED","mergedAt":""}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls []call
			c := withRunner(fakeRunner(map[string]string{"gh pr view": tc.reply}, &calls))
			got, err := c.PRMerged(context.Background(), "https://github.com/acme/widgets", 7)
			if err != nil {
				t.Fatalf("PRMerged: %v", err)
			}
			if got != tc.want {
				t.Errorf("PRMerged = %v, want %v", got, tc.want)
			}
			// Args must carry the number, the derived slug, and the json fields.
			args := calls[0].args
			mustContain(t, args, "7")
			mustContain(t, args, "acme/widgets")
			mustContain(t, args, "state,mergedAt")
		})
	}
}

// TestMergePRArgs verifies MergePR squash-merges and deletes the branch.
func TestMergePRArgs(t *testing.T) {
	var calls []call
	c := withRunner(fakeRunner(map[string]string{"gh pr merge": ""}, &calls))
	if err := c.MergePR(context.Background(), "https://github.com/acme/widgets.git", 7); err != nil {
		t.Fatalf("MergePR: %v", err)
	}
	args := calls[0].args
	mustContain(t, args, "merge")
	mustContain(t, args, "7")
	mustContain(t, args, "acme/widgets") // .git suffix stripped from slug
	mustContain(t, args, "--squash")
	mustContain(t, args, "--delete-branch")
}

// TestSlugFromURL exercises the owner/repo derivation, incl. error cases.
func TestSlugFromURL(t *testing.T) {
	ok := map[string]string{
		"https://github.com/acme/widgets":     "acme/widgets",
		"https://github.com/acme/widgets.git": "acme/widgets",
		"https://github.com/acme/widgets/":    "acme/widgets",
		"https://github.com/o/r":              "o/r",
	}
	for in, want := range ok {
		got, err := slugFromURL(in)
		if err != nil {
			t.Errorf("slugFromURL(%q): unexpected error %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("slugFromURL(%q) = %q, want %q", in, got, want)
		}
	}
	for _, in := range []string{"", "https://gitlab.com/a/b", "https://github.com/onlyowner", "not a url"} {
		if _, err := slugFromURL(in); err == nil {
			t.Errorf("slugFromURL(%q): expected error, got nil", in)
		}
	}
}

// TestListOpenPRsDetailedMergeable verifica que la detección de conflicto lee el
// enum `mergeable` de gh pr list y lo mapea a cada PR (incidente #83).
func TestListOpenPRsDetailedMergeable(t *testing.T) {
	var calls []call
	reply := `[
	  {"number":83,"title":"SP5","body":"Closes #12","url":"https://github.com/acme/widgets/pull/83","isDraft":false,"author":{"login":"copilot"},"mergeable":"CONFLICTING"},
	  {"number":84,"title":"SP6","body":"","url":"https://github.com/acme/widgets/pull/84","isDraft":true,"author":{"login":"alice"},"mergeable":"MERGEABLE"},
	  {"number":85,"title":"SP7","body":"","url":"https://github.com/acme/widgets/pull/85","isDraft":false,"author":{"login":"bob"},"mergeable":"UNKNOWN"}
	]`
	c := withRunner(fakeRunner(map[string]string{"gh pr list": reply}, &calls))

	prs, err := c.ListOpenPRsDetailed(context.Background(), "https://github.com/acme/widgets.git")
	if err != nil {
		t.Fatalf("ListOpenPRsDetailed: %v", err)
	}
	if len(prs) != 3 {
		t.Fatalf("prs = %d, want 3", len(prs))
	}
	if prs[0].Number != 83 || prs[0].Mergeable != "CONFLICTING" || prs[0].Author != "copilot" || prs[0].Draft {
		t.Fatalf("pr[0] = %+v", prs[0])
	}
	if prs[1].Mergeable != "MERGEABLE" || !prs[1].Draft {
		t.Fatalf("pr[1] = %+v", prs[1])
	}
	if prs[2].Mergeable != "UNKNOWN" {
		t.Fatalf("pr[2] = %+v", prs[2])
	}
	// Debe consultar el campo mergeable con slug derivado (sufijo .git quitado).
	args := calls[0].args
	mustContain(t, args, "list")
	mustContain(t, args, "acme/widgets")
	if !containsPrefix(args, "number,title") {
		t.Fatalf("args %v deben pedir --json con mergeable", args)
	}
}

// ---- helpers ----------------------------------------------------------------

type errExitCode1 struct{}

func (errExitCode1) Error() string { return "exit status 1" }

func mustContain(t *testing.T, args []string, s string) {
	t.Helper()
	for _, a := range args {
		if a == s {
			return
		}
	}
	t.Errorf("args %v must contain %q", args, s)
}

func mustNotContain(t *testing.T, args []string, s string) {
	t.Helper()
	for _, a := range args {
		if a == s {
			t.Errorf("args %v must NOT contain %q", args, s)
			return
		}
	}
}

func containsPrefix(ss []string, prefix string) bool {
	for _, s := range ss {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}
