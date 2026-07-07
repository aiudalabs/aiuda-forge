#!/usr/bin/env bash
# Regression harness for the generic provisioning-lint engine (S2).
#
# It runs the SAME generic engine (_common/.fluxo/verify/provisioning_lint.py) against
# a buggy and a clean fixture for BOTH reference stacks, using each stack's own rule
# table + contract. It asserts:
#   - buggy  fixture -> engine FAILS (exit 1): the 8-bug repo is caught (§1 regression).
#   - clean  fixture -> engine PASSES (exit 0): no false positives once fixed.
#
# This is the §1-bis proof: both stacks come out of the ONE engine — the abstraction
# is real, not Firebase-shaped. No stack strings live in the engine; they live in the
# rule tables this harness points it at.
#
# Usage: examples/verify-fixtures/run.sh   (from the repo root)
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$HERE/../.." && pwd)"
TPL="$REPO/engine/registry/templates/github-native"
ENGINE="$TPL/_common/.fluxo/verify/provisioning_lint.py.tmpl"

# fixture-subdir : stack-template-dir
CASES=(
  "flutter-firebase:aiuda-flutter-firebase"
  "react-supabase:react-supabase"
)

fails=0

run_case() {
  local root="$1" rules="$2" verify="$3"
  python3 "$ENGINE" --root "$root" --rules "$rules" --verify "$verify" \
    --provisioning "$root/docs/provisioning.yaml"
}

for c in "${CASES[@]}"; do
  fx="${c%%:*}"; stack="${c##*:}"
  rules="$TPL/$stack/.fluxo/verify/provisioning.rules.yaml.tmpl"
  verify="$TPL/$stack/.fluxo/verify/stack.verify.yaml.tmpl"

  echo "================================================================"
  echo "STACK: $stack"
  echo "================================================================"

  echo "--- BUGGY (expect FAIL) -----------------------------------------"
  out="$(run_case "$HERE/$fx/buggy" "$rules" "$verify")"; rc=$?
  echo "$out"
  if [ "$rc" -ne 0 ]; then
    echo "==> OK: buggy fixture failed as expected (rc=$rc)."
  else
    echo "==> REGRESSION: buggy fixture PASSED but should have failed."
    fails=$((fails + 1))
  fi

  echo "--- CLEAN (expect PASS) -----------------------------------------"
  out="$(run_case "$HERE/$fx/clean" "$rules" "$verify")"; rc=$?
  echo "$out"
  if [ "$rc" -eq 0 ]; then
    echo "==> OK: clean fixture passed (rc=$rc)."
  else
    echo "==> REGRESSION: clean fixture FAILED (false positive, rc=$rc)."
    fails=$((fails + 1))
  fi
  echo
done

echo "================================================================"
if [ "$fails" -eq 0 ]; then
  echo "ALL FIXTURES OK — engine catches the buggy repos, passes the clean ones."
  exit 0
fi
echo "$fails fixture assertion(s) FAILED."
exit 1
