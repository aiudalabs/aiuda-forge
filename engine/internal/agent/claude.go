package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

// ClaudeBackend runs Anthropic's `claude` CLI in headless print mode. It ports
// the validated semantics from v1's claude_code engine: stream-json NDJSON
// parsing, hard timeout, and process-group kill on cancel so no agent is
// orphaned. This is the REAL engine; the automated suite uses FakeBackend.
type ClaudeBackend struct {
	// Bin is the claude binary (default "claude").
	Bin string
	// ExtraArgs are appended verbatim (escape hatch for new CLI flags).
	ExtraArgs []string
}

// Run spawns `claude -p` and streams its NDJSON output.
func (c ClaudeBackend) Run(ctx context.Context, prompt string, opts Options, onEvent func(Event)) (Result, error) {
	bin := c.Bin
	if bin == "" {
		bin = "claude"
	}

	args := []string{
		bin,
		"-p",
		"--output-format", "stream-json",
		"--verbose",
		"--permission-mode", "acceptEdits",
	}
	if opts.Model != "" {
		args = append(args, "--model", opts.Model)
	}
	if len(opts.AllowedTools) > 0 {
		args = append(args, "--allowedTools", strings.Join(opts.AllowedTools, ","))
	}
	if opts.SystemPrompt != "" {
		args = append(args, "--append-system-prompt", opts.SystemPrompt)
	}
	args = append(args, c.ExtraArgs...)
	args = append(args, prompt)

	// Run INSIDE the per-task sandbox when one is provided (v1's
	// `popen_cmd = sandbox.wrap(cmd)`): only the invoked argv + env change; the
	// streaming loop below is identical. Without a sandbox, run on the host with
	// the legacy auth env (used by pure unit tests).
	var hostArgv, hostEnv []string
	if opts.Sandbox != nil {
		hostArgv, hostEnv = opts.Sandbox.WrapAgent(args, opts.ContainerEnv)
	} else {
		hostArgv = args
		hostEnv = childEnv(opts.Auth)
	}

	// Timeout via a child context; cancellation kills the whole process group.
	runCtx := ctx
	var cancel context.CancelFunc
	if opts.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	cmd := exec.Command(hostArgv[0], hostArgv[1:]...)
	cmd.Dir = opts.Workdir // ignored by `docker run` (-w sets the container cwd); needed for local
	cmd.Env = hostEnv
	// New process group so we can kill the agent (and the docker client / its
	// children) on cancel.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Result{}, err
	}
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return Result{}, fmt.Errorf("start claude: %w", err)
	}

	// Kill the process group when runCtx is done (timeout or external cancel).
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-runCtx.Done():
			if cmd.Process != nil {
				// Negative pid = the whole process group.
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			}
		case <-done:
		}
	}()

	var result Result
	sawResult := false
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 1024*1024), 16*1024*1024) // large lines (tool payloads)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			continue // tolerate non-JSON noise
		}
		ev, res, isResult := parseStreamLine(obj)
		if onEvent != nil {
			for _, e := range ev {
				onEvent(e)
			}
		}
		if isResult {
			result = res
			sawResult = true
		}
	}
	waitErr := cmd.Wait()

	if err := runCtx.Err(); err == context.DeadlineExceeded {
		return result, fmt.Errorf("claude timed out after %s", opts.Timeout)
	} else if err == context.Canceled {
		return result, context.Canceled
	}
	if !sawResult {
		if waitErr != nil {
			return result, fmt.Errorf("claude exited without result: %w", waitErr)
		}
		return result, fmt.Errorf("claude produced no result line")
	}
	return result, nil
}

// parseStreamLine converts one NDJSON object into events + (maybe) the result.
func parseStreamLine(obj map[string]any) (events []Event, result Result, isResult bool) {
	typ, _ := obj["type"].(string)
	switch typ {
	case "assistant":
		msg, _ := obj["message"].(map[string]any)
		content, _ := msg["content"].([]any)
		for _, blk := range content {
			b, _ := blk.(map[string]any)
			switch b["type"] {
			case "text":
				if t, _ := b["text"].(string); t != "" {
					events = append(events, Event{Kind: KindText, Text: t, Raw: b})
				}
			case "tool_use":
				name, _ := b["name"].(string)
				events = append(events, Event{Kind: KindToolUse, Tool: name, Raw: b})
			}
		}
	case "system":
		events = append(events, Event{Kind: KindSystem, Raw: obj})
	case "result":
		text, _ := obj["result"].(string)
		isErr, _ := obj["is_error"].(bool)
		cost, _ := obj["total_cost_usd"].(float64)
		turns := 0
		if n, ok := obj["num_turns"].(float64); ok {
			turns = int(n)
		}
		result = Result{Text: text, Success: !isErr, CostUSD: cost, NumTurns: turns, Raw: obj}
		events = append(events, Event{Kind: KindResult, Text: text, Raw: obj})
		isResult = true
	}
	return events, result, isResult
}

// childEnv builds the environment for the agent process per the auth mode. The
// sandbox (Wave 4) further restricts this; here we only set/clear the auth vars.
func childEnv(auth Auth) []string {
	env := os.Environ()
	switch auth.Mode {
	case AuthAPIKey:
		env = setEnv(env, "ANTHROPIC_API_KEY", auth.Token)
		env = unsetEnv(env, "CLAUDE_CODE_OAUTH_TOKEN")
	case AuthOAuthToken:
		env = setEnv(env, "CLAUDE_CODE_OAUTH_TOKEN", auth.Token)
		env = unsetEnv(env, "ANTHROPIC_API_KEY")
	case AuthSubscription, "":
		// Use the logged-in session; do not inject a key. Leave env as-is.
	}
	return env
}

func setEnv(env []string, key, val string) []string {
	prefix := key + "="
	out := env[:0:0]
	out = append(out, env...)
	for i, kv := range out {
		if strings.HasPrefix(kv, prefix) {
			out[i] = prefix + val
			return out
		}
	}
	return append(out, prefix+val)
}

func unsetEnv(env []string, key string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if !strings.HasPrefix(kv, prefix) {
			out = append(out, kv)
		}
	}
	return out
}
