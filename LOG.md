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

- **Wave 2 — Workflow executor**: `internal/workflow` = YAML manifest parser (steps: id/type/agent/
  model/inputs/on_fail{goto,max,feedback}) + `$ref` resolver ($trigger.x / $step.output) + GENERIC
  `Engine` (StartRun → enqueue first step; on completion resolve next step's inputs and enqueue, or
  apply on_fail goto; zero `if step.id==`). Step types `echo` (deterministic stub) + `gate` (runs
  command, exit code = pass/fail). `registry/workflows/demo.yaml` runs E2E. Tests: demo from YAML →
  DONE, adding a step in data changes flow (no code), on_fail loop recovers (gate fails once→passes),
  on_fail cap exhausted → FAILED with max+1 attempts, feedback injected into goto target. Gate green.

- **Wave 3 — Agent adapter**: `internal/agent` = generic `Backend` interface + `claude.go` (REAL
  engine: `claude -p --output-format stream-json --verbose --permission-mode acceptEdits
  --allowedTools --append-system-prompt`, NDJSON parse of assistant/tool_use/result, hard timeout,
  process-group SIGKILL on cancel, 3 auth modes subscription/api_key/oauth_token) + `FakeBackend`
  (deterministic, free) + agent manifest loader (yaml+persona.md, tool allowlist→claude names) +
  `agent` step runner (per-step model override = cross-model). Tests (fake, no LLM): agent step E2E
  through generic engine, cross-model override, manifest fallback, failure propagates, NDJSON parse,
  auth env per mode, registry dev/reviewer manifests load (reviewer model != dev). Gate green.

