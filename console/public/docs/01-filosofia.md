# Filosofía: todo configurable, la lógica vive en los .md

aiuda-forge se construye sobre una sola regla de oro, escrita literalmente en el
código del kernel:

> *"Per golden rule #1 there is no per-flow branching here: the executor
> interprets step **types** declared in data. Adding/removing an agent is editing
> a YAML file, never editing Go."*
> — `engine/internal/workflow/workflow.go:2-5`

La idea es radical y deliberada: **la metodología no se compila**. El kernel en Go
es un motor genérico que no sabe nada de "scrum", "BMAD", "arquitectura" ni "PRD".
Todo eso vive como **datos editables** en `engine/registry/`:

- Los **agentes** son una persona en Markdown + un manifiesto YAML pequeño.
- Los **workflows** son YAML: una lista de pasos que el kernel ejecuta como datos.
- El **registry** se edita sin código desde la consola.
- Los agentes se **releen de disco en cada run** — cambiar comportamiento no
  requiere recompilar ni reiniciar.

A continuación cada pieza, con ejemplos de los archivos reales.

---

## 1. Un agente = persona Markdown + manifiesto YAML

Cada agente son **dos archivos** que comparten el mismo `id` en
`engine/registry/agents/`:

- `<id>.yaml` — el **manifiesto**: metadatos y permisos (qué modelo, qué
  herramientas, qué rol). Es DATA.
- `<id>.md` — la **persona**: el juicio, el método, el "cómo se hace el trabajo".
  Es PROSA en lenguaje natural.

El manifiesto es deliberadamente pequeño. El de `architect`
(`engine/registry/agents/architect.yaml:1-7`) cabe entero:

```yaml
id: architect
version: 1.0.0
model: claude-opus-4-8
skills:
  - architecture-template
tools: [read, write]
role: "Derive the technology stack, module structure, data model, and key architectural decisions from the PRD."
```

La estructura la define el kernel en `engine/internal/agent/manifest.go:20-29`:
`ID`, `Version`, `Model`, `Skills`, `Tools`, `Role` — y `Persona` (cargada del
`.md`, marcada `yaml:"-"` porque no viene del manifiesto). Nada más. El kernel no
sabe que `architect` es "un arquitecto": solo carga estos campos y se los pasa al
backend.

**Toda la lógica de verdad vive en el `.md`.** El persona de `architect`
(`engine/registry/agents/architect.md`) tiene 118 líneas que explican cómo ser
opinado, cómo evitar complejidad accidental, la estructura exacta de
`docs/ARCHITECTURE.md` y hasta cómo escribir el comando de gate offline
(`architect.md:65-110`). El kernel nunca lee ese contenido como código: lo inyecta
como **system prompt** del agente (`engine/internal/agent/runner.go:74` →
`SystemPrompt: manifest.Persona`).

El `role` del YAML se usa solo como encabezado del *user prompt*
(`runner.go:191-193`); el comentario del propio código lo dice sin rodeos:

> *"the JUDGMENT (how to do the work) lives in the persona markdown and skills,
> not here."* — `engine/internal/agent/runner.go:187-188`

### Los permisos también son datos

El campo `tools` es una allowlist lógica (`read|edit|write|bash|grep|glob`) que el
kernel traduce a los nombres del CLI de Claude en
`engine/internal/agent/manifest.go:76-96`. Compará:

- `dev` (`dev.yaml:5`) → `tools: [read, edit, write, bash]` — puede modificar el árbol.
- `reviewer` (`reviewer.yaml:5`) → `tools: [read, bash]` — **no puede escribir**:
  un revisor que no puede tocar el código que revisa, por diseño.

El **modelo** también es dato, y eso habilita una decisión de calidad clave:
`reviewer.yaml:2` fija `claude-sonnet-4-6` con el comentario
`# CROSS-MODEL: deliberately different from dev (claude-opus-4-8)`. El verificador
adversarial corre en un modelo distinto al implementador — y eso se decide en una
línea de YAML, no en Go.

---

## 2. Un workflow = YAML que el kernel ejecuta como datos

