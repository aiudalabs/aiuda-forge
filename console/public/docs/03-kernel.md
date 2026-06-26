# El kernel de workflows

El **kernel** de aiuda-forge es un motor genérico de pasos: ejecuta cualquier
workflow definido en YAML sin contener ni una línea de código específica de la
metodología. No sabe qué es "implementar", "revisar" ni "abrir un PR"; sólo sabe
**reclamar una tarea, ejecutar su paso según el `type`, reportar el resultado y
avanzar el flujo**. Toda la "metodología" (el flujo factory, el flujo design)
vive como datos en `engine/registry/workflows/*.yaml`. Añadir un paso es editar
ese YAML — no recompilar.

Esta separación es deliberada y es el criterio de éxito de la v2:

> "The real factory flow — defined ENTIRELY in data. The kernel runs this with
> no flow-specific Go." — `engine/registry/workflows/factory.yaml:1`

---

## 1. El ejecutor genérico: despacho por `Type`, nunca por `id`

El corazón del kernel es `Engine` (`engine/internal/workflow/engine.go:24`). Su
contrato lo declara el propio comentario del tipo:

> "It dispatches step execution by Type via the runner registry. There is
> deliberately no `if step.id == ...`." — `engine.go:20-23`

El ciclo de un paso es **claim → run → advance** (`ExecuteOne`,
`engine.go:125`):

1. **Claim**: `e.Store.Claim(workerID)` reclama atómicamente una tarea lista
   (`engine.go:126`).
2. **Resolver el paso**: carga el workflow (`Loader.Load`) y busca el paso por
   id dentro del manifiesto (`wf.StepByID(task.StepID)`, `engine.go:137`). El id
   se usa **sólo** para localizar la definición del paso, jamás para decidir qué
   hacer.
3. **Despacho por tipo**: `runner, ok := e.runners[step.Type]`
   (`engine.go:141`). Si no hay runner para ese tipo, la tarea falla con
   `"no runner for step type"` (`engine.go:143`). Aquí está la única tabla de
   dispatch del kernel — un `map[string]Runner`.
4. **Run**: `runner.Run(stepCtx, step, inputs, workdir)` (`engine.go:174`).
5. **Advance**: según el resultado, encola el siguiente paso, aplica `on_fail`,
   aparca (human gate), reintenta (transient) o termina el run.

Cada `Runner` implementa una sola interfaz (`runner.go:47`):

```go
type Runner interface {
    Run(ctx, step, inputs, workdir) (StepResult, error)
}
```

El runner devuelve un `StepResult` (`runner.go:15`) cuyo `Output map[string]any`
se vuelve direccionable como `$<stepid>.output.<key>`, su `Detail` como
`$<stepid>.detail`, y su `Success` gobierna `on_fail`.

### Registro de tipos de paso

Los runners se cablean **en un único lugar**, `app.Build`
(`engine/internal/app/app.go:54`), por nombre de tipo:

| `type`            | Runner registrado                                  | Dónde (`app.go`) | Qué hace |
|-------------------|----------------------------------------------------|------------------|----------|
| `echo`            | `workflow.EchoRunner{}`                             | `app.go:66`      | Stub determinista de test: tiene éxito y devuelve sus inputs; puede escribir `write_file`. |
| `gate`            | `gate.NewHardenedRunner()`                          | `app.go:83`      | Ejecuta un comando en el sandbox; pasa/falla por exit code. |
| `agent`           | `agent.NewStepRunner(...)` (sandboxed)             | `app.go:123`     | Llama al agente LLM dentro del sandbox docker sobre un worktree de código. |
| `design`          | `agent.NewStepRunner(...)` (sin sandbox)          | `app.go:134`     | Mismo runner del agente pero `Sandboxed=false`: produce documentos, no código. |
| `agentic_verify`  | `agent.NewVerifyRunner(...)`                       | `app.go:141`     | Revisión adversarial cross-model; un veredicto "broken" falla el paso. |
| `human_gate`      | `agent.HumanGateRunner{}`                          | `app.go:143`     | Aparca el run en `AWAITING` hasta que un humano aprueba/rechaza. |
| `pr`              | `pr.NewRunner()`                                    | `app.go:144`     | Abre un Pull Request (real en GitHub si `PR_MODE=github`). |
| `ticket_publish`  | `&tickets.PublishRunner{...}` (opcional)          | `app.go:158`     | Publica el backlog en el ticket store nativo. |

