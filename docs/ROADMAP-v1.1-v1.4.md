# aiuda-forge v1.1 → v1.4 — Roadmap de funcionalidades

## Context

`aiuda-forge` (repo `github.com/aiudalabs/aiuda-forge`, etiquetado **v1.0.0** hoy) es una fábrica de software autónoma — validada E2E construyendo serviciospty. Ahora se quiere evolucionar hacia un **producto SaaS** con: un asistente conversacional por proyecto, colaboración multi-usuario, i18n, conexiones a canales externos (Telegram/WhatsApp/Slack/JIRA/GitHub), import de tickets, multi-engine con asignación modelo-por-agente, y una landing con pricing.

Son 8 funcionalidades grandes que **no caben en un release**. Este plan las secuencia en **v1.1→v1.4 por prioridad**, empezando por lo que el usuario marcó como más importante: el **agente global por proyecto** ("Brain") y la **iteración sobre proyectos existentes**.

**Decisiones tomadas con el usuario:**
1. **Roles por proyecto**: `owner · editor · viewer` (3 niveles).
2. **Brain**: *controla + propone* — ejecuta acciones reversibles solo (pausar/parar/reencolar/reintentar/diagnosticar); PROPONE las mutantes (lanzar features, mergear, editar registry/settings) y el humano aprueba.
3. **Phasing**: v1.1→v1.4 incremental, cada release entregable.
4. **Engines**: engine+modelo **por-agente en el registry** + **override por-proyecto** desde la UI; segundo engine = **opencode**.

Todas las rutas son relativas a `/Users/nmlemus/projects/genai/aiuda-forge`.

---

## Estado del código (grounding)

**Motor (Go, `engine/`):**
- `agent.Backend` interface (`engine/internal/agent/backend.go:87`) con `Options.Model` (`:34`); solo `ClaudeBackend` (`claude.go`) + `FakeBackend`. `claude -p … --model <opts.Model>`.
- **Modelo por-agente YA existe**: manifest `.yaml` con `model:` (`manifest.go:23`, ej. `registry/agents/architect.yaml: model: claude-opus-4-8`). Precedencia en `runner.go:65-67` y `verify.go:39-41`: `step.Model` (workflow) > `manifest.Model` (agente) > default. **Falta**: campo `engine:`, un segundo Backend, y override por-proyecto.
- Workflows como datos: `registry/workflows/{design,factory}.yaml`, kernel genérico por tipo de step.
- **Control endpoints (todos existen)** en `engine/internal/api/server.go`: `POST /runs`, `/runs/{id}/cancel|retry|requeue`, `/control/pause|resume`, `/runs/{id}/steps/{step}/approve|reject|merge`, `GET /control/status`, `/metrics`. Circuit-breaker `PauseUntil` en `workflow/engine.go`.
- No existe ningún "Brain"/chat.

**Auth/multi-tenancy (`engine/internal/`):**
- `auth/auth.go`: `User{ID,Email,hash,CreatedAt}` + `Session`; **sin roles**. Creación = solo `auth/seed.go SeedAdmin` (no hay self-signup).
- `api/access.go canAccessProject`: **single-owner estricto** (`p.OwnerID == uid`); service-token (uid="") sin restricción. `crossTenantDenied` para LISTs.
- `projects/projects.go`: `Project{…OwnerID,ExecutionUnit,MergeMode}`; **no hay `project_members`**.
- `settings/settings.go`: settings GLOBALES (MCP, AgentAuth, Sandbox) en `settings.json`, **secrets enmascarados** (`Masked()` → `••••`, round-trip preserva). **No hay credenciales por-usuario ni cifrado en reposo.**
- Tickets: `tickets/tickets.go` (Story/Sprint/Epic), `tickets/provider.go TicketProvider` interface (adaptable para JIRA/GitHub), `tickets/publish.go` (publish idempotente desde `backlog.yaml`).
- GitHub: `github/github.go` + `pr/pr.go` usan el `gh` CLI **a nivel host** (`VIBEFORGE_GH_ORG`), no per-usuario.

