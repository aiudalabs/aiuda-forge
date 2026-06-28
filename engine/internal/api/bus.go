package api

import (
	"context"
	"sync"
	"time"

	"forge/internal/store"
)

// Bus fans out persisted events to live subscribers (WebSocket). It is a TAIL of
// the events table — the single source of truth — so what a WS client sees is
// exactly what was persisted and what /runs/{id}/events replays. The store's
// state machine is the sole emitter; the bus never invents events.
type Bus struct {
	st       *store.Store
	interval time.Duration

	mu      sync.Mutex
	subs    map[int]*subscriber
	nextID  int
	lastSeq int64

	// onEvent, if set, fires for each newly-drained event (v1.3 channel delivery).
	// Called outside the bus lock; the handler must not block (it dispatches sends
	// asynchronously). nil disables outbound delivery.
	onEvent func(store.Event)
}

// SetOnEvent wires an outbound-delivery hook fired per new event. Set once at wiring
// time before Run; not safe to change concurrently with Run.
func (b *Bus) SetOnEvent(fn func(store.Event)) { b.onEvent = fn }

type subscriber struct {
	runID string // "" = all runs
	ch    chan store.Event
}

// NewBus builds a bus tailing st. Call Run(ctx) to start the poll loop.
func NewBus(st *store.Store) *Bus {
	return &Bus{st: st, interval: 25 * time.Millisecond, subs: map[int]*subscriber{}}
}

// Run polls the events table and fans new events out to subscribers until ctx is
// cancelled.
func (b *Bus) Run(ctx context.Context) {
	// Start from the current tip so we only stream NEW events live (replay is via
	// the HTTP events endpoint).
	if seq, err := b.st.MaxSeq(); err == nil {
		b.lastSeq = seq
	}
	t := time.NewTicker(b.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			b.drain()
		}
	}
}

func (b *Bus) drain() {
	b.mu.Lock()
	from := b.lastSeq
	b.mu.Unlock()

	events, err := b.allEventsAfter(from)
	if err != nil || len(events) == 0 {
		return
	}

	b.mu.Lock()
	for _, ev := range events {
		if ev.Seq > b.lastSeq {
			b.lastSeq = ev.Seq
		}
		for _, sub := range b.subs {
			if sub.runID != "" && sub.runID != ev.RunID {
				continue
			}
			select {
			case sub.ch <- *ev:
			default: // slow consumer: drop rather than block the bus
			}
		}
	}
	b.mu.Unlock()

	// Outbound channel delivery (v1.3): fire the hook OUTSIDE the lock so a slow
	// handler never stalls the bus. The handler dispatches the actual sends async.
	if b.onEvent != nil {
		for _, ev := range events {
			b.onEvent(*ev)
		}
	}
}

// allEventsAfter reads every event with seq > after across all runs.
func (b *Bus) allEventsAfter(after int64) ([]*store.Event, error) {
	return b.st.AllEventsAfter(after)
}

// Subscribe registers a subscriber; runID "" means all runs. Returns an id and a
// buffered channel. Always Unsubscribe when done.
func (b *Bus) Subscribe(runID string) (int, chan store.Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	id := b.nextID
	b.nextID++
	ch := make(chan store.Event, 256)
	b.subs[id] = &subscriber{runID: runID, ch: ch}
	return id, ch
}

// Unsubscribe removes a subscriber and closes its channel.
func (b *Bus) Unsubscribe(id int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if sub, ok := b.subs[id]; ok {
		close(sub.ch)
		delete(b.subs, id)
	}
}
