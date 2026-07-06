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
6. ✅ **Responsive UI for large/external monitors** (RESUELTO 2026-07-02 en Tickets, la peor página). Causa
   raíz encontrada: `.wrap { animation: fadeUp ... both }` animaba `transform`, dejando un transform computado
   permanente que convertía `.wrap` en containing block de los `.drawer position:fixed` → drawers posicionados
   contra el wrap (banda blanca fantasma a la derecha + drawer interceptando clicks). fadeUp ahora anima solo
   opacity — NO reintroducir transform ahí. Tickets rediseñado a shell full-bleed (header + toolbar + canvas).
7. ✅ **JIRA-style kanban board** (RESUELTO 2026-07-02). Board full-bleed sin caja wrapper, columnas hairline
   `flex:1 1 210px; min/max 150-480px`, colapsables a riel vertical (vacías colapsan por default) → las 6
   columnas caben sin scroll horizontal desde ~1280px. Cards con lane chip (owner), conteo de ACs, sprint,
   PR link y run link reales. `lib/statusToken.ts` es la ÚNICA fuente del color/pill de estados (antes 5 maps
   divergentes). Estado view/ticket/run en la URL. Filtros búsqueda/estado/sprint/lane en toolbar persistente.
   El endpoint GET /tickets ahora expone `owner`/`epic_id` (ticketView, api/tickets.go).
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
21. ✅ **"Ready" misleading in sprint mode** (RESUELTO 2026-07-02 a nivel UI). `waitingBySprint()` en
    TicketsView deriva, por story, los sprints que su sprint espera (deps cross-sprint no done); Kanban/Tabla/
    Sprints muestran "⧗ Esperando a SPn" y apagan la pill de una ready/backlog gated. (El orquestador ya
    difería el fire — esto alinea lo que la UI promete con lo que va a pasar.)
22. ✅ **Ticket card/detail: body + acceptance** (RESUELTO 2026-07-02). La card kanban muestra body (clamp 3
    líneas) + conteo de ACs (✓ n) + lane chip + sprint + PR/run links; el drawer muestra ACs completos con
    conteo, lane, repo, deps clickeables (navegan al dep) y botón Reencolar cuando la story está failed
    (antes requerían el viaje ticket→run→RunDrawer).

## Wave R — Resiliencia / recuperación (HIGH — bloquearon al usuario en vivo, 2026-06-26)
R1. **Session-limit de Claude marca stories como `failed` permanentemente.** Pegar el límite de sesión
    ("You've hit your session limit · resets ...") es transitorio, pero el step falló y la story quedó
    `failed` aunque el kernel reintentó el step solo y el run siguió RUNNING. → Tratar el límite como error
    retryable-con-backoff (esperar al reset / reintentar), NO como falla terminal. Distinguir errores
    transitorios (rate/limit/timeout) de fallas reales en el runner del agente.
R2. **No hay forma de sacar una story/sprint de `failed`** (la queja del usuario: "no tengo cómo devolverlas
    al backlog o a que se ejecuten de nuevo"). `legalSources` no tiene transición de salida desde `failed`
    (`failed`→`backlog` y `failed`→`running` ilegales). → Agregar acción "Reencolar": transición legal
    `failed`→`backlog`, endpoints `POST /stories/{id}/requeue` y `POST /sprints/{id}/requeue`, y botón en
    Board/RunDrawer. Requeue debe limpiar run_id/pr_url para que el orquestador dispare fresco.
R3. **Story `failed` con run que se recupera (RUNNING) queda desalineada y el PR quedaría huérfano.** El
    orquestador solo flipea story→running en su propio FireRun; una recuperación interna del kernel (requeue
    de step stale) no re-sincroniza el estado de la story. → Al recuperar un step/run, reconciliar el estado
    de las stories del run. (Incidente 2026-06-26: SP3+SP6, 10 stories, realineadas a mano failed→running
    vía tickets.db porque el run estaba vivo; backup en /tmp/forge/tickets.db.bak-*.)

## Wave V — Rediseño de verificación (ESTRATÉGICO, decidido 2026-06-26)
Contexto: el gate hoy combina TRES mecanismos. El frágil es el sello de hash del comando
(`.vibeforge-gate`), que se rompe distinto con cada stack (python ok, react `npm test`→`vitest`,
mañana flutter) y es una cinta de correr de mantenimiento. Los agentes lo editan (python-dev lo
reescribió y borró frontend; react-dev corrigió el comando) → tamper → run muere. El fix táctico
(seed correcto + regla "no editar el gate", commits 530eecd/40d185b) desbloquea, pero la causa de
fondo es DÓNDE pusimos el candado: sobre el *comando*, que el agente legítimamente define por-stack.

Decisión: pasar de "sello rígido del comando" a **verificación en capas**, cada mecanismo atacando
la amenaza que le toca. NO es "confiar ciegamente en el agente" (sin humano-por-paso, el reward-hack
de borrar tests es real) ni "sello rígido" (frágil por-stack). El humano sigue en el PR final
(merge_mode: manual), lo que baja el costo de error del gate.

V1. **Conservar el piso determinista AGNÓSTICO al stack** (barato, sin mantenimiento):
    - correr la suite en sandbox offline (sin red, deps vendorizadas) — hace creíble "los tests pasan".
    - suite-integrity: contar marcadores (`def test_`, `it(`, `testWidgets(`…) y que NO bajen. Ataca
      el reward-hack #1 (borrar el test que falla). Esto ya existe y se queda.
V2. **Quitar el sello de hash del COMANDO** (el `.vibeforge-gate` GateHash). En su lugar: el architect
    define un *contrato de mínimos* por lane ("frontend ≥N tests de componente, todo verde"); el dev
    es dueño del *cómo* (el comando exacto, que puede declarar). El contrato lo hace cumplir el conteo,
    no un hash. Elimina la cinta de correr por-stack (flutter ya no rompe nada).
V3. **Sumar verificación agéntica + funcional** (acá brilla la calidad de los modelos):
    - el `reviewer` (agentic_verify) revisa contra los ACs — ya existe.
    - para lanes de UI: paso `ui-verify` con browser (Playwright) que arranca la app y verifica que la
      pantalla renderiza y el flujo clickea de verdad — señal mucho más fuerte que "los tests pasan".
V4. **Antes de implementar V2**: análisis adversarial corto por escrito de "qué reward-hacks quedan
    abiertos si quitamos el sello de comando" (ej. comando que sale 0 sin correr nada; tests que
    siempre pasan). Mitigaciones: el conteo + el reviewer + ejecución-real-offline deben cubrirlos.