Un workflow es una lista ordenada de `steps`. Cada step tiene un `type` que
selecciona un *step-runner* registrado en el kernel; el resto de campos son datos
que ese runner interpreta (`engine/internal/workflow/workflow.go:26-42`).

El kernel **no tiene un `switch` por workflow**. Tiene un registro genérico de
tipos de step. En `engine/internal/app/app.go` se registran los runners:

```go
eng.Register("agent", agentRunner)            // app.go:123
eng.Register("design", designRunner)          // app.go:134
eng.Register("agentic_verify", verifyRunner)  // app.go:141
eng.Register("human_gate", agent.HumanGateRunner{}) // app.go:143
eng.Register("pr", pr.NewRunner())            // app.go:144
eng.Register("ticket_publish", ...)           // app.go:158
```

Un workflow nuevo solo combina estos tipos en distinto orden. La cabecera de
`factory.yaml` lo declara como tesis del producto:

> *"The real factory flow — defined ENTIRELY in data. The kernel runs this with no
> flow-specific Go. To add a step (e.g. a `simplify` agent, or `agentic_verify`),
> edit THIS file — no recompile. That editability is the v2 success criterion."*
> — `engine/registry/workflows/factory.yaml:1-5`

### El bucle `on_fail` es un primitivo genérico

El reintento/loop no está cableado por caso. Es **un solo constructo de datos**,
`OnFail{ Goto, Max, Feedback }` (`engine/internal/workflow/workflow.go:47-51`),
que cubre tanto "gate rojo → de vuelta al dev" como "review encontró un bug → de
vuelta al dev". En `factory.yaml`:

```yaml
- id: gate
  type: gate
  command_from: repo
  on_fail:
    goto: implement        # gate rojo -> de vuelta al dev con el fallo como feedback
    max: 2
    feedback: $gate.detail
```
— `engine/registry/workflows/factory.yaml:24-31`

El `$gate.detail` es una **referencia**: el kernel resuelve `$<step>.<campo>` y lo
inyecta en los inputs del step destino. Por eso `design.yaml` puede devolver el
feedback de un humano al agente que reescriba el documento
(`design.yaml:23-24`, `feedback: $discovery_gate.detail`).

### Mismo kernel, distintas metodologías

La prueba de que la lógica es 100% dato: hay **varios workflows que comparten el
mismo kernel sin una línea de Go distinta**:

- `factory.yaml` — flujo de fábrica: draft → implement → gate → review → pr.
- `factory-plus.yaml` — agrega `agentic_verify` y un `human_gate` antes del PR; la
  cabecera lo dice: *"Not a line of Go differs from factory.yaml — that is the
  thesis"* (`factory-plus.yaml:4`).
- `gated.yaml` — gobernanza: un `human_gate` que **estaciona el run** hasta que un
  humano aprueba.
- `design.yaml` — el proceso de diseño de producto completo (discovery → PRD →
  arquitectura → UI → mockups → backlog → handoff → PR de docs), donde cada fase
  es un step `design` seguido de un `human_gate` con loop de rechazo.

Agregar una metodología nueva es escribir un YAML nuevo. Eso es todo.

---

## 3. El registry se edita sin código desde la consola

La consola expone un editor no-code del registry en
`console/src/components/registry/RegistryView.tsx`. Tiene tres pestañas —
**Agentes · Skills · Workflows** (`RegistryView.tsx:67`) — y cada ítem se puede
ver renderizado (YAML resaltado + persona en Markdown) o editar en un textarea.

El editor está cableado contra endpoints CRUD genéricos por `{kind}`, definidos en
`engine/internal/api/server.go:84-90`:

```
GET    /registry/{kind}            # listar
GET    /registry/{kind}/{id}       # ver
PUT    /registry/{kind}/{id}       # guardar (valida)
DELETE /registry/{kind}/{id}       # borrar
GET    /registry/agents/{id}/persona   # el .md sidecar del agente
```

`{kind}` es `workflows | agents | skills`. No hay un endpoint por agente concreto:
la API es tan genérica como el kernel.