Que esta tabla exista en un solo sitio es lo que hace al kernel *methodology-free*:
para soportar un paso `simplify` bastaría registrar un runner más aquí y usarlo
en el YAML; el ejecutor no cambia.

---

## 2. Tipos de StepResult: cuatro salidas de un paso

`StepResult` (`runner.go:15`) modela las cuatro maneras en que un paso termina, y
el ejecutor las ramifica al final de `ExecuteOne` (`engine.go:176-186`):

- **Éxito / fracaso lógico** → `reportAndAdvance` (`engine.go:247`). `Success`
  decide si la tarea pasa a `DONE` o `FAILED`.
- **Error de ejecución** (`runErr != nil`) → se reporta como fracaso con
  `"runner error: ..."` (`engine.go:178`). No es un fallo lógico del paso sino
  una falla de infraestructura del runner.
- **`Park: true`** → `parkTask` (`engine.go:235`): mueve la tarea a `AWAITING` y
  emite `run.awaiting_approval`. **No avanza**; `ApproveStep`/`RejectStep` la
  resuelven después. Lo usa `human_gate`.
- **`Retry: true`** → `requeueTransient` (`engine.go:198`): el paso topó un
  límite transitorio del proveedor (rate/session limit). **No falla el run**; se
  reencola con backoff. Ver §5.

`Park`, `Retry` y `Events` tienen tag `json:"-"`: son señales de control internas
que nunca se persisten en el resultado.

`Events []ResultEvent` (`runner.go:35`) son eventos de bus que el runner quiere
emitir (`step.gate`, `step.verify`). El ejecutor los reenvía **verbatim** sin
interpretarlos (`engine.go:260-262`) — otra costura que mantiene el kernel
agnóstico a la metodología.

### EchoRunner y GateRunner

`EchoRunner` (`runner.go:55`) es el stub determinista (sin LLM, rápido, gratis,
no-flaky) que usan los tests; opcionalmente escribe `write_file`/`write_content`
a disco para preparar un archivo para un gate downstream.

`GateRunner` (`runner.go:77`) ejecuta el comando declarado (`step.Command`
inline, o leído de `.vibeforge-gate` cuando `command_from: repo`) en `workdir`
vía `bash -c`, y reporta pass/fail por exit code. El comando vacío es un fallo
explícito (`runner.go:88`).

---

## 3. El single-emit-point y la máquina de estados

Todas las transiciones de tarea pasan por **un solo punto**: `Store.Transition`
(`engine/internal/store/claim.go:103`), descrito como *"the single emit point for
task transitions"*. Allí, y sólo allí, se:

1. valida el **fencing token** (`fence != curFence` → `ErrStaleFence`,
   `claim.go:121`),
2. valida la transición contra la tabla legal (`transitionAllowed`,
   `claim.go:124`),
3. actualiza la fila y
4. emite el evento `step.status_changed` en la misma transacción (`emitTx`,
   `claim.go:151`).

La máquina de estados es la **única** fuente de verdad y vive en datos
(`engine/internal/store/types.go:19`):

```
QUEUED    → RUNNING | CANCELLED
RUNNING   → DONE | FAILED | QUEUED | CANCELLED | AWAITING
AWAITING  → DONE | FAILED | CANCELLED        (approve / reject / cancel)
FAILED    → QUEUED                            (retry)
DONE      → (terminal)
CANCELLED → (terminal)
```

> "The kernel state machine is defined entirely by these states and the
> legalTransitions table below — there is no per-flow special casing." —
> `types.go:3-5`

`reportAndAdvance` (`engine.go:247`) es el wrapper de alto nivel: hace la
transición (single emit point), reenvía los `result.Events` y luego llama a
`advance`.

---

## 4. `advance`: avanzar, hacer loop con `on_fail`, o terminar

`advance` (`engine.go:268`) es *"the heart of the executor"* y es **genérico,
data-driven**:

### Camino feliz

Si `result.Success`, busca el paso siguiente con `wf.Next(task.StepID)`
(orden de declaración en el YAML). Si no hay siguiente, el run pasa a `DONE`
(`engine.go:278`). Si lo hay, lo encola resolviendo sus inputs contra el
contexto (`enqueueStep`, `engine.go:280`).