- **Wave 4 — Sandbox + gate anti-tamper**: `internal/sandbox` = per-task isolation — `FilterEnv`
  (allowlist; daemon secrets GH_TOKEN/ANTHROPIC_API_KEY/DB_* never cross), `DockerSandbox` (--network
  none egress-deny, env allowlist, --runtime runsc for gVisor) + `LocalSandbox` fallback (scrubbed env,
  flagged not-a-boundary), `CopyTreeNoGit` (working tree without .git → agent can't commit/push).
  `internal/gate` = seal gate-file hash + test-marker count BEFORE agent (daemon-side, outside tree) →
  `HardenedRunner` runs gate in sandbox + re-checks: ErrTampered (gate edited) / ErrSuiteShrank (tests
  deleted). Tests: no-secret-crosses, default allowlist secret-free, local scrub, exit-code, no-.git
  copy, anti-tamper detects gate edit, suite-integrity detects deleted tests, clean gate passes. Gate green.

- **Wave 5 — API + event bus + factory**: `internal/api` HTTP server fulfilling doc-14 contract
  (§A ops POST/GET/DELETE /runs, cancel/retry, control/pause|resume, steps/approve|merge, artifacts,
  metrics/analytics, healthz/readyz, registry CRUD; §B `/runs/{id}/events?after=` replay + `/ws`
  WebSocket live push tailing the events table; §C `/runs/claim`, `/steps/{id}/report|heartbeat|usage`).
  Event `Bus` tails the single-source-of-truth events table → fans to WS. `internal/pr` local PR step
  (branch+commit, push if remote, synthetic when no repo). `internal/app` assembles store+engine+runners
  (echo/gate/agent/pr)+backend(echo|claude)+bus+server. `cmd/control` (API + in-proc worker + reaper),
  `cmd/worker` (standalone, shared store). `registry/workflows/factory.yaml` (implement→gate→review
  cross-model→pr). Contract tests (-race green): PRESENCE (every §A op, no 404/405), EMISSION (factory
  run emits run.created/step.status_changed/run.status_changed/run.done), NO-PRIVILEGED-PATH (cancel/
  retry/delete via HTTP only), E2E stub (trigger→implement→gate→review→pr→DONE), ADD-STEP-NO-RECOMPILE
  (a `simplify` step added via registry PUT runs with zero Go change). Gate green.

- **Wave 6 — verify loop + human_gate**: generalized `on_fail{goto,max}` already covers gate-fix AND
  qa→dev (Wave 2); added `agentic_verify` step (`internal/agent/verify.go`: fresh verifier, cross-model,
  parses `VERDICT: works|broken` + evidence; broken→on_fail goto implement; emits `step.verify`) and
  `human_gate` step (parks task in new `AWAITING` state — excluded from the stale reaper — emits
  `run.awaiting_approval`; resumes only via `/runs/{id}/steps/{step}/approve`). StepResult gained
  `Park` + `Events` (engine forwards runner events verbatim, stays methodology-free); gate emits
  `step.gate`. Added `verifier` agent + `registry/workflows/factory-plus.yaml` (implement→gate→review→
  verify→human_gate→pr, all data). Tests (-race green): verify broken→fix→works recovers, verify cap
  honored→FAILED, human_gate parks (AWAITING + awaiting_approval, run not terminal) then approve→DONE,
  step.verify events emitted. Gate green.

- **Wave 7 — LIVE validation (real `claude -p`)**: built a disposable target repo (bare remote +
  seed with a REAL gate `python3 -m unittest discover` + baseline green test); ran the FULL `factory`
  workflow with `VIBEFORGE_ENGINE=claude`, `PR_MODE=local`, sandbox=local, via `cmd/live`. Ticket:
  "implement add(a,b) in calc.py with unittest tests". **Result: DONE end-to-end.**
  - `implement` (real Claude, opus): wrote `calc.py` (`def add(a,b): return a+b`) + `test_calc.py`
    (4 unittest cases). DONE.
  - `gate` (real `python3 -m unittest`, in sandbox): **Ran 5 tests, OK**. step.gate passed=true,
    anti-tamper seal intact. DONE.
  - `review` (real Claude, **sonnet — cross-model**, adversarial): produced a real review verdict
    ("DEFECT FOUND" on overflow/type edge cases). DONE.
  - `pr` (local): created branch `vibeforge/<runid>`, committed, **pushed=true** to the bare remote.
    Verified: cloning the pushed branch and running its tests → `Ran 5 tests OK`.
  - First attempt exposed a real bug (pr runner double-prefixed `git git …`); fixed + added pr unit
    tests (regression-covered), re-ran green. The kernel itself was correct throughout — the only
    defect was in the pr step's command construction, now tested.
  - Note: gate ran in the LOCAL sandbox (host `python3`, scrubbed env); Docker/gVisor isolation is
    available (`VIBEFORGE_SANDBOX=docker`, `VIBEFORGE_SANDBOX_RUNTIME=runsc`) and unit-tested, but the
    live smoke used local for determinism. Agent ran in-process (cwd=workdir), not docker-wrapped.

- **Hardening (post-MVP)** — docker sandbox bind-mount fix: run workdirs are now ALWAYS absolute
  (`NewEngine` resolves `filepath.Abs`), and `DockerSandbox` resolves the `-v` source to an absolute
  path via a pure, testable `dockerArgs`. Docker rejects relative `-v` sources (treats them as invalid
  named volumes) — a live `VIBEFORGE_SANDBOX=docker` run surfaced this. Also wired
  `VIBEFORGE_SANDBOX_IMAGE` (default alpine; set e.g. `python:3.12-slim` for a real gate). Tests:
  `TestEngineWorkdirAbsolute`, `TestDockerArgsUseAbsoluteMount`. Verified empirically: demo under
  Docker now reaches DONE with `step.gate passed=true`. Full suite `-race` green.

## Cost ledger (real claude -p calls)

| Run | What | Cost (USD) | Cumulative |
|---|---|---|---|
| Wave 7 run #1 | factory: implement+gate+review (pr bug → FAILED) | $0.6771 | $0.6771 |
| Wave 7 run #2 | factory full: implement→gate→review→pr → **DONE** | $0.6333 | **$1.3104** |

Total real spend: **~$1.31 USD** (cap ~$40 — used ~3.3%). No further real runs needed.
