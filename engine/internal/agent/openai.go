package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// OpenAIBackend calls any OpenAI-compatible /chat/completions endpoint with
// streaming. It is a pure-generation backend: it does NOT implement a tool-use
// loop, so it cannot edit files. Suitable for generation-only steps (summaries,
// changelog, etc.); for agentic steps with file editing, use ClaudeBackend.
type OpenAIBackend struct {
	BaseURL string // e.g. "http://localhost:2022/v1" (Cursor) or "https://api.openai.com/v1"
	APIKey  string // empty = no Authorization header (for local endpoints that don't require it)
	Model   string // default model; overridden by opts.Model if set
}

// Run POSTs a streaming chat-completion request and fans each content delta out
// as a KindText event, returning the concatenated text as the terminal Result.
func (b OpenAIBackend) Run(ctx context.Context, prompt string, opts Options, onEvent func(Event)) (Result, error) {
	model := b.Model
	if opts.Model != "" {
		model = opts.Model
	}

	reqBody, err := json.Marshal(map[string]any{
		"model":    model,
		"messages": []map[string]string{{"role": "user", "content": prompt}},
		"stream":   true,
	})
	if err != nil {
		return Result{Success: false}, fmt.Errorf("openai marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.BaseURL+"/chat/completions", bytes.NewReader(reqBody))
	if err != nil {
		return Result{Success: false}, fmt.Errorf("openai build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if b.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+b.APIKey)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Result{Success: false}, fmt.Errorf("openai request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body := make([]byte, 2048)
		n, _ := resp.Body.Read(body)
		return Result{Success: false}, fmt.Errorf("openai status %d: %s", resp.StatusCode, strings.TrimSpace(string(body[:n])))
	}

	var text strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		// ctx cancellation surfaces as a scanner read error, but check explicitly
		// so we stop promptly even between buffered lines.
		if ctx.Err() != nil {
			return Result{Text: text.String(), Success: false}, ctx.Err()
		}
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue // tolerate keep-alive / non-JSON noise
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		delta := chunk.Choices[0].Delta.Content
		if delta == "" {
			continue
		}
		text.WriteString(delta)
		if onEvent != nil {
			onEvent(Event{Kind: KindText, Text: delta})
		}
	}
	if err := scanner.Err(); err != nil {
		if ctx.Err() != nil {
			return Result{Text: text.String(), Success: false}, ctx.Err()
		}
		return Result{Text: text.String(), Success: false}, fmt.Errorf("openai stream: %w", err)
	}

	out := text.String()
	if onEvent != nil {
		onEvent(Event{Kind: KindResult, Text: out})
	}
	return Result{Text: out, Success: true, NumTurns: 1}, nil
}
