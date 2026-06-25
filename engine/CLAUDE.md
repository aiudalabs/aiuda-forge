# VibeForge v2 — Kernel Constitution

VibeForge v2 is a **deterministic execution kernel** for autonomous agents. It is small,
testable, and knows nothing about methodology. ALL methodology — agents, skills, workflows —
lives in **DATA (YAML)** and **MARKDOWN**, never in code. This is a parallel bet to v1 (still
alive); do NOT touch v1.

## Golden Rules (non-negotiable)

1. **Zero methodology in code.** Every flow is a YAML workflow. The executor is generic: there
   is NO `if step.id == "studio"` / `if task_type == ...`. Step *types* are data-driven.
2. **The kernel is the moat: deterministic and HEAVILY tested.** Tests come before
   implementation. NEVER weaken a test to make it pass.
3. **Code = only the deterministic/security-critical:** queue, atomic claim, state machine,
   fencing/heartbeat, sandbox, gate isolation. Judgment/methodology = markdown. Never put
   claim/sandbox/state logic in markdown; never put a PRD recipe or persona in code.
4. **API-first, no privileged path.** Every operation goes through the API; the UI/Brain/CLI are
   equal clients. See `docs/14_API_CONTRACT_v2.md`.
5. **Every state transition emits an event from ONE place** (the state machine). No ad-hoc events.
6. **Commits small, conventional, tests green.** The gate (`.vibeforge-gate`) is the truth.

## Forward-compatibility

The kernel exposes **generic primitives** so the code↔markdown boundary slides toward markdown
over time (better models → more prompt, less code) without re-architecting. A step can be code
today and an agent tomorrow; the kernel never notices.

## Repo tree

```
cmd/control/      API HTTP server (the control plane)
cmd/worker/       poll/claim worker that executes steps
internal/store/   sqlite-backed persistence (tasks, runs, events)
internal/queue/   queue + state machine + atomic claim + fencing/heartbeat
internal/workflow/ YAML workflow parser + GENERIC step executor
internal/agent/   Backend interface + claude.go (claude -p) + echo stub
internal/sandbox/ per-task docker sandbox (egress-deny, env allowlist)
internal/gate/    isolated gate-runner + anti-tamper + suite-integrity
internal/api/     HTTP handlers + event bus (WS + replay)
registry/         DATA: workflows/*.yaml, agents/*.{yaml,md} — methodology lives here
docs/             13_ARQUITECTURA_DE_CERO.md (design), 14_API_CONTRACT_v2.md (contract), ARCHITECTURE.md
```

## Where to look

| I want to… | Look at |
|---|---|
| Change a flow (add/remove an agent step) | `registry/workflows/*.yaml` — NO Go change |
| Add an agent | `registry/agents/<id>.yaml` + `<id>.md` persona |
| Understand state transitions | `internal/queue/` (single emit point) |
| Understand the API contract | `docs/14_API_CONTRACT_v2.md` + `internal/api/` |
| Run the gate | `.vibeforge-gate` (`go build && go vet && go test`) |
| Understand the 4 layers | `docs/ARCHITECTURE.md` |

## Stack

Go 1.25 (toolchain; module floor pulled up by deps; kernel code is 1.23-clean). Pure-Go sqlite (`modernc.org/sqlite`, no CGO). Static single binary deploy.
Tests use a deterministic echo Backend (no LLM); the real engine is `claude -p`.
