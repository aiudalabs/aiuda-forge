package github

// Docs reading: the Studio "Confluence" view renders a project's specs straight
// from its repo (the source of truth that survives an ephemeral design run). These
// helpers read the repo's docs/ tree via the GitHub contents API through `gh`.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// DocEntry is a file or directory under a repo path — the subset of the GitHub
// contents API the docs viewer needs.
type DocEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Type string `json:"type"` // "file" | "dir"
	Size int    `json:"size"`
}

// ListContents lists directory `dir` of repoURL at `ref` (e.g. "dev") via the
// GitHub contents API. A missing dir/ref yields an error the caller can treat as
// "no docs yet".
func (c *Client) ListContents(ctx context.Context, repoURL, dir, ref string) ([]DocEntry, error) {
	slug, err := slugFromURL(repoURL)
	if err != nil {
		return nil, err
	}
	api := fmt.Sprintf("repos/%s/contents/%s", slug, dir)
	if ref != "" {
		api += "?ref=" + ref
	}
	out, err := c.runner(ctx, "", "gh", "api", api)
	if err != nil {
		return nil, fmt.Errorf("gh api contents %s: %w: %s", dir, err, strings.TrimSpace(out))
	}
	var entries []DocEntry
	if err := json.Unmarshal([]byte(out), &entries); err != nil {
		return nil, fmt.Errorf("decode contents %s: %w", dir, err)
	}
	return entries, nil
}

// ReadFile returns the decoded UTF-8 content of repo file `path` at `ref`.
func (c *Client) ReadFile(ctx context.Context, repoURL, path, ref string) (string, error) {
	slug, err := slugFromURL(repoURL)
	if err != nil {
		return "", err
	}
	api := fmt.Sprintf("repos/%s/contents/%s", slug, path)
	if ref != "" {
		api += "?ref=" + ref
	}
	// --jq .content yields the base64 blob (newline-wrapped per the contents API).
	out, err := c.runner(ctx, "", "gh", "api", api, "--jq", ".content")
	if err != nil {
		return "", fmt.Errorf("gh api file %s: %w: %s", path, err, strings.TrimSpace(out))
	}
	clean := strings.ReplaceAll(strings.TrimSpace(out), "\n", "")
	dec, err := base64.StdEncoding.DecodeString(clean)
	if err != nil {
		return "", fmt.Errorf("decode file %s: %w", path, err)
	}
	return string(dec), nil
}

// FileCommit is one entry in a design doc's version history on the `design` branch.
type FileCommit struct {
	SHA     string `json:"sha"`
	Date    string `json:"date"`
	Message string `json:"message"`
}

// ListCommitsForPath returns the commits that touched `path` on `branch`, newest
// first — the version history of a design doc. Empty (not an error) when the branch
// or path does not exist yet.
func (c *Client) ListCommitsForPath(ctx context.Context, repoURL, branch, path string) ([]FileCommit, error) {
	slug, err := slugFromURL(repoURL)
	if err != nil {
		return nil, err
	}
	endpoint := fmt.Sprintf("repos/%s/commits?sha=%s&per_page=50", slug, branch)
	if path != "" {
		endpoint += "&path=" + path
	}
	out, err := c.runner(ctx, "", "gh", "api", endpoint, "--jq",
		`[.[] | {sha: .sha, date: .commit.committer.date, message: (.commit.message | split("\n")[0])}]`)
	if err != nil {
		lo := strings.ToLower(out)
		if strings.Contains(out, "404") || strings.Contains(lo, "not found") || strings.Contains(lo, "no commit") {
			return nil, nil
		}
		return nil, fmt.Errorf("gh api commits %s: %w: %s", path, err, strings.TrimSpace(out))
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return nil, nil
	}
	var commits []FileCommit
	if err := json.Unmarshal([]byte(out), &commits); err != nil {
		return nil, fmt.Errorf("parse commits %s: %w", path, err)
	}
	return commits, nil
}

// ListCommits returns the recent commits on `branch` (no path filter) — the project's
// design history, for the project-level changelog. Empty when the branch is absent.
func (c *Client) ListCommits(ctx context.Context, repoURL, branch string) ([]FileCommit, error) {
	return c.ListCommitsForPath(ctx, repoURL, branch, "")
}

// WriteFile commits `content` to repo file `path` on `branch` via the GitHub
// contents API (PUT), creating the file or updating it in place. It is a no-op
// (changed=false, nil err) when the file already holds identical content, so a doc
// re-published unchanged on a later approval never errors or makes an empty commit.
// Used to persist each approved design doc to `dev` so the repo-backed Especificación
// view reflects approved specs incrementally, not only after the final docs PR.
func (c *Client) WriteFile(ctx context.Context, repoURL, branch, path, content, message string) (bool, error) {
	slug, err := slugFromURL(repoURL)
	if err != nil {
		return false, err
	}
	// Fetch the current blob sha (needed to UPDATE an existing file). A missing file
	// yields an error we treat as "create new" (sha stays empty).
	sha := ""
	if out, err := c.runner(ctx, "", "gh", "api",
		fmt.Sprintf("repos/%s/contents/%s?ref=%s", slug, path, branch), "--jq", ".sha"); err == nil {
		sha = strings.TrimSpace(out)
	}
	// Skip the PUT when the file already has identical content (avoids empty commits
	// and the contents API's 422 "no commit was created").
	if sha != "" {
		if existing, err := c.ReadFile(ctx, repoURL, path, branch); err == nil && existing == content {
			return false, nil
		}
	}
	body := map[string]string{
		"message": message,
		"content": base64.StdEncoding.EncodeToString([]byte(content)),
		"branch":  branch,
	}
	if sha != "" {
		body["sha"] = sha
	}
	// Pass the JSON body via a temp file (--input) so large docs/mockups never hit the
	// shell ARG_MAX limit that inline -f fields would.
	bj, err := json.Marshal(body)
	if err != nil {
		return false, err
	}
	tmp, err := os.CreateTemp("", "ghput-*.json")
	if err != nil {
		return false, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(bj); err != nil {
		tmp.Close()
		return false, err
	}
	tmp.Close()
	out, err := c.runner(ctx, "", "gh", "api", "-X", "PUT",
		fmt.Sprintf("repos/%s/contents/%s", slug, path), "--input", tmp.Name())
	if err != nil {
		return false, fmt.Errorf("gh api put %s: %w: %s", path, err, strings.TrimSpace(out))
	}
	return true, nil
}
