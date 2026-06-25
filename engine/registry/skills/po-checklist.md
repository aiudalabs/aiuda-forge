# Skill — po-checklist

The PO/Scrum Master uses this checklist before declaring a PRD or backlog ready for build.
Every item must pass; failing items are returned as feedback to the PM or Architect.

---

## PRD Quality Checklist

### Clarity
- [ ] Every functional requirement is verb-first and unambiguous.
- [ ] No two requirements overlap or contradict.
- [ ] The out-of-scope section exists and is non-empty.
- [ ] All acronyms and domain terms are defined on first use.

### Testability
- [ ] Every FR has at least one acceptance criterion that is falsifiable.
- [ ] Success metrics are measurable (not "users are happy").
- [ ] NFRs specify thresholds (e.g. p99 < 200 ms), not vague goals.

### Coverage
- [ ] Every use case from the project brief maps to at least one FR.
- [ ] Every actor mentioned in the brief appears in at least one user story.
- [ ] Dependencies and external services are named and risks documented.

### Architecture alignment
- [ ] Each epic maps to a coherent module or service boundary in the architecture doc.
- [ ] No FR implies a tech choice that conflicts with an ADR.
- [ ] Data model entities cover all FRs that store or retrieve data.

## Backlog Quality Checklist

### Story quality
- [ ] Every story has: title, owner, depends_on, priority, size, context, what-to-build, ACs, references.
- [ ] No story is larger than L (> 3 days).
- [ ] No circular dependencies in depends_on graph.
- [ ] Owner is a valid agent ID in the registry.

### Coverage
- [ ] Every P0 FR from the PRD has at least one story.
- [ ] Every entity in the data model has a creation story (schema migration or seed).
- [ ] Every external integration has a story for the integration layer.
- [ ] At least one story covers observability (logging, metrics, health check).

### Ordering
- [ ] Wave 1 contains only stories with no depends_on (safe parallelism).
- [ ] A story's wave number is strictly greater than all its dependencies.
