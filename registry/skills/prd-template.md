# Skill — prd-template

Use this template as the structure for the **Product Requirements Document** produced by the PM phase.
Every section is mandatory. Derive content from the project brief; surface gaps as open questions.

---

## Product Requirements Document

### 1. Goal
One sentence: what this PRD authorises the team to build and why it matters.

### 2. Background
Two to four sentences of context. Link to the project brief.

### 3. Functional Requirements
Group by epic. Each requirement: unique ID (FR-01), verb-first statement, priority (P0/P1/P2).

#### Epic 1 — <name>
- FR-01 [P0]: The system shall …
- FR-02 [P1]: …

#### Epic 2 — <name>
- FR-03 [P0]: …

### 4. Non-Functional Requirements
- NFR-01 [Performance]: …
- NFR-02 [Security]: …
- NFR-03 [Availability]: …
- NFR-04 [Scalability]: …

### 5. User Stories (key paths only)
Format: As a <role>, I want <action> so that <outcome>.
Cover the top 3 critical paths. Edge cases go in acceptance criteria, not here.

### 6. Acceptance Criteria (per epic)
Bullet list of testable conditions per epic. Each must be falsifiable.

### 7. Success Metrics
Map back to the brief's metrics. Add any product-level KPIs (DAU, conversion, p99 latency).

### 8. Out of Scope
Repeat the brief's out-of-scope list; add any that emerged during PRD elaboration.

### 9. Dependencies & Risks
- External APIs, third-party services, team bandwidth constraints.
- Top 3 risks with likelihood + impact + mitigation.

### 10. Open Questions
Numbered list of unresolved items blocking the next phase (Architecture).

---

**Quality bar**: Every FR must be testable. Every epic has at least one acceptance criterion.
No requirement is both functional AND non-functional — separate them cleanly.
