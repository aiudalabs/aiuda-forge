# aiuda-forge — project context & pending work

Autonomous software factory (idea → spec → backlog → PRs), sold by aiudalabs.
Monorepo: `engine/` (Go, module `forge`) + `console/` (Next.js/TS).

## Local run (dev)
- Control: `engine/cmd/control` on :8080. Needs auth env: `VIBEFORGE_ADMIN_EMAIL`/`VIBEFORGE_ADMIN_PASSWORD`
  (seeds the first user), `VIBEFORGE_API_TOKEN` (service token the orchestrator uses), `VIBEFORGE_CORS_ORIGIN`,
  `VIBEFORGE_AUTH_DB`, plus the docker/sandbox/PR env (see prior session commands).
- Orchestrator: `engine/cmd/orchestrator -source native -workflow factory` with `VIBEFORGE_API_TOKEN`.
- Console: `console/`, `npm run dev` on :3000.
- Sandbox: `vibeforge-agent:local` (agent, node+python) + `forge-gate:local` (gate, node+python) on the
  `vibeforge-egress` network behind the tinyproxy allowlist (`engine/scripts/egress-up.sh`).
- Full system audit: `docs/AUDIT-2026-06-25.md`.

## Remediation status (from the audit)
- ✅ Wave 0/1 — auth (email+password, sessions), mandatory auth middleware, CORS lockdown, repo validation,
  docker-required hard-fail, secret redaction; tickets-store state machine, orchestrator error-path
  compensation, kernel races (fence-guarded SyncBack, etc.). Commits 79603ca + the integration hooks.
- ✅ Wave 2 — multi-tenant: `project_id` on runs/tasks/events/stories/sprints; `owner_id` + per-project
  `execution_unit`/`merge_mode` on projects; owner-scoped `/projects`; `?project=` filters; per-project
  scheduler; dead `merge_policy` removed. Commit 7a9dc79.
- ⏳ Wave 5 (operability/scale) — NOT done: workdir GC + events retention (A3, "disk-full = outage"),
  Postgres + SKIP-LOCKED claim (A4), HTTP client timeouts + backpressure + parallel reconcile (A5).

## Pending / future tasks (backlog — keep this list current)

### From the 2026-06-25 E2E review (user feedback)
1. **Per-user GitHub workspace/org + GitHub auth.** Each user should configure which GitHub space/org to use
   (e.g. personal `nmlemus` vs `aiudalabs`), with a default, in their settings. Today the system uses the
   host's `gh` auth + the global `VIBEFORGE_GH_ORG`. Decide WHEN/HOW a user authenticates against GitHub
   (GitHub OAuth / per-user token) so repos are created under the right org with the user's own credentials.
2. **Move Studio to the TOP of the nav.** Studio is the entry point (create project → design); it currently
   sits mid-menu, which is unintuitive for new users. Reorder the sidebar.
3. **Conversational answers to design open-questions.** At each design gate, the user should be able to ANSWER
   the phase's open questions directly (chatbot-style), not only approve / reject-with-feedback. Today the
   only options are "approve" and "reject/request changes". Build a conversational response that feeds the
   answers back into the phase. (Interim workaround: type answers into the "request changes" feedback box —
   it loops the phase back and incorporates them.)
4. **Internationalization (i18n).** The web app must support Spanish + English at least (next-intl or similar);
   externalize all strings.
5. **Review the design-phase order vs BMAD / SDLC best practices.** Current flow:
   discovery → PRD → architecture → UI → mockups → backlog. Confirm this matches BMAD's canonical order and
   SDLC best practice (PRD = WHAT before architecture = HOW is standard; question is whether an explicit
   requirements/elicitation step belongs between discovery and PRD, and whether architecture should inform
   the PRD). Research and adjust `engine/registry/workflows/design.yaml` + the personas if needed.
6. **Responsive UI for large/external monitors.** On a laptop it's fine, but on a wide external monitor a
   fixed max-width container leaves a lot of empty space on the right. Use the full viewport width on large
   screens (fluid/responsive layout).
7. **JIRA-style kanban board.** The tickets board should show (almost) all columns in one view; today it
   requires horizontal scrolling. Make columns fit/condense so the whole kanban is visible at once.
8b. **Mockup viewer: open full-screen / in a new tab as a functional UI.** The generated
    `docs/mockups/index.html` is self-contained and meant to be clicked through to evaluate. Today it's
    embedded inside the Studio UI (iframe) where you can't do much — add an "open in new tab / full screen"
    action (serve/open the mockup standalone) so stakeholders can evaluate it as a real UI.
8. **Containerize the full stack for production.** Today only the egress-proxy (always) + ephemeral
   `vibeforge-agent`/`forge-gate` containers (during a FACTORY run) run in docker; the control, orchestrator,
   console, AND the design-phase agents run on the HOST. For production: docker-compose (or k8s) the whole
   stack, and consider sandboxing the design agents too (they run claude on the host with host env today).

