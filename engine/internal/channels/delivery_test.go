package channels

import (
	"context"
	"sync"
	"testing"
	"time"
)

type fakeConn struct {
	mu      sync.Mutex
	calls   []Event
	targets []string
}

func (f *fakeConn) Name() string { return "telegram" }
func (f *fakeConn) Notify(_ context.Context, target string, ev Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, ev)
	f.targets = append(f.targets, target)
	return nil
}

func (f *fakeConn) waitFor(n int) bool {
	for i := 0; i < 100; i++ {
		f.mu.Lock()
		got := len(f.calls)
		f.mu.Unlock()
		if got >= n {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

func newDelivery(fc *fakeConn, targets []Target) *Delivery {
	return &Delivery{
		Registry: Registry{"telegram": fc},
		Lookup:   func(_, _ string) ([]Target, error) { return targets, nil },
	}
}

func TestDeliverNotableFansToTargets(t *testing.T) {
	fc := &fakeConn{}
	d := newDelivery(fc, []Target{{Connector: "telegram", Target: "chat-1"}})

	d.Deliver(EvtRunFailed, "proj1", "run-7", []byte(`{"error":"boom"}`))

	if !fc.waitFor(1) {
		t.Fatal("connector was not notified for a notable event")
	}
	fc.mu.Lock()
	defer fc.mu.Unlock()
	if fc.targets[0] != "chat-1" {
		t.Fatalf("target = %q, want chat-1", fc.targets[0])
	}
	if fc.calls[0].Type != EvtRunFailed {
		t.Fatalf("delivered type = %q, want %q", fc.calls[0].Type, EvtRunFailed)
	}
}

func TestDeliverIgnoresNonNotable(t *testing.T) {
	fc := &fakeConn{}
	d := newDelivery(fc, []Target{{Connector: "telegram", Target: "chat-1"}})

	// step.event is the high-volume stream — never delivered to channels.
	d.Deliver("step.event", "proj1", "run-7", []byte(`{"kind":"text"}`))

	// Give any erroneous goroutine a chance to fire.
	time.Sleep(30 * time.Millisecond)
	fc.mu.Lock()
	defer fc.mu.Unlock()
	if len(fc.calls) != 0 {
		t.Fatalf("non-notable event should not be delivered, got %d calls", len(fc.calls))
	}
}
