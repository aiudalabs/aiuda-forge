package github

// Write surface for the backlog export (F0 of the GitHub-native pivot): create
// labels/issues and wire native issue dependencies (blocked_by). Mirrors the
// read-side in github.go; everything goes through the runner seam so tests can
// fake `gh`. The blocked_by endpoint takes the BLOCKER's database id — CreateIssue
// returns it so the exporter can wire edges without a second fetch.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// CreatedIssue is the subset of the create-issue response the exporter needs.
type CreatedIssue struct {
	Number int   `json:"number"`
	ID     int64 `json:"id"` // database id — what dependencies/blocked_by expects
}

// EnsureLabel creates the label if it doesn't exist. Returns true when created,
// false when it already existed.
func (c *Client) EnsureLabel(ctx context.Context, repoURL, name, color string) (bool, error) {
	slug, err := slugFromURL(repoURL)
	if err != nil {
		return false, err
	}
	out, err := c.runner(ctx, "", "gh", "api", "-X", "POST",
		fmt.Sprintf("repos/%s/labels", slug),
		"-f", "name="+name, "-f", "color="+color)
	if err != nil {
		if strings.Contains(out, "already_exists") {
			return false, nil
		}
		return false, fmt.Errorf("gh api create label %q: %w: %s", name, err, strings.TrimSpace(out))
	}
	return true, nil
}

// CreateIssue opens an issue and returns its number + database id. The JSON body
// travels via a temp file (--input) so long story bodies never hit ARG_MAX.
func (c *Client) CreateIssue(ctx context.Context, repoURL, title, body string, labels []string) (CreatedIssue, error) {
	slug, err := slugFromURL(repoURL)
	if err != nil {
		return CreatedIssue{}, err
	}
	// Un slice nil se serializa como JSON null y GitHub lo rechaza con 422
	// ("For 'properties/labels', nil is not an array") — pasa con stories
	// manuales sin sprint/lane/epic. Sin labels, omitimos el campo.
	fields := map[string]any{"title": title, "body": body}
	if len(labels) > 0 {
		fields["labels"] = labels
	}
	payload, err := json.Marshal(fields)
	if err != nil {
		return CreatedIssue{}, err
	}
	tmp, err := os.CreateTemp("", "ghissue-*.json")
	if err != nil {
		return CreatedIssue{}, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(payload); err != nil {
		tmp.Close()
		return CreatedIssue{}, err
	}
	tmp.Close()
	out, err := c.runner(ctx, "", "gh", "api", "-X", "POST",
		fmt.Sprintf("repos/%s/issues", slug), "--input", tmp.Name())
	if err != nil {
		return CreatedIssue{}, fmt.Errorf("gh api create issue %q: %w: %s", title, err, strings.TrimSpace(out))
	}
	var created CreatedIssue
	if err := json.Unmarshal([]byte(out), &created); err != nil {
		return CreatedIssue{}, fmt.Errorf("decode create-issue response: %w", err)
	}
	return created, nil
}

// IssueID returns the database id of an existing issue (needed to wire a
// dependency onto an issue exported in a previous run).
func (c *Client) IssueID(ctx context.Context, repoURL string, number int) (int64, error) {
	slug, err := slugFromURL(repoURL)
	if err != nil {
		return 0, err
	}
	out, err := c.runner(ctx, "", "gh", "api",
		fmt.Sprintf("repos/%s/issues/%d", slug, number), "--jq", ".id")
	if err != nil {
		return 0, fmt.Errorf("gh api issue #%d: %w: %s", number, err, strings.TrimSpace(out))
	}
	var id int64
	if _, err := fmt.Sscanf(strings.TrimSpace(out), "%d", &id); err != nil {
		return 0, fmt.Errorf("parse issue id %q: %w", out, err)
	}
	return id, nil
}

// AddIssueBlockedBy marks issueNumber as blocked by the issue whose DATABASE id is
// blockedByID (native issue dependencies, GA 2025). Returns false when the edge
// already existed.
func (c *Client) AddIssueBlockedBy(ctx context.Context, repoURL string, issueNumber int, blockedByID int64) (bool, error) {
	slug, err := slugFromURL(repoURL)
	if err != nil {
		return false, err
	}
	out, err := c.runner(ctx, "", "gh", "api", "-X", "POST",
		fmt.Sprintf("repos/%s/issues/%d/dependencies/blocked_by", slug, issueNumber),
		"-F", fmt.Sprintf("issue_id=%d", blockedByID))
	if err != nil {
		low := strings.ToLower(out)
		if strings.Contains(low, "already") || strings.Contains(low, "duplicate") {
			return false, nil
		}
		return false, fmt.Errorf("gh api blocked_by #%d<-%d: %w: %s", issueNumber, blockedByID, err, strings.TrimSpace(out))
	}
	return true, nil
}

// CloseIssue cierra un issue como completado, con un comentario de contexto.
// El conductor lo usa para cerrar el loop cuando el PR de una story mergeó
// pero sus closing keywords no auto-cerraron (p.ej. escritos entre backticks,
// que GitHub no parsea — cazado en vivo).
func (c *Client) CloseIssue(ctx context.Context, repoURL string, number int, comment string) error {
	slug, err := slugFromURL(repoURL)
	if err != nil {
		return err
	}
	if comment != "" {
		out, err := c.runner(ctx, "", "gh", "api", "-X", "POST",
			fmt.Sprintf("repos/%s/issues/%d/comments", slug, number), "-f", "body="+comment)
		if err != nil {
			return fmt.Errorf("gh api comment issue #%d: %w: %s", number, err, strings.TrimSpace(out))
		}
	}
	out, err := c.runner(ctx, "", "gh", "api", "-X", "PATCH",
		fmt.Sprintf("repos/%s/issues/%d", slug, number),
		"-f", "state=closed", "-f", "state_reason=completed")
	if err != nil {
		return fmt.Errorf("gh api close issue #%d: %w: %s", number, err, strings.TrimSpace(out))
	}
	return nil
}

// RepoSecretExists verifica si un Actions secret existe en el repo.
func (c *Client) RepoSecretExists(ctx context.Context, repoURL, name string) (bool, error) {
	slug, err := slugFromURL(repoURL)
	if err != nil {
		return false, err
	}
	out, err := c.runner(ctx, "", "gh", "api", fmt.Sprintf("repos/%s/actions/secrets/%s", slug, name))
	if err != nil {
		if strings.Contains(out, "Not Found") || strings.Contains(out, "404") {
			return false, nil
		}
		return false, fmt.Errorf("gh api secret %s: %w: %s", name, err, strings.TrimSpace(out))
	}
	return true, nil
}

// SetRepoSecret crea/actualiza un Actions secret (gh cifra con la public key
// del repo por nosotros).
func (c *Client) SetRepoSecret(ctx context.Context, repoURL, name, value string) error {
	slug, err := slugFromURL(repoURL)
	if err != nil {
		return err
	}
	out, err := c.runner(ctx, "", "gh", "secret", "set", name, "-R", slug, "--body", value)
	if err != nil {
		return fmt.Errorf("gh secret set %s: %w: %s", name, err, strings.TrimSpace(out))
	}
	return nil
}