### Carryover
9. `merge_policy` could return later as a real RISK dimension of `merge_mode` (auto for low-risk, human for
   high-risk) — but wired this time, not a no-op.
10. Password-change endpoint (Wave 1 only seeds the admin; no self-serve password change yet).
11. B2 from earlier: per-lane sandbox IMAGES (today one fat node+python image covers python+react; flutter/
    other stacks need their own image + the lane→image routing).
12. Operability/scale (audit Wave 5): workdir GC + events-table retention (A3), Postgres + SKIP-LOCKED
    claim (A4), HTTP client timeouts + backpressure (A5).
13. **Wire the live-log (step.event) into the DESIGN runner too.** Today the terminal live-log only works for
    factory runs (the agent StepRunner emits step.event); the `design` step runner doesn't, so a design run's
    live view is empty. Thread the same ctx emitter into the design runner.
14. **Agent wall-clock timeout is hardcoded at 20m** (`cmd/control/main.go:47`). Make it configurable
    (env `VIBEFORGE_AGENT_TIMEOUT`), and consider a LONGER timeout for the backlog/design phases — large
    projects blow 20m.
15. **Scrum-master over-generates for large projects → backlog times out (HIGH, found via serviciospty E2E).**
    The marketplace backlog ran the full 20m and timed out, failing the whole design run. Fix: make the
    scrum-master produce a LEAN, MVP-first backlog (core happy-path loop first, ~8–15 stories, defer
    nice-to-haves) and/or actually USE the referenced `sharding-method` to chunk generation, so big projects
    don't exceed the agent timeout. A failed backlog step should also not nuke the whole run (add on_fail/retry
    on the backlog step, like the gates have).
16. **`$step.text` doesn't resolve (resolver no-op).** Agent step result text lands at
    `$<step>.output.text`, NOT `$<step>.text`. design.yaml's `$discovery.text`/`$prd.text`/`$architecture.text`
    inputs are therefore latent no-ops — the design agents only work because each re-reads docs/ from the
    workdir. factory.yaml correctly uses `$draft_story.output.text`. Fix: promote output.text to a top-level
    `.text` in reportAndAdvance (or special-case it in resolve.go) and clean up design.yaml's inputs.
17. **Suppress benign hydration warning** from browser extensions (Grammarly injects
    `data-gr-ext-installed`/`data-new-gr-c-s-check-loaded` on `<body>`). Add `suppressHydrationWarning`
    to the `<body>` in the root layout so the dev console isn't noisy.
18. **Retry leaves duplicate steps → UI/artifact pick the stale FAILED one.** After POST /runs/{id}/retry,
    a re-fired step appears twice (old FAILED + new DONE). `GET /runs/{id}/artifacts/{step}` and the Studio
    phase logic return/pick the FIRST match (the failed one), so the doc won't render even though the phase
    re-completed. Fix: artifact/status lookups should use the LATEST step instance (or RetryRun should
    supersede, not duplicate). Found re-running serviciospty's backlog.
19. **Factory fires before the design docs are merged to dev (ordering bug, HIGH).** handoff publishes the
    stories and the orchestrator claims+fires SP1 immediately, racing the human merge of the docs_pr PR.
    The factory clones `dev`, which lacks docs/ AND the architect's `.vibeforge-gate` until that PR is merged,
    so draft_story has no PRD/architecture and the gate fails ("no .vibeforge-gate"). Fix options: publish
    stories in a 'blocked' state until the docs PR is merged; or have the design COMMIT docs+gate to dev
    directly (gated); or make the factory ensure docs+gate present (and the orchestrator not fire until the
    project's design docs are on dev). Found re-running serviciospty.
20. **design.yaml handoff didn't forward project_id (FIXED 2026-06-26).** publish.go reads
    inputs["project_id"] but the handoff step only passed backlog+repo, so published stories/sprints got
    project_id="default" → the factory run got "default" → the project-scoped Board showed nothing. Added
    `project_id: $trigger.project_id` to the handoff step. (Wave-2 integration miss: registry/workflows
    wasn't in MTEngine's lane.)
21. **"Ready" column is misleading in sprint mode (UX inconsistency).** The Kanban derives "ready" per-STORY
    (deps satisfied), but in sprint mode the factory fires per-SPRINT. A no-dep story (e.g. S1-28 app shell in
    SP6 frontend) shows "ready" even though its sprint can't run until earlier sprints are done+merged — so it
    won't actually fire. Fix: in sprint mode, derive readiness at the sprint level (a story is "ready" only
    when its whole sprint is ready), or visually indicate the story is gated by its sprint's ordering.
22. **Ticket card/detail shows only the title — surface the description (body + acceptance).** Each story HAS
    a user-story `body` ("As a X, I want Y, so that Z") + detailed `acceptance` criteria, but the Kanban card
    only renders the title, so the user can't see what a story is. Show the body + ACs in the ticket card
    (truncated) and full in the ticket detail/drawer. (The full build spec is generated just-in-time by
    draft_story — that's separate; this is just surfacing the skeleton description that already exists.)