**Console (Next.js App Router, `console/`):**
- Rutas en `src/app/` (Overview `/`, studio, board, tickets, registry, spend, docs, settings). Nav en `src/lib/sections.ts` + `components/Sidebar.tsx`. Gate de auth en `AppShell.tsx`.
- **Cero i18n** — todo hardcoded en español, `<html lang="es">`. ~200–300 strings en 39 componentes.
- Cliente: `src/lib/{config.ts,api.ts,ws.ts,auth.ts,hooks.ts}` — REST + **WebSocket con auto-reconnect** (`ws.ts`), token en localStorage, TanStack Query.
- Reusables para chat: `components/board/LiveLog.tsx` (stream estilo terminal) + `ws.ts subscribe()` + ReactMarkdown. Patrón de entrada conversacional en `studio/StudioEntry.tsx`.
- Settings de dos niveles en `components/settings/SettingsView.tsx` (global: MCP/AgentAuth/Sandbox; por-proyecto: ExecutionUnit/MergeMode) — donde irán integraciones y selección de engine/modelo.
- **Sin landing**: todo tras auth; única ruta pública `/login`.

---

## v1.1 — Brain (asistente por proyecto) + Iteración de proyectos

**Goal:** un asistente conversacional embebido en la UI al que el usuario le dice "revisa el estado", "para la ejecución", "arregla X", "agrégale tal feature" — y poder **iterar** sobre un proyecto ya construido (añadir features, mejorar la UI) sin recrearlo.

### 1.1a — Brain (control + propone)
- **Servicio Brain (engine):** nuevo paquete `engine/internal/brain/` que corre un loop conversacional con **tool-use** (Anthropic API directo o `claude -p` con MCP) donde las herramientas son llamadas HTTP a los control endpoints existentes, **scopeadas por proyecto + rol**:
  - *Autónomas (reversibles):* `GET /control/status`, `/metrics`, `/runs*`, `cancel`, `retry`, `requeue`, `pause`, `resume`, diagnóstico de fallos (lee artifacts/eventos).
  - *Propone→aprueba (mutantes):* `POST /runs` (lanzar feature/iteración), `merge`, editar registry, cambiar settings. El Brain genera una "acción propuesta" que la UI muestra con botón Aprobar/Rechazar (reusa el patrón gate de `StudioView` approve/reject).
- **Gating por rol:** `viewer` → solo lectura/diagnóstico; `editor` → acciones reversibles; mutantes siempre requieren aprobación (incluso para owner). Reusa el contexto de auth/rol de v1.2 (en v1.1, gate simple por owner).
- **Endpoint + streaming:** `POST /projects/{id}/assistant` (mensaje) + stream de respuesta por el WS existente (`engine/internal/api` + `console/src/lib/ws.ts`). El Brain emite eventos tipo `assistant.token`/`assistant.action`.
- **UI:** panel de chat lateral/inferior reutilizando `LiveLog.tsx` (scroll + timestamps), `ws.ts subscribe()`, ReactMarkdown, y burbujas user/assistant nuevas. Caja de input estilo `StudioEntry.tsx`. Acciones propuestas como tarjetas con Aprobar/Rechazar.
- **Archivos clave:** nuevo `engine/internal/brain/*.go`, ruta en `engine/internal/api/server.go`, nuevo `console/src/components/assistant/AssistantPanel.tsx`, entrada en `lib/sections.ts`/`AppShell.tsx`.

### 1.1b — Iteración de proyectos
- Hoy el diseño es one-shot (`StudioEntry` → `POST /projects` → `POST /runs workflow:design`). Para **iterar**: nuevo workflow `registry/workflows/iterate.yaml` cuyos agentes leen los docs existentes (`docs/PRD.md`, `ARCHITECTURE.md` ya en `dev`) **+ el estado actual del repo** + un *change request*, y emiten un **backlog DELTA** (sprints/stories nuevas) que `ticket_publish` **agrega** al backlog existente (el publish ya es idempotente). La fábrica los construye sobre `dev`.
- **Disparo:** `POST /runs` con `workflow: iterate`, `project_id`, y el change request (desde la UI "Iterar proyecto" o pedido al Brain → propone → aprueba). Reusa orquestador + `ticket_publish` + factory existentes.
- **Beneficio colateral:** como la iteración trabaja sobre un repo que YA tiene docs+gate en `dev`, evita el race #19 para iteraciones.

### 1.1c — Hardening previo (bugs HIGH del backlog que bloquearon el E2E)
- **#19** (la fábrica dispara SP1 antes de mergear los docs a `dev`): publicar stories en estado `blocked` hasta que el `docs_pr` se mergee, o que el diseño commitee docs+gate a `dev` directamente. `engine/internal/orchestrator/native.go` + `design.yaml handoff/docs_pr`.
- **#15 (resto):** agregar `on_fail`/retry al step `backlog` en `design.yaml` (el dos-niveles ya está hecho; solo falta que un timeout no tumbe el run).
- **#18** (retry deja steps duplicados → la UI toma el FAILED viejo): que los lookups de artifacts/status usen la instancia MÁS RECIENTE del step. `engine/internal/api` (artifacts/getRun) + Studio phase logic.
- **R3** (`reconcileStoryRunDesync`): al recuperar un step/run, re-sincronizar el estado de las stories del run. `engine/internal/orchestrator/native.go`.

