# Las fases: Diseño y Fábrica

aiuda-forge construye software en dos movimientos. Primero el **Diseño**: una
cadena de fases que convierte una idea cruda en un paquete de especificación
buildable (brief → PRD → arquitectura → UI → mockups → backlog), con un humano
aprobando cada paso. Después la **Fábrica**: un pipeline autónomo que toma cada
historia del backlog y la lleva hasta un Pull Request (detallar → implementar →
gate → revisión → PR), con verificación cross-model y un gate sellado.

Ambos flujos están definidos **enteramente en datos** —archivos YAML en
`engine/registry/workflows/`— y los corre el mismo kernel genérico. Para añadir
o quitar un paso editas el YAML; no se recompila Go. Esa editabilidad es el
criterio de éxito de la v2 (`engine/registry/workflows/factory.yaml:1-3`).

---

## Parte 1 — La fase de Diseño

El flujo de diseño vive en `engine/registry/workflows/design.yaml`. Cada fase es
un paso `type: design` (un agente que produce un documento) seguido de un paso
`type: human_gate` que **estaciona la ejecución** hasta que un humano aprueba. Si
el humano rechaza, el gate hace `goto` de vuelta a la fase, inyectando el
feedback del revisor como `feedback` en el prompt del agente
(`design.yaml:18-24`).

Orden de fases: `discovery → PRD → architecture → UI → mockups → backlog →
handoff → docs_pr` (`design.yaml:6`).

Los agentes de diseño corren **en el host, sin sandbox** (`Sandboxed=false`),
porque producen documentos, no código, y no necesitan un worktree en contenedor
(`engine/internal/app/app.go:125-134`).

### Fase 1 — Discovery (agente `analyst`)

El `analyst` toma una idea o problema crudo (`$trigger.instructions`) y produce
`docs/BRIEF.md`: problema, visión, usuarios (con nombre y rol, nunca "users"),
casos de uso, *out of scope*, restricciones, métricas de éxito y preguntas
abiertas (`engine/registry/agents/analyst.md:33-62`). Es decisivo sobre el
alcance —"it depends" no es una respuesta de discovery
(`analyst.md:24-26`)— y nombra personas, no "usuarios".

**Gate:** revisar `docs/BRIEF.md`; aprobar pasa a PRD, rechazar vuelve a
`discovery` con feedback (`design.yaml:18-24`).

### Fase 2 — PRD (agente `pm`)

El `pm` convierte el brief en un PRD preciso y buildable: cada caso de uso del
brief mapea a ≥1 requisito funcional, **un requisito = un ID**, todo testeable
(cada FR con un criterio de aceptación falsable, cada NFR con un umbral concreto
como `p99 < 200 ms`), priorizado P0/P1/P2 (`engine/registry/agents/pm.md:20-32`).
Sale `docs/PRD.md`.

**Gate:** revisar el PRD; aprobar pasa a arquitectura (`design.yaml:35-41`).

### Fase 3 — Architecture (agente `architect`)

El `architect` traduce el PRD en una arquitectura concreta y opinada:
stack tecnológico, estructura de módulos, modelo de datos, ADRs, mecanismo por
cada NFR, grafo de dependencias sin ciclos (`engine/registry/agents/architect.md:36-63`).

**Crucial:** además del documento, el architect **escribe el gate del proyecto**,
`.vibeforge-gate` en la raíz del repo (`architect.md:65-110`). Es la forma
ejecutable de su estrategia de test: la fábrica lo corre **sin red**, en un
contenedor **distinto**, **offline**. Por eso las dependencias deben quedar
**vendorizadas dentro del árbol** (un `.venv` local para Python, `node_modules`
para Node), e invocarse **por ruta** (`.venv/bin/python -m pytest`,
`node_modules/.bin/vitest run`) — nunca `npm`/`pytest` globales, que no existen en
el contenedor del gate (`architect.md:73-110`). El gate es el *test runner*
(comportamiento), no un linter/typechecker.

