# PLAN — Pivote GitHub-native: de fábrica propia a Studio + Conductor

**Fecha:** 2026-07-03 · **Deriva de:** ADR-2026-07-03-studio-first-github-native.md
**Insumos:** 3 auditorías profundas (consola completa, engine paquete-por-paquete, registry
persona-por-persona) + PoC vivo en github.com/nmlemus/forge-copilot-poc.

---

## 1. La visión (qué es el producto después del pivote)

El usuario llega DE CERO con dos prerequisitos: cuenta de GitHub y Copilot (o Claude Max).
Desde nuestra consola:

1. **Diseña** su producto en Studio (discovery → PRD → arquitectura → UI → mockups →
   backlog, con gates humanos) — SIN CAMBIOS: es el corazón y corre en nuestro engine.
2. Al aprobar el diseño, el Studio **scaffoldea el repo en SU GitHub**: código base del
   stack, docs/, la especialización horneada (`.github/agents/`, `AGENTS.md`, `CLAUDE.md`,
   `.claude/skills/`, `.github/instructions/`, `copilot-setup-steps.yml`, workflows de QA)
   y **exporta el backlog como GitHub Issues con dependencias nativas** `blocked_by`
   (validado: 44/44 issues, 73/73 aristas).
3. **El desarrollo pasa en GitHub** (Copilot cloud agent / Claude / Codex / claude-code-action)
   y nuestra consola es el **mission-control visual tipo JIRA**: kanban/grafo/sprints (la UI
   recién rediseñada), sesiones de agentes en vivo, cola de PRs, costos — más los gates
   humanos que su nivel de autonomía haya dejado activos.
4. **El engine queda en dos piezas**: el pipeline de diseño (intacto) y el **CONDUCTOR** —
   la política que GitHub no tiene: gating por sprint, goal-mode, ruteo de modelo por lane,
   presupuesto, merges y aprobación de workflows según autonomía configurada.

**El lock-in queda donde no duele:** la especialización es markdown portable (AGENTS.md es
estándar multi-tool), los repos son del cliente en su GitHub, el conductor habla 3 canales de
ejecución intercambiables, y el engine legacy queda congelado como fallback self-hosted.

## 2. Journey usuario-desde-cero

