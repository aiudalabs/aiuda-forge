# VibeForge v2 — Build Log

One line per wave. Gate per wave: `go build ./... && go vet ./... && go test ./...`.
Real `claude -p` cost is tracked in the Cost ledger at the bottom.

- **Wave 0 — Scaffold**: Go repo (`vibeforge-kernel`), CLAUDE.md constitution, Makefile,
  `.vibeforge-gate`, CI mirror, `docs/ARCHITECTURE.md`, design docs 13/14 copied, cmd stubs. Gate green.
- **Wave 1 — Kernel queue**: `internal/store` = sqlite (modernc, pure-Go) schema (runs/tasks/events) +
  state machine (legal-transition table, single emit point) + atomic claim (BEGIN IMMEDIATE = SKIP-LOCKED
  on sqlite, Postgres hook noted) + fencing token + heartbeat + requeue_stale + dependency-gated claim.
  Tests (`-race -count=3` green): concurrent claim (16 workers / 50 tasks, 0 double-claim), illegal
  transition rejected, stale-fence rejected after reap, heartbeat keeps alive, deps gate claim, events
  emitted on every transition. NOTE: deps pulled module floor to go 1.25 (kernel code is 1.23-clean); CI on 1.25.

## Cost ledger (real claude -p calls)

| Wave | What | Est. cost (USD) | Cumulative |
|---|---|---|---|
| — | (no real calls yet) | $0.00 | $0.00 |

Cap: ~$40 USD total. Stop real runs if approaching.
