# Skill — retro-method

The method for the **sprint retrospective**: turn a sprint's telemetry into a small set
of concrete, approvable edits to the METHOD (personas / skills / workflows). Used by the
`retro-analyst` agent in the `retro` workflow. The retro is the auto-improvement loop:
the next sprint should run with a better method because of what the last one taught us.

## Inputs

- `sprint_id` — the sprint under retrospection.
- `telemetry` — bounded JSON of the sprint's runs. Its shape (fields you will cite):
  - `runs[].steps[]` — `step`, `type`, `status`, `attempts` (>1 = it retried),
    `duration_ms`, `stalled` (idle/timeout), `agent`, `error`.
  - `gates[]` — `decision` (approve|reject|answer) + the human's `text` (reject feedback
    / answer). This is gold: the reviewer told you, in words, what was wrong.
  - `plan` — the actions plan_apply applied (count).
  - `review` — the review `decision` + the `corrections` published.
  - `stories` — `done` / `failed` / `cancelled` counts + `failures[]` with `cause`.
  - `spend_usd` — the sprint's token cost.
- The registry, via your read tool — read the file you intend to change so `content` is
  the whole improved file.

## The core discipline — SYMPTOM vs CAUSE-IN-THE-METHOD

The telemetry shows symptoms. Your job is the cause, and specifically the cause **in the
method** — because the method (personas/skills/workflows) is the only thing you can edit
to make the next sprint better. Examples:

- **Symptom:** the `ux` step timed out / stalled repeatedly (`stalled:true`, `attempts>1`).
  **Cause-in-method:** the ux skill asks for one giant generation. **Fix:** edit the skill
  to shard the screens / cap the output; or lower the step's ambition in the workflow.
- **Symptom:** a gate was rejected three times with feedback "missing empty states".
  **Cause-in-method:** the persona's acceptance checklist omits empty/loading/error
  states. **Fix:** add them to the persona/skill.
- **Symptom:** several stories failed with the same dependency/compile error.
  **Cause-in-method:** the story-detailer isn't emitting the setup step, or the
  architecture skill omits a convention. **Fix:** that upstream doc.

A symptom with **no** method cause (a flaky third-party outage, a one-off) is NOT a
proposal — name it under "what didn't" and move on.

## Rules for good proposals

1. **Group by pattern, not by incident.** Three timeouts of the same agent are ONE
   proposal, not three.
2. **Cite the evidence.** Every proposal names the telemetry that motivates it (which
   steps, which gate text, which failures). No evidence → not a proposal.
3. **Cap at ~3–5 proposals** — the highest-impact ones. A 20-change retro can't be
   reviewed and won't be applied well. Prefer the few edits that prevent the most pain.
4. **Whole files, not diffs.** `content` is the COMPLETE proposed file (the registry
   validates and stores whole files; a textual diff is fragile). Read the current file
   first and return it with your change folded in, preserving everything else.
5. **No problems → no proposals.** Emit `proposals: []` and say the method held up. This
   is a good outcome, not a failure — do not invent work.

## Output — `docs/RETRO-SP<n>.md`

Markdown with: **What worked**, **What didn't** (grouped by pattern, evidence-cited), and
a fenced `proposals:` yaml block:

```yaml
proposals:
  - kind: skill              # agent | skill | workflow
    id: ui-screens-template  # the registry id to replace
    rationale: "The ux step stalled 3× (telemetry runs[].steps ux, stalled:true) — it generates all screens in one pass."
    evidence: "SP3 ux step: attempts=3, stalled=true, duration_ms>… ; gate answer 'split the generation'."
    content: |
      # Skill — ui-screens-template
      … the WHOLE improved file …
```

## Guardrails (enforced by registry_apply — a violation aborts the whole apply)

- Edit ONLY `agent` / `skill` / `workflow` registry files. Any other path is rejected.
- **Never** edit the retro's own method — the `retro-analyst` agent, THIS `retro-method`
  skill, or the `retro` workflow. The method of the retro is not self-editable; those
  meta-changes are made by a human by hand. Proposing one is rejected and aborts.
- Ids are a single safe segment (`^[A-Za-z0-9_-]+$`) — no paths, no traversal.

## The conversational gate

The human accepts, answers, or rejects your retro. On an `answers`/`feedback` re-run,
UPDATE this doc (refine or drop proposals) — do not regenerate. The approved proposals
are then applied to the registry and the sprint's `retro_at` is stamped; the next sprint
runs with the improved method.
