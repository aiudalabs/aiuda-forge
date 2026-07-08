#!/usr/bin/env bash
# scripts/setup-env.sh — Claude Code SessionStart bootstrap for aiuda-forge.
#
# Goals: idempotent, fault-tolerant, and NEVER abort the session. Every step is
# guarded; a failure prints a clear warning and continues. This script only
# prepares the environment (toolchains + deps) — it changes NO project behavior.
#
# Deliberately NOT using `set -e`: a failing sub-step must never kill the hook.
set -uo pipefail

# Resolve the repo root regardless of where the hook is invoked from.
ROOT="${CLAUDE_PROJECT_DIR:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
cd "$ROOT" 2>/dev/null || { echo "[setup-env] cannot cd to repo root; skipping"; exit 0; }

log()  { printf '[setup-env] %s\n' "$*"; }
warn() { printf '[setup-env][WARN] %s\n' "$*" >&2; }

# --- helpers ---------------------------------------------------------------

# Compare dotted versions: returns 0 if $1 >= $2.
ver_ge() {
  [ "$(printf '%s\n%s\n' "$2" "$1" | sort -V | head -n1)" = "$2" ]
}

# Minimum Go version required by engine/go.mod (fallback to 1.25 if unreadable).
required_go() {
  local v
  v="$(awk '/^go [0-9]/ {print $2; exit}' engine/go.mod 2>/dev/null)"
  # engine/go.mod may pin e.g. "1.25.0"; keep the full string for the toolchain.
  printf '%s' "${v:-1.25.0}"
}

# --- Go toolchain ----------------------------------------------------------

REQ_GO="$(required_go)"
# Reduce to major.minor for the >= comparison (1.25.0 -> 1.25).
REQ_GO_MM="$(printf '%s' "$REQ_GO" | awk -F. '{print $1"."$2}')"

GO_OK=0
GO_VER="(not found)"
if command -v go >/dev/null 2>&1; then
  GO_VER="$(go version 2>/dev/null | awk '{print $3}' | sed 's/^go//')"
  GO_VER_MM="$(printf '%s' "$GO_VER" | awk -F. '{print $1"."$2}')"
  if ver_ge "${GO_VER_MM:-0}" "$REQ_GO_MM"; then
    GO_OK=1
    log "Go $GO_VER satisfies required >= $REQ_GO_MM."
  else
    warn "Go $GO_VER is older than required $REQ_GO_MM."
    log "Attempting to let Go resolve the pinned toolchain (GOTOOLCHAIN=auto)..."
    export GOTOOLCHAIN=auto
    # `go mod download` below will trigger the toolchain download if network allows.
  fi
else
  warn "Go is not installed. Install Go >= $REQ_GO_MM (engine/go.mod pins $REQ_GO)."
  warn "If offline, engine builds will be skipped this session."
fi

# --- engine deps -----------------------------------------------------------

if command -v go >/dev/null 2>&1; then
  log "Downloading Go module dependencies (engine/)..."
  if ( cd engine && GOTOOLCHAIN="${GOTOOLCHAIN:-auto}" go mod download ) 2>/dev/null; then
    log "engine: go mod download OK."
    # Re-read the effective Go version (a toolchain may have just been fetched).
    GO_VER="$(cd engine && GOTOOLCHAIN="${GOTOOLCHAIN:-auto}" go version 2>/dev/null | awk '{print $3}' | sed 's/^go//')"
    GO_VER_MM="$(printf '%s' "$GO_VER" | awk -F. '{print $1"."$2}')"
    ver_ge "${GO_VER_MM:-0}" "$REQ_GO_MM" && GO_OK=1
  else
    warn "engine: go mod download failed (offline or toolchain unavailable). Continuing."
  fi
fi

# --- console deps ----------------------------------------------------------

NODE_VER="(not found)"
command -v node >/dev/null 2>&1 && NODE_VER="$(node --version 2>/dev/null)"

if command -v npm >/dev/null 2>&1; then
  if [ -f console/package-lock.json ]; then
    log "Installing console deps with npm ci (clean, reproducible)..."
    if ! ( cd console && npm ci --no-audit --no-fund ) 2>/dev/null; then
      warn "console: npm ci failed; falling back to npm install."
      ( cd console && npm install --no-audit --no-fund ) 2>/dev/null \
        || warn "console: npm install failed too (offline?). Continuing."
    else
      log "console: npm ci OK."
    fi
  else
    log "No lockfile; installing console deps with npm install..."
    ( cd console && npm install --no-audit --no-fund ) 2>/dev/null \
      || warn "console: npm install failed (offline?). Continuing."
  fi
else
  warn "npm not found; skipping console deps."
fi

# --- non-blocking health checks -------------------------------------------

GO_BUILD="skipped"
if [ "$GO_OK" = "1" ] && command -v go >/dev/null 2>&1; then
  log "Checking: go build ./... (engine, non-blocking)..."
  if ( cd engine && GOTOOLCHAIN="${GOTOOLCHAIN:-auto}" go build ./... ) 2>/dev/null; then
    GO_BUILD="PASS"
  else
    GO_BUILD="FAIL"
  fi
fi

TSC="skipped"
if command -v npx >/dev/null 2>&1 && [ -d console/node_modules ]; then
  log "Checking: npx tsc --noEmit (console, non-blocking)..."
  if ( cd console && npx --no-install tsc --noEmit ) 2>/dev/null; then
    TSC="PASS"
  else
    TSC="FAIL"
  fi
fi

# --- summary ---------------------------------------------------------------

echo
echo "======== aiuda-forge setup-env summary ========"
printf '  Go version   : %s (required >= %s)\n' "$GO_VER" "$REQ_GO_MM"
printf '  Node version : %s\n' "$NODE_VER"
printf '  go build ./... : %s\n' "$GO_BUILD"
printf '  npx tsc --noEmit: %s\n' "$TSC"
echo "==============================================="
echo

# Always succeed — the session must start even if checks failed.
exit 0
