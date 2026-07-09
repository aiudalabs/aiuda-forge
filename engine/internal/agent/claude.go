package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

// CliBackend spawns an agent CLI in headless print mode and streams its NDJSON
// output. It is protocol-agnostic: the CLI to invoke comes from BaseArgv (set
// by the backend registry), and the stream is parsed with the claude-code
// stream-json protocol (parseStreamLine). Any CLI that implements that protocol
// — claude, opencode, etc. — works without adding Go code.
//
// The only required YAML to add a new engine is engine/registry/backends/<id>.yaml.
type CliBackend struct {
	// BaseArgv is the base command and fixed flags, e.g.:
	//   ["claude", "-p", "--output-format", "stream-json", "--verbose", "--permission-mode", "acceptEdits"]
	// When nil/empty, defaults to the claude CLI argv so existing deployments
	// need no configuration change (zero-value = claude, same as before).
	BaseArgv []string

	// ExtraArgs are appended verbatim after the dynamic flags and before the
	// prompt — an escape hatch for new CLI flags not yet in the schema.
	ExtraArgs []string
}

// defaultArgv is the argv used when BaseArgv is nil/empty. It targets the
// claude CLI so the zero-value CliBackend (and the ClaudeBackend alias) behave
// exactly as the old ClaudeBackend{} did.
var defaultArgv = []string{
	"claude", "-p",
	"--output-format", "stream-json",
	"--verbose",
	"--permission-mode", "acceptEdits",
}

// ClaudeBackend is a type alias for CliBackend so existing code that creates
// agent.ClaudeBackend{} or refers to ClaudeBackend by name compiles unchanged.
type ClaudeBackend = CliBackend

// Run spawns the CLI and streams its NDJSON output.
func (c CliBackend) Run(ctx context.Context, prompt string, opts Options, onEvent func(Event)) (Result, error) {
	base := c.BaseArgv
	if len(base) == 0 {
		base = defaultArgv
	}

	args := make([]string, len(base), len(base)+8)
	copy(args, base)

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

	// Run INSIDE the per-task sandbox when one is provided; otherwise run on the
	// host with the legacy auth env (used by pure unit tests).
	var hostArgv, hostEnv []string
	if opts.Sandbox != nil {
		hostArgv, hostEnv = opts.Sandbox.WrapAgent(args, opts.ContainerEnv)
	} else {
		hostArgv = args
		hostEnv = childEnv(opts.Auth)
	}

	// The absolute timeout is handled by a renewable timer in the watchdog
	// goroutine (not a hard context deadline) so it can be extended when the agent
	// is still actively streaming. An extended-thinking run emits thinking_tokens
	// events continuously — it is NOT stalled even if it exceeds the absolute wall.
	// Only true silence (no events for IdleTimeout) means stuck.

	cmd := exec.Command(hostArgv[0], hostArgv[1:]...)
	cmd.Dir = opts.Workdir
	cmd.Env = hostEnv
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Result{}, err
	}
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return Result{}, fmt.Errorf("start %s: %w", hostArgv[0], err)
	}

	done := make(chan struct{})
	defer close(done)
	activity := make(chan struct{}, 1)
	var stalled atomic.Bool
	var absoluteTimedOut atomic.Bool
	var lastActivity atomic.Int64
	lastActivity.Store(time.Now().UnixNano())
	kill := func() {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
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
		var absC <-chan time.Time
		var absTimer *time.Timer
		if opts.Timeout > 0 {
			absTimer = time.NewTimer(opts.Timeout)
			absC = absTimer.C
			defer absTimer.Stop()
		}
		for {
			select {
			case <-ctx.Done():
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
			case <-idleC:
				// The idle timer fired, but the `activity` channel is a lossy signal
				// (buffered-1, non-blocking send): a burst of lines can drop a reset, and
				// under load this goroutine can be scheduled late enough that the timer
				// fires while the agent is in fact still streaming. So decide on the
				// AUTHORITATIVE monotonic `lastActivity` timestamp (the same source the
				// absolute-timeout branch trusts), not on whether a reset was processed in
				// time. Only genuine silence for the full window is a stall; otherwise
				// re-arm for the remaining window. This makes the watchdog robust to
				// goroutine-scheduling jitter (no false kills of a healthy agent).
				idleFor := time.Since(time.Unix(0, lastActivity.Load()))
				if idleFor < opts.IdleTimeout {
					idleTimer.Reset(opts.IdleTimeout - idleFor)
					continue
				}
				stalled.Store(true)
				kill()
				return
			case <-absC:
				idleFor := time.Since(time.Unix(0, lastActivity.Load()))
				if opts.IdleTimeout > 0 && idleFor < opts.IdleTimeout {
					absTimer.Reset(opts.Timeout)
					log.Printf("agent: absolute timeout elapsed but agent still active (idle %s < %s); extending", idleFor.Round(time.Second), opts.IdleTimeout)
				} else {
					absoluteTimedOut.Store(true)
					kill()
					return
				}
			}
		}
	}()

	var result Result
	sawResult := false
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 1024*1024), 16*1024*1024)
	for scanner.Scan() {
		lastActivity.Store(time.Now().UnixNano())
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
			continue
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

	if stalled.Load() {
		return result, fmt.Errorf("agent stalled: no streamed output for %s", opts.IdleTimeout)
	}
	if absoluteTimedOut.Load() {
		return result, fmt.Errorf("agent hit the absolute timeout of %s", opts.Timeout)
	}
	if ctx.Err() != nil {
		return result, context.Canceled
	}
	if scanErr != nil {
		return result, fmt.Errorf("agent stream scan failed: %w", scanErr)
	}
	if !sawResult {
		if waitErr != nil {
			return result, fmt.Errorf("agent exited without result: %w", waitErr)
		}
		return result, fmt.Errorf("agent produced no result line")
	}
	return result, nil
}

// killContainer force-removes the docker container whose id was written to
// cidFile. No-op when cidFile is empty or unreadable.
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

// parseStreamLine converts one NDJSON object (claude-code stream-json protocol)
// into events + (maybe) the terminal result. Shared by all CLI backends that
// use this protocol (claude, opencode, …).
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
			case "thinking":
				if t, _ := b["thinking"].(string); t != "" {
					events = append(events, Event{Kind: KindThinking, Text: t, Raw: b})
				}
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

func jsonInt(v any) int {
	if f, ok := v.(float64); ok {
		return int(f)
	}
	return 0
}

// childEnv builds the environment for the agent process per the auth mode.
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
		// Use the logged-in session; do not inject a key.
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