### Bucle `on_fail{goto, max, feedback}`

Si el paso falla y declara `on_fail.goto` (`engine.go:284`):

1. Cuenta los fallos del paso con `countFailures` (`engine.go:285`).
2. Si `failures <= step.OnFail.Max`, salta al `goto` target. **Nota la
   semántica off-by-one intencional**: `max: 2` permite 3 intentos (`engine.go:289`,
   confirmado en `engine.go:352-353`).
3. Si `OnFail.Feedback` está declarado, lo **resuelve** (p.ej. `$gate.detail`) y
   lo inyecta como `inputs["feedback"]` en el paso destino (`engine.go:295-306`).
   Así la dev recibe el log del gate rojo o el veredicto del reviewer.
4. Si se agota el cap (`failures > max`), el run pasa a `FAILED` (`engine.go:311`).

Sin `on_fail`, cualquier fallo termina el run en `FAILED` (`engine.go:315`).

Este único constructo cubre tanto el bucle **gate-rojo → dev** como
**reviewer → dev** como **rechazo humano → re-trabajo**. En `factory.yaml`:

```yaml
- id: gate
  type: gate
  command_from: repo
  on_fail:
    goto: implement        # gate rojo -> de vuelta a dev
    max: 2
    feedback: $gate.detail # el log de fallo se inyecta como feedback
```

Y en `design.yaml` un `human_gate` rechazado hace loop a la fase anterior con el
feedback del revisor (`design.yaml:18-24`).

### `countFailures` y la ventana de reintento (H3)

`countFailures` (`engine.go:354`) **no** cuenta todos los `FAILED` históricos:
los acota a la ventana del intento actual usando un *watermark*
(`Store.LastRetryAt`). Así, tras un `RetryRun` manual (que reencola sin limpiar
los `FAILED` previos), el paso obtiene un presupuesto `on_fail` fresco en vez de
heredar un contador ya agotado.

---

## 5. Modelo de concurrencia

### Worker pool

`StartBackground` (`app.go:249`) lanza un **pool** de N workers
(`WorkerLoop`, `engine_control.go:124`) más el reaper (`ReaperLoop`,
`engine_control.go:144`) y el bus. El pool da paralelismo entre runs/tareas
independientes:

> "the atomic claim (BEGIN IMMEDIATE + fence) guarantees no two workers ever
> claim the same task, so N workers drain the ready queue concurrently. Steps
> within one run stay serial." — `app.go:245-248`

Es decir: **paralelo entre runs, serial dentro de un run** (el paso N+1 sólo se
encola cuando N completa).

### Claim atómico (BEGIN IMMEDIATE)

`Store.Claim` (`claim.go:19`) toma el write-lock **antes** de cualquier lectura
vía la transacción `BEGIN IMMEDIATE` (DSN `_txlock=immediate`):

> "two concurrent claimers can never see the same QUEUED row as claimable —
> exactly the SKIP LOCKED semantics, on sqlite." — `claim.go:11-14`

"Listo" significa: `QUEUED`, con `available_at <= now` (gate de backoff), y con
**todas** sus dependencias (`depends_on`, por step_id) en `DONE`
(`depsSatisfiedTx`, `claim.go:79`). El orden es `wave ASC, created_at ASC` — la
barrera de wave cae gratis de ahí.

### Fencing tokens

Cada claim **rota el fence** (`newFence = t.Fence + 1`, `claim.go:55`). El fence
es un token monótono por tarea: cualquier operación posterior (`Transition`,
`Heartbeat`, `RequeueTransient`) debe presentar el fence con el que reclamó, o se
rechaza con `ErrStaleFence`. Esto neutraliza al **worker zombi**: si el reaper le
quitó la tarea (rotando el fence) y un segundo worker la reclamó, el reporte
tardío del zombi falla la comprobación de fence y no corrompe nada.

`OwnsClaim` (`claim.go:249`) es el guardia del **lado de lectura** para efectos
secundarios que el fence no puede deshacer (p.ej. el `SyncBack` de filesystem del
agente, B5): comprueba que el fence coincide **y** la tarea sigue `RUNNING` antes
de tocar el worktree. El ejecutor lo inyecta en el contexto del paso vía
`WithOwnershipCheck` (`engine.go:169`), fallando en cerrado ("lost") ante error.

