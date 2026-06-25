# Parte 14 — Contrato de API + eventos del kernel v2 (la garantía de alineación)

> Responde: *"¿qué garantía tengo de que la UI podrá después hacer todo lo que hace hoy —
> start/stop/delete, eventos, etc.?"* La garantía = **este contrato**, derivado de las ~60 rutas
> reales de v1, convertido en **criterio de aceptación testeado del kernel** (no de la UI).
> Si el kernel pasa estos tests, la UI (capa 4) podrá renderizar todo — porque no hay camino
> privilegiado: UI, Brain y CLI son clientes iguales de esta API + del event bus.

## Principio (regla de oro del kernel v2)

> **Toda operación pasa por la API; toda transición de estado emite un evento.** No existe forma
> de hacer algo (trigger/stop/delete/merge/approve) que no sea una operación de la API. La UI no
> tiene backend propio.

## Lo que pasa al generalizar: v1 (~60 endpoints) → v2 (~12 genéricos)

En v1, Studio/dev/qa/ideation tienen endpoints propios (`/studio/...`, `/ideations/...`,
`/tasks/...`). En v2 **todo es un workflow run**, así que esos ~60 colapsan en una API genérica
de *runs/steps/events/artifacts/approvals*. Menos superficie, misma capacidad. La UI renderiza
runs genéricos en vez de pantallas hardcodeadas por flujo.

---

## A. Operaciones que la UI usa HOY (deben existir en la API v2)

Cada fila: operación de la UI de v1 → endpoint(s) v1 → operación genérica v2 que la cubre.

| Capacidad (lo que el humano hace) | v1 | v2 (genérico) |
|---|---|---|
| **Arrancar trabajo** (webhook, botón, handoff, enqueue) | `POST /webhooks/*`, `POST /tasks`, `POST /studio/.../handoff`, `POST /ideations/.../enqueue` | `POST /runs` {workflow, payload} (+ webhooks → `POST /runs`) |
| **Listar el board** | `GET /tasks`, `GET /control/status` | `GET /runs` (+ `?status=`), `GET /runs/{id}` |
| **Ver detalle / pasos** | `GET /tasks/{id}/...`, `GET /studio/projects/{id}` | `GET /runs/{id}` (incluye steps) |
| **Detener** (Detener/cancel) | `POST /tasks/{id}/cancel`, `/stop` | `POST /runs/{id}/cancel` |
| **Borrar** | `DELETE /tasks/{id}` | `DELETE /runs/{id}` |
| **Reintentar / reconstruir** | `POST /tasks/{id}/rebuild`, `/resolve` | `POST /runs/{id}/retry` (o `steps/{id}/retry`) |
| **Pausar / reanudar la fábrica** | `POST /control/pause`, `/resume`, `/wave-barrier` | `POST /control/pause`, `/resume` |
| **Aprobar (gate humano)** | `POST /studio/.../phases/{phase}/approve`, `/ideations/.../approve` | `POST /runs/{id}/steps/{step}/approve` (human_gate) |
| **Merge / promover** | `POST /tasks/{id}/merge` | `POST /runs/{id}/steps/{step}/merge` (o un step `pr` con política) |
| **Remediar** | `POST /control/remediate`, `/tasks/{id}/resolve` | `POST /runs` {workflow: remediation} |
| **Editar config por proyecto/flujo** | `GET/PUT /studio/projects/{id}/settings`, `/pipeline` | `GET/PUT /registry/workflows/{id}`, `/registry/agents/{id}` |
| **Componer/editar agentes y skills** | (hoy: editar archivos) | `GET/PUT /registry/{agents,skills,workflows}/{id}` (CRUD del registry) |
| **Ver artefactos** (PRD, specs, diffs) | `GET /tasks/{id}/artifacts/{kind}`, `/studio/conversations/.../artifacts` | `GET /runs/{id}/artifacts/{kind}` (+ revisions) |
| **Analítica / costo** | `GET /analytics`, `/metrics` | `GET /metrics`, `/analytics` |
| **Salud** | `GET /healthz` | `GET /healthz`, `/readyz` |

