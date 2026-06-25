# Parte 13 — VibeForge v2 desde cero: arquitectura, lenguaje y prompts

> Apuesta paralela: una sesión de Claude Code escribe **v2 de cero** (kernel limpio) mientras
> seguimos endureciendo **v1** (el actual). Maximiza P(una funcione). Síntesis de todo lo
> aprendido: loop-engineer, Anthropic harness (skills/workflows/agents), BMAD/aiuda-stack, y la
> validación en vivo de v1.

## 0. Regla de oro de la apuesta paralela

> **La metodología (agents/skills/workflows en markdown) es UNA sola, COMPARTIDA por v1 y v2.**
> Lo único que v2 reescribe es el **kernel** (el runtime). Si la metodología se duplica, las dos
> versiones divergen y pierdes la apuesta. Vive en un repo/carpeta común (o submódulo) que ambas
> leen.

**v2 gana si** (criterio explícito, decidir ANTES de empezar): el kernel queda **< ~3k LOC**,
corre el mismo workflow end-to-end que v1, y **agregar/quitar un agente de un flujo no toca
código** — solo edita un manifiesto. Si a las N semanas v2 no llega ahí, se archiva y v1 sigue.

---

## 1. La tesis

> Un **kernel determinista pequeño** (el moat) + **todo lo demás como DATOS (manifiestos) y
> MARKDOWN (metodología)**, conducido por un **Brain con chat**, que aprende en una **memoria git
> que compone.** Cero metodología en el código.

El error de v1: la metodología está codificada en Python (`studio.py`, `qa.py`, `foundation.py`,
el ruteo `if task_type==…`). v2 invierte eso: el kernel no sabe qué es "studio" ni "dev" —
ejecuta **pasos** declarados en datos.

---

## 2. Arquitectura (4 capas)

```
┌───────────────────────────────────────────────────────────────────────┐
│ 4 · CHAT + BRAIN  (conductor)               ← el humano habla aquí       │
│    interpreta workflow+estado, dispatcha pasos, gestiona aprobaciones    │
├───────────────────────────────────────────────────────────────────────┤
│ 3 · REGISTRY  (DATOS — lo configurable, cero código)                    │
│    agents (manifest + persona.md) · skills (md + lock) · workflows (DAG) │
│    "Studio"/"Factory" son WORKFLOWS aquí; +/- agente = editar el grafo   │
├───────────────────────────────────────────────────────────────────────┤
│ 2 · KERNEL  (CÓDIGO — pequeño, determinista, MUY testeado) ← el moat     │
│    cola + claim atómico + state machine + fencing/heartbeat/reconcile ·  │
│    sandbox (gVisor/egress) · adaptador multi-CLI · gate-runner aislado · │
│    event bus + API + WS · EJECUTOR DE PASOS genérico                     │
├───────────────────────────────────────────────────────────────────────┤
│ 1 · MEMORIA  (markdown + git — compone)                                 │
│    kinds (signal/doc/experiment…) · domains=loops · ## Timeline ·        │
│    collectors deterministas escriben métricas; agentes escriben saber    │
└───────────────────────────────────────────────────────────────────────┘
```

### La regla de diseño que casi nadie acierta: dónde va código vs markdown
- **Código (kernel):** lo **determinista y crítico para seguridad/consistencia** — locking,
  state machine, sandbox, aislamiento del gate, fencing. *Nunca* en markdown.
- **Markdown/manifiestos:** lo que es **juicio o metodología** — cómo escribir un PRD, el orden de
  fases, la persona, las skills, los criterios. *Nunca* en código.
- **Forward-compatible:** el kernel expone **primitivas genéricas** para que la frontera
  código↔markdown **se deslice hacia markdown** con el tiempo (mejores modelos → más prompt, menos
  código) **sin re-arquitecturar**. Un paso puede ser hoy código y mañana un agente; el kernel no
  se entera.

---

## 3. Decisión de lenguaje — recomendación: **Go** (con honestidad sobre el costo)

El kernel es **I/O + orquestación de procesos + concurrencia**: spawnear/streamear N CLIs de
agente, heartbeats, poll loops, cancelación (kill de process-group), claim atómico, sandbox. Eso
es exactamente el sweet spot de Go.

