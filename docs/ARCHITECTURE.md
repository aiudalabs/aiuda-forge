# aiuda-forge — Architecture

> The map of what is **actually built**, not a blueprint. Last reviewed: 2026-06-24.

## What it is

An **autonomous software factory**: it turns an idea into a spec, a spec into a backlog,
and a backlog into pull requests — every transition governed by data and (where it matters)
human approval.

Two planes, one kernel:

- **Design plane (Studio)** — idea → discovery → PRD → architecture → UI → backlog.
- **Execution plane (Factory)** — story → implement → gate → review → PR.

Both planes are **just workflows** run by the same kernel. There is no special "Studio engine"
and no special "factory engine" — only generic steps over data.

## The one idea: everything is data

Workflows, agent **personas**, and **skills** (methodology) live in `engine/registry/` as
YAML/markdown and are editable from the console — **no code change** to add or alter a flow.

```
engine/registry/
  workflows/   design.yaml · factory.yaml · gated.yaml · demo.yaml      (the flows)
  agents/      analyst · pm · architect · ux · scrum-master · dev · reviewer · verifier
               (each = <id>.yaml manifest + <id>.md persona)
  skills/      prd-template · architecture-template · story-template · sharding-method · …
```

The kernel knows nothing about "PRD" or "Stripe". It executes whatever the YAML says.

## Repo layout

```
aiuda-forge/
├── engine/      Go — kernel + control-plane + orchestrator + ticket store + Studio steps
└── console/     Next.js/TS — the dashboard (Board, Tickets, Studio, Registry, Spend, Settings)
```

Go module: `forge`. The console talks to the control-plane over HTTP and falls back to mock
data when it is unreachable.

---

## The kernel (control-plane) — `engine/internal/{store,workflow,api,app}`

A generic step executor. A **run** of a workflow becomes a sequence of **tasks** (steps) on a
queue; **workers** claim and execute them.

- **Step types** (registered by name in `app.Build`, the only wiring point):
  `echo` · `gate` · `agent` · `agentic_verify` · `human_gate` · `pr` · `design` · `ticket_publish`.
  Adding a step type = registering a runner. Flows pick types; the kernel has no `if type == …`.
- **Queue + atomic claim** (`store/claim.go`): `BEGIN IMMEDIATE` takes the write lock before any
  read — SKIP-LOCKED semantics on sqlite. Two workers can never claim the same task (tested under
  `-race` with 16 goroutines, zero double-claims).
- **Fencing + heartbeat + reaper**: a claimed task carries a fence; stale (heartbeat-expired) tasks
  are requeued with a bumped fence, so a zombie worker's late report is rejected.
- **Single emit point**: every run/task state transition emits exactly one event from the store,
  inside the same DB transaction that writes the change. The event bus (`api/bus`) fans out to the
  WebSocket and the `/runs/{id}/events` poll. UI, orchestrator and CLI are equal clients — no
  privileged path.
- **Worker pool**: `VIBEFORGE_WORKERS` (default 4) in-process workers drain the queue concurrently
  → **parallel execution across independent runs/tickets**. Steps within one run stay serial (step
  N+1 is enqueued only after N completes). Measured: 4 echo-gate runs, 12.6 s with 1 worker vs 4.4 s
  with 4.
- **Loops as data**: `on_fail: { goto, max, feedback }` on any step covers gate-fix retries AND the
  human-reject-then-revise loop — one primitive, in the YAML, not in code.

## Flows as data — the registry

`engine/registry/` is served by the control-plane (`GET/PUT/DELETE /registry/{kind}/{id}`). A PUT
is validated by the **same parser the kernel runs**, so "saved" means "runnable". Path-traversal
is rejected at the boundary. The console renders YAML as highlighted code and personas/skills as
markdown, and edits them in place.

## Native ticket store — `engine/internal/tickets`

The **source of truth** for the backlog (SCRUM model), in its own sqlite store:

```
Epic   { id, title, description }
Sprint { id, name, goal }
Story  { id, epic_id, sprint_id, title, body, acceptance, owner, deps[], status, run_id }
status: backlog → ready → running → in_review → done | failed
```

`ready` is **derived** from the dependency graph (a backlog story whose deps are all `done`), not
stored. "Wave"/parallelism falls out of the graph — there is no separate wave concept. SCRUM
(epic/story/sprint) is the planning vocabulary; the kernel only sees runs/tasks.

## Orchestrator (native scheduler) — `engine/internal/orchestrator`

A separate loop that drives runs from the ticket store (`-source native`; a `github` mode also
exists). Each cycle:

1. **Reconcile**: for each running story, check its run's status. `DONE` → mark the story `done`
   (which unblocks dependents the same cycle); terminal-failed (`FAILED`/`CANCELLED`) → mark `failed`
   (so it is not stuck forever and dependents stay blocked).
2. **Fire**: for each `ready` story, **claim-then-fire** — `ClaimStory` atomically flips
   `backlog → running` (`UPDATE … WHERE status='backlog'`); only the winner fires a run via
   `POST /runs` and records the `run_id`. This makes firing idempotent under concurrent schedulers
   (no double-fire) without a state file.

The control-plane exposes `GET /tickets` (derived status) which the console reads — the UI is
self-contained against the native store.

