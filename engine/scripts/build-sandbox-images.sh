#!/usr/bin/env bash
# Build the local sandbox images the control plane runs untrusted repo code in via
# Docker-outside-of-Docker (`docker run` against the host socket). These are NOT
# compose services — they are base images the control spawns at runtime, so they must
# exist in the HOST docker daemon before the first run that needs them.
#
#   vibeforge-agent:local  → the `release` step build (npm/firebase) + legacy factory agent
#   forge-gate:local       → the legacy `gate` step (offline test runner; node + python)
#
# Run this once before `docker compose up`, and again after an upgrade if either
# Dockerfile changed. The layer cache makes re-runs fast when nothing changed.
#
#   ./engine/scripts/build-sandbox-images.sh
#
# Override tags via env (must match VIBEFORGE_AGENT_IMAGE / VIBEFORGE_SANDBOX_IMAGE):
#   AGENT_IMAGE=vibeforge-agent:local GATE_IMAGE=forge-gate:local ./engine/scripts/build-sandbox-images.sh
set -euo pipefail
HERE="$(cd "$(dirname "$0")/.." && pwd)"   # engine/
AGENT_IMAGE="${AGENT_IMAGE:-vibeforge-agent:local}"
GATE_IMAGE="${GATE_IMAGE:-forge-gate:local}"

echo "building $AGENT_IMAGE (release build: node + npm + git + python3 + firebase-tools) ..."
docker build -t "$AGENT_IMAGE" "$HERE/deploy/agent"

echo "building $GATE_IMAGE (legacy offline gate: node + python) ..."
docker build -t "$GATE_IMAGE" "$HERE/deploy/gate"

echo "done:"
docker image ls --format '  {{.Repository}}:{{.Tag}}  {{.Size}}' "$AGENT_IMAGE" "$GATE_IMAGE"