| Criterio | **Go** (recomendado para el kernel) | Python (lo de v1) |
|---|---|---|
| Concurrencia (fan-out de N agentes + streams + heartbeats) | ✅ goroutines, nativo | 🟡 threads + GIL (v1 ya pelea con esto: hilos para stderr, timers) |
| Deploy | ✅ **binario estático único** (sin venv, sin wheels) | 🟡 v1 ni compila en py3.14 (pydantic-core) |
| Arranque / memoria / muchos workers | ✅ rápido, liviano | 🟡 pesado |
| SQL tipado sin ORM | ✅ sqlc | SQLAlchemy |
| Precedente probado | ✅ **Multica** (control-plane + daemon en Go por esto mismo) | — |
| **Mantenibilidad del equipo** | 🔴 curva (eres Python-first) | ✅ nativo |

**Mi llamada honesta:** para un kernel de cero, **Go es la elección técnicamente correcta** — y
este es el contexto IDEAL para probarlo: el kernel es chico (~2-3k LOC), bien acotado,
concurrency-heavy, los pedazos riesgosos (claim/sandbox) son los que más se benefician, y si Go
no cuaja **v1 en Python sigue** (es justo la red de la apuesta paralela). La metodología es
markdown → **el lenguaje solo afecta al kernel**, no a lo que el equipo edita a diario.

**El único contra real es la mantenibilidad.** Si en frío sientes que el equipo no va a poder
mantener Go, **Python es aceptable** — la arquitectura es idéntica, solo cambia el lenguaje del
kernel. Pero dado que es una apuesta paralela acotada, **yo iría con Go**. (Rust = overkill;
Node/TS = viable pero peor modelo de procesos que Go y tampoco es nativo del equipo.)

> Frontend (Brain UI/chat) más adelante: TS/Next, como Multica. No es parte del kernel.

---

## 4. Los contratos (DATOS) — el corazón configurable

**Agent** (`registry/agents/<id>.yaml` + `registry/agents/<id>.md` persona):
```yaml
id: dev
version: 1.0.0
model: claude-opus-4-8       # configurable por agente (reviewer usa otro)
skills: [coding-conventions@1.0.0]
tools: [read, edit, write, bash]   # allowlist
role: "Implementa un ticket dejando el árbol modificado, sin commit"
```

**Skill** (`registry/skills/<id>/SKILL.md` + files + lock) — formato de aiuda-stack/BMAD,
reusado tal cual.

**Workflow** (`registry/workflows/<id>.yaml`) — el grafo; aquí Studio y Factory son datos:
```yaml
id: factory
version: 1.0.0
steps:
  - id: implement
    agent: dev
    inputs: { ticket: $trigger.ticket }
  - id: gate
    type: gate                 # tipo de paso del kernel (corre el comando aislado)
    command_from: repo         # .vibeforge-gate
    on_fail: { goto: implement, max: 2, feedback: $gate.detail }
  - id: review
    agent: reviewer
    model: claude-sonnet-4-6   # CROSS-MODEL: distinto al que implementó
    prompt: adversarial
  - id: verify
    type: agentic_verify       # verificador fresco conduce la app + prueba (de loop-engineer)
    on_fail: { goto: implement, max: 2 }
  - id: pr
    type: pr
    approval: risk-policy       # bajo riesgo → automerge; alto → humano
```
Agregar BMAD/otro agente = un `agents/bmad.yaml` + un paso en el grafo. **Cero código.**

**Memoria** (de loop-engineer): `signals/ docs/ domains/` por kind, `domain` = campo, cuerpo +
`## Timeline` append-only, collectors deterministas para métricas. Es la capa de aprendizaje.

---

## 5. El kernel — qué expone (tipos de paso genéricos)

El ejecutor NO tiene `if task_type==studio`. Tiene un puñado de **tipos de paso**:
- `agent` — corre un CLI de agente (vía el adaptador) con persona+skills+tools del manifiesto.
- `gate` — corre un comando declarado en aislamiento + anti-tamper.
- `agentic_verify` — spawnea un verificador fresco que prueba que funciona (con evidencia).
- `pr` — push + abre/actualiza PR (idempotente por rama) + política de merge.
- `human_gate` — pausa y notifica (aprobación de fase / decisión de negocio).