### Heartbeat + reaper

Mientras el runner bloquea (un agente puede tardar minutos), una goroutine
`heartbeat` (`engine.go:213`) pinguea la liveness cada `HeartbeatInterval`
(default 15s, `engine.go:216`). El reaper (`RequeueStale`, `claim.go:301`)
reencola toda tarea `RUNNING` cuyo `heartbeat_at` sea más viejo que la ventana
stale (60s en producción, `app.go:253`), rotando el fence.

> Sin el heartbeat, el reaper reencolaría una tarea en vuelo y la re-ejecutaría —
> para un paso de agente eso es una llamada LLM duplicada (pagada). —
> `engine.go:149-152`

Las tareas `AWAITING` (human gate) están **excluidas** del reaper, así un gate
parado espera indefinidamente sin re-ejecutarse (`engine.go:233-234`).

### Transient-retry / `available_at`

Cuando el agente reconoce un error transitorio (rate/session limit del
proveedor), el runner devuelve `Retry: true` (`runner.go:142-143`). El ejecutor
llama `requeueTransient` (`engine.go:198`): calcula `available_at = now +
transientBackoff` (5 min, `engine.go:193`) y llama `RequeueTransient`
(`claim.go:268`), que mueve la tarea `RUNNING → QUEUED`, rota el fence y fija
`available_at`. El gate `available_at <= now` del claim (`claim.go:28`) impide
que se re-reclame antes de que pase el backoff. **El run sigue `RUNNING`** todo el
tiempo. Es la misma recuperación que da el reaper para un worker caído, pero
disparada por un error transitorio reconocido.

Si `RequeueTransient` falla (p.ej. el fence se movió), se cae al camino de fallo
normal para que la tarea no quede colgada (`engine.go:201-203`).

### Resolución de gates concurrentes (M3)

`ResolveAwaiting` (`claim.go:164`) hace el *find-then-act* del approve/reject bajo
**una sola** transacción con write-lock, de modo que dos resolvedores concurrentes
(o un approve compitiendo con un cancel) nunca doble-transicionan el mismo gate:
el perdedor obtiene `ok=false` → `ErrNoAwaitingStep` (`engine_control.go:95`).

---

## 6. Cómo se referencian las salidas de un paso en YAML

El `Context` (`resolve.go:12`) que ve cada paso al resolver sus inputs es:

```json
{
  "trigger": { ... },                                  // payload del trigger
  "<stepid>": { "output": {...}, "detail": "...", "success": true }
}
```

`buildContext` (`engine.go:320`) lo arma con el payload del trigger más el
**resultado de cada paso completado**, indexado por step id (gana el último
intento: *"Last writer for a step id wins"*, `engine.go:341`).

`resolveValue` (`resolve.go:19`) resuelve un valor: si es string que empieza por
`$`, lo trata como **path con puntos** dentro del contexto y navega el árbol; lo
demás pasa sin cambios. Una referencia que **no** resuelve produce `""` (string
vacío), no un error — para que un `feedback` opcional ausente no bloquee el flujo
(`resolve.go:16-18`, `resolve.go:30-34`).

Ejemplos reales:

| Referencia              | Resuelve a                                              |
|-------------------------|---------------------------------------------------------|
| `$trigger.ticket`       | el campo `ticket` del payload del trigger               |
| `$gate.detail`          | el `Detail` del paso `gate` (su log de fallo)           |
| `$draft_story.output.text` | el `text` dentro del `Output` del paso `draft_story` |

Como el runner del agente pone su respuesta en `Output["text"]`
(`agent/runner.go:148-149`), la forma **correcta** de leer el texto de un agente
es `$<step>.output.text` — exactamente lo que hace `factory.yaml`:

```yaml
- id: implement
  type: agent
  inputs:
    ticket: $draft_story.output.text   # el texto del agente está en .output.text
```

---

## Hallazgos de validación

Análisis adversarial del kernel. Cada hallazgo cita `file:line`.

