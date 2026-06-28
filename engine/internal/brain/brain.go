package brain

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Event kinds the Brain emits over the bus (consumed by the console panel).
const (
	EvtToken  = "assistant.token"  // a streamed text delta {text}
	EvtAction = "assistant.action" // a proposed mutating action {action_id, tool, args}
	EvtDone   = "assistant.done"   // turn finished {error?}
)

const (
	approvalTimeout = 10 * time.Minute // a proposed action auto-rejects if unanswered
	turnDeadline    = 15 * time.Minute // whole-turn wall clock
	maxToolRounds   = 12               // bound the tool-use loop
)

// Brain runs the per-project conversational tool-use loop inside the control plane.
type Brain struct {
	llm   LLM
	ops   ControlOps
	store *Store
	emit  func(convID, typ string, data map[string]any)

	mu      sync.Mutex
	pending map[string]chan bool // actionID → approval channel (in-process)
}

// New builds a Brain. emit publishes an event scoped to a conversation id (wired
// to store.AppendEvent by the caller); may be nil in tests.
func New(llm LLM, ops ControlOps, st *Store, emit func(convID, typ string, data map[string]any)) *Brain {
	return &Brain{llm: llm, ops: ops, store: st, emit: emit, pending: map[string]chan bool{}}
}

// Send appends the user message and runs the turn ASYNCHRONOUSLY, streaming over
// the bus. Returns the conversation id immediately. The request context is NOT
// used for the goroutine (it ends when the handler returns); the turn has its own
// deadline.
func (b *Brain) Send(projectID, role, userMsg string) (string, error) {
	convID, err := b.store.ConversationFor(projectID)
	if err != nil {
		return "", err
	}
	if err := b.store.AppendMessage(convID, "user", userMsg); err != nil {
		return "", err
	}
	go b.run(convID, projectID, role)
	return convID, nil
}

func (b *Brain) fire(convID, typ string, data map[string]any) {
	if b.emit != nil {
		b.emit(convID, typ, data)
	}
}

func (b *Brain) run(convID, projectID, role string) {
	ctx, cancel := context.WithTimeout(context.Background(), turnDeadline)
	defer cancel()

	msgs, err := b.store.History(convID)
	if err != nil {
		b.fire(convID, EvtDone, map[string]any{"error": err.Error()})
		return
	}
	system := systemPrompt(projectID)

	for round := 0; round < maxToolRounds; round++ {
		res, err := b.llm.Stream(ctx, system, msgs, toolList(), func(t string) {
			b.fire(convID, EvtToken, map[string]any{"text": t})
		})
		if err != nil {
			b.fire(convID, EvtDone, map[string]any{"error": err.Error()})
			return
		}
		if res.Text != "" {
			_ = b.store.AppendMessage(convID, "assistant", res.Text)
		}
		msgs = append(msgs, assistantMessage(res))

		if res.StopReason != "tool_use" || len(res.ToolUses) == 0 {
			b.fire(convID, EvtDone, map[string]any{})
			return
		}

		results := make([]ContentBlock, 0, len(res.ToolUses))
		for _, tu := range res.ToolUses {
			content, isErr := b.execTool(ctx, convID, projectID, role, tu)
			results = append(results, ContentBlock{Type: "tool_result", ToolUseID: tu.ID, Content: content, IsError: isErr})
		}
		msgs = append(msgs, Message{Role: "user", Content: results})
	}
	b.fire(convID, EvtDone, map[string]any{"error": "tool loop exceeded"})
}

