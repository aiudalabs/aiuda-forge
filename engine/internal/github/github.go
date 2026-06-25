// Package github shells out to the `gh` CLI to manage GitHub repos.
// The host must be authenticated (gh auth login) before use.
// A runner func seam makes the package testable without real gh/git invocations.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// Client wraps GitHub operations implemented via the gh/git CLIs.
// runner is injectable for tests; nil uses exec.CommandContext.
type Client struct {
	runner func(ctx context.Context, workdir string, name string, args ...string) (string, error)
}

// New returns a Client that shells out to the real gh/git CLIs.
func New() *Client { return &Client{runner: defaultRunner} }

// withRunner builds a Client with a fake runner (for tests).
func withRunner(r func(ctx context.Context, workdir string, name string, args ...string) (string, error)) *Client {
	return &Client{runner: r}
}

func defaultRunner(ctx context.Context, workdir string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if workdir != "" {
		cmd.Dir = workdir
	}
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	return buf.String(), err
}

// ErrRepoExists is returned by CreateRepo when the repository already exists.
var ErrRepoExists = errors.New("repository already exists")

// CreateRepo creates a GitHub repository org/name and returns its HTTPS URL.
// If the repo already exists it returns ErrRepoExists (not a crash).
func (c *Client) CreateRepo(ctx context.Context, org, name, description string, private bool) (string, error) {
	slug := org + "/" + name
	// --add-readme seeds an initial commit so `main` exists — otherwise the repo
	// is empty and EnsureDevBranch has no base branch to fork `dev` from.
	args := []string{"repo", "create", slug, "--description", description, "--add-readme"}
	if private {
		args = append(args, "--private")
	} else {
		args = append(args, "--public")
	}
	out, err := c.runner(ctx, "", "gh", args...)
	if err != nil {
		// gh prints "already exists" when the repo is present; surface that clearly.
		if strings.Contains(out, "already exists") || strings.Contains(strings.ToLower(out), "already exists") {
			return "", fmt.Errorf("%w: %s", ErrRepoExists, slug)
		}
		return "", fmt.Errorf("gh repo create %s: %w: %s", slug, err, strings.TrimSpace(out))
	}
	// gh outputs the URL as the last https:// line (or the only line).
	url := lastHTTPS(out)
	if url == "" {
		url = "https://github.com/" + slug
	}
	return url, nil
}

// EnsureDevBranch clones repoURL shallowly into a temp dir, creates a `dev`
// branch off the default branch if it does not exist, pushes it, then removes
// the temp dir. Idempotent: if `dev` already exists on the remote, it succeeds.
func (c *Client) EnsureDevBranch(ctx context.Context, repoURL string) error {
	tmp, err := os.MkdirTemp("", "forge-dev-*")
	if err != nil {
		return fmt.Errorf("mktemp: %w", err)
	}
	defer os.RemoveAll(tmp)

	// Shallow clone (depth 1 fetches only the tips; we just need the branch list).
	if out, err := c.runner(ctx, "", "git", "clone", "--depth", "1", "--quiet", repoURL, tmp); err != nil {
		return fmt.Errorf("clone %s: %w: %s", repoURL, err, strings.TrimSpace(out))
	}

	// Check if dev already exists on the remote.
	out, _ := c.runner(ctx, tmp, "git", "ls-remote", "--heads", "origin", "dev")
	if strings.Contains(out, "refs/heads/dev") {
		return nil // already there
	}

	// Create and push the dev branch.
	if out, err := c.runner(ctx, tmp, "git", "checkout", "-b", "dev"); err != nil {
		return fmt.Errorf("checkout -b dev: %w: %s", err, strings.TrimSpace(out))
	}
	if out, err := c.runner(ctx, tmp, "git", "push", "origin", "dev"); err != nil {
		return fmt.Errorf("push dev: %w: %s", err, strings.TrimSpace(out))
	}
	return nil
}

// PRMerged reports whether PR number in repoURL has been merged. It shells out to
// `gh pr view <number> --repo <owner/repo> --json state,mergedAt`; a PR is merged
// when its state is MERGED (equivalently, mergedAt is non-null). An open or closed
// (but not merged) PR returns false.
func (c *Client) PRMerged(ctx context.Context, repoURL string, number int) (bool, error) {
	slug, err := slugFromURL(repoURL)
	if err != nil {
		return false, err
	}
	out, err := c.runner(ctx, "", "gh", "pr", "view", strconv.Itoa(number),
		"--repo", slug, "--json", "state,mergedAt")
	if err != nil {
		return false, fmt.Errorf("gh pr view %d (%s): %w: %s", number, slug, err, strings.TrimSpace(out))
	}
	var v struct {
		State    string `json:"state"`
		MergedAt string `json:"mergedAt"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		return false, fmt.Errorf("decode gh pr view %d: %w", number, err)
	}
	return v.State == "MERGED" || (v.MergedAt != "" && v.MergedAt != "null"), nil
}

// MergePR squash-merges PR number in repoURL and deletes its head branch via
// `gh pr merge <number> --repo <owner/repo> --squash --delete-branch`. Merging an
// already-merged PR is reported by gh as an error; callers should PRMerged-check
// first (the reconcile loop does) so this is only invoked on an open PR.
func (c *Client) MergePR(ctx context.Context, repoURL string, number int) error {
	slug, err := slugFromURL(repoURL)
	if err != nil {
		return err
	}
	out, err := c.runner(ctx, "", "gh", "pr", "merge", strconv.Itoa(number),
		"--repo", slug, "--squash", "--delete-branch")
	if err != nil {
		return fmt.Errorf("gh pr merge %d (%s): %w: %s", number, slug, err, strings.TrimSpace(out))
	}
	return nil
}

// slugFromURL derives "owner/repo" from an HTTPS GitHub repo URL, e.g.
// "https://github.com/acme/widgets" or "https://github.com/acme/widgets.git" →
// "acme/widgets". It tolerates a trailing slash and the optional ".git" suffix.
func slugFromURL(repoURL string) (string, error) {
	s := strings.TrimSuffix(strings.TrimSpace(repoURL), "/")
	s = strings.TrimSuffix(s, ".git")
	i := strings.Index(s, "github.com/")
	if i < 0 {
		return "", fmt.Errorf("not a github.com URL: %q", repoURL)
	}
	slug := s[i+len("github.com/"):]
	if strings.Count(slug, "/") != 1 || strings.HasPrefix(slug, "/") || strings.HasSuffix(slug, "/") {
		return "", fmt.Errorf("cannot derive owner/repo from %q", repoURL)
	}
	return slug, nil
}

// lastHTTPS returns the last https:// token from a multiline string.
func lastHTTPS(s string) string {
	url := ""
	for _, tok := range strings.Fields(s) {
		if strings.HasPrefix(tok, "https://") {
			url = tok
		}
	}
	return url
}
