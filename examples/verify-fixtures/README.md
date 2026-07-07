# verify-fixtures — regression harness for provisioning-lint (S2)

Proves the generic, table-driven `provisioning-lint` engine
(`engine/registry/templates/github-native/_common/.fluxo/verify/provisioning_lint.py`)
catches the code↔system-boundary bugs from §1 of the verification plan — and does so
for **both** reference stacks from the **same** engine (the §1-bis proof).

## Run

```bash
examples/verify-fixtures/run.sh      # from the repo root; needs python3 + pyyaml
```

It runs the engine against a `buggy` and a `clean` fixture per stack and asserts:
- **buggy → FAIL** (exit 1): the bugs are caught.
- **clean → PASS** (exit 0): no false positives once fixed.

## What each buggy fixture reproduces

| Fixture | Bugs (from §1) reproduced |
|---|---|
| `flutter-firebase/buggy` | #1 android/ scaffold missing · #2 ACCESS_FINE_LOCATION missing · #4 Maps API key missing · #5 Admin SDK never initialized · #6 `roles/datastore.user` used-but-undeclared · #8 composite index missing |
| `react-supabase/buggy` | Vite entry/config missing (web analogue of #1) · `VITE_SUPABASE_*` env undeclared (#2/#4) · Supabase client never constructed (#5) · `service_role` used-but-undeclared (#6) · `.eq()+.order()` query with no index migration (#8) |

Bugs **#3** (rules/RLS vs client) and **#7** (a doc the app creates at runtime) are
NOT here: they need a real backend and are caught by `e2e-verify` in a later sprint.

## Why the clean fixtures still emit one WARN

Check A (indexes) can prove a query *needs* an index but cannot prove a *specific*
declared index matches it — so when an index IS declared it emits a visible WARNING
("verify manually"), never a silent pass and never a blocking failure. That is the
plan's false-positive rule (§2A). The exact `FAILED_PRECONDITION` proof comes from the
emulator in `e2e-verify` (S3).

## Note

These are throwaway trees used only by `run.sh`; they are not scaffolded projects and
carry no lockfiles or dependencies.