**Onboarding (nuevo):** registro → wizard "Conecta GitHub" (instala nuestra **GitHub App**
en su org — reemplaza el `gh` del host y `VIBEFORGE_GH_ORG`, cierra backlog #1) → checklist
de capacidades (¿Copilot? ¿plan? ¿partner agents? ¿token Claude opcional para el canal Max?)
→ org destino + defaults de autonomía.

**Diseño:** Studio igual que hoy. El final cambia: "publicar" = scaffold + especialización +
issues con deps + registro del proyecto en el conductor.

**Ejecución + seguimiento:** el conductor despacha según deps+sprint+política; el usuario ve
kanban/grafo/sesiones/PRs/costos y ejerce sus gates (aprobar sprint, mergear, contestar al
agente comentando el PR desde la UI).

**Cierre:** merge → webhook → recalcular ready-set → siguiente story/sprint → resumen de
sprint (entregado vs goal).

## 3. Modelo de autonomía (settings por proyecto, default por org)

Extiende `internal/projects` (ya tiene `execution_unit` + `merge_mode` de Wave 2):

| Setting | Valores | Estado |
|---|---|---|
| `execution_unit` | `story` \| `sprint` (goal-mode) | existe |
| `dispatch_mode` | `auto` \| `approve` (humano OK por story/sprint) | nuevo |
| `max_concurrency` | N agent-tasks en paralelo | nuevo |
| `merge_mode` | `auto` (checks verdes + QA aprueba) \| `manual` | existe, se endurece |
| `workflow_approval` | `auto_if_safe` (rerun solo si el diff NO toca `.github/workflows/**`) \| `manual` | nuevo — caveat de seguridad del PoC |
| `model_by_lane` | map lane→modelo (python-dev→sonnet, infra→opus, ui→barato) | nuevo (motor en `billing.DefaultPolicy`) |
| `executor` | copilot \| claude_action \| partner \| mixed por lane | nuevo |
| `budget_monthly` + `on_exceed` | monto + `hard_stop`\|`warn` | extiende `billing` |

Presets: **Piloto automático** / **Copiloto** (default) / **Manual**. El chequeo corre en
dispatch-time (`conductor/policy.go`) y merge-time (`conductor/reconcile.go`).

## 4. La consola — veredicto por vista

**Regla de oro (hallazgo del audit):** `lib/api.ts` ya es la capa única fetch+map — el
pivote es re-apuntar mappers a la proyección del conductor, no reescribir vistas.

| Vista | Veredicto | Cambio |
|---|---|---|
| Studio (Entry/View/Docs) | **KEEP** | Solo el handoff backend publica a GitHub; `createProject` usa la GitHub App |
| Tickets (kanban/grafo/sprints/tabla) | **ADAPT (caso canónico)** | UI intacta (recién rediseñada); `OrchestratorTicket`↔Issue calza 1:1; el status derivado (ready/running/in_review) lo compone el conductor |
| Brain | **KEEP** | Futuro: tools del conductor (crear issue, reencolar) |
| Board·Runs | **REBUILD** → **Sesiones de Agentes** + **Pull Requests** | Reusa LiveLog/TimelineBar/StatusPill/DiffBox; muere pausar-fábrica |
| Overview | ADAPT | KPIs desde proyección (issues/PRs/sessions/billing) |
| Registry | ADAPT/KILL parcial | Diseño se queda; agentes de ejecución salen (van a templates por-repo) |
| Spend | ADAPT | Copilot premium requests + Actions minutes + agent-tasks + design runs |
| Team | KEEP | Reconciliar con colaboradores del repo |
| Settings global | REBUILD | Muere sandbox/agent_auth; nace Conexión GitHub + secrets + Copilot check |
| Settings proyecto | REBUILD | El modelo de autonomía §3 |
| Docs | KEEP | Reescribir contenido al modelo nuevo |
| Auth/AppShell/Topbar | KEEP | Topbar: costo y campana desde proyección |

**Vistas nuevas:** Onboarding wizard · Sesiones de Agentes (agent-tasks + workflow runs
unificados) · PR-view/cola de merge (checks, reviews, gate `action_required`, acciones
según `merge_mode`) · Config de autonomía · Billing GitHub.

**Nav nueva:** Studio · Backlog · Agentes · Pull Requests · Resumen · Brain · Costos ·
Equipo · Settings · Docs.

**Transporte:** reusar el WS bus (`lib/ws.ts`); el conductor traduce webhooks GitHub a
eventos del bus con el vocabulario existente. Billing y agent-tasks por polling
(`refetchInterval`), el resto push.

## 5. El engine — inventario y conductor

### Veredictos (paquete → destino)

- **KEEP (pipeline de diseño, ~1/3 del engine):** `workflow` (kernel corre design.yaml),
  `agent` (runner+backends, SIN sandbox/egress — diseño corre en host), `store`,
  `tickets` (store de proyección), `api`, `auth`, `projects`, `settings`, `billing`,
  `brain`, `channels`, `httpx`, `pr` (docs_pr).
- **TRANSFORM:** `orchestrator` → **`internal/conductor`** (su política YA es la spec:
  `native.go` RunOnce tiene ready-set, gating por sprint #19, goal-mode, auto-merge acotado,
  PR-terminal, R3 re-sync — cambia la FUENTE: store local→GitHub, poll→webhooks);
  `imports` → **`internal/export`** (se invierte: backlog→issues+deps, el script del PoC
  en Go); `cmd/orchestrator` → binario conductor.
- **NUEVO:** **`internal/ghapp`** — GitHub App (JWT→installation tokens por org),
  `POST /webhooks/github` (firma X-Hub-Signature-256, dedupe por delivery-id; patrón:
  el webhook Telegram de `api/server.go:168`), REST tipado (agent-tasks con `model`,
  dependencies, workflow approve/rerun, checks).
- **FREEZE/KILL:** `sandbox`, `gate` (Actions ES el sandbox y el gate), egress,
  `cmd/worker`, `cmd/live`, workflows factory/factory-plus/gated, backends argv.

**GitHub App, no PAT:** multi-tenant real (org por usuario, #1), webhooks limpios,
permisos finos. El modo `gh`/PAT queda como fallback self-hosted (es el `internal/github`
actual, 385 LOC, que ya tiene el 80% de la superficie de escritura).

**Estado local mínimo (SQLite alcanza):** mapping story↔issue↔PR↔agent_task (reusar
`external_ref`/`run_id` del store tickets), política por proyecto, ledger de presupuesto,
dedupe de webhooks. GitHub es la verdad para issue/PR/check.

### Backlog: qué muere solo con el pivote

- **MUEREN:** #8 (containerizar stack: solo control+console+conductor necesitan hosting),
  #11/B2 (imágenes por lane → `copilot-setup-steps.yml`), A3/A5 (sin workdirs ni pool
  sandbox), R1/R2/R3 (patologías del kernel-ejecutor; requeue = reasignar issue),
  #14/#15-timeout (agent tasks tienen los suyos), V1/V2 (sello de hash del gate — se retira
  la doctrina entera), D8-parcial (metodología en Go se resuelve por borrado).
- **SIGUEN (nuestro valor):** #1 (ahora central: la App por org ES el auth), #2-#7 (UX
  Studio), #9 (resurge como workflow_approval/merge por riesgo), V3 (verify agéntico +
  ui-verify Playwright — MÁS central que nunca), billing/budget, A4 baja a "quizás".

## 6. Registry — el port de agentes y skills

**Hecho clave (runtime):** el kernel inyecta solo el persona `.md` + skills concatenadas
(`manifest.go:58-80`); el `.yaml` (model/tools) es config del runner → se re-expresa en
frontmatter destino.

