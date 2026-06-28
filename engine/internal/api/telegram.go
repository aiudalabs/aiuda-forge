package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"forge/internal/brain"
	"forge/internal/channels"
	"forge/internal/projects"
)

// ---- link-code store --------------------------------------------------------
//
// Binding a Telegram user to an aiuda-forge account needs proof the person owns the
// account. We avoid email by issuing a short-lived code from the AUTHENTICATED
// console; the user sends `/link <code>` to the bot, and the webhook consumes the
// code to bind (telegram user id → account). Codes live in memory with a TTL.

const linkCodeTTL = 10 * time.Minute

type linkCode struct {
	connector string
	userID    string
	expiresAt time.Time
}

type linkCodeStore struct {
	mu    sync.Mutex
	codes map[string]linkCode
}

func newLinkCodeStore() *linkCodeStore { return &linkCodeStore{codes: map[string]linkCode{}} }

// issue mints a code binding (connector, userID), valid for linkCodeTTL.
func (l *linkCodeStore) issue(connector, userID string, now time.Time) (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	code := strings.ToUpper(hex.EncodeToString(b)) // 8 hex chars
	l.mu.Lock()
	defer l.mu.Unlock()
	l.codes[code] = linkCode{connector: connector, userID: userID, expiresAt: now.Add(linkCodeTTL)}
	return code, nil
}

// consume validates+removes a code, returning the bound userID for connector.
func (l *linkCodeStore) consume(connector, code string, now time.Time) (string, bool) {
	code = strings.ToUpper(strings.TrimSpace(code))
	l.mu.Lock()
	defer l.mu.Unlock()
	c, ok := l.codes[code]
	if !ok || c.connector != connector || now.After(c.expiresAt) {
		if ok && now.After(c.expiresAt) {
			delete(l.codes, code)
		}
		return "", false
	}
	delete(l.codes, code)
	return c.userID, true
}

// ---- POST /channels/{connector}/link-code -----------------------------------

// issueLinkCode mints a link code for the authenticated user to send to the bot.
// Requires a user session (not the service token) — the code is the proof of
// account ownership.
func (s *Server) issueLinkCode(w http.ResponseWriter, r *http.Request) {
	connector := r.PathValue("connector")
	if s.Auth == nil {
		httpErr(w, http.StatusServiceUnavailable, "auth not configured")
		return
	}
	u, err := s.Auth.UserForToken(bearer(r))
	if err != nil {
		httpErr(w, http.StatusForbidden, "linking requires a user session")
		return
	}
	code, err := s.linkCodes.issue(connector, u.ID, time.Now())
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"code":         code,
		"expires_in":   int(linkCodeTTL.Seconds()),
		"instructions": "Envía este código al bot: /link " + code,
	})
}

// ---- POST /webhooks/telegram ------------------------------------------------

// tgUpdate is the slice of a Telegram Update we use.
type tgUpdate struct {
	Message *struct {
		Chat struct {
			ID int64 `json:"id"`
		} `json:"chat"`
		From struct {
			ID       int64  `json:"id"`
			Username string `json:"username"`
		} `json:"from"`
		Text string `json:"text"`
	} `json:"message"`
}

