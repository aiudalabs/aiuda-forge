# Sandbox build image (tag: vibeforge-agent:local, set via VIBEFORGE_AGENT_IMAGE).
#
# WHERE IT IS USED: the `release` step (PR #23) runs a project's build INSIDE this
# image on the egress-allowlisted network — never on the host (audit C2). Concretely:
#   - static target : `npm ci && npm run build`  (output dist/ or build/)
#   - firebase target: `firebase hosting:channel:deploy … --json --non-interactive`
# The legacy factory agent runner also uses this image when re-enabled (profile
# `legacy`); the current GitHub-native product only exercises it via `release`.
#
# It therefore needs: node LTS + npm (the build), git + ca-certificates (clone/https),
# firebase-tools (the firebase target), and python3 (parity with forge-gate:local and
# any python project build). Egress is default-deny: npm needs registry.npmjs.org and
# firebase needs *.googleapis.com — both are in engine/deploy/egress-proxy/filter.
#
# Build it with:  ./engine/scripts/build-sandbox-images.sh   (before `docker compose up`)
FROM node:20-bookworm-slim

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
      git ca-certificates python3 python3-venv python3-pip && \
    # firebase-tools for the release(firebase) target. Pinned major to keep the
    # image reproducible; bump deliberately.
    npm install -g firebase-tools@13 && \
    npm cache clean --force && \
    rm -rf /var/lib/apt/lists/*

# The release step mounts the cloned repo as the workdir and sets it at runtime; no
# ENTRYPOINT/CMD is needed — the sandbox runner passes the exact command to run.