// execTool runs one tool call: reversible tools execute immediately; mutating ones
// are proposed and block until the human approves/rejects (or the timeout).
func (b *Brain) execTool(ctx context.Context, convID, projectID, role string, tu ToolUse) (string, bool) {
	def, err := lookup(tu.Name)
	if err != nil {
		return err.Error(), true
	}
	if !roleAllows(def.MinRole, role) {
		return fmt.Sprintf("permission denied: %q requires role %q", tu.Name, def.MinRole), true
	}
	if def.Kind == Reversible {
		out, err := def.Run(b.ops, projectID, tu.Input)
		if err != nil {
			return err.Error(), true
		}
		return out, false
	}
	// Mutating → propose to the human, wait. Register the approval channel BEFORE
	// emitting the proposal so a Resolve that races right behind the event finds it.
	actionID, err := b.store.CreateAction(convID, tu.Name, string(tu.Input))
	if err != nil {
		return err.Error(), true
	}
	ch := make(chan bool, 1)
	b.mu.Lock()
	b.pending[actionID] = ch
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(b.pending, actionID)
		b.mu.Unlock()
	}()

	b.fire(convID, EvtAction, map[string]any{"action_id": actionID, "tool": tu.Name, "args": tu.Input})

	approved := false
	select {
	case approved = <-ch:
	case <-time.After(approvalTimeout):
	case <-ctx.Done():
	}
	if !approved {
		_ = b.store.SetActionStatus(actionID, "rejected")
		return "the user rejected this action; do NOT retry it — acknowledge and ask what they want instead", false
	}
	_ = b.store.SetActionStatus(actionID, "approved")
	out, err := def.Run(b.ops, projectID, tu.Input)
	if err != nil {
		return err.Error(), true
	}
	return out, false
}

// Resolve answers a proposed mutating action. Called by the approve/reject handler.
func (b *Brain) Resolve(actionID string, approved bool) error {
	b.mu.Lock()
	ch, ok := b.pending[actionID]
	b.mu.Unlock()
	if !ok {
		st, err := b.store.ActionStatus(actionID)
		if err != nil {
			return err
		}
		if st != "pending" {
			return fmt.Errorf("action already %s", st)
		}
		return fmt.Errorf("action is no longer awaiting (the turn ended)")
	}
	ch <- approved
	return nil
}

// History exposes the conversation for the UI.
func (b *Brain) History(projectID string) ([]map[string]any, string, error) {
	convID, err := b.store.ConversationFor(projectID)
	if err != nil {
		return nil, "", err
	}
	msgs, err := b.store.HistoryView(convID)
	return msgs, convID, err
}

func assistantMessage(res StreamResult) Message {
	var blocks []ContentBlock
	if res.Text != "" {
		blocks = append(blocks, ContentBlock{Type: "text", Text: res.Text})
	}
	for _, tu := range res.ToolUses {
		blocks = append(blocks, ContentBlock{Type: "tool_use", ID: tu.ID, Name: tu.Name, Input: tu.Input})
	}
	return Message{Role: "assistant", Content: blocks}
}

func systemPrompt(projectID string) string {
	return `You are the Brain — the in-app assistant for the aiuda-forge project "` + projectID + `".
You help the user review the project, control the autonomous factory, diagnose failures, and propose changes.

You have tools that ARE the control plane's own operations. Use them to get REAL state — never guess run ids, statuses, or counts; call a tool.

CRITICAL — reporting state without lying:
- To answer "how is it going / what's running / is it paused / what needs approval", ALWAYS call get_state FIRST, in THIS turn. It returns the DIGESTED current truth (pause flag, ACTIVE runs, awaiting gates, a count of old terminal runs).
- NEVER infer the current state from list_runs or from earlier messages in this conversation. list_runs includes every OLD failed/cancelled run; summarizing it produces a confidently WRONG narrative ("paused", "nothing running") that contradicts reality.
- If get_state shows active_runs, the factory IS working — do NOT claim it's paused or idle. Only say "paused" if get_state.paused is true. Only say a run is QUEUED/DONE/etc. if a tool says so for THAT run id.
- When unsure, call the tool again. Do not narrate state you did not just verify.

Two kinds of tools:
- Reversible (read + safe control: get_state, get_status, get_metrics, list_runs, get_run, pause, resume, cancel_run, retry_run, requeue_run) — call these directly.
- Mutating (launch_run, approve_step, reject_step) — calling these PROPOSES the action to the human, who approves or rejects in the UI. If rejected, do not retry; acknowledge and ask what they prefer.

Be concise and concrete. Match the user's language (Spanish or English). When you report status, summarize what matters (progress, what's running, what failed and why) rather than dumping raw JSON.`
}