**Verificación v1.1:** levantar control+console local; abrir el panel del Brain en serviciospty; pedir "resume el estado" (lee metrics/runs), "para la ejecución" (llama pause, verificar `/control/status`), "agrega login con Google" (propone una iteración → aprobar → se publica un delta-backlog y corre). Confirmar gating por rol (viewer no puede pausar). Reproducir #19/#18 con un proyecto nuevo y ver que ya no ocurren.

---

## v1.2 — Multi-usuario / roles + i18n

**Goal:** varios usuarios colaboran en un proyecto con roles; UI en ES/EN/PT.

### 1.2a — Colaboración + roles (`owner · editor · viewer`)
- **Nueva tabla `project_members`** (`project_id, user_id, role, created_at`) en el store de projects (`engine/internal/projects/projects.go`). Owner = el `owner_id` actual (migración: insertar owner como member `owner`).
- **Self-signup:** endpoint `POST /auth/register` (hoy solo `SeedAdmin`) — necesario para SaaS; en `engine/internal/api/auth.go` + `auth/auth.go CreateUser` (ya existe).
- **Invitaciones:** `POST /projects/{id}/members` (invita por email, asigna rol), `DELETE …/members/{uid}`, `GET …/members`. Email de invitación vía el adaptador de email (stub local).
- **Guard de acceso:** extender `api/access.go canAccessProject` → consultar `project_members` y devolver el rol; nuevo helper `requireRole(ctx, projectID, minRole)` aplicado a escrituras (approve/cancel/launch = editor+; settings/members/billing = owner). Reusa el patrón de `crossTenantDenied`.
- **UI:** sección "Miembros" en Settings (`components/settings/`) — listar/invitar/cambiar rol/quitar. Selector de rol. El Brain (v1.1) ahora respeta el rol real.

### 1.2b — i18n (ES/EN/PT, full UI)
- **Framework:** `next-intl` (estándar App Router). Routing por locale `/[locale]/…` o detección + cookie; `<html lang>` dinámico.
- **Extracción:** mover los ~200–300 strings de los 39 componentes a `messages/{es,en,pt}.json`; `useTranslations()` en cada componente. Empezar por los de mayor superficie: `SettingsView.tsx`, `StudioView.tsx`, `BoardView.tsx`, `Overview.tsx`, `sections.ts`.
- **Selector de idioma** en la UI (topbar/perfil), persistido por usuario.
- **Archivos clave:** `console/next.config.ts`, nuevo `src/middleware.ts`, `src/i18n/*`, `messages/*`, y los 39 componentes.

**Verificación v1.2:** crear 2 usuarios; el owner invita al 2º como `editor` y a un 3º como `viewer`; comprobar que viewer no aprueba gates ni lanza runs (403/UI deshabilitada) y editor sí; cambiar idioma y ver toda la UI en EN y PT.

---

## v1.3 — Canales (OpenClaw) + Import de tickets

**Goal:** que un cliente SaaS conecte sus sistemas (Telegram, WhatsApp, Slack, JIRA/Confluence, GitHub) y opere/recibe notificaciones por esos canales; e importe tickets de JIRA/GitHub.

### 1.3a — Fundación de credenciales (cross-cutting, necesaria aquí)
- **Nueva tabla `integrations`/`credentials`** (`owner: user_id|project_id, provider, token(cifrado), metadata, created_at, expires_at`). **Cifrado en reposo** (clave por `VIBEFORGE_SECRET_KEY` / KMS en GCP) — hoy los secrets van en claro en `settings.json`. Reusar el patrón `Masked()` de `settings/settings.go` para nunca devolver el token.
- **UI de configuración** (Settings → "Integraciones"): conectar/desconectar cada proveedor, probar conexión, estado. Extiende la grilla MCP de `SettingsView.tsx`.

### 1.3b — Canales (estilo OpenClaw)
- **Adaptador de canal** común (interface `Channel{ Send(msg), OnInbound(handler) }`): webhooks entrantes (Slack slash / Telegram bot / WhatsApp) → mensajes al **Brain** del proyecto; salientes = notificaciones (run terminó, gate espera aprobación, fábrica pausada por créditos).
- **Orden sugerido:** Telegram + Slack primero (bots simples) → GitHub (ya hay base) → JIRA/Confluence → WhatsApp (Business API, más fricción).
- **Per-usuario/proyecto:** cada canal usa las credenciales de 1.3a.

