#!/usr/bin/env bash
# check-compose-config.sh — guards the 2026-07-09 incident class at the compose layer:
# "config correcta que depende de que el operador recuerde flags". Validates that the
# IDENTITY-CRITICAL public URLs render correctly in the three supported modes and, above
# all, that the BARE BASE never renders localhost (it must layer dev or prod, or the
# control's boot guard refuses to start — see engine/cmd/control/main.go).
#
# Modes:
#   dev   = base + auto-loaded docker-compose.override.yml   → localhost (+ ALLOW_LOCAL)
#   prod  = COMPOSE_FILE=base:deploy/prod + FLUXO_DOMAIN      → the public domain
#   base  = -f docker-compose.yml only (no overlay)          → EMPTY, never localhost
#   prod- = prod without FLUXO_DOMAIN                         → MUST fail (compose config)
#
# Run from the repo root:  ./scripts/check-compose-config.sh
set -uo pipefail
cd "$(dirname "$0")/.." || exit 2

# FORGE_WORKDIR is a non-identity var with no default; set it so `config` is quiet.
export FORGE_WORKDIR="${FORGE_WORKDIR:-/tmp/vibeforge-runs}"
# Never let a stray shell/.env identity var pollute the checks.
unset VIBEFORGE_PUBLIC_URL VIBEFORGE_CONSOLE_URL VIBEFORGE_CORS_ORIGIN NEXT_PUBLIC_VIBEFORGE_API_URL

fail=0
ok()   { printf '  ok   %s\n' "$1"; }
bad()  { printf '  FAIL %s\n' "$1"; fail=1; }

echo "[1/4] dev — base + auto override → localhost expected"
out="$(docker compose config 2>/dev/null)" && [ -n "$out" ] || { bad "dev config failed to render"; }
grep -q 'VIBEFORGE_PUBLIC_URL: http://localhost:8080' <<<"$out" && ok "PUBLIC_URL=localhost" || bad "dev PUBLIC_URL not localhost"
grep -q 'VIBEFORGE_ALLOW_LOCAL_URL: "1"' <<<"$out" && ok "ALLOW_LOCAL=1" || bad "dev ALLOW_LOCAL not set"

echo "[2/4] prod — COMPOSE_FILE + FLUXO_DOMAIN → domain expected"
out="$(FLUXO_DOMAIN=fluxo.example.com COMPOSE_FILE=docker-compose.yml:deploy/docker-compose.prod.yml docker compose config 2>/dev/null)"
grep -q 'VIBEFORGE_PUBLIC_URL: https://fluxo.example.com/forge-api' <<<"$out" && ok "PUBLIC_URL=domain" || bad "prod PUBLIC_URL not domain"
grep -q 'NEXT_PUBLIC_VIBEFORGE_API_URL: https://fluxo.example.com/forge-api' <<<"$out" && ok "NEXT_PUBLIC baked=domain" || bad "prod NEXT_PUBLIC not domain"

echo "[3/4] base — bare base, no overlay → EMPTY identity URLs, NEVER localhost"
out="$(docker compose -f docker-compose.yml config 2>/dev/null)"
if grep -E 'VIBEFORGE_PUBLIC_URL:|VIBEFORGE_CONSOLE_URL:|NEXT_PUBLIC_VIBEFORGE_API_URL:' <<<"$out" | grep -q localhost; then
  bad "bare base renders localhost (the incident) — must be empty"
else
  ok "bare base renders no localhost identity URL"
fi

echo "[4/4] prod without FLUXO_DOMAIN → compose config MUST fail"
if COMPOSE_FILE=docker-compose.yml:deploy/docker-compose.prod.yml docker compose config >/dev/null 2>&1; then
  bad "prod without FLUXO_DOMAIN rendered instead of failing"
else
  ok "prod without FLUXO_DOMAIN fails at compose config"
fi

echo
if [ "$fail" -eq 0 ]; then echo "ALL COMPOSE CONFIG CHECKS PASSED"; else echo "COMPOSE CONFIG CHECKS FAILED"; fi
exit "$fail"