Un workflow = lista de estos pasos + dónde va el output de cada uno + `on_fail` (goto+max =
el loop qa→dev/gate-fix generalizado). **Eso es todo.** Studio, dev, qa, remediate, foundation
dejan de ser código → son archivos `workflows/*.yaml` + `agents/*.{yaml,md}`.

**Del kernel se KEEPea (portado de v1, no reinventado):** la lógica de claim atómico
(`FOR UPDATE SKIP LOCKED`), la máquina de estados, fencing/heartbeat/reconcile, el sandbox F3
(egress/gVisor), el gate aislado + anti-tamper + suite-integrity, el adaptador `claude -p`
(streaming NDJSON), y el loop on_fail (qa→dev recién validado). Son el moat — se reimplementan en
Go, no se inventan de nuevo.

---

## 6. MVP (estrangulamiento incluso en greenfield — NO construir todo)

Orden para que v2 PRUEBE la tesis rápido y barato:

1. **Kernel mínimo**: cola + claim atómico + state machine + tests. (sqlite primero.)
2. **Ejecutor de pasos + intérprete de workflow** (tipos `agent` y `gate`).
3. **Adaptador `claude -p`** (NDJSON, timeout, cancel).
4. **1 workflow `dev` en YAML** (implement → gate → pr-local) corriendo E2E desde un trigger CLI.
   → *Aquí ya se prueba "flujos como datos + kernel chico".*
5. Sandbox + gate aislado + anti-tamper.
6. `on_fail` loop + paso `review` cross-model + `agentic_verify`.
7. Brain + chat + WS. 8. Memoria que compone.

No construyas el registry-UI ni el Brain hasta el paso 7 — primero que el kernel interprete YAML.

---

## 7. Prompts para la sesión de Claude Code (paste-ready, uno a la vez)

Estructura de cada prompt (la que ya usas en aiuda-stack): **Anchor · Estado actual · Tarea ·
Gates de validación · Gate de aprobación.** Pégalos en orden; no avances sin el verde.

### Prompt 0 — Bootstrap (constitución + harness + decisión de stack)
```
Vas a construir VibeForge v2: un KERNEL de ejecución autónoma de agentes, desde cero, en Go.
NO es una reescritura 1:1 — es un kernel pequeño y determinista; TODA la metodología
(agentes/skills/workflows) vive en DATOS (YAML) + MARKDOWN, nunca en código.

Lee primero, como contexto de diseño (no los copies, internalízalos):
  /Users/nmlemus/code/aiuda-projects/vibeforge/docs/comparativa/13_ARQUITECTURA_DE_CERO.md
  /Users/nmlemus/code/aiuda-projects/vibeforge/docs/comparativa/14_API_CONTRACT_v2.md  ← el
    contrato de API+eventos que el kernel DEBE cumplir (lo que la UI hará después). Regla de oro:
    toda operación pasa por la API; toda transición de estado emite un evento. Sin camino privilegiado.
  (y, si ayudan, 03 = arquitectura v1, 10/11 = qué ya está validado)

Tarea (solo scaffolding, sin lógica de negocio todavía):
1. Crea el repo Go: go.mod (module vibeforge-kernel, go 1.23+), estructura
   cmd/{control,worker}/  internal/{queue,workflow,agent,sandbox,gate,api,store}/  registry/
2. Escribe CLAUDE.md como CONSTITUCIÓN ~100 líneas: overview, árbol, GOLDEN RULES
   (regla #1: cero metodología en código — todo flujo es YAML; #2: el kernel es determinista y
   testeado; #3: nada de claim/sandbox/state en markdown; #4: commits pequeños, tests antes),
   y una tabla "dónde mirar".
3. Harness: Makefile (build/test/lint), .vibeforge-gate = `go test ./... && go vet ./...`,
   y un .github/workflows/ci.yml que espeje ese gate.
4. Un docs/ARCHITECTURE.md de 1 página con las 4 capas y la regla código↔markdown.

Gates: `go build ./...` y `go vet ./...` pasan; el repo arranca vacío pero compila.
Aprobación: muéstrame el árbol + CLAUDE.md + go.mod antes de seguir. NO escribas lógica aún.
```