Modelo mental: determinista donde es barato y el gaming es probable (borrar tests); agéntico donde
hace falta juicio (¿el código funciona?, ¿la UI sirve?). Esencialmente BMAD + piso determinista barato.

## Wave D — Hallazgos de validación (revisión paralela 2026-06-26, ver console/public/docs/*.md)
D1. **[CRÍTICO·SEGURIDAD] Hueco de lectura cross-tenant.** `listRuns` toma `?project=` sin verificar
    propiedad contra el usuario, y `getRun` no tiene scoping alguno (`api/server.go:180-214`). A1 se cerró
    en DATOS (columnas project_id) pero NO en autorización: cualquier sesión válida lee runs/tasks de otro
    proyecto. Fix: chequear ownership del proyecto en listRuns/getRun (y demás rutas por-id).
D2. **[CRÍTICO] design.yaml encadena contexto VACÍO** (era #16, confirmado ACTIVO). `$discovery.text`,
    `$prd.text`, `$architecture.text` resuelven a "" porque el texto del agente vive en `output.text`
    (`resolve.go`, `design.yaml:32,47,64,81,97`). Cada fase recibe el brief/PRD/arquitectura previos vacíos,
    sin error; los agentes sobreviven re-leyendo docs/ del workdir. Fix: usar `$step.output.text` en
    design.yaml (o promover output.text a top-level `.text` en el resolver).
D3. **[ALTO·SEGURIDAD] Gate ausente pasa trivialmente.** Sin `.vibeforge-gate`, `Snapshot` hashea a "" y el
    anti-tamper se salta (`gate.go:54-60,116`). El piso determinista solo protege si el gate existía al sellar.
    Combinado con el race "factory clona dev antes de mergear docs" (#19) da fallos reales. Fix: gate ausente
    debe FALLAR, no pasar.
D4. **[ALTO] Run hard-delete sin retención** (`store/control.go:37-59`): explica la "desaparición del design
    run de Studio". Sin `deleted_at` ni audit; deja stories colgadas a un run_id inexistente. Fix: soft-delete
    + retención (liga a A3).
D5. **[MEDIO] Config muerta / campos YAML decorativos.** `skills:` nunca se carga ni inyecta (la consola
    muestra pestaña Skills no consumida); `prompt: adversarial` se emite literal sin efecto; `approval:
    risk-policy` declarado en 3 workflows pero `pr.go` no lo interpreta. Fix: implementarlos o quitarlos.
D6. **[MEDIO] `modeFor` cachea una lectura de settings fallida** (`native.go:508-525`): un blip de la API tira
    todo el ciclo de un proyecto a sprint+manual, pausando merges `auto` en silencio. Fix: no cachear errores.
D7. **[MEDIO] `depsDone` enmascara datos corruptos** (`tickets.go:1182`): una dep que apunta a un id borrado
    se trata como "no done" → deadlock silencioso del dependiente, sin error. Fix: dep inexistente = error visible.
D8. **[MEDIO] Vocabulario de metodología hardcodeado en Go** (diverge de la filosofía): forma del backlog
    (épicas/sprints) en `publish.go:13-42`, nombre del gate `.vibeforge-gate` en `gate.go:35`. Idealmente en
    el registry. Otros: timeout REAL es 20m hardcodeado (`main.go:50`), el 45m es solo env de despliegue;
    reaper puede duplicar llamada LLM pagada (>60s sin heartbeat); `mixed-lane → dev` solo loguea, sin señal UI;
    falta password-change (#10); imágenes de sandbox por-lane (#11/B2); R3 (desync story↔run borrado) pendiente.

## Wave D — ESTADO (2026-06-26): D1–D7 + password-change RESUELTOS
- D1 ✅ cross-tenant ownership (access.go) · D2 ✅ resolver $step.output fallback · D3 ✅ gate ausente falla
- D4 ✅ soft-delete runs · D5 ✅ skills inyectados · D6 ✅ no cachear settings fallidos · D7 ✅ dep inexistente loguea
- #10 ✅ POST /auth/change-password
- PENDIENTES (refactors grandes, no fixes de barrido): metodología hardcodeada en Go (publish.go epics/sprints,
  gate filename) = el refactor de "kernel 100% methodology-free"; imágenes de sandbox por-lane (#11/B2, infra);
  reaper-dup de llamada LLM (inherente a at-least-once, riesgoso);
  señal UI de mixed-lane→dev (menor). Cada uno merece su propio esfuerzo enfocado.

## v1.1 hardening — ESTADO (2026-06-27, PR #1, rama v1.1-hardening)
Primera tajada de v1.1 (ver `docs/ROADMAP-v1.1-v1.4.md`). Los 4 bugs HIGH que bloquearon el E2E, RESUELTOS con test:
- ✅ **#15** backlog `on_fail{goto:backlog,max:1}` + hint lean (`design.yaml`) — un timeout del scrum-master ya no tumba el design run.
- ✅ **#18** artifacts/status toman la instancia MÁS RECIENTE del step (prefiere DONE) — `api/server.go` + `console api.ts statusOf`.
- ✅ **#19** el orquestador difiere disparar un sprint hasta que `.vibeforge-gate` esté en `dev` — `github.FileOnBranch` + `BranchFileChecker` en `native.go` (solo github.com; local intacto).
- ✅ **R3** `reconcileRevivedRuns` re-sincroniza una story `failed` cuyo run revive a RUNNING — `native.go` + nuevo `StoryProvider.Failed()`.

## FORJA — estado del pivote GitHub-native (cierre 2026-07-04)
El producto se llama **Fluxo** (fluxo.aiudalabs.com; fluxo.sh libre como defensa). Plan F0–F4 COMPLETO.
Informes clave: docs/ADR-2026-07-03-studio-first-github-native.md, docs/PLAN-2026-07-03-pivot-github-native.md,
docs/GTM-2026-07-03-onboarding-monetizacion.md, docs/UI-AUDIT-2026-07-03.md. Docs de usuario reescritos (console/public/docs, 5 nuevos).

### Hecho y validado E2E (marketpty = 24/44 stories mergeadas + SP5/7/10/11 en PRs)
- Conductor completo: proyección (GitHub=verdad, tick 25s, autocorrección), dispatch (sprint-goal-mode, deps, picker
  de canal con probe real, override por despacho), auto-merge, workflow_approval auto_if_safe (nunca si toca
  .github/workflows/**), max_concurrency, executor/model por lane, failover de canal, requeue UI, barrido de
  sesiones muertas (Copilot), cierre de issues de PRs mergeados, Rescue checkpoint en claude.yml.
- Multi-tenant auth CERRADO: GitHub App "forja-by-aiudalabs" (manifest flow, public); OAuth login (token
  user-to-server + refresh persistido en auth.github_tokens); installation tokens (JWT RS256 propio, ghapp/tokens.go);
  resolución por proyecto (tenant.go: usuario→installation→host) en API y conductor loop. Validado: marketpty opera
  con credenciales del tenant.
- Costura: publicar backlog → export issues+deps + scaffold automáticos (api/costura.go, hook OnPublished).
- Secret Claude: probe real (workflow+secret) + siembra desde Settings→Canal Claude (no se almacena en Fluxo).
- UI: vista Agentes (sesiones+cola PRs+aprobar workflows), Settings completo, Registry con tab Templates GitHub
  (cards humanas, editor renderizado, Aplicar al proyecto), Board legacy eliminado, rebranding Fluxo.
- design.yaml: docs_pr → base main (trunk-based; dev era del factory legacy). Factory legacy OFF por default.

### Pila pendiente (próximas sesiones, en orden)
1. **Permiso Copilot en la App**: la Agent tasks API devuelve 403 'does not have read access' con el token
   user-to-server → falta permiso Copilot en el manifest/App (editar permisos + re-aprobación del usuario).
   Mientras: degradación al host en dev (github/dispatch.go CreateAgentTask + probe).
2. **Wizard UI de onboarding** (5 pantallas del GTM): Continue with GitHub → instalar App → semáforos de
   capacidades (Copilot/Claude-secret) → org+preset autonomía → primer proyecto. Todo el backend ya existe.
3. **Despliegue público** fluxo.aiudalabs.com: webhooks activos (receptor F1 listo; la App ya apunta ahí con
   active:false), TLS, y el manifest deja de omitir hooks.
4. Menores acumulados: barrido de sesiones muertas para claude_action (mirar conclusión del run);
   gatear/avisar dispatch si docs/ no está en main; quitar EnsureDevBranch (rama dev vestigial);
   live-log del design run (#13); editor per-proyecto de .github/ del repo; conversational gates (#3);
   hardening tenantToken (verificar usabilidad del token antes de devolverlo).
