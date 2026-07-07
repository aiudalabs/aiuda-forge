#!/usr/bin/env bash
# Regression harness for the generic e2e-verify ORCHESTRATOR (S3, §2B) — the behavior
# floor that RUNS the integrated system instead of reading files (that is S2's run.sh).
#
# For BOTH reference stacks it assembles a repo exactly as scaffolding would (the ONE
# generic orchestrator from _common + the stack's own DATA under .fluxo/verify/e2e/ +
# the stack contract), boots the stack's REAL backend, and runs boot -> seed -> flow ->
# invariants -> teardown. It asserts, against a real backend:
#   - buggy fixture -> orchestrator FAILS (exit 1): the boundary bugs are caught.
#   - clean fixture -> orchestrator PASSES (exit 0): no false positives once fixed.
#
# This is the §1-bis proof at the behavior layer: both stacks come out of the SAME
# _common orchestrator; only their DATA (contract + e2e assets) differs. No stack string
# lives in the orchestrator — it lives in the per-stack e2e assets this harness assembles.
#
# Requirements (real backends, not mocks):
#   flutter-firebase : firebase-tools + a JRE (Firebase Emulator Suite). No Docker needed.
#   react-supabase   : the Supabase CLI + a running Docker daemon (supabase start).
# A stack whose backend toolchain is unavailable is SKIPPED with a visible notice — never
# silently passed. Select a subset with:  run.sh flutter-firebase   |   run.sh react-supabase
#
# Usage: examples/verify-fixtures/e2e/run.sh [stack ...]   (from anywhere)
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$HERE/../../.." && pwd)"
TPL="$REPO/engine/registry/templates/github-native"
ORCH="$TPL/_common/.fluxo/verify/e2e_verify.py.tmpl"

# fixture-subdir : stack-template-dir : preflight-check-cmd
ALL_CASES=(
  "flutter-firebase:aiuda-flutter-firebase:command -v firebase >/dev/null && command -v java >/dev/null"
  "react-supabase:react-supabase:command -v supabase >/dev/null && command -v timeout >/dev/null && timeout 8 docker info >/dev/null 2>&1"
)

# Accept either the fixture name (flutter-firebase) or the stack template name
# (aiuda-flutter-firebase) as a selector; no args = run all.
WANT=("$@")
want_case() { [ ${#WANT[@]} -eq 0 ] && return 0; for w in "${WANT[@]}"; do { [ "$w" = "$1" ] || [ "$w" = "$2" ]; } && return 0; done; return 1; }

fails=0
ran=0

# Assemble a scaffold-equivalent repo: fixture app files + the generic orchestrator from
# _common + the stack's e2e DATA + the rendered contract. .tmpl suffixes are stripped, the
# same transform scaffold.Render does.
prep_workdir() {
  local fx="$1" stack="$2" wd="$3"
  cp -R "$fx"/. "$wd"/
  mkdir -p "$wd/.fluxo/verify"
  cp "$ORCH" "$wd/.fluxo/verify/e2e_verify.py"
  cp "$TPL/$stack/.fluxo/verify/stack.verify.yaml.tmpl" "$wd/.fluxo/verify/stack.verify.yaml"
  ( cd "$TPL/$stack/.fluxo/verify/e2e" && find . -name '*.tmpl' | while read -r f; do
      dest="$wd/.fluxo/verify/e2e/${f%.tmpl}"; mkdir -p "$(dirname "$dest")"; cp "$f" "$dest"; done )
}

run_case() {
  local fx="$1" stack="$2" case="$3" want_rc="$4"
  local wd; wd="$(mktemp -d "${TMPDIR:-/tmp}/fluxo-e2e-XXXXXX")"
  prep_workdir "$HERE/$fx/$case" "$stack" "$wd"

  # setup_cmd (the stack-declared toolchain install) — the CI workflow runs this too.
  local setup
  setup="$(python3 -c "import yaml,sys;print((yaml.safe_load(open('$wd/.fluxo/verify/stack.verify.yaml')) or {}).get('e2e',{}).get('setup_cmd',''))")"
  if [ -n "$setup" ]; then ( cd "$wd" && bash -lc "$setup" ) >"$wd/setup.log" 2>&1 || { echo "  (setup failed; see $wd/setup.log)"; }; fi

  ( cd "$wd" && python3 .fluxo/verify/e2e_verify.py --repo . ) 2>&1 | sed 's/^/    /'
  local rc=${PIPESTATUS[0]}

  if [ "$want_rc" -eq 0 ]; then
    if [ "$rc" -eq 0 ]; then echo "  ==> OK: clean fixture PASSED (rc=0)."; else echo "  ==> REGRESSION: clean FAILED (false positive, rc=$rc)."; fails=$((fails+1)); fi
  else
    if [ "$rc" -ne 0 ]; then echo "  ==> OK: buggy fixture FAILED as expected (rc=$rc)."; else echo "  ==> REGRESSION: buggy PASSED but should have failed."; fails=$((fails+1)); fi
  fi
  rm -rf "$wd"
}

for c in "${ALL_CASES[@]}"; do
  IFS=':' read -r fx stack check <<<"$c"
  want_case "$fx" "$stack" || continue
  echo "================================================================"
  echo "STACK: $stack"
  echo "================================================================"
  if ! bash -c "$check"; then
    echo "  SKIP: backend toolchain not available locally for $stack (need: $check)."
    echo "        Correct-by-construction: the same orchestrator + contract run in CI"
    echo "        on the fluxo-backend image. Not marking as a pass or a failure."
    echo
    continue
  fi
  ran=$((ran+1))
  echo "--- BUGGY (expect FAIL) -----------------------------------------"
  run_case "$fx" "$stack" buggy 1
  echo "--- CLEAN (expect PASS) -----------------------------------------"
  run_case "$fx" "$stack" clean 0
  echo
done

echo "================================================================"
if [ "$ran" -eq 0 ]; then
  echo "NO STACKS RAN — no backend toolchain available. Nothing verified."
  exit 2
fi
if [ "$fails" -eq 0 ]; then
  echo "ALL E2E FIXTURES OK — the orchestrator FAILS the buggy repos and PASSES the clean ones,"
  echo "against a REAL backend, from the same _common orchestrator ($ran stack[s] exercised)."
  exit 0
fi
echo "$fails e2e fixture assertion(s) FAILED."
exit 1
