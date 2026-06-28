package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
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
	// Killing the host docker CLIENT process group alone leaves the container
	// (a child of dockerd, not of our process group) orphaned, holding the egress
	// net + /work mount. So we ALSO remove the container by id (M1) when a cidfile
	// was requested — `--rm` only cleans up on a normal exit.
	done := make(chan struct{})
	defer close(done)
	// activity is poked on every streamed line; the watchdog resets the idle timer
	// on each poke. A healthy agent streams continuously (tool calls, text), so it
	// never trips the idle timer; a hung/stalled one goes silent and gets killed in
	// IdleTimeout instead of waiting out the whole absolute wall.
	activity := make(chan struct{}, 1)
	var stalled atomic.Bool
	kill := func() {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) // negative pid = process group
		}
		killContainer(opts.CIDFile)
	}
	go func() {
		var idleC <-chan time.Time
		var idleTimer *time.Timer
		if opts.IdleTimeout > 0 {
			idleTimer = time.NewTimer(opts.IdleTimeout)
			idleC = idleTimer.C
			defer idleTimer.Stop()
		}
		for {
			select {
			case <-runCtx.Done(): // absolute backstop OR external cancel
				kill()
				return
			case <-done:
				return
			case <-activity:
				if idleTimer != nil {
					if !idleTimer.Stop() {
						select {
						case <-idleTimer.C:
						default:
						}
					}
					idleTimer.Reset(opts.IdleTimeout)
				}
			case <-idleC: // no streamed output for IdleTimeout → stalled
				stalled.Store(true)
				kill()
				return
			}
		}
	}()

	var result Result
	sawResult := false
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 1024*1024), 16*1024*1024) // large lines (tool payloads)
	for scanner.Scan() {
		// Heartbeat: every line of output keeps the agent alive (non-blocking poke).
		select {
		case activity <- struct{}{}:
		default:
		}
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
	scanErr := scanner.Err()
	waitErr := cmd.Wait()

	// A stalled kill (idle watchdog) takes precedence: it's the actionable failure
	// ("the agent hung", not "the task is too long for the wall").
	if stalled.Load() {
		return result, fmt.Errorf("claude stalled: no streamed output for %s", opts.IdleTimeout)
	}
	if err := runCtx.Err(); err == context.DeadlineExceeded {
		return result, fmt.Errorf("claude hit the absolute timeout of %s", opts.Timeout)
	} else if err == context.Canceled {
		return result, context.Canceled
	}
	// A scan error (e.g. bufio.ErrTooLong on a >16 MB tool payload) means the
	// stream was truncated: any result we did or did not see is unreliable. Fail
	// hard rather than silently truncating to SUCCESS or misreporting "no result".
	if scanErr != nil {
		return result, fmt.Errorf("claude stream scan failed: %w", scanErr)
	}
	if !sawResult {
		if waitErr != nil {
			return result, fmt.Errorf("claude exited without result: %w", waitErr)
		}
		return result, fmt.Errorf("claude produced no result line")
	}
	return result, nil
}

// killContainer force-removes the docker container whose id was written to
// cidFile (by `docker run --cidfile`). It is a no-op when cidFile is empty (no
// docker sandbox) or unreadable. docker writes the id at container start, which
// may lag our kill slightly, so we retry a few times before giving up. Best
// effort: an already-gone container is fine, the goal is to never orphan one.
func killContainer(cidFile string) {
	if cidFile == "" {
		return
	}
	for i := 0; i < 10; i++ {
		b, err := os.ReadFile(cidFile)
		id := strings.TrimSpace(string(b))
		if err == nil && id != "" {
			cmd := exec.Command("docker", "rm", "-f", id)
			cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
			_ = cmd.Run()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
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
		// Token usage is reported even on subscription billing (where cost may be 0),
		// so it is the meaningful "spend" signal there. Input includes cache create/read.
		var tin, tout int
		if u, ok := obj["usage"].(map[string]any); ok {
			tin = jsonInt(u["input_tokens"]) + jsonInt(u["cache_creation_input_tokens"]) + jsonInt(u["cache_read_input_tokens"])
			tout = jsonInt(u["output_tokens"])
		}
		result = Result{Text: text, Success: !isErr, CostUSD: cost, NumTurns: turns, TokensIn: tin, TokensOut: tout, Raw: obj}
		events = append(events, Event{Kind: KindResult, Text: text, Raw: obj})
		isResult = true
	}
	return events, result, isResult
}

// jsonInt coerces a decoded JSON number (always float64) to int; 0 if absent.
func jsonInt(v any) int {
	if f, ok := v.(float64); ok {
		return int(f)
	}
	return 0
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
