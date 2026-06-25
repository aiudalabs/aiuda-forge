# Running VibeForge v2 end-to-end

Two ways to run the kernel: the **stub E2E** (fast, free, deterministic — the default
test path) and the **live E2E** (real `claude -p`, the Wave-7 validation).

## 0. The gate (must be green before anything)

```bash
./.vibeforge-gate          # go build ./... && go vet ./... && go test ./...
# or
make gate
```

## 1. Stub E2E (no LLM, no Docker, no cost)

The whole factory flow with the deterministic echo backend runs in the contract tests:

```bash
go test ./internal/api/ -run TestE2EFactoryStub -v
```

This proves: `POST /runs` → claim → implement → gate → review → pr-local → **DONE**, entirely
over HTTP. The other contract tests (`TestContractPresence`, `TestContractEmission`,
`TestNoPrivilegedPath`, `TestAddStepNoRecompile`) pin the doc-14 guarantees.

## 2. Run the control plane locally

```bash
# echo engine (stub) — safe to poke at the API
VIBEFORGE_ENGINE=echo go run ./cmd/control      # listens on :8080

# in another shell:
curl -s localhost:8080/healthz
curl -s -XPOST localhost:8080/runs -d '{"workflow":"demo","payload":{"name":"hi"}}'
curl -s localhost:8080/runs
curl -s "localhost:8080/runs/<id>/events"
# live event stream:  websocat ws://localhost:8080/ws
```

Add a step to `registry/workflows/factory.yaml` (or PUT `/registry/workflows/{id}`) and the
flow changes with **no recompile** — that is the v2 thesis.

## 3. Live E2E (real `claude -p`) — Wave 7

Prerequisites: `claude` in PATH and logged in (subscription) or a token; `git`; `python3`
(for the target's unittest gate). Docker optional (gate falls back to a scrubbed-env local
sandbox).

```bash
# a) build a disposable target repo (bare remote + seed with a REAL gate)
LIVE=$(mktemp -d)
git init --quiet --bare "$LIVE/bare.git"
git --git-dir="$LIVE/bare.git" symbolic-ref HEAD refs/heads/main
git clone --quiet "$LIVE/bare.git" "$LIVE/seed" 2>/dev/null; cd "$LIVE/seed"
git config user.email kernel@vibeforge && git config user.name vibeforge
printf '%s\n' 'python3 -m unittest discover -q -s . -p "test_*.py"' > .vibeforge-gate
printf 'import unittest\nclass T(unittest.TestCase):\n    def test_truth(self): self.assertTrue(True)\n' > test_baseline.py
git add -A && git commit --quiet -m seed && git branch -M main && git push --quiet origin main
cd -

# b) run the factory for real (PR_MODE=local, no GitHub)
TARGET_REMOTE="$LIVE/bare.git" \
WORKFLOW=factory \
VIBEFORGE_REGISTRY=registry \
VIBEFORGE_DB="$LIVE/live.db" \
VIBEFORGE_WORKDIR="$LIVE/runs" \
VIBEFORGE_SANDBOX=local \
go run ./cmd/live
```

`cmd/live` clones the target into each run's workdir, seals the gate (anti-tamper), runs
`implement` (real Claude) → `gate` (real `python -m unittest`) → `review` (cross-model) →
`pr` (local branch+commit+push to the bare remote), streams every event, and prints the
final status + total cost. Exit 0 == the run reached DONE.

Env knobs: `VIBEFORGE_ENGINE` (echo|claude), `VIBEFORGE_SANDBOX` (local|docker),
`VIBEFORGE_SANDBOX_RUNTIME=runsc` (gVisor), `PR_MODE` (local), `WORKFLOW` (factory|factory-plus|demo).
