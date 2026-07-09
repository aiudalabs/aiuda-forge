# Skill — sharding-method

How to decompose a PRD + Architecture document into a prioritised, wave-ordered backlog
of stories that each carry full context and have correct dependency edges.

---

## Step 1 — Identify atomic units of work

Walk the architecture document's module list. For each module:
1. Is there a data model change? → one migration/schema story.
2. Is there a service/API? → one story per endpoint group (CRUD for one entity = one story).
3. Is there a background job? → one story per job.
4. Is there frontend? → one story per screen or component group. A per-screen story
   carries its `screen_key` (`role.screen`); a component-group / design-system foundation
   story that builds no screen of its own carries `screen_key: none` (the explicit opt-out
   — every frontend story declares the field, real key or `none`).
5. Is there glue (integration, auth middleware, event bus)? → one story per integration point.

Avoid mixing layers in one story (e.g. "add DB table AND build the API" → split into two).

## Step 2 — Assign dependencies (depends_on)

A story B depends on story A when:
- B's implementation imports or calls code produced by A.
- B's test data requires A's schema to exist.
- B's acceptance criteria cannot be verified without A being done.

Avoid speculative dependencies — only add depends_on when the constraint is real.
Check for cycles: if A→B and B→A, one must be split.

## Step 3 — Assign wave numbers

- Wave 1: stories with no depends_on. These run in parallel first.
- Wave N: stories whose every dependency is in wave < N.
- Prefer fewer, larger waves over many tiny ones (reduces coordination overhead).
- P0 stories should appear in the earliest possible wave.

## Step 4 — Assign owners

Each story's `owner` is the agent ID that will implement it:
- `dev` — backend logic, API, workers, data model.
- `ux` — frontend screens and components (if a UX step is part of build).
- Use only agent IDs that exist in the registry.

## Step 5 — Size and priority

Size from the architecture: count the number of files-to-change and function-to-write.
XS = trivial config/migration. S = one function + test. M = one module. L = cross-module change.
Split any story the architect cannot describe in one paragraph of what-to-build.

Priority from the PRD:
- P0: blocks the core use case (the product cannot be demoed without it).
- P1: important but the core demo works without it.
- P2: nice-to-have or internal tooling.

## Quality gate

After sharding, run the PO checklist (po-checklist.md) over the full backlog before
declaring it ready. If any checklist item fails, add, split, or enrich stories.
