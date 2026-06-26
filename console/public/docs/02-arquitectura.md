# Arquitectura y flujo de datos

aiuda-forge es una **fábrica de software autónoma**: una idea entra por un
extremo y por el otro salen Pull Requests en GitHub, pasando por diseño →
especificaciones → backlog → ejecución por agentes. Esta página describe el
panorama completo: los procesos que componen el sistema, dónde vive el estado, y
cómo fluye una idea de punta a punta.

El repositorio es un monorepo con dos mundos:

- `engine/` — Go (módulo `forge`). El **control plane**, el **orchestrator**, el
  **worker** y los cuatro almacenes SQLite.
- `console/` — Next.js / TypeScript. La **consola** web (la UI).

---

## 1. Mapa de componentes

```
                            ┌──────────────────────────────────────┐
                            │            console (Next.js)          │
                            │  secciones: Resumen · Studio · Board  │
                            │   Tickets · Registry · Gasto · Settings│
                            └───────────────┬──────────────────────┘
                                            │  HTTP + Bearer token
                                            │  (lib/api.ts → API_URL)
                                            ▼
   ┌────────────────────────────────────────────────────────────────────────┐
   │                  CONTROL PLANE — cmd/control (Go, :8080)                 │
   │                                                                          │
   │   httpx.CORS → httpx.Auth → api.Server (mux con todas las rutas)        │
   │                                                                          │
   │   ┌───────────────┐   ┌──────────────────────────────────────────────┐ │
   │   │ workflow.Engine│  │  In-process: WorkerLoop ×N · ReaperLoop · Bus │ │
   │   │  (runners:     │  │  (claim atómico BEGIN IMMEDIATE + fence)      │ │
   │   │   echo, gate,  │  └──────────────────────────────────────────────┘ │
   │   │   agent, design,│                                                   │
   │   │   agentic_verify,│   sandbox docker + egress-proxy (agente)         │
   │   │   human_gate, pr,│                                                  │
   │   │   ticket_publish)│                                                  │
   │   └────────┬─────────┘                                                  │
   │            │                                                            │
   │   ┌────────┴──────┬──────────────┬───────────────┬──────────────────┐  │
   │   ▼               ▼              ▼               ▼                  │  │
   │ store.Store    tickets.Store  projects.Store  auth.Store           │  │
   │ (vibeforge.db) (tickets.db)   (projects.db)   (auth.db)            │  │
   │  runs/tasks/    epics/sprints/  projects+       users+              │  │
   │  events         stories/deps    settings        sessions           │  │
   └────────────────────────────────────────────────────────────────────────┘
                                            ▲
                                            │ GET /tickets, /sprints/ready
                                            │ POST /runs, PUT /stories/{id}/status
                            ┌───────────────┴──────────────────────┐
                            │     orchestrator — cmd/orchestrator   │
                            │  resuelve deps → dispara runs READY   │
                            │  merge-reconcile loop (gh CLI)        │
                            └───────────────┬──────────────────────┘
                                            │ git clone / push / gh pr
                                            ▼
                                   ┌──────────────────┐
                                   │      GitHub      │
                                   │  repos + PRs     │
                                   └──────────────────┘
```

### 1.1 Control plane — `engine/cmd/control`

Es el **único punto privilegiado** del sistema y el corazón del MVP: un solo
binario que expone la API HTTP, hospeda el motor de workflows y corre el pool de
workers, el reaper y el bus de eventos en proceso.

- **Boot y configuración** — `engine/cmd/control/main.go:27`. Toda la
  configuración llega por variables de entorno (`VIBEFORGE_*`): la dirección de
  escucha (`:8080`, `main.go:28`), las rutas de los cuatro SQLite
  (`main.go:38-41`), el modo del motor (`echo`/`claude`, `main.go:44`), el
  runtime de sandbox, el pool de workers (`main.go:51`), y la autenticación del
  agente (subscription / OAuth token / API key, `main.go:31-36`).
- **Ensamblado del kernel** — `engine/internal/app/app.go:54`. `app.Build`
  abre los almacenes, registra los **step runners** por tipo (`app.go:66-158`)
  y construye el `api.Server` (`app.go:209`). Es el único lugar donde se cablea
  el sistema; tanto `cmd/control` como los tests de contrato pasan por aquí.
- **Servidor HTTP** — `engine/internal/api/server.go:53`. El mux con todas las
  rutas (ver §3). Regla de oro (`server.go:1-5`): toda operación pasa por aquí
  y toda transición de estado emite un evento; no hay camino privilegiado —
  UI, orchestrator y CLI son clientes iguales.
- **Procesos de fondo** — `engine/internal/app/app.go:249`. `StartBackground`
  lanza N `WorkerLoop` (pool paralelo), un `ReaperLoop` (re-encola tasks cuyo
  heartbeat venció) y el `Bus` (poll del tail global de eventos).