## B. Eventos que la UI consume HOY (el kernel DEBE emitirlos)

v1 tiene un live-log por tarea (`GET /tasks/{id}/events`, el daemon escribe con
`POST /tasks/{id}/events` / `add_event`). En v2 esto es **un solo stream del event bus**:

| Evento (mínimo) | Cuándo lo emite el kernel |
|---|---|
| `run.created` | al crear un run |
| `run.status_changed` / `step.status_changed` | **toda** transición de la máquina de estados (QUEUED→RUNNING→…) |
| `step.event` (tool_use / text / log) | streaming del agente (lo que hoy es el live-log) |
| `step.gate` (passed/failed + detail) | resultado del gate |
| `step.verify` (works/broken + evidence) | verificación agéntica |
| `run.awaiting_approval` | un human_gate pausó esperando al humano |
| `run.done` / `run.failed` / `run.cancelled` | cierre |

**Transporte:** WebSocket (`/ws`) para push en vivo + `GET /runs/{id}/events?after=` para
polling/replay (compat con el patrón de v1). Un único bus; la UI, el Brain y las métricas se
suscriben igual.

## C. Operaciones daemon-internas (NO UI, pero parte del contrato del kernel)

`POST /runs/claim` · `POST /steps/{id}/report` (status) · `POST /steps/{id}/heartbeat` ·
`POST /steps/{id}/usage`. Son worker↔kernel; la UI no las llama, pero el kernel las expone.

---

## D. Cómo se TESTEA la garantía (en la fase del KERNEL, no de la UI)

Esto es lo que convierte el contrato en garantía:

1. **Test de presencia de contrato:** un test que afirma que **cada** operación de §A existe en
   la API (status != 404/405) con su forma de request/response. (Estilo OpenAPI o tabla de rutas.)
2. **Test de emisión de eventos:** por cada transición de la máquina de estados, un test que
   afirma que se publicó el evento de §B en el bus. *"Toda transición emite"* deja de ser promesa.
3. **Test "sin camino privilegiado":** las operaciones del board (cancel/delete/retry/approve/
   merge) se ejercitan **solo vía la API** en los tests E2E — no llamando funciones internas. Si
   un test E2E del board pasa usando solo HTTP, la UI podrá hacer lo mismo por definición.

> Estos tests viven en el **Prompt 5** (API + worker), que es ANTES del Prompt 7 (UI). Cuando la
> UI se construya, se enchufa a una API ya probada contra este contrato, con eventos ya fluyendo.

## E. Honestidad: dónde la garantía se rompería (y cómo lo evitamos)
- **Si una operación se hace fuera de la API** (un handler que muta estado sin endpoint) → la UI
  no la alcanza. *Mitigación:* regla de oro + test "sin camino privilegiado" (§D.3).
- **Si los eventos se emiten ad-hoc** (no en la transición de estado) → el live-log se desincroniza.
  *Mitigación:* emitir SIEMPRE desde la máquina de estados del kernel, un solo lugar (§B + §D.2).
- **Si el contrato no se escribe hasta tener la UI** → se descubren huecos tarde. *Mitigación:*
  este documento ES el contrato, y entra como gate del Prompt 5 (ya, no después).

---

## Resumen
La garantía = **API-first + este contrato derivado de v1 + tests de contrato/eventos en la fase
del kernel.** La UI no aporta capacidades nuevas; solo *renderiza* operaciones y eventos que el
kernel ya expone y testea. Por eso diferir la UI es seguro: lo que la UI necesita (start/stop/
delete/events/approve/merge) se construye y se prueba en el kernel (Prompts 1 y 5), no en la UI.

Serie: [`13`](13_ARQUITECTURA_DE_CERO.md) · `14` (este, el contrato que lo garantiza)