- **BUG latente — `$step.text` es un no-op silencioso (`design.yaml` está roto en
  varias fases).** El contexto de resolución indexa cada paso como
  `{output:{...}, detail, success}` (`engine.go:248`, `engine.go:341`), y el
  runner del agente pone su texto en `output.text` (`agent/runner.go:148-149`).
  Por tanto la referencia correcta es `$<step>.output.text` (así lo hace
  `factory.yaml:22`). Pero `design.yaml` usa la forma **sin** `.output` en
  `prd.brief: $discovery.text` (`design.yaml:32`), `architecture.prd: $prd.text`
  (`design.yaml:47`), `ui.prd: $prd.text` y `ui.architecture: $architecture.text`
  (`design.yaml:64-65`), `mockups.screens: $ui.text` y `mockups.prd: $prd.text`
  (`design.yaml:81-82`), `backlog.prd`/`architecture` (`design.yaml:97-98`).
  Como `$discovery.text` navega `ctx["discovery"]["text"]` y ahí sólo hay
  `output`/`detail`/`success`, `resolveValue` devuelve `""` (`resolve.go:30-34`).
  **Cada fase de design recibe el brief/PRD previo vacío** y debe re-inferirlo del
  filesystem o de `$trigger.instructions`. Es un fallo silencioso de primer orden:
  no hay error, sólo contexto perdido. Lo mismo aplica a los `feedback:
  $<gate>.detail` de los human gates — `$discovery_gate.detail`
  (`design.yaml:24`) sí es válido (`detail` es campo de primer nivel), pero un
  hipotético `$discovery_gate.text` no lo sería.

- **El propio enunciado del `$step.text` no-op:** `resolveValue` (`resolve.go:19`)
  no distingue "referencia a una clave inexistente" de "valor vacío legítimo".
  Ambos colapsan a `""`. No hay modo estricto ni warning. Un typo en un path
  (`$trigge.ticket`, `$review.detial`) se traga silenciosamente y el paso corre
  con un input vacío. Frágil para un DSL que pretende que humanos editen YAML sin
  recompilar.

- **`asString` aplana estructuras de forma lossy.** Cuando un `feedback`/input
  referencia un objeto (no un escalar), `asString` lo renderiza con
  `fmt.Sprintf("%v", t)` (`resolve.go:50-57`), produciendo la sintaxis de mapa de
  Go (`map[k:v]`) dentro del prompt del agente. Si alguien escribiera
  `feedback: $gate.output` en vez de `$gate.detail`, el feedback inyectado sería
  basura no-JSON. No hay validación de que un feedback resuelva a string.

- **Race en `OwnsClaim`: TOCTOU entre la comprobación y el efecto.** `OwnsClaim`
  (`claim.go:249`) lee fence+status **fuera de transacción** (`s.db.QueryRow`
  directo, sin `BEGIN IMMEDIATE`). El agente lo consulta justo antes de su
  `SyncBack` (`engine.go:169`), pero entre el check y la escritura de filesystem
  el reaper podría requeue-ar la tarea (otro worker la reclama). La ventana es
  pequeña pero existe: el guardia reduce, no elimina, el riesgo de clobber del
  worktree (B5). El fence cubre la base de datos; el filesystem no es
  transaccional con ella.

- **El reaper puede duplicar trabajo con efectos ya cometidos.** El heartbeat
  evita la mayoría de re-ejecuciones, pero si un paso de agente cuelga >60s sin
  pinguear (p.ej. GC stop-the-world, o el ticker bloqueado), el reaper lo
  reencola (`claim.go:301`) y otro worker re-corre **toda** la llamada LLM. Para
  `agent`/`agentic_verify` eso es coste duplicado real. `OwnsClaim` protege el
  `SyncBack` pero **no** evita la segunda llamada LLM (ya pagada). El comentario
  en `engine.go:149-152` reconoce el riesgo sólo para el caso heartbeat-ausente.

- **`RetryRun` puede dejar un run colgado sin diagnóstico.** Si el último paso
  fallido ya no existe en el workflow (`wf.StepByID` → `!ok`, `engine_control.go:51`),
  `RetryRun` retorna `nil` **silenciosamente** sin reencolar nada y sin marcar el
  run. El run queda reabierto (`ReopenRun`) pero sin tarea lista: `RunToCompletion`
  lo detectaría como "no ready task but run not terminal" (`engine.go:396`), pero
  en producción el worker pool simplemente nunca progresa ese run. Mismo patrón
  silencioso si `LastFailedTask` es `nil` (`engine_control.go:43`).