### Prompt 1 — Kernel: cola + claim atómico + máquina de estados
```
Anchor: CLAUDE.md (golden rules). El kernel es el moat: determinista y MUY testeado.
Estado: repo scaffolded, vacío de lógica.
Tarea: implementa internal/store (sqlite primero, vía sqlc o database/sql) + internal/queue:
  - tabla tasks (id, workflow_id, step_id, status, payload JSON, attempts, fence, timestamps,
    depends_on, wave) y task_events.
  - máquina de estados QUEUED→RUNNING→DONE/FAILED/CANCELLED con transiciones legales validadas.
  - claim atómico: sqlite BEGIN IMMEDIATE (deja un hook para Postgres FOR UPDATE SKIP LOCKED).
  - fencing token rotado por claim; heartbeat; requeue_stale (zombi).
  Replica la SEMÁNTICA de v1 (docs/comparativa/03) — es lógica ya probada, no la reinventes.
Gates: tests de concurrencia (N goroutines reclamando → 0 doble-claim), tests de transición
  ilegal rechazada, test de fence viejo rechazado. `go test ./internal/...` verde.
Aprobación: muéstrame los tests de claim concurrente pasando antes de seguir.
```

### Prompt 2 — Ejecutor de pasos + intérprete de workflow (el corazón de la tesis)
```
Anchor: la regla #1 (flujos = datos). Estado: kernel de cola listo.
Tarea: internal/workflow:
  - parsea registry/workflows/<id>.yaml (steps: id, type[agent|gate|pr|human_gate|agentic_verify],
    agent, inputs con refs $step.output / $trigger.x, on_fail{goto,max,feedback}).
  - un EJECUTOR que, dado un workflow + un trigger, encola el primer paso y, al completarse cada
    paso, resuelve inputs y encola el siguiente (o aplica on_fail goto). SIN ningún `if id==...`:
    el ejecutor es genérico, los pasos son datos.
  - implementa SOLO el tipo de paso `gate` por ahora (corre un comando, captura salida) y un tipo
    `echo` de prueba (stub determinista, como el EchoEngine de v1) para testear el flujo sin LLM.
Gates: un registry/workflows/demo.yaml (echo → gate) corre E2E en un test con un repo temp;
  on_fail(goto,max) probado (gate rojo → reintenta → tope). `go test ./...` verde.
Aprobación: el workflow demo corre desde YAML sin código específico del flujo. Muéstramelo.
```

### Prompt 3 — Adaptador de agente (`claude -p`) + tipo de paso `agent`
```
Anchor: adaptador genérico estilo "Backend interface" (envuelve el CLI, no construye el loop).
Estado: ejecutor con pasos gate/echo.
Tarea: internal/agent:
  - una interfaz Backend{ Run(ctx, prompt, opts) (stream de eventos, result) }.
  - implementación claude.go: spawnea `claude -p --output-format stream-json --verbose
    --permission-mode acceptEdits --allowedTools <del manifest> --append-system-prompt <persona>`,
    parsea NDJSON (eventos tool_use/text/result), timeout, cancel (kill process-group), 3 modos
    de auth (subscription/api_key/oauth_token). Porta la semántica de v1
    (src/aiuda_factory/worker/engine/claude_code.py) — está validada en vivo.
  - el tipo de paso `agent`: carga el manifest del agente (yaml+persona.md), arma el prompt
    desde inputs, corre el Backend, registra el output para el siguiente paso.
Gates: test con un Backend fake (sin LLM). Un smoke test OPCIONAL real detrás de un flag
  (-tags=live) que corra `claude -p` en un repo temp (como scripts/try-claude-engine.sh de v1).
Aprobación: tests verdes; NO corras el smoke real sin que yo lo apruebe (consume cuota).
```

### Prompt 4 — Sandbox + gate aislado + anti-tamper
```
Anchor: el sandbox es frontera de seguridad DURA (no negociable). Estado: agente corriendo.
Tarea: internal/sandbox + internal/gate:
  - sandbox por tarea (docker exec; runtime gVisor si VIBEFORGE_SANDBOX_RUNTIME=runsc; red
    egress-proxy default-deny). El código del LLM corre ADENTRO; el commit/push AFUERA. Porta el
    diseño F3 de v1 (docs/audit + sandbox.py): worktree SIN .git, env allowlist, secretos fuera.
  - gate-runner: snapshot del comando+hash ANTES del agente; corre aislado; re-chequea hash
    DESPUÉS (anti-tamper) + suite-integrity (no menos tests).
Gates: tests de aislamiento (env del sandbox = allowlist, sin GH_TOKEN ni secretos); anti-tamper
  (modificar .vibeforge-gate → falla). `go test ./...` verde.
Aprobación: muéstrame el test de "ningún secreto del daemon cruza al sandbox".
```