**Gate:** revisar la arquitectura; aprobar pasa a UI (`design.yaml:51-57`).

### Fase 4 — UI / Screens (agente `ux`)

El `ux` produce `docs/UI_SCREENS.md`: una especificación pantalla por pantalla
—nombre/ruta, propósito (referenciando FR-XX), layout, componentes, estados
(loading/empty/error/populated), navegación y datos— más los flujos numerados
por caso de uso. Describe el comportamiento, no el mecanismo: nada de clases CSS,
componentes React ni endpoints (`engine/registry/agents/ux.md:12-29`).

**Gate:** revisar la spec de UI; aprobar pasa a mockups (`design.yaml:68-74`).

### Fase 5 — Mockups (agente `designer`)

El `designer` convierte `UI_SCREENS.md` + el PRD en **un único archivo HTML
autocontenido** (`docs/mockups/index.html`) que un stakeholder abre en cualquier
navegador, offline, sin servidor, para recorrer el flujo principal antes de que
exista código (`engine/registry/agents/designer.md:11-19`). Sin dependencias
externas: todo el CSS y JS inline, sin CDN, sin web fonts, datos realistas (nada
de "Lorem ipsum") (`designer.md:46-51`). El mockup es la herramienta de
extracción de requisitos más barata del proceso: provoca feedback concreto que
ningún documento provoca (`designer.md:21-26`).

**Gate:** revisar el mockup HTML; aprobar o pedir cambios (`design.yaml:85-91`).

### Fase 6 — Backlog (agente `scrum-master`) — el nivel 1 de BMAD

El `scrum-master` (rol de Scrum Master + Product Owner de BMAD) convierte
PRD + arquitectura en un backlog **esqueleto**, ordenado por olas (waves) y
correcto en dependencias, en `docs/backlog.yaml`. Cada historia lleva título,
una user-story corta (`As a <role>, I want <capability>, so that <value>.`),
2–5 criterios de aceptación falsables, `deps`, `owner` (la lane) y `sprint_id`
—pero **NO** la especificación de implementación completa
(`engine/registry/agents/scrum-master.md:20-25`).

Este es el **nivel 1 de los dos niveles de backlog de BMAD**: deliberadamente
ligero. Una sola llamada de agente que emitiera el cuerpo dev-ready completo de
cada historia agotaría el timeout del agente en un proyecto grande
(`scrum-master.md:10-18, 76-80`). El detalle pesado se difiere al
`story-detailer`, que expande **una historia a la vez** en tiempo de build.

El `owner` de cada historia es el **router de lane** — el id del agente
especialista que implementará la historia: `react-dev` (frontend/admin),
`python-dev` (backend/API), `flutter-dev` (mobile), `firebase-dev` (cloud
functions/reglas) o `dev` (genérico). Una historia vive en **una sola lane**; si
necesitara dos, se parte (`scrum-master.md:61-72`). Los sprints son incrementos
coherentes y demoables —**un sprint = un PR**— con dependencias cross-sprint
**solo hacia atrás** (SP2 puede depender de SP1, nunca al revés), para que un
sprint no se deadlockee (`scrum-master.md:48-60`).

**Gate:** revisar el backlog; aprobar lo **publica** al ticket store
(`design.yaml:102-108`).

### Fase 7 — Handoff: publicar el backlog (`type: ticket_publish`)

`handoff` no es un agente: es un paso `ticket_publish` que lee `docs/backlog.yaml`
y lo inserta en el ticket store nativo, **scoping cada historia/sprint al
`project_id`** del trigger (auditoría A1) (`design.yaml:111-116`). El runner es
`tickets.PublishRunner` (`engine/internal/app/app.go:158`). Tras este paso, las
historias existen como tickets que el orquestador puede reclamar y disparar en la
fábrica.