### 1.3c — Import de tickets (JIRA + GitHub)
- **Adaptadores `TicketProvider`** (la interface ya existe en `tickets/provider.go`): `JiraImporter`/`GithubImporter` que leen issues externos y los mapean a `Story` (title→Title, description→Body, labels→Owner/lane, epics/sprints). `POST /projects/{id}/import` (provider + filtro).
- Reusa `tickets/publish.go` para insertar; mantener un `external_ref` en `Story` para evitar duplicados y sincronizar estado.

**Verificación v1.3:** conectar un bot de Telegram de prueba → mensajear al Brain desde Telegram y recibir el estado; importar 5 issues de un repo GitHub a un proyecto y verlos en el Board; comprobar que el token se guarda cifrado y se devuelve enmascarado.

---

## v1.4 — Multi-engine + Landing/Pricing (SaaS)

**Goal:** elegir engine+modelo por agente/proyecto; tener una página pública de marketing con pricing para vender el SaaS.

### 1.4a — Multi-engine + modelo por-agente
- **Segundo Backend:** `OpenCodeBackend` implementando `agent.Backend` (`engine/internal/agent/`), capaz de hablar con modelos multi-proveedor (qwen/kimi/sonnet) vía opencode. La interface ya existe; añadir `Engine string` a `Options` y al `Manifest` (`manifest.go`), y selección de backend por-agente (no solo el `EngineMode` global).
- **Precedencia:** `override por-proyecto` > `step.Model/Engine` (workflow) > `manifest` (agente) > default. Nuevo store `project_agent_overrides` (project_id, agent_id, engine, model) consultado en `runner.go`/`verify.go`.
- **UI:** Settings por-proyecto → tabla "Agentes" (Studio=opus, dev=sonnet/qwen, review=…) con dropdowns engine+modelo. Reusa `SettingsView` por-proyecto.
- **(Relacionado) Wave V — verificación por-lane:** al meter engines/stacks nuevos, el `ui-verify` NO es universal (Playwright es solo web; Flutter usa `flutter test integration_test`). Hacer el step de verificación funcional **por-lane en el registry** (web→Playwright, flutter→flutter test, backend→smoke API), y sumar `testWidgets(` al conteo de `gate.go`. Quitar el sello de hash del comando (V2) a favor del contrato-de-mínimos por conteo.

### 1.4b — Landing + Pricing (SaaS)
- **Separar marketing de consola** con route groups Next: `src/app/(marketing)/{page,pricing,docs}` (público) vs `src/app/(console)/…` (tras auth). El `/` público presenta producto + pricing; CTA → signup (v1.2).
- **Modelo de negocio:** planes (free/pro/team) atados a límites (proyectos, créditos/engines, miembros, integraciones). Medición de uso (ya hay `/spend`/metrics). Billing (Stripe) — definir en su momento; el `owner` es quien paga (rol ya lo contempla).

**Verificación v1.4:** asignar `dev=opencode/qwen` en un proyecto y correr una story con ese engine; ver la landing pública sin login en `/` y `/pricing`; signup desde la landing crea cuenta y entra a la consola.

---

## Cross-cutting / consideraciones

- **Cifrado de secrets** (1.3a) y **self-signup** (1.2a) son fundaciones SaaS; si se quiere, adelantarlas. Despliegue GCP (Cloud Run + Postgres, ya discutido) es el sustrato para todo esto — encaja con audit Wave 5 (Postgres + SKIP-LOCKED, A4) pendiente.
- **Postgres**: varias features nuevas (members, credentials, overrides, external_refs) refuerzan migrar de SQLite a Postgres antes/durante v1.2.
- **Backlog existente que encaja**: #1 (GitHub auth per-usuario) → v1.3a; #2 (Studio al tope nav), #3 (respuestas conversacionales en gates → lo absorbe el Brain), #8 (containerizar) → despliegue; #13 (live-log en design runner) → útil para el stream del Brain.

## Orden de implementación recomendado
1. **v1.1c** (hardening #19/#15/#18/R3) — desbloquea iteración fiable.
2. **v1.1a/b** (Brain + iteración).
3. **v1.2** (roles + i18n).
4. **v1.3** (credenciales cifradas → canales → import).
5. **v1.4** (multi-engine + landing/pricing).

Cada fase se puede construir **por la propia fábrica** (dogfooding) como su propio backlog/sprints, o directo en código para lo más estructural (interfaces Go, migraciones).