### Guardar = "el kernel puede correrlo"

Lo más importante del editor: **al guardar, el servidor valida con el MISMO parser
que usa el kernel para ejecutar**. En `validateRegistry`
(`engine/internal/api/helpers.go:135-156`):

- un `workflow` se valida con `workflow.Parse(body)` — el mismo `Parse` del kernel;
- un `agent` se deserializa a `agent.Manifest` y exige `model`;
- una `skill` solo no puede estar vacía.

El comentario lo resume: *"the schema check is the SAME parser the kernel uses to
run it — so 'saved' means 'the kernel can run it'"* (`helpers.go:133-134`). No hay
forma de guardar un manifiesto que el kernel luego no entienda. La UI lo refleja:
*"El servidor valida el schema… Los cambios son efectivos en el próximo run"*
(`RegistryView.tsx:398-403`).

Hay además guardas de seguridad: `pathFor` rechaza cualquier `id` que intente salir
del root del registry (`helpers.go:90-95`), y el loader de agentes rechaza ids que
no sean un slug plano (`manifest.go:15`, `agentIDRe`), evitando
`agent: "../../etc/passwd"`.

---

## 4. Recarga por run: cambiar comportamiento sin recompilar

El paso final de la filosofía: los cambios al registry impactan **el próximo run**,
sin rebuild ni reinicio.

**Agentes — sin caché.** El `DirLoader` de agentes lee `<id>.yaml` + `<id>.md` de
disco en cada `Load()` (`engine/internal/agent/manifest.go:42-62`). No hay mapa de
caché. El `StepRunner` llama a `r.Agents.Load(agentID)` **dentro de cada ejecución
de step** (`engine/internal/agent/runner.go:60`). Resultado: editás el persona de
`dev.md`, y el siguiente story que toque `dev` usa la versión nueva — sin tocar el
binario.

**Workflows — caché con invalidación explícita.** El `DirLoader` de workflows sí
cachea el parseo (`engine/internal/workflow/loaders.go:34-46`), pero el PUT del
registry invalida la entrada para que el próximo run relea el archivo:

```go
// Invalidate the workflow cache so the next run re-reads the new manifest.
if kind == "workflows" && s.Engine != nil {
    s.Engine.InvalidateWorkflow(id)
}
```
— `engine/internal/api/server.go:458-461`

En ambos casos el efecto es el mismo que promete la filosofía: **la metodología es
dato editable en caliente**, y el kernel es un intérprete estable que no necesita
saber qué metodología corre.

---

## Hallazgos de validación

Esta sección es adversarial: el objetivo es **validar** que la implementación
honra la filosofía, no adornarla. Estos son los puntos donde diverge o donde la
lógica se filtra al kernel.

- **`skills:` es config muerta para el kernel.** El manifiesto declara
  `Skills []string` (`manifest.go:24`) y varios agentes lo usan —
  `architect` (`architect.yaml:4-5`), `scrum-master` (`scrum-master.yaml:5-8`),
  `analyst`, `pm`, `designer`. Pero **el kernel nunca carga ni inyecta esos
  skills**: el único uso de `Skills` en Go es la declaración del struct. El
  `buildPrompt` (`runner.go:189-228`) no los menciona; el persona admite la
  contradicción explícitamente: *"The `architecture-template` skill is INLINED
  below because the runtime injects only this persona into the agent — the skill
  file is never loaded for you"* (`architect.md:5-8`). Es decir: el campo `skills`
  del YAML **no tiene efecto en runtime**; el contenido tuvo que copiarse a mano
  dentro del `.md`. La consola incluso muestra una pestaña "Skills" editable
  (`RegistryView.tsx:67`) y un endpoint `/registry/skills` (`server.go:84`) para
  archivos que el kernel jamás consume. Esto es lógica que *debería* vivir en el
  registry y ser cargada como dato, pero hoy está duplicada/inlineada a mano — la
  filosofía "la lógica vive en los .md/skills" se cumple solo a medias.

