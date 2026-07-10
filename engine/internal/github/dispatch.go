package github

// Dispatch surface (F2 pivot): create Copilot agent tasks (Agent tasks REST API,
// public preview — pinned via X-GitHub-Api-Version) and fire workflow_dispatch
// for the claude-code-action channel. Prompts can be long (a whole sprint), so
// bodies travel via temp file --input, same as CreateIssue.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

// agentTasksAPIVersion pins the Agent tasks public-preview shape we tested live
// (ADR-2026-07-03). Isolated here so a preview change is a one-line fix.
const agentTasksAPIVersion = "2026-03-10"

// ErrTaskNotFound is returned by AgentTaskState when the Agent tasks API answers
// 404 "not found" for a task id. This is NOT a transient error: a task that WAS
// running and now 404s no longer exists (Copilot purges the registry record when
// the run ends — completed, failed, or out of credits), and the ephemeral preview
// endpoint stops resolving it by id. The conductor's dead-session sweep treats a
// SUSTAINED 404 (N consecutive) as a dead session — distinct from a 5xx/timeout,
// which stays transient and keeps the existing backoff.
var ErrTaskNotFound = errors.New("agent task not found")

// isNotFound recognizes gh api's 404 for a task id regardless of exact wording
// ("not found", "HTTP 404", "(404)"). execRunner merges stdout+stderr, so the
// JSON body {"message":"not found"} AND the `gh: not found (HTTP 404)` line are
// both in the captured output.
func isNotFound(out string) bool {
	low := strings.ToLower(out)
	return strings.Contains(low, "not found") || strings.Contains(low, "http 404") || strings.Contains(low, "(404)")
}

// CreateAgentTask starts a Copilot cloud-agent task on repoURL. model "" lets
// GitHub pick (auto). Returns the task's html_url when the API exposes it.
// CreateAgentTask con degradación: si el token user-to-server de la App recibe un
// 403 de la Agent tasks API (el permiso Copilot está pendiente en el manifest), se
// reintenta con la auth del host (dev/single-tenant). CLAVE: el 403 tiene un mensaje
// DISTINTO por verbo — "does not have read access" en el GET de disponibilidad, pero
// un simple "forbidden" en el POST de creación. La condición vieja sólo matcheaba el
// primero, así que el POST de creación NUNCA degradaba → Copilot caía a claude_action
// por el fallback silencioso del conductor. Ahora cualquier 403/forbidden del tenant
// degrada. En cloud sin host auth el error se propaga igual (New() sin token falla).
func (c *Client) CreateAgentTask(ctx context.Context, repoURL, prompt, model string) (string, error) {
	url, err := c.createAgentTask(ctx, repoURL, prompt, model)
	if err != nil && c.token != "" && isForbidden(err) {
		return New().createAgentTask(ctx, repoURL, prompt, model)
	}
	return url, err
}

// isForbidden reconoce un 403 de gh api sin importar el verbo/mensaje exacto
// ("forbidden", "does not have read access", "HTTP 403").
func isForbidden(err error) bool {
	if err == nil {
		return false
	}
	low := strings.ToLower(err.Error())
	return strings.Contains(low, "forbidden") || strings.Contains(low, "http 403")
}

func (c *Client) createAgentTask(ctx context.Context, repoURL, prompt, model string) (string, error) {
	slug, err := slugFromURL(repoURL)
	if err != nil {
		return "", err
	}
	body := map[string]any{
		"prompt":              prompt,
		"create_pull_request": true,
	}
	if model != "" {
		body["model"] = model
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp("", "ghtask-*.json")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(payload); err != nil {
		tmp.Close()
		return "", err
	}
	tmp.Close()
	out, err := c.runner(ctx, "", "gh", "api", "-X", "POST",
		"-H", "X-GitHub-Api-Version: "+agentTasksAPIVersion,
		fmt.Sprintf("/agents/repos/%s/tasks", slug), "--input", tmp.Name())
	if err != nil {
		return "", fmt.Errorf("gh api create agent task: %w: %s", err, strings.TrimSpace(out))
	}
	var res struct {
		HTMLURL string `json:"html_url"`
	}
	_ = json.Unmarshal([]byte(out), &res) // URL ausente no es fallo del dispatch
	return res.HTMLURL, nil
}

// AgentTaskState returns the state of a Copilot agent task ("queued",
// "in_progress", "completed", "failed", …) — the conductor's dead-session
// detector reads it to un-pin stories whose agent died.
func (c *Client) AgentTaskState(ctx context.Context, repoURL, taskID string) (string, error) {
	slug, err := slugFromURL(repoURL)
	if err != nil {
		return "", err
	}
	out, err := c.runner(ctx, "", "gh", "api",
		"-H", "X-GitHub-Api-Version: "+agentTasksAPIVersion,
		fmt.Sprintf("/agents/repos/%s/tasks/%s", slug, taskID), "--jq", ".state")
	if err != nil {
		if isNotFound(out) {
			// Wrap the sentinel so the sweep can distinguish a purged task from a
			// transient 5xx/timeout (errors.Is unwraps through %w).
			return "", fmt.Errorf("gh api agent task %s: %w: %s", taskID, ErrTaskNotFound, strings.TrimSpace(out))
		}
		return "", fmt.Errorf("gh api agent task %s: %w: %s", taskID, err, strings.TrimSpace(out))
	}
	return strings.TrimSpace(out), nil
}

// DispatchWorkflow fires workflow_dispatch on workflowFile@ref with inputs (the
// claude-code-action channel: inputs["prompt"]). GitHub responds 204/no body.
func (c *Client) DispatchWorkflow(ctx context.Context, repoURL, workflowFile, ref string, inputs map[string]string) error {
	slug, err := slugFromURL(repoURL)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{"ref": ref, "inputs": inputs})
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp("", "ghwf-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(payload); err != nil {
		tmp.Close()
		return err
	}
	tmp.Close()
	out, err := c.runner(ctx, "", "gh", "api", "-X", "POST",
		fmt.Sprintf("repos/%s/actions/workflows/%s/dispatches", slug, workflowFile),
		"--input", tmp.Name())
	if err != nil {
		return fmt.Errorf("gh api workflow_dispatch %s: %w: %s", workflowFile, err, strings.TrimSpace(out))
	}
	return nil
}

// AgentTasksAvailable sondea si la Agent tasks API de Copilot responde para
// este repo (el canal copilot depende de la suscripción/ajustes del usuario).
// 200 = disponible; 403/404/402 = no (con la razón cruda para la UI).
func (c *Client) AgentTasksAvailable(ctx context.Context, repoURL string) (bool, string) {
	slug, err := slugFromURL(repoURL)
	if err != nil {
		return false, err.Error()
	}
	out, err := c.runner(ctx, "", "gh", "api",
		"-H", "X-GitHub-Api-Version: "+agentTasksAPIVersion,
		fmt.Sprintf("/agents/repos/%s/tasks?per_page=1", slug))
	if err != nil {
		msg := strings.TrimSpace(out)
		if len(msg) > 140 {
			msg = msg[:140]
		}
		return false, msg
	}
	return true, ""
}
