// Package channels is the connector spine (v1.3): it delivers factory events out to
// external channels (Telegram first) and routes inbound commands back to the Brain.
// A Connector is one integration; the delivery layer (wired at the event bus) fans
// each subscribed event to the configured connectors. Connector config (which chat
// to notify) is per-project; the connector's credential (bot token) is per-instance
// (settings.MCP). This file defines the contract; concrete connectors live in
// subpackages (e.g. channels/telegram).
package channels

import "context"

// Event is the channel-facing view of a factory event — already formatted to a
// human one-liner so connectors don't parse raw store payloads. The delivery layer
// maps store.Event → Event (type, project/run ids, a title and optional detail).
type Event struct {
	Type      string // e.g. "run.failed", "run.awaiting_approval", "billing.spend_cap_tripped"
	ProjectID string
	RunID     string
	Title     string // human one-liner, e.g. "❌ Run failed: serviciospty"
	Detail    string // optional extra context (step, error, link)
}

// Connector is a single channel integration. Notify pushes an event to one target
// (e.g. a Telegram chat id). Inbound handling (webhook → Brain) is added per
// connector in v1.3 #60; this is the outbound contract.
type Connector interface {
	// Name is the connector key used in config (e.g. "telegram").
	Name() string
	// Notify delivers ev to target. It should be best-effort and self-contained:
	// the delivery layer logs and continues on error so one bad channel never
	// blocks the others.
	Notify(ctx context.Context, target string, ev Event) error
}

// Registry maps a connector name to its implementation.
type Registry map[string]Connector

// Get returns the connector for name, or nil if not registered.
func (r Registry) Get(name string) Connector { return r[name] }