### 1.2 Orchestrator — `engine/cmd/orchestrator`

Proceso **separado** que actúa como planificador del backlog. No tiene estado
propio en modo nativo: lee del control plane y le escribe.

- `engine/cmd/orchestrator/main.go:45`. Dos modos (`-source`): **`native`**
  (por defecto) lee el store nativo de tickets del control plane y deriva
  readiness del grafo de dependencias (`main.go:76`); **`github`** (legacy)
  sondea issues de un repo GitHub.
- En cada ciclo: toma las stories/sprints **READY** (deps resueltas), dispara
  un `POST /runs` por cada una y refleja el resultado con
  `PUT /stories/{id}/status`. Además corre el **merge-reconcile loop**: verifica
  si un PR `in_review` ya se mergeó y, en modo `auto`, lo mergea vía `gh` CLI
  (`main.go:78-80`).

### 1.3 Consola — `console/`

La UI Next.js. Toda acción de usuario = un endpoint del contrato; no hay lógica
de negocio del flujo en el cliente (`console/src/lib/api.ts:1-3`).

- **Secciones** — `console/src/lib/sections.ts:13`: Resumen, Studio, Board ·
  Runs, Tickets, Registry, Gasto, Settings. El sidebar y la topbar leen de aquí.
- **Cliente de API** — `console/src/lib/api.ts`. Una sola capa `fetch` que
  habla con `API_URL`. El token Bearer va en cada llamada (`api.ts:99-101`); un
  `401` borra el token y redirige a `/login` (`api.ts:103-106`). El modo mock es
  **opt-in** (`NEXT_PUBLIC_FORCE_MOCK=1`): no hay fallback silencioso a datos
  falsos si el backend está caído (`api.ts:63-78`).

### 1.4 Los cuatro almacenes SQLite

Cada almacén abre su **propio archivo SQLite** y nunca toca el de otro. La
frontera es deliberada: el kernel es código crítico de seguridad, los demás son
del control plane.

| Almacén | Archivo (env) | Paquete | Qué guarda |
|---|---|---|---|
| **Kernel** | `vibeforge.db` (`VIBEFORGE_DB`) | `engine/internal/store` | `runs`, `tasks` (los steps), `events`. El motor de ejecución: máquina de estados, claim atómico, fencing y el único punto de emisión de eventos. |
| **Tickets** | `tickets.db` (`VIBEFORGE_TICKETS_DB`) | `engine/internal/tickets` | `epics`, `sprints`, `stories`, `story_deps`. La **fuente de verdad del backlog**; readiness es derivada del grafo de deps. |
| **Projects** | `projects.db` (`VIBEFORGE_PROJECTS_DB`) | `engine/internal/projects` | `projects` (id, nombre, repo GitHub, owner, `execution_unit`, `merge_mode`). Un proyecto = un repo. |
| **Auth** | `auth.db` (`VIBEFORGE_AUTH_DB`) | `engine/internal/auth` | `users` (email + hash bcrypt), `sessions` (tokens opacos de 32 bytes). |

Esquemas: kernel `store.go:36-81`; tickets `tickets.go:128-164`; projects
`projects.go:61-72`; auth `auth.go:52-66`. Los tres últimos almacenes son
**opcionales**: si su variable de entorno está vacía, el almacén no se abre y sus
rutas devuelven `503` vía los guards `needTickets`/`needProjects`
(`server.go:145`, `app.go:150-205`).

---

## 2. Estado, eventos y ejecución (el kernel)

El kernel (`store.Store`) es deterministico y concentra la integridad:

- **Máquina de estados** — toda transición de `run`/`task` valida transiciones
  legales (`store.go:267`) y emite un evento en la **misma transacción** desde
  `emitTx` (`store.go:401`), el único punto de emisión (regla de oro #5).
- **Claim atómico + fencing** — el DSN fuerza `BEGIN IMMEDIATE` (`store.go:105`),
  así dos workers nunca reclaman la misma task; cada claim incrementa un `fence`
  que invalida reportes de workers zombis.
- **Multi-tenant (audit A1)** — `runs`, `tasks` y `events` llevan `project_id`;
  migraciones idempotentes lo añaden a DBs antiguas y backfillean al proyecto
  `default` (`store.go:88-143`).
- **Bus de eventos** — `AllEventsAfter` (`store.go:432`) es el tail global que
  el `Bus` sondea; la consola se suscribe por REST (`GET /runs/{id}/events`,
  con cursor `?after=`) o por WebSocket (`GET /ws`).

El **engine** (`workflow.Engine`) avanza un run paso a paso: encola el step N+1
solo cuando N termina (steps seriales dentro de un run), pero el pool de N
workers drena runs/tasks independientes en paralelo (`app.go:244-255`).

---

## 3. Camino de la petición y autenticación

Toda petición HTTP atraviesa, en orden (`engine/cmd/control/main.go:147`):

```
  cliente → httpx.CORS → httpx.Auth → api.Server.mux → handler
```

1. **CORS** (más externo, atiende el preflight `OPTIONS` antes que Auth). Se
   bloquea a un único origen con credenciales cuando hay auth; `*` solo con
   `VIBEFORGE_CORS=open` (`main.go:137-143`).
2. **Auth** (`main.go:104-133`). La autenticación está **activa** cuando hay un
   service token (`VIBEFORGE_API_TOKEN`) **o** al menos un usuario en el store de
   auth. Con auth activa, cada request necesita un token de sesión o el service
   token. `POST /auth/login` es el único endpoint exento.
3. **Cierre seguro** (audit C1): si NO hay ni service token ni usuarios, el
   servidor es abierto pero se fuerza a escuchar **solo en loopback**
   (`127.0.0.1`, `main.go:128-129,185`) — nunca sirve abierto al mundo.

El login emite una sesión (token opaco bcrypt-respaldado, TTL 7 días,
`auth.go:33-34`) que la consola guarda y manda como `Authorization: Bearer
<token>` en cada llamada (`api.ts:99-101`).

### Rutas principales (`server.go:53-141`)

- **Operaciones (§A)**: `POST/GET /runs`, `/runs/{id}` (+ cancel, retry,
  requeue, delete), `/runs/{id}/steps/{step}/approve|reject|merge`,
  `/control/pause|resume`, `/metrics` (= `/analytics`), `/healthz`, `/readyz`.
- **Auth**: `/auth/login|logout|me`.
- **Registry CRUD (no-code)**: `/registry/{kind}/{id}` para workflows · agents ·
  skills; valida con el mismo parser que usa el kernel.
- **Settings**: globales `/settings`; por-proyecto `/projects/{id}/settings`.
- **Eventos (§B)**: `/runs/{id}/events`, `/ws`.
- **Worker↔kernel (§C)**: `/runs/claim`, `/steps/{id}/report|heartbeat|usage`.
- **Tickets**: epics, sprints (`/sprints/ready`, `/sprints/{id}/claim`),
  stories, `/tickets` (compat para la UI).
- **Projects**: `/projects`, `/projects/{id}/docs` (Studio lee specs del repo).

---

## 4. Ciclo de vida de punta a punta (idea → PR)

```
  idea ─▶ design run ─▶ specs+backlog ─▶ factory runs ─▶ GitHub PRs
```

**1. Idea → Design run.** En **Studio**, el usuario crea un *design run* ligado a
un proyecto/repo: `createDesignRun` envía `POST /runs {workflow:"design",
payload:{project_id, repo, instructions}}` (`api.ts:905-938`). El workflow
`design` corre fases en el host (no sandbox, porque produce documentos, no
código): Descubrimiento → PRD → Arquitectura → UI → Mockups → Backlog → Handoff
(`api.ts:834-842`). Cada fase usa el runner `design` (`app.go:128-134`) y puede
tener un `human_gate` para aprobación.

**2. Specs + backlog.** Las specs se escriben en el `docs/` del repo (Studio =
Confluence: fuente de verdad que sobrevive al run efímero, `server.go:113-116`).
El paso final `handoff` usa el runner **`ticket_publish`**
(`engine/internal/tickets/publish.go:56`) que crea sprints y stories en el store
de **tickets**, heredando el `project_id` del design run
(`publish.go:67-154`). Las dependencias entre stories se registran en
`story_deps`.

**3. Factory runs.** El **orchestrator** sondea `GET /sprints/ready` /
`GET /tickets`, resuelve el grafo de deps y dispara un `POST /runs` por cada
unidad **READY**. La unidad es por proyecto: `execution_unit=sprint` (un run →
un PR por sprint, default) o `story` (un run por story) (`projects.go:28-33`).
El workflow de factory corre los runners **dentro del sandbox docker** con
egress restringido al proxy del LLM (`app.go:104-122`): `agent` (implementa),
`agentic_verify` (verifica), `gate` (sella el workdir), y un `human_gate`
opcional.

**4. Seeding y aislamiento.** Antes de que corra cualquier agente, `OnSeed`
(`main.go:69-98`) clona el repo del payload del run en el workdir, hace checkout
de `dev` (para incluir sprints ya mergeados) y **sella** el workdir
(`gate.SealWorkdir`). El remote se valida (`httpx.ValidateRemote`) para rechazar
URLs no permitidas.

**5. PR.** El runner **`pr`** (`engine/internal/pr/pr.go`) toma el árbol de
trabajo del run y abre un Pull Request (`PR_MODE`: `local` commitea/pushea,
o `gh pr create`). La story pasa a `in_review` con su `pr_url`
(`tickets.go:60-65`).

**6. Merge → done.** El **merge-reconcile loop** del orchestrator verifica si el
PR se mergeó; en modo `auto` lo mergea él mismo. Solo un PR mergeado avanza la
story `in_review → done` (`tickets.go:62-65`). Como los factory runs se basan en
`dev`, las stories dependientes solo arrancan tras el merge de sus deps
(merge-gated deps). El ciclo se repite hasta drenar el backlog.

A lo largo de todo el flujo, **Gasto/Analytics** agrega coste y tokens leyendo el
`Result.output` de cada step de agente (`server.go:334-383`) — sin cambios en el
kernel, solo lectura de lo persistido.

---

## Hallazgos de validación

- **SPOF: el control plane es un único binario con todo dentro.** API, motor,
  pool de workers, reaper y bus corren en el mismo proceso
  (`engine/internal/app/app.go:249-255`). Si `cmd/control` cae, se detiene la
  ejecución, la ingesta de eventos y la API a la vez. El comentario de
  `cmd/control/main.go:1-3` reconoce que `cmd/worker` puede correr standalone
  "para escala horizontal", pero el despliegue por defecto no lo hace.

- **SPOF de almacenamiento: SQLite local, sin réplica.** Los cuatro almacenes
  son archivos SQLite en disco local (`store.go:104`, `tickets.go`, etc.). No hay
  réplica ni failover; la pérdida del volumen pierde runs, backlog, proyectos y
  sesiones. El código anticipa el port a Postgres (`store.go:28-30`) pero hoy es
  un único nodo.

- **Acoplamiento por convención entre fases del flujo, no por contrato.** El mapa
  de fases del workflow `design` está **hardcodeado en la consola**
  (`console/src/lib/api.ts:834-842`, `DESIGN_PHASE_MAP`) y debe coincidir
  exactamente con los `step_id` del manifest del workflow en el registry. Si
  alguien edita el workflow vía Registry CRUD (no-code), la UI de Studio se
  desincroniza silenciosamente — un acoplamiento frágil entre dos artefactos sin
  validación cruzada.

- **Fuga de la frontera de stores vía `project_id` duplicado.** `DefaultProjectID
  = "default"` se redefine como constante independiente en **tres** paquetes
  (`store.go:100`, `tickets.go:126`, `projects.go:38`), cada uno con un comentario
  diciendo que "espeja" al otro. No hay una sola fuente; un cambio en uno no
  propaga a los demás. Es un contrato implícito entre stores que deberían estar
  aislados.

- **Inconsistencia documentada vs. implementado: `merge` y `reject` son
  parcialmente stubs.** `mergeStep` (`server.go:309-314`) no mergea nada: solo
  responde `200 {"merged":...}` porque "en el MVP el paso `pr` ya commitea/pushea";
  existe solo para que la superficie del contrato exista. En la consola,
  `rejectStep` (`api.ts:498-505`) lleva un `TODO(endpoint)` admitiendo que el
  endpoint podría no existir (404) y habría que reusar retry-con-feedback. El
  contrato anunciado y el comportamiento real divergen.

- **Endpoints "aditivos" no garantizados por el contrato.** La consola pasa
  `?project=` a `/metrics` aun sabiendo que "el contrato no lista /metrics como
  scopeado" (`api.ts:359-364`), confiando en que el engine lo ignore sin romper.
  Funciona hoy (`server.go:336` sí lo lee), pero es un acoplamiento basado en una
  suposición, no en un contrato versionado.

- **Notificaciones y openPRs son derivadas/aproximadas, no datos reales.** La
  campana de notificaciones se deriva de runs en `AWAITING`/`FAILED` porque "el
  kernel no expone /notifications todavía" (`api.ts:399-417`), y `openPRs` en las
  stat-cards se mapea a `by_status.DONE` (`api.ts:371-377`) — que no es el número
  de PRs abiertos. Métricas que *parecen* exactas pero son proxies.

- **El orchestrator es un segundo SPOF para el progreso del backlog.** Es un
  proceso separado sin réplica (`cmd/orchestrator/main.go`). Si se cae, los runs
  en vuelo terminan pero ninguna story nueva se dispara y ningún PR mergeado
  avanza a `done` — la fábrica se "congela" sin que el control plane lo reporte
  como fallo.

- **`GET /tickets` tiene dos implementaciones con la misma forma.** El
  orchestrator en modo `github` sirve su propio `/tickets` (`main.go:123-130`) y
  el control plane sirve uno nativo (`server.go:140`). La consola apunta al store
  nativo (`api.ts:707-724`), pero la coexistencia de dos endpoints con idéntico
  shape y semántica distinta invita a confusión sobre cuál es la fuente de verdad.
</content>
</invoke>