- **`countFailures` cuenta `FAILED` de *cualquier* causa, no sólo fallos
  lógicos.** Un `FAILED` por `runner error` de infraestructura (`engine.go:178`) o
  un `failTask` por "no runner for step type" (`engine.go:143`) suma al contador
  de `on_fail.max` igual que un gate-rojo legítimo (`engine.go:365`). Un blip de
  infraestructura transitorio (no reconocido como `Retry`) consume presupuesto de
  reintento del bucle de calidad. El budget de "el dev no logró pasar el gate" se
  contamina con "docker falló al arrancar".

- **El kernel NO es 100% methodology-free — hay fugas.** (a) El `Step` struct
  tiene campos con semántica de dominio cableados: `Agent`, `Model`, `Prompt`
  (`workflow.go:31-33`), `Command`/`CommandFrom` (`workflow.go:36-37`). El
  ejecutor genérico no los usa, pero el esquema sí los conoce — un tipo de paso
  nuevo con su propio campo requiere editar el struct (forward-compat parcial: los
  inputs sí son `map[string]any`). (b) El comentario `engine.go:54-55` lista tipos
  por nombre ("Built-ins: echo, gate. Later waves register agent..."). (c) El
  `Type` enum aparece hardcodeado en comentarios de `types.go:59` y `claim.go`.
  Nada de esto rompe el dispatch genérico, pero el "no flow-specific Go" es un
  ideal con asteriscos.

- **`feedback` clobbea cualquier `feedback` real del paso destino.** Al hacer
  goto, el ejecutor fija `inputs["feedback"] = asString(fb)` **después** de
  resolver los inputs del target (`engine.go:300-301`). Si el paso destino ya
  declaraba un input llamado `feedback` en su YAML, queda sobrescrito sin aviso.
  La clave `feedback` es mágica y no documentada en el esquema.

- **Off-by-one de `max` documentado pero contra-intuitivo.** `max: 2` ⇒ 3
  intentos (`engine.go:289` con `<=`, confirmado en `engine.go:352-353`). Es
  intencional pero es una trampa para quien edita el YAML esperando que `max: 2`
  signifique "2 intentos". Combinado con `countFailures` por-ventana, el conteo
  real de reintentos depende de cuántos `RetryRun` manuales ocurrieron antes.

- **`buildContext` ignora silenciosamente resultados mal formados.** Si el
  `Result` de un task no es JSON parseable, el `continue` lo salta
  (`engine.go:338`) y el step queda **ausente** del contexto — sus referencias
  resuelven a `""`. Otro modo de pérdida silenciosa de datos sin traza.

- **`RunToCompletion` con budget fijo de 10000 pasos.** El driver de test/CLI
  aborta tras 10000 iteraciones (`engine.go:383`). Un workflow con un bucle
  `on_fail` mal configurado (o un `max` enorme) podría agotarlo y reportar un
  error genérico de "did not terminate" en vez del problema real. No afecta a
  producción (que usa `WorkerLoop`), pero enmascara loops en tests.

### Resumen (6 líneas)

1. El kernel es un ejecutor genérico claim→run→advance que despacha por `step.Type` vía un `map[string]Runner`, nunca por id (`engine.go:141`); toda la metodología vive en YAML.
2. `StepResult` tiene cuatro salidas — éxito/fallo, park (human gate), retry (transient) y events reenviados verbatim — y todas las transiciones pasan por un único emit-point fenced (`store.Transition`).
3. `on_fail{goto,max,feedback}` es el único primitivo de bucle: cubre gate→dev, reviewer→dev y rechazo-humano→re-trabajo, con feedback resuelto e inyectado en el paso destino.
4. La concurrencia es paralela-entre-runs / serial-en-run: claim atómico `BEGIN IMMEDIATE`, fencing tokens que neutralizan workers zombi, heartbeat+reaper, y transient-retry vía `available_at`.
5. Las salidas se referencian como `$<step>.output.<key>` / `$<step>.detail` / `$trigger.<k>`; una referencia que no resuelve da `""` silencioso, no error.
6. Validación: `design.yaml` está roto por usar `$step.text` en vez de `$step.output.text` (no-op silencioso), y el "methodology-free" tiene fugas en el `Step` struct y en `countFailures`.
