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

// UpdateIssueBody reemplaza el cuerpo de un issue existente (PATCH). Lo usa el
// grooming JIT del conductor (internal/conductor/groom.go) para enriquecer el
// issue de una story con el spec dev-ready + spec visual + inventario de
// componentes justo antes de despacharla. El body viaja por temp file (--input)
// para que un spec largo nunca choque con ARG_MAX (igual que CreateIssue).
func (c *Client) UpdateIssueBody(ctx context.Context, repoURL string, number int, body string) error {
	slug, err := slugFromURL(repoURL)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{"body": body})
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp("", "ghissue-body-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(payload); err != nil {
		tmp.Close()
		return err
	}
	tmp.Close()
	out, err := c.runner(ctx, "", "gh", "api", "-X", "PATCH",
		fmt.Sprintf("repos/%s/issues/%d", slug, number), "--input", tmp.Name())
	if err != nil {
		return fmt.Errorf("gh api update issue #%d body: %w: %s", number, err, strings.TrimSpace(out))
	}
	return nil
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

// SetIssueRunning marca los issues dados con el label `agent:running` al DESPACHAR
// por el canal claude_action. Es lo que cierra la ventana entre el dispatch y el
// arranque del workflow (que es quien normalmente pone el label en su primer step):
// sin esta marca inmediata, durante ese hueco la proyección no ve señal de "corriendo"
// (claude_action no asigna el issue ni abre PR y su sesión no ancla) → degrada la story
// a backlog y, en auto, la re-despacha en bucle = el flap ready/running. El paso
// if:always() del workflow sigue siendo quien LO QUITA al terminar. Best-effort:
// asegura el label (idempotente, mismo color/desc que claude.yml) y lo añade a cada
// issue; el step del workflow es el respaldo si algún add falla.
func (c *Client) SetIssueRunning(ctx context.Context, repoURL string, numbers []int) error {
	slug, err := slugFromURL(repoURL)
	if err != nil {
		return err
	}
	// Asegura que el label exista (upsert); si esto fallara, el add por-issue lo
	// auto-crea igual con color por defecto — la proyección sólo mira el NOMBRE.
	_, _ = c.runner(ctx, "", "gh", "label", "create", runningLabelName, "-R", slug,
		"-c", "FBCA04", "-d", "Un agente trabaja este issue AHORA (aiuda-forge)", "--force")
	var firstErr error
	for _, n := range numbers {
		if out, err := c.runner(ctx, "", "gh", "api", "-X", "POST",
			fmt.Sprintf("repos/%s/issues/%d/labels", slug, n),
			"-f", "labels[]="+runningLabelName); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("gh api add %s to #%d: %w: %s", runningLabelName, n, err, strings.TrimSpace(out))
			}
		}
	}
	return firstErr
}

// runningLabelName es el nombre del label observable en GitHub que señala "un agente
// está corriendo AHORA" (debe coincidir con projection.runningLabel y con claude.yml).
const runningLabelName = "agent:running"

// RemoveIssueRunning quita el label `agent:running` de los issues dados — la
// limpieza GitHub-observable que el conductor hace cuando declara MUERTA la sesión
// de un agente cuyo workflow claude.yml murió sin correr su paso if:always() (el
// caso créditos/kill). Sin esto el label queda pegado y la proyección deriva
// `running` para siempre. Best-effort e idempotente: un 404 (label ausente en el
// issue) NO es error — el objetivo ya se cumplió. Devuelve el primer error real.
func (c *Client) RemoveIssueRunning(ctx context.Context, repoURL string, numbers []int) error {
	slug, err := slugFromURL(repoURL)
	if err != nil {
		return err
	}
	var firstErr error
	for _, n := range numbers {
		out, err := c.runner(ctx, "", "gh", "api", "-X", "DELETE",
			fmt.Sprintf("repos/%s/issues/%d/labels/%s", slug, n, runningLabelName))
		if err != nil && !strings.Contains(strings.ToLower(out), "not found") && !strings.Contains(out, "404") {
			if firstErr == nil {
				firstErr = fmt.Errorf("gh api remove %s from #%d: %w: %s", runningLabelName, n, err, strings.TrimSpace(out))
			}
		}
	}
	return firstErr
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