## Studio (design plane) — `registry/workflows/design.yaml` + personas/skills

Studio is **the `design` workflow**, not a service. Each phase = a `design` step (a non-sandboxed
agent turn producing a document) followed by a `human_gate`:

```
discovery → discovery_gate → prd → prd_gate → architecture → arch_gate
  → ui → ui_gate → backlog → backlog_gate → handoff
```

- Phases are **BMAD-derived personas** (analyst, pm, architect, ux, scrum-master) with **skills**
  (templates + checklists + the sharding method). All editable as registry data.
- A reject loops the phase back with feedback via the gate's `on_fail` — the same primitive the
  factory uses.
- A `design` step writes its document to the run workdir (`docs/PRD.md`, …). If the agent wrote the
  file itself, that file is kept as the artifact (not the agent's summary).
- **`handoff`** (`ticket_publish` step) reads `docs/backlog.yaml` (epic + stories with
  `acceptance`, `owner`, `deps`) and writes it into the **native ticket store** — closing the loop
  to the factory. The story's `owner` says which dev-agent implements it; its `body` carries the
  context, so the executing agent doesn't guess (the BMAD "story carries context" idea).

The console's Studio page is a **view of a `design` run**: the phase pipeline, the rendered
document per phase (markdown + GFM tables), and approve/reject — over the same run/event/gate
machinery as the Board.

## Factory (execution plane) — `registry/workflows/factory.yaml`

```
implement (agent) → gate (real tests, anti-tamper) → review (agent, cross-model) → pr
factory-plus adds: verify (fresh cross-model verifier) → human_gate before pr
```

The `agent` step runs an AI coding agent **inside a sandbox** (docker + egress allowlist) over a
clone of the target repo; `gate` runs the real tests and detects tampering; `pr` opens a PR.

## Provider-agnostic

- **Tickets**: the native store is the source of truth. A `TicketProvider` interface allows
  GitHub/JIRA as optional **sync** adapters (GitHub issue/`blockedBy` reading exists; full sync is
  future). The console reads tickets from the native store, not GitHub.
- **Docs** (Studio artifacts): currently files in the run workdir. A `DocProvider` (Confluence/Wiki/
  native) is the planned seam — not built yet.

## Engine-agnostic

The agent `Backend` interface (`Run(ctx, prompt, opts, onEvent) → Result`) is the seam. `claude -p`
is the implemented backend (3 auth modes: subscription / api_key / oauth_token). Adding
opencode/codex/cursor/local LLMs = a new `Backend` + selector; the coupling to Claude is
concentrated in `internal/agent/claude.go` (argv, stream parsing, tool-name map, auth env).

## Security

- **API auth**: opt-in bearer token (`VIBEFORGE_API_TOKEN`). Off by default (local dev); when set,
  all endpoints except `/healthz` require `Authorization: Bearer …` — covers the internal
  worker endpoints too.
- **Path traversal** rejected in registry + manifest loaders.
- **`TARGET_REMOTE`** validated (no `ext::`/`file://`) before clone.
- **Sandbox**: agent code-execution runs in docker with a default-deny egress allowlist; the gate is
  anti-tamper (the agent cannot fake passing tests).
- **Known gaps** (tracked): §C worker endpoints share the public port; OAuth token passed via
  docker `-e`; non-sandboxed `design` agents run on the host with a write tool.

## The console — `console/`

Next.js App Router + TanStack Query. Sections: **Board** (live runs, drawer with steps/events/diff,
approve/reject/retry/pause), **Tickets** (table · Kanban · DAG graph via React Flow · create story),
**Studio** (the design pipeline), **Registry** (edit agents/skills/workflows), **Spend** (metrics),
**Settings** (MCP/auth/sandbox/merge). Mock fallback when the control-plane is down.

## End-to-end

```
IDEA ─Studio(design)→ docs + backlog.yaml ─handoff→ native stories (epic/deps/owner)
     ─orchestrator(ready→claim→fire)→ Factory(implement→gate→review→pr) ─→ PR
```

Everything is data (workflows/personas/skills), self-contained (GitHub/JIRA optional sync), parallel
(worker pool), and secure-by-capability (auth opt-in, sandbox).

## Running it

```bash
cd engine && go run ./cmd/control                       # control-plane :8080 (echo, free)
#   real LLM:  VIBEFORGE_ENGINE=claude CLAUDE_CODE_OAUTH_TOKEN=… go run ./cmd/control
cd engine && go run ./cmd/orchestrator -source native -workflow demo   # scheduler
cd console && npm install && npm run dev                # UI :3000
```

## Status (built vs not)

Built & validated: kernel + worker pool, native ticket store, native orchestrator (with
failed-handling + claim-then-fire), Studio as the `design` workflow + handoff, the console
(Board/Tickets/Kanban/DAG/Studio/Registry/Settings/Spend), opt-in auth + traversal fixes, real-LLM
design run producing real documents.

Not yet: GitHub/JIRA sync adapters, `DocProvider`, multi-project (`project_id` + sqlite migrations),
retiring the legacy standalone studio service (`internal/studio` + `cmd/studio`, now redundant),
intra-run fan-out, and the remaining audit follow-ups (a11y, §C port split, claim rollback on
fire error).