### Prompt 5 — Workflow real `factory` + API/worker + corrida E2E
```
Anchor: ahora juntamos todo en un flujo real, definido SOLO en YAML.
Estado: kernel + agente + sandbox + gate.
Tarea:
  - cmd/control: API HTTP que cumpla el CONTRATO de docs/comparativa/14_API_CONTRACT_v2.md
    (§A operaciones: POST /runs, GET /runs, GET /runs/{id}, /cancel, DELETE, /retry,
    /control/pause|resume, /steps/{id}/approve, artifacts, /metrics, /healthz; §C internas:
    /runs/claim, /steps/{id}/report|heartbeat) + un EVENT BUS (WS /ws + GET /runs/{id}/events?after=)
    que emita los eventos de §B en CADA transición de estado (un solo lugar: la máquina de estados).
  - cmd/worker: poll/claim → ejecuta el paso → reporta.
  - registry/workflows/factory.yaml: implement(agent dev) → gate → review(agent reviewer,
    model distinto, adversarial) → pr(local). registry/agents/{dev,reviewer}.{yaml,md}.
  - registry/workflows/factory.yaml debe poder agregar/quitar un paso SIN tocar Go.
Gates (del contrato — esto GARANTIZA que la UI futura podrá hacer todo):
  - test de PRESENCIA: cada operación de §A existe (no 404/405).
  - test de EMISIÓN: cada transición de estado publica su evento de §B.
  - test SIN-CAMINO-PRIVILEGIADO: el E2E del board (cancel/delete/retry/approve) usa SOLO HTTP.
  - test E2E con Backend fake: trigger → claim → implement → gate → review → pr-local → DONE.
Aprobación: (1) los 3 tests de contrato verdes; (2) demuéstrame que agregar un paso al YAML
  (p.ej. un `simplify`) cambia el flujo SIN recompilar lógica nueva. Ese es el criterio de éxito v2.
```

### Prompt 6 — `on_fail` loop generalizado + `agentic_verify` + cross-model
```
Anchor: la verificación es agéntica ("¿funciona?") + cross-model, no solo "¿pasan tests?".
Estado: factory E2E con fake.
Tarea:
  - generaliza on_fail{goto,max,feedback} como el loop de fix (cubre gate-fix Y qa→dev de v1).
  - tipo de paso `agentic_verify`: spawnea un verificador FRESCO (modelo distinto) que prueba
    el cambio y devuelve works|broken + evidencia; broken → on_fail goto implement (cap N).
  - el paso `review` ya usa modelo distinto; añade `human_gate` (pausa+notifica) para alto riesgo.
Gates: tests del loop (broken→fix→works, tope respetado, human_gate pausa). `go test ./...` verde.
Aprobación: corrida E2E (fake) del factory completo con un fallo inyectado que el loop recupera.
```

> Después del Prompt 6: recién ahí Brain+chat (Prompt 7) y memoria que compone (Prompt 8) — pero
> con los 0-6 ya tienes la tesis probada: **kernel chico + flujos como datos + verificación
> layered, agregar agentes sin código.**

---

## 8. Cómo conviven las dos apuestas (operacional)
- **Metodología compartida**: una sola carpeta `registry/` (agents/skills/workflows) que v1
  aprende a leer y v2 lee nativo. Single source of truth.
- **v1 sigue su camino** (bloqueantes de staging + cross-model + agentic-verify sobre el loop ya
  hecho). Si v1 llega a stage antes, perfecto.
- **v2 corre la apuesta** con el criterio de éxito del §0. Re-evaluar en N semanas: el que cumpla
  el criterio gana; el otro se archiva o se funde.
- **No mezclar el kernel**: v2 en Go es un repo aparte; comparten solo `registry/` (markdown/yaml).

Serie: [`01`](01_DESARROLLO_AGIL_SCRUM_KANBAN.md)…[`12`](12_LOOP_ENGINEER_TEMPLATE.md) · `13` (este)