### Fase 8 — docs_pr (`type: pr`)

El último paso del diseño commitea `docs/` a una rama y abre un PR contra `dev`
(GitHub Flow). En `PR_MODE=github` es un PR de GitHub real
(`design.yaml:121-126`).

---

## Parte 2 — La fase de Fábrica

El flujo de fábrica vive en `engine/registry/workflows/factory.yaml`. El
orquestador reclama una historia (o, en modo sprint, un sprint) y dispara un run
de la fábrica por cada una. Pasos: `draft_story → implement → gate → review → pr`.

A diferencia del diseño, los agentes de la fábrica corren **dentro de un sandbox
docker por tarea**, sobre una copia **sin `.git`** del worktree, detrás de un
**allowlist de egress** (`Sandboxed=true`, `app.go:104-123`). El agente edita la
copia; sus cambios se sincronizan de vuelta (`SyncBack`) para que el gate los vea,
pero el agente nunca toca `.git` ni el remoto — git (commit/push) pasa **fuera**
del sandbox, lo hace el kernel (`engine/internal/agent/runner.go:87-105`;
`engine/internal/sandbox/sandbox.go:1-10`).

### Paso 1 — draft_story (agente `story-detailer`) — el nivel 2 de BMAD

El `story-detailer` es el **nivel 2 de BMAD** (`create-story`): corre
**just-in-time, sobre UNA historia**, justo antes de implementarla. Recibe el
esqueleto ligero (`$trigger.ticket`), lee `docs/PRD.md` + `docs/ARCHITECTURE.md`
(y `docs/UI_SCREENS.md` para historias de frontend) del worktree clonado de `dev`,
y expande esa única historia en la spec dev-ready completa: contexto (citando los
FR/§ de arquitectura), qué construir (archivos, módulos, funciones, tipos
concretos) y criterios de aceptación expandidos a checks testeables
(`engine/registry/agents/story-detailer.md:1-42`). Una historia por llamada es lo
que mantiene este paso rápido y permite escalar a proyectos grandes sin una
generación gigante que agote el timeout (`story-detailer.md:6-11`). Su respuesta
final **ES el ticket** que recibe el implementador (`story-detailer.md:50-55`).

Si falta `docs/PRD.md` o `docs/ARCHITECTURE.md` (p.ej. los docs aún no se
mergearon a `dev`), no falla: trabaja desde el esqueleto + convenciones del repo y
lo declara explícitamente (`story-detailer.md:43-48`).

### Paso 2 — implement (router de lane → especialista)

El paso declara `agent: dev` como fallback estático, pero el **router de lane** lo
sobrescribe: `inputs.agent = $trigger.agent` (el `owner` de la historia). Si está
presente, ese especialista (`python-dev`, `react-dev`, …) implementa la historia;
si está vacío, cae a `dev` (`factory.yaml:18-24`;
`runner.go:46-59`). El ticket que recibe es `$draft_story.output.text` — la spec
completa que produjo `draft_story`.

El `dev` hace **el cambio mínimo** que satisface los criterios y pasa el gate
—sin hardening ni abstracciones que el ticket no pidió—, escribe los tests que el
gate corre, y deja el árbol modificado sin commitear (lo hace el paso `pr`)
(`engine/registry/agents/dev.md:15-43`).

**Regla dura para todos los implementadores: NUNCA editar `.vibeforge-gate`.**
Está hasheado y sellado antes de que el agente corra, así que *cualquier* cambio
—incluso "arreglarlo" o "mejorarlo"— hace fallar el gate como **tamper**. Si el
comando del gate parece mal, eso es un bug de diseño para reportar, no para
arreglar editando (`dev.md:28-36`).

### Paso 3 — gate (`type: gate`, sellado y offline)

El gate corre el `.vibeforge-gate` del repo (`command_from: repo`) dentro del
sandbox. Es **el piso determinista** de la verificación, y tiene tres propiedades:

1. **Offline y aislado.** Corre con `--network none`, env allowlist (los secretos
   del daemon nunca cruzan), en un contenedor fresco distinto al del build. Solo
   los archivos **dentro del árbol** sobreviven del build al gate — por eso las
   deps deben estar vendorizadas (`engine/internal/sandbox/sandbox.go:156-200`;
   `engine/internal/gate/runner.go:25-27`).

2. **Anti-tamper (GateHash).** Antes de que el agente corra, el kernel sella el
   hash SHA-256 de `.vibeforge-gate` en una ruta **del lado del daemon**, fuera
   del alcance del agente (`engine/internal/gate/gate.go:90-98`). Antes de confiar
   en el gate, recomputa el hash; si difiere → `ErrTampered`, el gate falla aunque
   el comando hubiera salido 0 (`gate.go:114-123, 137-149`).

3. **Suite-integrity (test-marker count).** Cuenta marcadores de test
   (`func Test…`, `def test_…`, `it(`, `test(`) antes y después; si **baja**, el
   gate falla (`ErrSuiteShrank`) — ataca el reward-hack de borrar el test que
   falla para ir a verde (`gate.go:39, 68-88, 119-121`).

Por qué está sellado: el agente corre código no confiable y tiene incentivo a
hacer *reward-hacking* (debilitar el gate, borrar tests). El candado vive
**fuera** de su árbol y es **kernel code, nunca markdown** —un check
determinista y security-critical (`gate.go:1-11`).

**`on_fail`:** si el gate sale rojo, `goto: implement` con `$gate.detail` como
feedback, hasta `max: 2` (`factory.yaml:25-31`). Por el off-by-one intencional
(`failures <= max`), `max: 2` = 3 intentos (`engine/internal/workflow/engine.go:289, 353`).

### Paso 4 — review (`type: agentic_verify`, cross-model, adversarial)

El `reviewer` corre **un modelo distinto** al implementador (a propósito:
`claude-sonnet-4-6` vs el opus del implementador) — un par de ojos frescos y
adversariales sobre código del que el implementador está demasiado cerca
(`factory.yaml:33-44`; `engine/registry/agents/reviewer.md:6-11`). Es
**bloqueante**: un veredicto `broken` falla el paso.

Su barra es **los criterios de aceptación del ticket, y nada más**. Trata la
implementación como culpable hasta probarse correcta contra ese contrato. Solo el
nivel **BLOCKER** bloquea: un AC incumplido (con input que falla nombrado), un bug
de correctitud/seguridad *en alcance*, o un test falso/vacuo. Todo lo demás
(edge cases que el ticket no pidió, refactors, estilo) es WARNING/INFO → **pasa**
(`reviewer.md:32-53`). El persona advierte explícitamente contra el modo de falla
de inventar una objeción nueva cada ronda hasta matar el run: "si el ticket está
satisfecho, ship it" (`reviewer.md:44-53`).

**`on_fail`:** `goto: implement` con la evidencia, `max: 2` (`factory.yaml:41-44`).

> El `verifier` es el default genérico de `agentic_verify` cuando no se setea un
> reviewer nombrado, con el mismo contrato de veredicto `works`/`broken`
> (`engine/registry/agents/verifier.md:1-21`). La variante `factory-plus.yaml`
> añade un paso `verify` adicional (verificador fresco, otro modelo) y un
> `human_gate` antes del PR — todo en datos, sin una línea de Go distinta
> (`engine/registry/workflows/factory-plus.yaml:1-50`).

### Paso 5 — pr (`type: pr`, `approval: risk-policy`)

El último paso abre el PR contra `dev`. La aprobación es por política de riesgo:
bajo riesgo → auto-merge; alto riesgo → `human_gate` (Wave 6)
(`factory.yaml:46-50`). Mantener al humano en el PR final (`merge_mode: manual`)
baja el costo de error del gate (`CLAUDE.md:144-145`).

