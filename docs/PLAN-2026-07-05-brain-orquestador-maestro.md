# Plan — Brain como orquestador maestro de Forja

**Fecha:** 2026-07-05 · **Objetivo:** que el Brain (el asistente conversacional per-proyecto) haga **cualquier cosa que hoy se hace a mano operando Forja** — crear/editar flujos y personas, lanzar/parar/reintentar runs, aprobar gates, diagnosticar fallos, componer soluciones — con aprobación humana para lo mutante/hacia-afuera. Meta: el Brain se comporta como un operador experto de Forja en lenguaje natural.

**Referencia viva:** una sesión de asistente operando Forja end-to-end (crear `mockups-only.yaml` al vuelo, cambiar el modelo del `designer` a Opus, lanzar runs por la API, aprobar gates, diagnosticar el stall del watchdog, servir mockups por túnel) ES la especificación del toolbox que el Brain necesita. Enumerar esas acciones = el catálogo de tools objetivo.

---

## 1. Principios

1. **Sin ruta privilegiada.** Todo pasa por la misma superficie (ops/API) — el kernel ya lo manda: *"UI/Brain/CLI son clientes iguales"*. Darle tools al Brain = darle el mismo acceso que tiene un operador, no un poder especial.
2. **Método = datos.** Los tools de "autoría" editan YAML/markdown del registry (workflows/agents/skills), nunca código del kernel.
3. **Reversible corre solo; Mutating/hacia-afuera se propone y espera OK.** El candado de responsabilidad — ya existe en el diseño (`ToolKind`).
4. **Modelo = Opus** para el razonamiento de "master".

## 2. Estado actual (grounded en el código)

`internal/brain/`:
- **Loop agéntico con tools** (`brain.go`: `llm.Stream(system, msgs, toolList, onText)` en un bucle acotado `maxToolRounds`).
- **13 tools** (`tools.go`): `launch_run, cancel_run, retry_run, requeue_run, pause, resume, approve_step, reject_step, get_state, get_run, list_runs, get_status, get_metrics`.
- **Gating Reversible/Mutating** (`tools.go:8-14`): reversibles corren automático; mutantes se **proponen** y corren **solo con aprobación** (endpoints `assistant/actions/{id}/approve|reject`). *La lección del "merge sin OK" ya está diseñada adentro.*
- **RBAC** por tool (`MinRole`).
- **Extensión limpia:** `type toolDef struct { Tool; Kind; MinRole; Run func(ops ControlOps, projectID, input) (string,error) }` en un `registry map`. La capacidad real vive en la interfaz **`ControlOps`** (`ops.go`), implementada por `EngineOps` (in-process, "mirrors what the HTTP handlers do").

**Qué cubre hoy:** control de runs + inspección de estado. **Qué NO:** editar el método (registry), leer docs/código/artefactos, y la cola larga (exec). Eso es el hueco a "ser el master".

Mapa de tus ejemplos:
- *"corre tal cosa"* → `launch_run` ✅ · *"detén el sprint"* → `pause`/`cancel_run` ✅
- *"crea un flujo nuevo"*, *"edita la persona"*, *"cambia el modelo"*, *"elimina X"* → ❌ **faltan (Fase 1)**
- *"cualquier cosa que le pida"* → ❌ **falta la escotilla (Fase 3)**

## 3. Fases

### Fase 1 — Autoría del método (registry tools) · LA PALANCA GRANDE · ✅ HECHA (commit db6b73a)
Agregar a `ControlOps`/`EngineOps` + `tools.go`:
- `read_registry(kind, id)` · Reversible — leer un workflow/agent/skill.
- `list_registry(kind)` · Reversible.
- `write_registry(kind, id, content)` · **Mutating** — crear/editar. Valida con el MISMO parser del kernel (el endpoint `PUT /registry/{kind}/{id}` ya hace esa validación — reusar).
- `delete_registry(kind, id)` · **Mutating**.

Con esto el Brain hace lo que hoy se hace a mano: *"crea un flujo de solo-mockups y córrelo"* (write_registry workflow + launch_run), *"cambia el modelo del designer a Opus"*, *"edita el scrum-master para que derive los lanes de la arquitectura"*. **Deja de ser operador y pasa a ser autor del método.**
- **Entregable:** el Brain autorea y corre un flujo nuevo end-to-end, con tu aprobación en cada `write`.
- **Subsume** la feature que faltaba ("re-correr solo la fase X"): el Brain compone un workflow de 1 paso y lo lanza — no necesita botón dedicado.

