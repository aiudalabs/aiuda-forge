package channels

import (
	"context"
	"time"
)

// Target is a resolved (connector, target) pair a notable event fans out to.
type Target struct {
	Connector string
	Target    string
}

// Delivery fans notable factory events to a project's subscribed channels. It is
// decoupled from the project store via Lookup (the app wires it to
// projects.ChannelsForEvent). Sends are async + best-effort: one slow/failed
// channel never blocks the bus or the others.
type Delivery struct {
	Registry Registry
	Lookup   func(projectID, eventType string) ([]Target, error)
	Logf     func(format string, args ...any)
}

// notifyTimeout bounds a single channel send so a hung external API can't leak
// goroutines forever.
const notifyTimeout = 10 * time.Second

// Deliver maps a raw event and dispatches it to every subscribed channel. Safe to
// call from the bus hook: it returns immediately and sends in goroutines.
func (d *Delivery) Deliver(eventType, projectID, runID string, data []byte) {
	if d == nil || d.Registry == nil || d.Lookup == nil {
		return
	}
	ev, ok := Format(eventType, projectID, runID, data)
	if !ok {
		return // not a notable event
	}
	targets, err := d.Lookup(projectID, eventType)
	if err != nil {
		d.logf("channel lookup (%s/%s): %v", projectID, eventType, err)
		return
	}
	for _, t := range targets {
		conn := d.Registry.Get(t.Connector)
		if conn == nil {
			continue
		}
		go func(conn Connector, target string) {
			ctx, cancel := context.WithTimeout(context.Background(), notifyTimeout)
			defer cancel()
			if err := conn.Notify(ctx, target, ev); err != nil {
				d.logf("notify %s/%s: %v", conn.Name(), target, err)
			}
		}(conn, t.Target)
	}
}

func (d *Delivery) logf(format string, args ...any) {
	if d.Logf != nil {
		d.Logf(format, args...)
	}
}