- **La forma del backlog (epic/sprints/stories) está hardcodeada en Go.** El
  `ticket_publish` parsea `backlog.yaml` con structs Go fijos —
  `BacklogFile{Epic, Sprints, Stories}`, y `backlogStory` con campos
  `owner`, `sprint_id`, `deps` (`engine/internal/tickets/publish.go:13-42`). Esto
  es vocabulario **específico de metodología** (sprints, épicas, ownership por
  lane) cableado en el kernel, no en el registry. Si una metodología no usa
  sprints, el step `ticket_publish` no le sirve sin recompilar. Contradice
  parcialmente la regla de oro: el kernel *sí* sabe qué es un "sprint".

- **El nombre del archivo de gate y su semántica están hardcodeados.**
  `const GateFile = ".vibeforge-gate"` (`engine/internal/gate/gate.go:35`) y la
  lectura `os.ReadFile(filepath.Join(workdir, ".vibeforge-gate"))`
  (`engine/internal/workflow/runner.go:82`) fijan el nombre del comando de prueba.
  Más aún, todo el concepto de "estrategia de test = un archivo sellado por hash en
  el repo" (`architect.md:65-110`) es una decisión metodológica fuerte que vive
  mitad en Go (anti-tamper, `gate.go:29`) y mitad en un persona. El workflow puede
  elegir `command_from: repo` (dato) pero el *qué* archivo y el ritual del sello no
  son configurables sin tocar Go.

- **`prompt: adversarial` es decorativo en el kernel.** El workflow pasa
  `prompt: adversarial` (`factory.yaml:38`, `factory-plus.yaml:26`) y el kernel lo
  emite literalmente como `"Prompt style: adversarial\n"` (`runner.go:195-197`).
  El comportamiento adversarial real vive en `reviewer.md` (la persona) — lo cual
  es *coherente* con la filosofía, pero significa que el campo `prompt` del YAML no
  hace nada por sí mismo: es un string libre sin validación ni efecto. Un editor
  no-code podría escribir `prompt: foobar` y el kernel lo aceptaría sin distinción.

- **Inconsistencia de caché entre agentes y workflows.** Los agentes se releen de
  disco en cada run (sin caché, `manifest.go:42-62`), pero los workflows se cachean
  y requieren `InvalidateWorkflow` en el PUT (`loaders.go:34-46`,
  `server.go:458-461`). El resultado observable es correcto para ambos *vía la
  consola*, pero si un workflow se edita **fuera** del endpoint PUT (p.ej. `git
  pull` en disco, o un editor externo), la caché del proceso servirá la versión
  vieja hasta reiniciar — mientras que un agente editado igual de "por fuera" sí se
  recargaría. La promesa "recarga por run" es asimétrica y depende del canal de
  edición.

- **Override de lane (`agent: $trigger.agent`) mezcla routing en los inputs.** En
  `factory.yaml:19-23`, el step `implement` declara `agent: dev` pero recibe
  `agent: $trigger.agent` en `inputs`, y el runner usa `inputs["agent"]` para
  sobrescribir `step.Agent` (`runner.go:53-56`). Funciona, pero `agent` viaja como
  *input* (clave de datos) **y** como campo de step (estructura), y el `buildPrompt`
  tiene que filtrarlo explícitamente para que no se cuele en el prompt
  (`runner.go:216-219`, `case "agent": continue`). Es una fuga conceptual: una clave
  de routing comparte namespace con el contenido del prompt y solo un `continue`
  con comentario evita el bug.

- **`pr` con `approval: risk-policy` está declarado pero la política aún no
  discrimina riesgo.** Los workflows usan `approval: risk-policy`
  (`factory.yaml:48`, `factory-plus.yaml:48`, `gated.yaml:26`) con el comentario
  "low risk -> auto; high -> human_gate (Wave 6)". En `internal/pr/pr.go` no hay
  rama que lea `risk`/`approval` ni que distinga low/high (la única mención es un
  comentario de cabecera, `pr.go:5`). Hoy el campo `approval` del YAML es
  aspiracional: el dato existe pero el kernel todavía no lo interpreta — config sin
  efecto, igual que `skills`.
