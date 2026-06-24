# Skill — architecture-template

Use this template for the **Architecture Document** produced by the Architect phase.
Be precise and opinionated — the team will implement from this doc. Defer to PRD for requirements.

---

## Architecture Document

### 1. Technology Stack
Table of every major technology choice with rationale and alternatives considered.

| Layer | Choice | Version | Rationale | Alternative rejected |
|-------|--------|---------|-----------|----------------------|
| Backend | … | … | … | … |
| Frontend | … | … | … | … |
| Database | … | … | … | … |
| Auth | … | … | … | … |
| Infra/Deploy | … | … | … | … |

### 2. System Structure
Describe the top-level modules/services. One paragraph per service/package, covering:
- Responsibility boundary (what it owns, what it does NOT own)
- Public interface (API endpoints, events, function signatures)
- Internal structure if non-trivial

### 3. Data Model
One subsection per entity. For each:
- Fields (name, type, constraints, indexed)
- Relationships (FK, embedding, join table)
- Key access patterns (the queries this model must support)

### 4. Key Technical Decisions (ADRs)
Number each decision. Format: **Decision** — **Context** — **Consequences**.
1. …
2. …

### 5. NFR Approach
For each NFR in the PRD, state the architectural mechanism that satisfies it.
- Performance: caching strategy, query optimisation, connection pooling…
- Security: auth model, RBAC, secret storage, egress controls…
- Availability: retry, circuit-breaker, health-check strategy…
- Scalability: horizontal vs vertical, stateless design, partitioning…

### 6. Dependency Graph
List the inter-module dependencies (A depends on B) to expose circular-dependency risk.
Mark external services (third-party APIs, cloud providers).

### 7. Open Questions
Numbered list of unknowns the Architect could not resolve; must be answered before build.

---

**Quality bar**: A senior engineer should be able to implement any module from this doc
without asking a clarifying question about tech choice or data ownership.