- **SE QUEDAN (diseño):** analyst, pm, architect, ux, designer, scrum-master,
  product-advisor, iteration-planner + 8 skills de diseño + design.yaml/iterate.yaml.
- **SE PORTAN (ejecución → repo generado):** dev, python-dev, react-dev, flutter-dev,
  firebase-dev → `.github/agents/<lane>.agent.md` (caben holgados: mayor persona 5k chars
  + skills inline ≈ 8.8k de 30k máx) + espejo `.claude/agents|skills` + reglas por-path en
  `.github/instructions/*.instructions.md` (`applyTo:` glob por lane) + toolchain en
  `copilot-setup-steps.yml` por stack.
- **SE BORRAN, no se portan (~30-40% de cada dev-persona):** doctrina `.vibeforge-gate`
  /anti-tamper, vendor-deps-offline, "NO hagas commit/PR" (se invierte: el agente SÍ abre
  el PR), contrato `$step.output.text`/reportAndAdvance, backends argv. Borrar esto retira
  el gate frágil que Wave V quería muerto.
- **story-detailer se pliega en el export:** la expansión skeleton→spec corre al exportar,
  y el body del issue viaja con la spec dev-ready completa (contexto + refs FR/NFR + ACs
  falsificables). Sin agente build-time; resuelve #16 de paso.
- **Generador:** `registry/templates/github-native/{_common,<stack>}/...` con substitución
  simple de strings. Variables que el pipeline de diseño YA produce: stack, lanes,
  design_tokens (de DESIGN_SYSTEM.md), path_map (del ARCHITECTURE.md), validation_commands,
  project_name, language. Reusar los stack-profiles de `aiuda-stack` como fuente de verdad.

**QA/verificación (config recomendada, Wave V re-expresada):**
1. Piso CI: suite en Actions + **suite-integrity marker-count** como workflow reusable
   (el único guard determinista contra borrar-el-test-que-falla).
2. **Copilot code review** always-on en cada PR de agente (gratis, nativo).
3. **Nuestro reviewer cross-model** como `.github/workflows/claude-review.yml`
   (claude-code-action, opus, on: pull_request): reviewer.md porta casi verbatim — ya es
   read-only y máquina-de-veredicto-vs-ACs. verifier.md → paso **ui-verify Playwright**
   para lanes frontend.
4. El conductor NUNCA auto-aprueba workflows cuyo diff toque `.github/workflows/**`.

## 7. Roadmap consolidado (el sistema actual sigue corriendo)

**F0 — Export productizado** (chico, aditivo; la demo del pivote)
`internal/export` (script PoC en Go: issues + labels + blocked_by) + story-detailer plegado
(spec completa en el body) + `POST /projects/{id}/export` + botón en el handoff de Studio.

**F1 — Proyección + Tickets re-apuntado** (el par mayor-impacto/menor-costo)
`internal/ghapp` (App + webhooks + firma) + `conductor/projection.go` → endpoints con el
shape que la UI ya espera → re-apuntar `listTickets`/`listEpics`/`createStory` en `api.ts`.
La UI JIRA recién hecha muestra el backlog real proyectado desde GitHub. Onboarding wizard
mínimo (instalar App, verificar Copilot, org).

**F2 — Scaffold especializado + dispatch en modo approve**
Templates `github-native` generados al publicar diseño + `conductor/dispatch.go` con
`model_by_lane` (Agent tasks REST) en `dispatch_mode=approve` (humano confirma cada
despacho). Settings de autonomía v1.

**F3 — Autonomía completa + vistas nuevas**
`conductor/reconcile.go` webhook-driven (auto-merge, retry acotado, PR-terminal),
`workflow_approval=auto_if_safe`, budget hard-stop. REBUILD de Board → Sesiones + PR-view.
Proyecto piloto en `auto` total.

**F4 — QA layer + cierre**
claude-review.yml cross-model + suite-integrity + ui-verify en los templates; Spend desde
billing GitHub; **apagar factory legacy** (desregistrar runners sandboxed en `app.go`,
FREEZE en sitio — no borrar: fallback self-hosted).

## 8. Riesgos y mitigaciones

| Riesgo | Mitigación |
|---|---|
| Dependencia de GitHub (precios/preview/términos) | 3 canales de ejecución; especialización portable en el repo del cliente; engine legacy congelado como fallback |
| Agent tasks API en public preview (puede cambiar) | Aislarla en `ghapp/rest.go`; canal claude-code-action no depende de ella |
| Partner agents requieren plan del usuario | Checklist de onboarding degrada elegante: siempre queda claude-code-action o Copilot básico |
| Seguridad: agente modifica CI para exfiltrar | `workflow_approval` nunca auto si toca `.github/workflows/**`; review obligatorio |
| Calidad sin nuestro sandbox/gate | La spec de calidad (Studio) + QA de 3 capas §6; el PoC dio 4/4 ACs |
| Coexistencia legacy/nuevo durante migración | Conductor crece AL LADO del scheduler leyendo los mismos stores; nada se apaga hasta F4 |
