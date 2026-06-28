// Package telegram implements the channels.Connector for Telegram Bot API (v1.3).
// Outbound: sendMessage to a chat id. The bot token is read per-send from a provider
// func (wired to settings) so a token change is picked up without a restart.
package telegram

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"forge/internal/channels"
)

// Connector sends Telegram messages. Implements channels.Connector.
type Connector struct {
	token func() string // reads the bot token (per-instance, from settings)
	doer  func(*http.Request) (*http.Response, error)
	base  string // API base; overridable in tests
}

// New builds a Telegram connector. token is a provider read at send time.
func New(token func() string) *Connector {
	return &Connector{
		token: token,
		doer:  http.DefaultClient.Do,
		base:  "https://api.telegram.org",
	}
}

// Name is the connector key used in channel config.
func (c *Connector) Name() string { return "telegram" }

// Notify sends ev's title (+detail) to the Telegram chat id `target`.
func (c *Connector) Notify(ctx context.Context, target string, ev channels.Event) error {
	tok := c.token()
	if tok == "" {
		return errors.New("telegram bot token not configured (settings.mcp.telegram.token)")
	}
	if target == "" {
		return errors.New("telegram target chat id is empty")
	}
	text := ev.Title
	if ev.Detail != "" {
		text += "\n" + ev.Detail
	}
	form := url.Values{"chat_id": {target}, "text": {text}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.base+"/bot"+tok+"/sendMessage", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.doer(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("telegram sendMessage: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return nil
}
