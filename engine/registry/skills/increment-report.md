# Skill — increment-report

The method for the **sprint-review report**: turn a completed sprint into an
evidence-based increment report that a human accepts (or rejects with feedback) at a
gate. Used by the `demo-reporter` agent in the `sprint-review` workflow.

The report is a REVIEW of what shipped — not a demo script and not a pitch. Its job is
to let a stakeholder decide, in a few minutes, whether the increment meets the sprint
goal, using the preview and the acceptance criteria as evidence.

## Inputs

- `sprint_id` — the completed sprint under review.
- `sprint_goal` — the goal as written upstream (may be empty).
- `stories_snapshot` — JSON array of the sprint's stories, each with `id`, `title`,
  `status`, `acceptance`, `owner`, `screen_key`. This is the authoritative list of what
  the sprint set out to build and the falsifiable criteria for each.
- `preview_url` — the live navigable build of the integrated increment.
- The integrated repository in the workdir, when present.

### Determining the sprint goal

1. If `docs/PLAN-SP<n>.md` exists in the workdir, use the goal it states (the
   planning ceremony sharpened it there).
2. Otherwise use the `sprint_goal` input.
3. If both are empty, infer the goal from the common theme of the stories in the
   snapshot and state that it was inferred.

## Output — `docs/REVIEW-SP<n>.md`

Write exactly one markdown document at the `output` path, with these sections in order:

### 1. Preview (first)
Lead with the `preview_url` on its own line — "▶ Open the increment: <preview_url>".
This is the single most useful artifact for the reviewer; it goes first, not buried.

### 2. Sprint goal & verdict
State the goal in one or two sentences, then a one-line plain verdict: does the
integrated increment meet the goal — fully, partially, or not — and in one clause, why.

### 3. What was built — story by story
One subsection per story in `stories_snapshot`, in the given order:
- **`<id>` — `<title>`** and a one-sentence statement of what it delivers.
- **Evidence**: cite the story's acceptance criteria and, for each, whether the
  increment satisfies it. When the repository is available, point at the concrete code
  or screen that implements it (a real path/symbol you actually read — never a guess).
  When the repo is not available, cite the AC + the preview screen (`screen_key`) as
  the evidence instead.
- Keep each entry tight: a reviewer should be able to map story → evidence at a glance.

### 4. Deviations & risks
List anything that diverged from the plan, is only partially done, or is a risk worth
weighing before acceptance (missing edge cases, deferred ACs, tech debt taken on). If
there are genuinely none, say "No deviations from the sprint goal." — do not pad.

## Evidence rules (non-negotiable)

- **Evidence, not marketing.** No superlatives, no "beautiful/blazing/seamless". State
  what the code does and let the reviewer judge. A report that reads like a sales page
  has failed.
- **Never fabricate.** Do not invent file paths, functions, metrics, or screens. If you
  did not read it, do not cite it. An acceptance criterion you cannot verify is named as
  "unverified", not asserted as met.
- **Tie every claim to a story's acceptance criteria or the preview.** Each "was built"
  claim maps to at least one AC line from the snapshot or a preview screen.
- **Name the gaps.** A partial or missing AC is the most useful thing you can surface —
  it is what the reject path turns into a correction story. Do not hide it to make the
  increment look complete.

## The conversational gate

The reviewer accepts, answers open questions, or rejects with feedback:
- **answer** — the text arrives as the `answers` input on a re-run. UPDATE this report
  to incorporate the answer (e.g. resolve an open question); preserve the rest.
- **reject** — routes to the corrections step, NOT back to you: the scrum-master turns
  the reviewer's feedback into correction stories in the next sprint. You do not write
  corrections.

Put anything you need clarified in a short "## Open questions" section so the reviewer
can answer it directly at the gate.
