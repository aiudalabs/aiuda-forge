# VibeForge v2 — Architecture (1 page)

VibeForge v2 = a **deterministic execution kernel** + everything else as **DATA** and **MARKDOWN**.

## The 4 layers

```
┌─────────────────────────────────────────────────────────────────────┐
│ 4 · CHAT + BRAIN (conductor)            ← deferred (post-MVP)         │
│    interprets workflow+state, dispatches steps, manages approvals     │
├─────────────────────────────────────────────────────────────────────┤
│ 3 · REGISTRY (DATA — configurable, zero code)                        │
│    agents (yaml + persona.md) · skills (md + lock) · workflows (DAG)  │
│    "Factory"/"Studio" are WORKFLOWS here; +/- an agent = edit graph   │
├─────────────────────────────────────────────────────────────────────┤
│ 2 · KERNEL (CODE — small, deterministic, HEAVILY tested) ← the moat   │
│    queue + atomic claim + state machine + fencing/heartbeat ·         │
│    sandbox (docker/egress) · multi-CLI adapter · isolated gate-runner ·│
│    event bus + API + WS · GENERIC step executor                       │
├─────────────────────────────────────────────────────────────────────┤
│ 1 · MEMORY (markdown + git) ← deferred (post-MVP)                     │
└─────────────────────────────────────────────────────────────────────┘
```

## The design rule: code vs markdown

- **Code (kernel):** the **deterministic & security/consistency-critical** — locking, state
  machine, sandbox, gate isolation, fencing. *Never* in markdown.
- **Markdown/manifests:** **judgment or methodology** — how to write a PRD, phase order, persona,
  skills, criteria. *Never* in code.
- **Forward-compatible:** the kernel exposes generic primitives so the boundary slides toward
  markdown over time without re-architecting.

## Step types (the generic executor)

The executor has NO `if task_type == ...`. It interprets a handful of step types declared in YAML:

- `echo` — deterministic stub (testing, no LLM).
- `agent` — runs an agent CLI (via the Backend adapter) with persona+skills+tools from manifest.
- `gate` — runs a declared command in isolation + anti-tamper.
- `agentic_verify` — spawns a fresh verifier (different model) that proves it works, with evidence.
- `pr` — push + open/update PR (idempotent by branch) + merge policy.
- `human_gate` — pauses and notifies (phase approval / business decision).

A workflow = a list of these steps + where each output goes + `on_fail{goto,max,feedback}` (the
generalized qa→dev / gate-fix loop). That is all. Flows are data; the executor is generic.

## API-first contract

Every operation passes through the HTTP API; every state transition emits an event from a single
place (the state machine). UI, Brain, and CLI are equal clients. See `docs/14_API_CONTRACT_v2.md`.
