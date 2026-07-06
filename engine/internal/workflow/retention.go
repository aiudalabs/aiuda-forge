package workflow

import (
	"context"
	"log"
	"os"
	"time"
)

// RetentionLoop periodically frees disk (audit A3): prunes old events and removes the
// workdirs of DONE/CANCELLED runs older than `retention`. Disabled when retention <= 0.
//
// Workdirs of FAILED runs are kept (they can still be re-run). Because re-running one
// phase of a DONE run (RerunStep) also reuses its workdir, `retention` should be long
// enough (days) that such a late re-run is unlikely — the operator sets it via
// VIBEFORGE_RETENTION_DAYS. Best-effort: every error is logged, never fatal.
func (e *Engine) RetentionLoop(ctx context.Context, retention, interval time.Duration) {
	if retention <= 0 {
		return
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			e.pruneOnce(retention)
		}
	}
}

func (e *Engine) pruneOnce(retention time.Duration) {
	before := time.Now().Add(-retention).UnixMilli()
	if n, err := e.Store.PruneEvents(before); err != nil {
		log.Printf("retention: prune events: %v", err)
	} else if n > 0 {
		log.Printf("retention: pruned %d events older than %s", n, retention)
	}
	ids, err := e.Store.TerminalRunIDsBefore(before)
	if err != nil {
		log.Printf("retention: terminal runs: %v", err)
		return
	}
	for _, id := range ids {
		if err := os.RemoveAll(e.Workdir(id)); err != nil {
			log.Printf("retention: remove workdir %s: %v", id, err)
		}
	}
}