---

## El backlog de dos niveles (BMAD: esqueleto, luego detalle)

El patrón central de los dos flujos es BMAD de dos niveles:

| Nivel | Cuándo | Quién | Salida |
|---|---|---|---|
| 1 — esqueleto | fin del Diseño, una pasada | `scrum-master` | todas las historias ligeras (título + user-story + ACs + deps + owner + sprint) en `backlog.yaml` |
| 2 — detalle | Fábrica, just-in-time, 1 historia/llamada | `story-detailer` | la spec dev-ready completa de **esa** historia, como ticket para el `dev` |

Por qué partirlo: emitir el cuerpo completo de cada historia de una vez agota el
timeout del agente en proyectos grandes (es un incidente real — ver Hallazgos).
El esqueleto se genera rápido; el detalle pesado se difiere y se amortiza, una
historia a la vez, justo antes de construirla
(`scrum-master.md:10-18`; `story-detailer.md:6-11`).

---

## Hallazgos de validación

Análisis adversarial del estado actual (a 2026-06-26):

- **Tensión sello-de-gate vs evolución legítima del gate.** El anti-tamper hashea
  `.vibeforge-gate` y trata *cualquier* cambio como ataque
  (`gate.go:116-118`, `dev.md:28-36`). Pero el comando es legítimamente
  *por-stack*: `python-dev` lo reescribió y borró el frontend; `react-dev`
  corrigió `npm test`→`vitest` — ambos cambios legítimos dispararon tamper y
  mataron el run (`CLAUDE.md:136-140`). El candado está puesto en el lugar
  equivocado: sobre el *comando*, que el agente legítimamente define. La decisión
  estratégica (Wave V, `CLAUDE.md:134-163`) es **quitar el GateHash del comando**
  (V2) y conservar solo el piso agnóstico al stack (suite offline + conteo de
  marcadores, V1) más la verificación agéntica (V3). Aún **no implementado**: hoy
  el sello sigue activo y sigue siendo una cinta de correr de mantenimiento que
  romperá con cada stack nuevo (flutter el próximo).

- **"Whack-a-mole" de la revisión agota `on_fail`.** El `reviewer` corre con
  `max: 2` → 3 intentos (`factory.yaml:41-44`, `engine.go:289,353`). El persona se
  esfuerza por evitar inventar un BLOCKER nuevo cada ronda
  (`reviewer.md:50-53`), pero **nada lo fuerza estructuralmente**: si el modelo
  encuentra un defecto distinto cada ronda (un defecto por ronda), las 3 rondas se
  agotan y el run muere — sin un humano que destrabe. El gate tiene el mismo
  patrón. La mitigación es solo el prompt, no el mecanismo.

- **Orden de fases de diseño vs SDLC.** El orden `discovery → PRD → architecture →
  UI → mockups → backlog` (`design.yaml:6`) pone PRD (el QUÉ) antes de
  architecture (el CÓMO), lo cual es estándar. Pero el propio backlog del proyecto
  lo marca para revisión (`CLAUDE.md:42-46`): falta una pregunta abierta sobre si
  un paso explícito de elicitación/requisitos va entre discovery y PRD, y si la
  arquitectura debería retroalimentar el PRD. Hoy es estrictamente lineal y de un
  solo sentido: el `analyst` mismo difiere ambigüedades irresolubles a "Open
  Questions" (`analyst.md:24-26`), pero esas preguntas no tienen un canal de
  respuesta conversacional en el gate — solo aprobar o rechazar-con-feedback
  (`CLAUDE.md:36-39`).