### Fase 2 — Inspección (diagnóstico como operador experto) · ✅ HECHA (read_artifact + get_run_events)
- `read_artifact(runID, stepId)` · Reversible — leer el doc/artefacto que produjo un paso.
- `read_file(path)` / `search_code(query)` · Reversible, **scoped** al repo del proyecto + al registry (no fuera).
- `get_run_events(runID)` · Reversible — el live-log / eventos de los steps.

Con esto: *"¿por qué falló UI?"*, *"muéstrame el PRD"*, *"revisa el backlog contra la arquitectura"* — diagnóstico real, no adivinar.

### Fase 3 — Escotilla de escape (la cola larga) · GATED FUERTE · ✅ HECHA (exec: Mutating + owner + opt-in VIBEFORGE_BRAIN_EXEC)
El "cualquier cosa" total. Dos caminos:
- **(a) Tools específicos hacia-afuera** (preferido): `github_create_issue`, `github_merge_pr`, `git_*`, `dispatch_work` — cada uno **Mutating**, aprobación por-acción. Menos poder, más seguro y auditable.
- **(b) `exec(cmd)`** · **Mutating**, `MinRole: owner`, doble confirmación, sandbox/allowlist. Es lo que da máxima flexibilidad (como un shell), y lo más peligroso.
- **Decisión:** empezar por (a); dejar (b) como último recurso, muy gated. No abrir un shell libre sin allowlist.

### Fase 4 — Cerebro de master (prompt + modelo + memoria)
- **System prompt ampliado** (`brain.go: systemPrompt`): hoy dice "review/control/diagnose/propose"; sumar el mandato de **autoría del método + composición + diagnóstico**, e **inyectar el mapa de la arquitectura** (constitución del kernel `engine/CLAUDE.md`, contrato de API, estructura del registry) para que razone con el contexto que un operador experto tiene.
- **Modelo → Opus** (el razonamiento de master lo pide; los mockups probaron que el tier importa).
- **Memoria por-proyecto** (ya hay `store.go` con historial): que recuerde decisiones/estado entre turnos.
- **Loop más largo** (`maxToolRounds`) para tareas multi-paso.

### Fase 5 — Seguridad y responsabilidad (transversal) · ✅ HECHA (evento de auditoría por tool; RBAC ya enforced; exec opt-in)

> **Estado global (2026-07-05):** Fases 1, 2, 3, 5 ✅. Fase 4 parcial: system prompt ampliado ✅;
> faltan modelo→Opus (env `BRAIN_MODEL`), memoria por-proyecto, y la **adaptación a suscripción**
> (el bloqueo para chatear en vivo sin saldo API). El resto del contenido de la Fase 5 abajo ya
> estaba en el diseño original (Reversible/Mutating, aprobación por-acción, RBAC).
- Clasificar bien lo nuevo: **todo write/exec/hacia-afuera = Mutating**.
- **Hacia-afuera** (merge PR, dispatch, deploy): **aprobación explícita por-acción**, aunque exista un "sí" general antes (la lección del merge).
- RBAC `MinRole` por tool (ya existe) + **auditoría** de acciones del Brain (qué tool, qué input, aprobado por quién).

## 4. Orden recomendado

1. **Fase 1 (registry)** — el mayor salto de capacidad, bajo riesgo (edita datos validados, gated).
2. **Fase 4 (prompt + Opus)** — barato, multiplica la calidad del razonamiento.
3. **Fase 2 (inspección)** — diagnóstico.
4. **Fase 5 (seguridad)** — en paralelo, endurecer el gating de lo nuevo.
5. **Fase 3 (escotilla)** — al final; preferir tools específicos hacia-afuera antes que un `exec` libre.

## 5. Norte

El Brain como **"Claude Code especializado en Forja"** (Agent SDK: loop + tools + memoria + aprobación). Ya está el ~60-70%: el loop, los tools base, el gating Reversible/Mutating, el RBAC. Con **Fases 1 + 2 + 4** el Brain ya hace lo que hoy se ve hacer a mano: componer flujos, correrlos, diagnosticar, editar el método. La **Fase 3** es el 100% de "cualquier cosa que le pida".

## 6. Decisiones abiertas

- **D-1** Escotilla: ¿tools específicos hacia-afuera (a) o `exec` gated (b) o ambos por etapas? Propuesta: (a) primero.
- **D-2** Scope de `read_file`/`search_code`: ¿solo repo del proyecto + registry, o también el código de Forja? Propuesta: repo del proyecto + registry; el código de Forja solo lectura si se pide explícito.
- **D-3** ¿El Brain puede editar el registry GLOBAL (afecta a todos los proyectos) o solo un overlay por-proyecto? Propuesta: por-proyecto primero; global detrás de rol admin + aprobación.
