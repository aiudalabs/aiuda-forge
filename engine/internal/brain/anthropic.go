// Package brain is the per-project conversational assistant ("Brain"). It runs a
// tool-using LLM loop INSIDE the control plane: the model can call the control
// API's own operations as tools. Reversible tools execute automatically; mutating
// ones are proposed to the human for approval. The loop is driven by the Anthropic
// Messages API (this file) behind an LLM interface so tests inject a fake.
package brain

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultModel is the Brain's model when none is configured.
const DefaultModel = "claude-opus-4-8"

// Message is one turn in the conversation, in the Anthropic content-block format.
type Message struct {
	Role    string         `json:"role"` // "user" | "assistant"
	Content []ContentBlock `json:"content"`
}

// ContentBlock is a text, tool_use, or tool_result block. Fields are shared across
// the three shapes; omitempty keeps each marshalled block to its own fields.
type ContentBlock struct {
	Type string `json:"type"` // "text" | "tool_use" | "tool_result"
	// text
	Text string `json:"text,omitempty"`
	// tool_use
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
	// tool_result
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`
}

// Tool is a tool the model may call, described by a JSON Schema input.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// ToolUse is a tool call the model emitted in a turn.
type ToolUse struct {
	ID    string
	Name  string
	Input json.RawMessage
}

// StreamResult is the outcome of one assistant turn.
type StreamResult struct {
	Text       string    // concatenated assistant text
	ToolUses   []ToolUse // tool calls, in order
	StopReason string    // "end_turn" | "tool_use" | "max_tokens" | ...
}

// LLM is the model surface the Brain loop needs. Stream runs one assistant turn,
// invoking onText for each text delta (for live token streaming) and returning the
// full text + any tool calls + the stop reason. Tests provide a fake.
type LLM interface {
	Stream(ctx context.Context, system string, msgs []Message, tools []Tool, onText func(string)) (StreamResult, error)
}

// Client is the production LLM backed by the Anthropic Messages API.
type Client struct {
	apiKey    string
	model     string
	maxTokens int
	base      string
	http      *http.Client
}

// NewClient builds an Anthropic client. model "" → DefaultModel.
func NewClient(apiKey, model string) *Client {
	if model == "" {
		model = DefaultModel
	}
	return &Client{
		apiKey:    apiKey,
		model:     model,
		maxTokens: 4096,
		base:      "https://api.anthropic.com",
		http:      &http.Client{Timeout: 5 * time.Minute},
	}
}

type apiRequest struct {
	Model     string    `json:"model"`
	MaxTokens int       `json:"max_tokens"`
	System    string    `json:"system,omitempty"`
	Messages  []Message `json:"messages"`
	Tools     []Tool    `json:"tools,omitempty"`
	Stream    bool      `json:"stream"`
}

// Stream POSTs /v1/messages with stream:true and parses the SSE event stream,
// accumulating text and tool_use input-json deltas by content-block index.
func (c *Client) Stream(ctx context.Context, system string, msgs []Message, tools []Tool, onText func(string)) (StreamResult, error) {
	body, _ := json.Marshal(apiRequest{
		Model: c.model, MaxTokens: c.maxTokens, System: system,
		Messages: msgs, Tools: tools, Stream: true,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return StreamResult{}, err
	}
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")
	req.Header.Set("accept", "text/event-stream")

	resp, err := c.http.Do(req)
	if err != nil {
		return StreamResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return StreamResult{}, fmt.Errorf("anthropic messages: status %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return parseSSE(resp.Body, onText)
}

// blockAccum accumulates a single content block as its deltas arrive.
type blockAccum struct {
	typ       string
	id, name  string
	text      strings.Builder
	inputJSON strings.Builder
}

// parseSSE reads the Messages streaming protocol and assembles the turn result.
func parseSSE(r io.Reader, onText func(string)) (StreamResult, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	blocks := map[int]*blockAccum{}
	var res StreamResult

	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			break
		}
		var ev struct {
			Type         string `json:"type"`
			Index        int    `json:"index"`
			ContentBlock struct {
				Type string `json:"type"`
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"content_block"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				PartialJSON string `json:"partial_json"`
				StopReason  string `json:"stop_reason"`
			} `json:"delta"`
		}
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			continue // ignore keep-alives / non-JSON
		}
		switch ev.Type {
		case "content_block_start":
			blocks[ev.Index] = &blockAccum{typ: ev.ContentBlock.Type, id: ev.ContentBlock.ID, name: ev.ContentBlock.Name}
		case "content_block_delta":
			b := blocks[ev.Index]
			if b == nil {
				continue
			}
			if ev.Delta.Type == "text_delta" && ev.Delta.Text != "" {
				b.text.WriteString(ev.Delta.Text)
				if onText != nil {
					onText(ev.Delta.Text)
				}
			}
			if ev.Delta.Type == "input_json_delta" {
				b.inputJSON.WriteString(ev.Delta.PartialJSON)
			}
		case "message_delta":
			if ev.Delta.StopReason != "" {
				res.StopReason = ev.Delta.StopReason
			}
		case "message_stop":
			// end of stream
		}
	}
	if err := sc.Err(); err != nil {
		return res, err
	}
	// Assemble final blocks in index order.
	for i := 0; i < len(blocks); i++ {
		b := blocks[i]
		if b == nil {
			continue
		}
		switch b.typ {
		case "text":
			res.Text += b.text.String()
		case "tool_use":
			raw := b.inputJSON.String()
			if raw == "" {
				raw = "{}"
			}
			res.ToolUses = append(res.ToolUses, ToolUse{ID: b.id, Name: b.name, Input: json.RawMessage(raw)})
		}
	}
	return res, nil
}