- **Timeout del agente (20m default) vs tamaño del sprint en goal-mode.** El
  wall-clock por agente es 20m por default, configurable vía
  `VIBEFORGE_AGENT_TIMEOUT_MIN` (`cmd/control/main.go:47-50`). Pero en goal-mode un
  sprint construye **varias historias en una pasada**, así que un run de
  sprint-completo excede fácilmente los 20m (`cmd/control/main.go:47-48`). El
  default conserva los 20m históricos — un sprint grande lo revienta a menos que el
  operador suba el env. (El backlog menciona un timeout de 45m como objetivo
  configurable, pero el código **no** lleva ningún 45 hardcodeado; el único default
  en código es 20m, y `cmd/worker` también usa 20m — el "45m" del prompt es un
  valor operativo de despliegue, no del código.)

- **Incidente real: el scrum-master sobre-genera y el backlog hace timeout.** En el
  E2E de *serviciospty* el backlog del marketplace corrió los 20m completos y
  falló, tumbando todo el run de diseño (`CLAUDE.md:75-80`). Es exactamente lo que
  el split BMAD esqueleto-luego-detalle pretende evitar — y la razón por la que el
  persona del `scrum-master` ahora insiste en mantener el nivel 1 ligero
  (`scrum-master.md:10-18`). Pendiente: el paso `backlog` **no tiene `on_fail`/retry**
  (a diferencia de los gates), así que un backlog fallido nukea el run entero
  (`CLAUDE.md:79-80`).

- **`$step.text` no resuelve — los inputs del diseño son no-ops latentes.** El
  texto de un agente aterriza en `$<step>.output.text`, no en `$<step>.text`
  (`runner.go:148-156`). Los inputs de `design.yaml` (`$discovery.text`,
  `$prd.text`, `$architecture.text`) son por tanto **no-ops silenciosos**: los
  agentes de diseño solo funcionan porque cada uno **re-lee** los docs de
  `docs/` en el workdir (`CLAUDE.md:81-85`). `factory.yaml` lo hace bien
  (`$draft_story.output.text`, `factory.yaml:22`). Es deuda frágil: si un agente
  olvidara re-leer, recibiría un input vacío sin error.

- **Race de ordenamiento: la fábrica dispara antes de mergear los docs a `dev`.**
  `handoff` publica las historias y el orquestador reclama+dispara SP1 de
  inmediato, compitiendo con el merge humano del `docs_pr`. La fábrica clona `dev`,
  que **carece** de `docs/` y del `.vibeforge-gate` del architect hasta que ese PR
  se mergea — así que `draft_story` no tiene PRD/arquitectura (mitigado por su
  fallback, `story-detailer.md:43-48`) y el **gate falla con "no .vibeforge-gate"**
  (`gate/runner.go:33-37`; `CLAUDE.md:94-100`). Es un bug HIGH abierto: el sello
  offline es correcto, pero el orden de publicación lo deja sin su archivo de
  comando.

- **Sello offline correcto, pero un gate sin archivo es indistinguible de un
  gate-less flow.** `Snapshot` hashea a `""` cuando falta `.vibeforge-gate`
  (`gate.go:54-60`), y `CheckIntegrity` salta el anti-tamper si el hash sellado es
  `""` (`gate.go:116`). Combinado con el race anterior, un repo seedeado sin gate
  pasa el anti-tamper trivialmente — la integridad solo protege cuando el archivo
  existió al sellar. Correcto por diseño, pero conviene tenerlo presente: el piso
  determinista solo es tan fuerte como la presencia del gate en el momento del
  seed (`cmd/control/main.go:96-97`).

- **El host fallback (`LocalSandbox`) no es frontera de seguridad — y los agentes
  de diseño corren ahí siempre.** El diseño corre sin sandbox en el host con el env
  del host (`app.go:125-134`; `CLAUDE.md:56-59`). La fábrica exige docker
  (`MustDocker` hard-fail, `runner.go:124-126`; `sandbox.go:61-75`), pero el
  diseño no — corre `claude` en el host con credenciales del operador. Para
  producción está pendiente contenerizar también los agentes de diseño
  (`CLAUDE.md:56-59`).
