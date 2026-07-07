# e2e-verify — regression harness for the behavior floor (S3)

Where the S2 harness (`../run.sh`) proves the **static** `provisioning-lint` engine catches
the code↔system-boundary bugs by *reading files*, this harness proves the **behavioral**
`e2e-verify` orchestrator
(`engine/registry/templates/github-native/_common/.fluxo/verify/e2e_verify.py`) catches them
by *running the integrated system*: it boots each stack's **real backend**, seeds world
state, exercises the app's flow as an authenticated end-user client, and checks the universal
invariants — for **both** reference stacks, from the **same** `_common` orchestrator (the
§1-bis proof at the behavior layer).

## Run

```bash
examples/verify-fixtures/e2e/run.sh                 # both stacks (skips any without a backend)
examples/verify-fixtures/e2e/run.sh flutter-firebase
examples/verify-fixtures/e2e/run.sh react-supabase
```

Per stack it assembles a scaffold-equivalent repo (the fixture's app files + the generic
orchestrator from `_common` + the stack's e2e DATA under `.fluxo/verify/e2e/` + the rendered
`stack.verify.yaml`), boots the real backend, and asserts:

- **buggy → orchestrator FAILS** (exit 1): the boundary bugs are caught against a real backend.
- **clean → orchestrator PASSES** (exit 0): no false positives once fixed.

### Requirements (real backends, not mocks)

| Stack | Backend | Needs |
|---|---|---|
| `flutter-firebase` | Firebase Emulator Suite (firestore + auth + functions) | `firebase-tools` + a JRE. **No Docker.** |
| `react-supabase` | `supabase start` (Postgres + GoTrue + PostgREST, RLS enforced) | Supabase CLI + a **running Docker daemon**. |

A stack whose backend toolchain is unavailable is **SKIPPED with a visible notice** — never
silently passed. The Firebase side runs anywhere with Java; the Supabase side needs Docker up.

## What each buggy fixture reproduces (behaviorally, against a real backend)

| Fixture | Bugs (from §1) reproduced as behavior |
|---|---|
| `flutter-firebase/buggy` | **#3** rule denies the owner's own `bookings` read · **#5** callable crashes on cold start (`app/no-app`, Admin SDK never initialized) · **#7** no `onCreate` trigger, so signup never creates `users/{uid}` · **session_persists** app persistence is `NONE` · **no_client_over_read** over-permissive rule leaks `admin_config` |
| `react-supabase/buggy` | **#3** no RLS SELECT policy, so the owner sees zero of their own `bookings` · **#7** no trigger on `auth.users`, so signup never creates the `profiles` row · **session_persists** `persistSession` is off · **no_client_over_read** over-permissive RLS leaks `admin_secrets` |

The signup flow is exercised as **behavior**, never seeded — seeding `users`/`profiles` would
hide bug #7. The seed loaders here mechanically **refuse** to seed those tables.

## Honest coverage notes (verified by running, not assumed — §2B)

- **Bug #8 (missing composite index) is NOT catchable by the Firebase emulator.** Probed
  empirically: the Firestore emulator **enforces security rules** from client context (so #3
  is real here) but **runs composite queries with no declared index anyway** — it does not
  emit `FAILED_PRECONDITION`. So #8 stays covered where it actually bites: **statically** by
  `provisioning-lint` check A (S2, with the exact index JSON) and at **deploy-time** by the
  release-gate (S8, the index *BUILT*). This corrects the plan's §2B assumption that the
  emulator would surface #8.
- **Bug #5 (cold-start init) is Firebase-specific** (an Admin SDK/Cloud Function shape). On
  the Supabase side its analogue is an Edge Function without init, covered statically by that
  stack's `provisioning-lint`; the Supabase e2e flow focuses on the RLS/trigger bugs (#3/#7),
  which are the ones a real Postgres backend is uniquely needed to prove.
- **react-supabase requires Docker.** When the daemon is down the harness SKIPs it. The
  scripts are otherwise validated: they syntax-check, `@supabase/supabase-js` loads and
  constructs a client inside real Chromium (the Playwright flow mechanism), and the schema
  migrations encode the buggy/clean RLS + trigger differences. The same orchestrator +
  contract run it in CI on the `fluxo-web`/Supabase-service image.

## Note

Like the S2 fixtures, these `buggy`/`clean` trees are throwaway repos used only by `run.sh`;
the shared verify assets (orchestrator + per-stack e2e scripts) are copied in from the
templates at run time — proving both stacks come out of the same `_common`. `node_modules`
and copied-in files live in a temp workdir, never in the committed fixture.
