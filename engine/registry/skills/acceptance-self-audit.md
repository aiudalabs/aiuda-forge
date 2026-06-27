# Skill — acceptance-self-audit

Before you declare the work done, you MUST run this audit and write its result as the
last section of your reply. It is the gate between "I wrote code" and "this satisfies
the ticket." Most review rejections are a criterion you *read* but never *verified*.

## The audit (mechanical — do not skip any line)

For **each** acceptance criterion, produce one row:

```
AC: "<the criterion, verbatim>"
   met by: <file>:<line(s)>   — <how it is satisfied, concretely>
   test:   <test file>:<name> — asserts the REQUIRED behavior
```

Then apply these checks to every criterion before you accept it as met:

1. **Measurable thresholds** — if the AC states a number (≤5s, ≥3 items, within 2 retries,
   page size N), confirm the implementation actually meets THAT number. A 10s poll does
   not satisfy "updates within 5s". Quote the line that sets the value.
2. **Enumerated requirements** — if the AC lists items joined by "and" ("category, rating,
   AND coverage area"), confirm EACH item is implemented. Two of three = unmet.
3. **Named states/edges** — if the AC names a state (empty, loading, error, unauthorized,
   duplicate), confirm that exact case is handled the way the AC requires. "Shows a
   placeholder when empty" is NOT satisfied by hiding the section.
4. **Honest tests (anti-vacuous)** — write the test from the AC's REQUIRED behavior, never
   from what the code currently does. If the AC says "empty → placeholder", the test must
   assert the placeholder renders; a test asserting the section is hidden is WRONG and
   masks the bug. Removing the production behavior must make the test fail — if it
   wouldn't, the test is vacuous.

## Output

End your reply with:

```
## Acceptance self-audit
- AC1 "…" → met by file:line · test … ✓
- AC2 "…" → met by file:line · test … ✓
- …
All criteria verified against their exact wording (thresholds, enumerations, states).
```

If any criterion is NOT met, FIX it before finishing — do not report it as met. An
audit that lists an unmet criterion as ✓ is worse than no audit.