// telegramWebhook receives Telegram updates. It is a public route, authenticated by
// the X-Telegram-Bot-Api-Secret-Token header matching settings.mcp.telegram.
// webhook_secret (set when registering the webhook). It always answers 200 so
// Telegram does not retry; user-facing errors are sent as chat replies.
func (s *Server) telegramWebhook(w http.ResponseWriter, r *http.Request) {
	secret := s.Settings.MCPValue("telegram", "webhook_secret")
	if secret == "" || r.Header.Get("X-Telegram-Bot-Api-Secret-Token") != secret {
		httpErr(w, http.StatusForbidden, "invalid webhook secret")
		return
	}
	var up tgUpdate
	if err := json.NewDecoder(r.Body).Decode(&up); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true}) // ignore malformed
		return
	}
	if up.Message == nil || strings.TrimSpace(up.Message.Text) == "" {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	chatID := strconv.FormatInt(up.Message.Chat.ID, 10)
	fromID := strconv.FormatInt(up.Message.From.ID, 10)
	text := strings.TrimSpace(up.Message.Text)

	reply := s.handleTelegramText(r.Context(), fromID, chatID, text)
	if reply != "" {
		s.replyTelegram(chatID, reply)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleTelegramText routes a message to a reply string. /link binds the identity;
// any other message resolves the account, the chat's project and the caller's role
// (the inbound auth chain). Command execution (Brain) lands in the next step.
func (s *Server) handleTelegramText(ctx context.Context, fromID, chatID, text string) string {
	if strings.HasPrefix(text, "/link") {
		code := strings.TrimSpace(strings.TrimPrefix(text, "/link"))
		if code == "" {
			return "Uso: /link <código>. Genera el código en la consola (Equipo → vincular Telegram)."
		}
		userID, ok := s.linkCodes.consume("telegram", code, time.Now())
		if !ok {
			return "❌ Código inválido o expirado. Genera uno nuevo en la consola."
		}
		if err := s.Auth.BindChannelIdentity("telegram", fromID, userID); err != nil {
			return "❌ No se pudo vincular: " + err.Error()
		}
		return "✅ Vinculado. Ya puedes operar la fábrica desde este chat."
	}

	// Every non-/link message requires a linked identity + a project on this chat.
	userID, ok, err := s.Auth.UserForChannelIdentity("telegram", fromID)
	if err != nil {
		return "❌ Error interno."
	}
	if !ok {
		return "No estás vinculado. En la consola genera un código y envía aquí: /link <código>."
	}
	projectID, ok, err := s.Projects.ProjectForChannel("telegram", chatID)
	if err != nil {
		return "❌ Error interno."
	}
	if !ok {
		return "Este chat no está vinculado a un proyecto. Vincúlalo en la consola (Equipo → Canales)."
	}
	role, err := s.Projects.MemberRole(projectID, userID)
	if err != nil || role == "" {
		return "No tienes acceso a este proyecto."
	}

	// Approve / reject a Brain-proposed action from the chat (channel-based
	// approval). Editor+ only — a viewer may ask/read but not act.
	if id, approved, isResolve := parseResolveCmd(text); isResolve {
		if !projects.RoleAtLeast(role, projects.RoleEditor) {
			return "Resolver una acción requiere rol editor u owner."
		}
		if s.Brain == nil {
			return "El asistente no está disponible."
		}
		if err := s.Brain.Resolve(id, approved); err != nil {
			return "❌ No se pudo resolver: " + err.Error()
		}
		if approved {
			return "✅ Acción aprobada."
		}
		return "🚫 Acción rechazada."
	}

	// Free text → the Brain interprets it (its per-tool MinRole gating applies to
	// the caller's real role). The turn is async; we ack now and stream the result
	// (and any approval prompt) back to the chat as it completes.
	if s.Brain == nil {
		return "El asistente no está disponible (el control se inició sin ANTHROPIC_API_KEY)."
	}
	convID, err := s.Brain.Send(projectID, role, text)
	if err != nil {
		return "❌ " + err.Error()
	}
	go s.streamBrainToTelegram(convID, chatID)
	return "🤔 Procesando…"
}

// parseResolveCmd recognizes "/approve <id>" and "/reject <id>".
func parseResolveCmd(text string) (id string, approved bool, ok bool) {
	switch {
	case strings.HasPrefix(text, "/approve "):
		return strings.TrimSpace(text[len("/approve "):]), true, true
	case strings.HasPrefix(text, "/reject "):
		return strings.TrimSpace(text[len("/reject "):]), false, true
	}
	return "", false, false
}

// streamBrainToTelegram subscribes to a Brain conversation and relays it to the
// chat: it accumulates streamed text, posts an approval prompt when the Brain
// proposes a mutating action, and sends the final answer on done (or a timeout
// notice). Runs in its own goroutine so the webhook returns to Telegram at once.
func (s *Server) streamBrainToTelegram(convID, chatID string) {
	if s.Bus == nil {
		return
	}
	id, ch := s.Bus.Subscribe(convID)
	defer s.Bus.Unsubscribe(id)
	var sb strings.Builder
	timeout := time.After(90 * time.Second)
	for {
		select {
		case ev := <-ch:
			switch ev.Type {
			case brain.EvtToken:
				sb.WriteString(dataField(ev.Data, "text"))
			case brain.EvtAction:
				tool := dataField(ev.Data, "tool")
				aid := dataField(ev.Data, "action_id")
				s.replyTelegram(chatID, "🔔 Acción propuesta: "+tool+"\nResponde /approve "+aid+" o /reject "+aid)
			case brain.EvtDone:
				txt := strings.TrimSpace(sb.String())
				if e := dataField(ev.Data, "error"); e != "" {
					txt = "❌ " + e
				} else if txt == "" {
					txt = "✅ Listo."
				}
				s.replyTelegram(chatID, txt)
				return
			}
		case <-timeout:
			s.replyTelegram(chatID, "⏱ Tiempo agotado esperando al asistente.")
			return
		}
	}
}

// dataField extracts a string field from an event's JSON data payload.
func dataField(data, key string) string {
	if data == "" {
		return ""
	}
	var m map[string]any
	if json.Unmarshal([]byte(data), &m) != nil {
		return ""
	}
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// replyTelegram sends text back to a chat via the telegram connector, bounded by a
// short timeout so a hung send can't leak a goroutine.
func (s *Server) replyTelegram(chatID, text string) {
	if s.Channels == nil {
		return
	}
	conn := s.Channels.Get("telegram")
	if conn == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = conn.Notify(ctx, chatID, channels.Event{Title: text})
}
