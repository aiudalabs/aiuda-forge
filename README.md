# aiuda-forge

Autonomous software factory by **[aiudalabs](https://aiudalabs.com)** — turns an idea
into a spec, a spec into a backlog, and a backlog into pull requests, governed by data.

Two pieces, one product:

| Dir | What | Stack |
|---|---|---|
| **`engine/`** | The kernel + control-plane: generic step executor (flows are YAML data), native ticket store, orchestrator, and Studio (the `design` workflow). | Go |
| **`console/`** | The dashboard: Board, Tickets (Kanban + DAG), Studio (guided design), Registry, Spend, Settings. | Next.js / TypeScript |

See **[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)** for the full map of what's built.

## How it fits together

```
IDEA
 └─ Studio (workflow `design`: analyst→pm→architect→ux→scrum-master, human gates)
     └─ handoff → native ticket store (epic / stories / deps / owner)
         └─ orchestrator → factory (workflow: implement→gate→review→pr) → PR
```

Everything is **data**: workflows, agent personas and skills are YAML/markdown in
`engine/registry/`, editable from the console — no code changes to add or change a flow.

## Run (local)

```bash
# engine (control-plane on :8080)
cd engine && go run ./cmd/control            # echo engine (no LLM, free)
# real LLM:  VIBEFORGE_ENGINE=claude CLAUDE_CODE_OAUTH_TOKEN=… go run ./cmd/control

# console (UI on :3000)
cd console && npm install && npm run dev
```

The console auto-detects the control-plane via `/healthz` and falls back to mock data.
