# Skill — story-template

Every backlog story MUST use this format. Stories carry full context — the implementer
should never need to read the PRD or architecture doc to implement a story.

---

## Story Template

```
## <STORY-ID>: <Title>

**Epic**: <epic name from PRD>
**Owner**: <agent id that implements this — e.g. dev>
**Screen key**: <role.screen for a frontend screen story, e.g. customer.catalog — or `none` for a frontend foundation story with no screen of its own; OMIT for backend stories>
**Depends on**: [<STORY-ID>, …] or none
**Priority**: P0 | P1 | P2
**Size**: XS | S | M | L (XS ≤ 1h, S ≤ 4h, M ≤ 1d, L ≤ 3d)

### Context
Two to four sentences explaining WHY this story exists. Reference the PRD requirement ID
(e.g. "implements FR-03") and the architecture section it touches. Do NOT repeat the acceptance
criteria here; explain the broader purpose.

### What to build
Concrete, implementation-level description. Name the files, modules, functions, and schemas
that need to change. If the story touches the data model, include the exact field names.

### Acceptance criteria
Hereda los **escenarios GIVEN/WHEN/THEN** de los FR del PRD que esta story implementa —
esos escenarios SON el contrato de aceptación y la señal que la verificación comprueba.
Cópialos aquí verbatim (no los reinventes) y añade edge cases propios de la story si aplican.
- [ ] AC-1 (de FR-XX): GIVEN … WHEN … THEN …
- [ ] AC-2 (de FR-XX): GIVEN … WHEN … THEN …
- [ ] AC-3 (edge case de la story): GIVEN … WHEN … THEN …

### References
- PRD: FR-XX, NFR-YY
- Architecture: §2 (module), §3 (entity name)
- Depends on: STORY-ID (reason for dependency)
```

---

**Quality bar**: Any story that requires the implementer to read the PRD or arch doc to
understand WHAT to build is too thin — add more context.
Any story larger than L should be split.
A story with no acceptance criteria is not a story.
