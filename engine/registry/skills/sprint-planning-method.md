# Skill — sprint-planning-method

How to run a sprint-planning ceremony: turn the live backlog + what's been learned
into a **sprint goal** and a small, reversible set of **actions**, written as a PLAN
doc a human approves at a gate. The approved actions are applied to the ticket store
verbatim, so they must be concrete and individually legal.

## 1. Read the real state (not the design snapshot)

- **`backlog_snapshot`** (input) is the source of truth: a JSON array of the project's
  live stories, each with `id`, `title`, `status`, `sprint_id`, `owner`, `kind`,
  `screen_key`, `deps`. Parse it. Never read `docs/backlog.yaml` — that is the frozen
  design-time plan, and mid-flight moves/splits already diverged from it.
- **`sprint_id`** is the sprint about to fire; **`sprint_goal`** is its design-time goal.
- If **`docs/REVIEW-SP<n>.md`** or **`docs/RETRO-SP<n>.md`** exist for earlier sprints,
  read them for signal (what slipped, what was harder than expected, what got deferred).
  They will NOT exist for the first sprint — that's fine; plan from the backlog alone.

> **Snapshot currency.** `backlog_snapshot` is a point-in-time copy taken when the
> ceremony was triggered — the moment the sprint became ready — NOT a live view. Hours
> may pass at the approval gate, and the store can move underneath (a mid-sprint move, a
> bug that entered via an iteration). If your plan hinges on a story's exact state
> (its sprint, status, or deps) and you're unsure it's still current, SAY SO in the
> plan's rationale so the reviewer can double-check before approving. The apply step is
> the safety net — it re-validates every action against the LIVE store at apply time and
> aborts the whole plan (nothing applied) if any action went stale — but a plan that
> flags its own assumptions is easier to approve with confidence.

## 2. Define the sprint goal

State, in one or two sentences, the single coherent outcome this sprint delivers — a
demoable increment, not a list of tickets. A good goal makes it obvious which stories
belong and which should be deferred.

## 3. Decide the adjustments

Look at the stories currently assigned to this sprint (and the wider backlog) and ask:

- **Is the sprint the right size?** ~8–15 stories is a healthy, shippable batch — a
  heuristic, not a rule. An overloaded sprint should shed its weakest-priority stories
  (`defer`); a thin one may pull forward a ready, in-goal story (`move`).
- **Is every story still in-goal?** A story that no longer serves the goal → `defer` it
  to a later sprint, or `cancel` it if it's genuinely obsolete.
- **Is any story too big to finish in one sprint?** `split` it into independently
  shippable parts.
- **Did a review/retro reveal a wrong assumption?** `edit` the story's body/acceptance,
  or fix its `deps`/owner.
- **Is the plan already right?** Then propose **no** actions (`actions: []`) and just
  confirm the goal. This is the expected outcome for a first sprint or a healthy plan —
  the ceremony's value is the deliberate check, not forcing change.

Prefer the **smallest reversible change**. Every action is a real mutation applied after
a human approves, so bias toward `defer`/`move` (reversible) over `cancel` (terminal).

## 4. Write the plan

Write ONE markdown doc to the `output` path. Structure:

```markdown
# Sprint plan — <sprint_id>

## Goal
<one or two sentences>

## Rationale
<what you looked at; why these actions, or why none>

## Open questions
<questions for the reviewer to `answer` at the gate — omit the section if none>

## Actions
​```yaml
actions:
  - op: move
    story_id: S-12
    args:
      sprint_id: SP3          # target sprint — MUST already exist
  - op: defer
    story_id: S-9             # → the next sprint (created if it doesn't exist yet)
  - op: split
    story_id: S-4
    args:
      parts:                  # ≥2 parts; each inherits the original's sprint/epic/deps
        - id: S-4a            # optional; omit to auto-name S-4-a, S-4-b, …
          title: "Checkout — cart summary"
          body: "As a buyer, I see my cart totals before paying."
          acceptance: "- Totals reflect quantity and tax."
          owner: react-dev    # optional; inherits the original's owner if omitted
        - id: S-4b
          title: "Checkout — payment"
          body: "As a buyer, I pay with a saved card."
          acceptance: "- A successful charge advances to confirmation."
  - op: edit
    story_id: S-7
    args:                     # only the fields you name are changed
      title: "Refined title"
      body: "Updated user story"
      acceptance: "- New acceptance criteria"
      owner: python-dev
      deps: [S-1, S-2]        # REPLACES the dep set; every id must exist, no cycles
  - op: cancel
    story_id: S-8             # abandon (obsolete work) — terminal
​```
```

## Actions reference (exact — the apply step parses these keys)

- The `actions:` list must live in a fenced ```yaml block (or be the whole file as
  yaml). Each entry has `op`, `story_id`, and an op-specific `args`.
- **`move`** — `args.sprint_id` is the target; it must already exist. Legal only for a
  `backlog`/`failed` story.
- **`defer`** — no args. Moves the story to the sprint that fires right after its current
  one, creating that sprint if it doesn't exist. The story must be in a sprint already.
- **`cancel`** — no args. Abandons the story (legal from `backlog`/`failed`/`in_review`).
- **`edit`** — `args` may set any of `title`, `body`, `acceptance`, `owner`,
  `screen_key`, `deps`. `deps` REPLACES the whole set (must exist, no self-dep, no cycle).
- **`split`** — `args.parts` is a list of ≥2 new stories; each part inherits the
  original's sprint, epic, kind, repo, and dependency edges. The original is cancelled and
  every dependent is rewired onto the parts.

## Atomicity — get every action right

The apply step **validates all actions first and applies none if any is illegal**, then
applies them in order. A single bad action (unknown `op`, a `move` to a missing sprint, an
`edit` dep that doesn't exist or forms a cycle, a `split` with <2 parts or a colliding id)
aborts the whole plan with nothing applied. So: use ids that appear in the snapshot, don't
target the same story with two conflicting actions, and keep each action legal on its own.
