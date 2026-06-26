# El orquestador

El **orquestador** (o *native scheduler*) es el lazo de control que convierte un
backlog de historias en *runs* de la fábrica. No tiene estado propio: hace
*polling* del control-plane, deriva qué trabajo está listo a partir del grafo de
dependencias, dispara *runs*, y reconcilia el merge de los PRs hasta llevar cada
historia a `done`. Es **multi-tenant**: todo el trabajo se particiona por
`project_id` y cada proyecto se evalúa bajo SUS propios ajustes.

Hay dos implementaciones de orquestador en el binario; este documento cubre la
**nativa** (`-source=native`, la opción por defecto), que es la que opera contra
el ticket store nativo del control-plane. La variante `github` es el camino
original y queda fuera de alcance aquí
(`engine/cmd/orchestrator/main.go:62-72`).

---

## Cómo corre como proceso

El comando vive en `engine/cmd/orchestrator/main.go`. `main` parsea flags, exige
`-workflow`, construye un `ControlPlaneClient` y, en modo nativo, llama a
`runNative` (`main.go:45-73`).

`runNative` arma las tres piezas del scheduler (`main.go:76-99`):

- el **provider** `NewNativeHTTPProvider(cpURL)` — habla con el control-plane vía
  HTTP (`native.go:124-126`);
- el **control-plane** `cp` — para disparar *runs* y consultar su estado;
- el **MergeChecker** `github.New()` — el cliente `gh` que maneja el lazo de
  reconciliación de merges. Si es `nil`, ese lazo se desactiva y el trabajo se
  queda en `in_review` (`native.go:462-466`, `native.go:480-484`).

Flags relevantes (`main.go:45-54`): `-cp` (URL del control-plane, default
`http://localhost:8080`), `-workflow` (obligatorio), `-interval` (default `10s`)
y `-once` (un solo ciclo y salir — útil para CI/tests). En modo nativo el
listener `-addr` **no** se levanta: el control-plane ya expone `GET /tickets`,
así que el scheduler no monta su propio servidor (`main.go:92-98`).

Con `-once`, se ejecuta `RunOnce` una vez y se sale (`main.go:82-87`). En modo
normal, `sched.Run(ctx, interval)` corre el lazo hasta que llega `SIGINT`/`SIGTERM`
(`main.go:89-98`).

### Autenticación

Toda llamada del provider al control-plane se autentica con un Bearer token leído
de `VIBEFORGE_API_TOKEN` en el entorno (`native.go:124-126`, `native.go:131-140`).
Un token vacío (auth desactivada en dev) se envía sin header.

---

## El poll loop

`NativeScheduler.Run` es un lazo infinito que llama a `RunOnce` cada `interval`,
con backoff exponencial (`native.go:1308-1331`):

- Si `RunOnce` reportó **acciones > 0** (se disparó o avanzó algo), el intervalo
  se **resetea a la base** para recoger pronto el trabajo de seguimiento
  (completions, dependientes que se desbloquean).
- Si el ciclo fue **ocioso**, el intervalo se **duplica**, hasta un tope de `5×`
  la base.

Cada `RunOnce` hace dos cosas, en este orden (`native.go:540-552`):

1. **`reconcileMerges`** primero — avanza el trabajo `in_review` cuyo PR ya
   mergeó (y, según el `merge_mode` del proyecto, mergea los PRs él mismo)
   **antes** de disparar nuevo trabajo. El orden importa: así un dependiente puede
   desbloquearse en el MISMO ciclo en que aterriza el PR de su prerrequisito.
2. **`fireByProject`** — avanza completions de lo que corre y dispara el trabajo
   listo, todo agrupado por proyecto.

`RunOnce` devuelve el número de acciones para que el lazo decida el backoff.

### Idempotencia / recuperación tras reinicio

No se necesita archivo de estado. `MarkRunning` voltea la historia a `running`,
lo que la saca de `Ready()` en el siguiente ciclo. Reiniciar el scheduler es
simplemente volver a hacer *polling*: lo que ya corre se rastrea vía `Running()`
(`native.go:436-452`).

---

## Cómo se deriva "ready" (deps en done)

La readiness es **derivada**, nunca almacenada. El estado `ready` es un valor
calculado, no una columna (`tickets.go:17-28`, `tickets.go:23`).

En **modo historia**, `Store.Ready()` carga todas las historias en `backlog` y
para cada una verifica que **todas** sus deps estén en `done`
(`tickets.go:811-849`). Una historia sin deps queda lista en cuanto su estado
almacenado es `backlog`. La verificación de deps (`depsDone`) trata una dep
inexistente como "no done" — es decir, una historia que depende de un id que no
existe nunca queda lista (`tickets.go:1178-1193`).

El provider HTTP del scheduler consume esto vía `GET /tickets` filtrando por el
estado `ready` que el control-plane ya derivó del grafo
(`native.go:146-150`, `native.go:157-184`).

### Validación del grafo de deps

El store rechaza grafos malformados al crear/editar para que la readiness no se
deadlockee en silencio:

- **Self-dep**: una historia que depende de sí misma se rechaza con `ErrDepCycle`
  (`tickets.go:339-345`, `tickets.go:664-668`).
- **Ciclos**: `checkNoCycle` rechaza un edge que cierre un ciclo en el grafo
  almacenado (`tickets.go:747-764`); `ValidateDeps` revisa el grafo COMPLETO a
  nivel publish (forward references que no resolvieron) (`tickets.go:699-725`).
- **Dep inexistente**: `AddDep` exige que cada dep resuelva a una historia real
  (`ErrDepNotFound`, `tickets.go:664-676`).

---

## Modo sprint vs modo historia (execution_unit)

Cada proyecto se ejecuta bajo su `execution_unit`, leído del control-plane
(`native.go:508-525`). `modeFor` cachea los ajustes una vez por ciclo (en
`modeCache`) para que todo el trabajo de un proyecto se evalúe con el mismo
`execution_unit` + `merge_mode`. Un ajuste ilegible cae a los **defaults seguros**
`sprint` + `manual` (`native.go:517-522`).

- **`execution_unit = "story"`**: un *run* y un PR por historia. `fireReadyStories`
  reclama cada historia (`Claim` atómico `backlog→running`) y dispara un *run* con
  el contexto completo de la historia (`native.go:980-1058`).
- **`execution_unit = "sprint"` (default)**: un sprint completo → **UN** *run* →
  **UN** PR. Es el "goal mode" (`native.go:606-610`, `native.go:1177-1259`).

`fireByProject` particiona `Ready()` y `ReadySprints()` por `project_id`, calcula
la unión de proyectos con trabajo listo, y para cada uno decide según su
`execution_unit`: si es `story`, dispara historias sueltas; si no, dispara los
sprints listos del proyecto (`native.go:559-613`).

### Readiness de un sprint

Un sprint está listo (modo goal) cuando: tiene **≥1 historia**, **todas** están
aún en `backlog` (ninguna empezó), y **toda dep que apunte FUERA del sprint** está
en `done` (`tickets.go:954-1016`). Las deps **intra-sprint** se resuelven dentro
del único *run*, así que nunca bloquean la readiness del sprint.

### El ticket combinado (goal mode)

`fireSprint` (`native.go:1177-1259`):

1. `ClaimSprint` reclama **atómicamente** todas las historias backlog del sprint
   en una transacción (`tickets.go:1018-1071`). Un claim perdido (otro scheduler
   ganó) corta a `false`.
2. `SprintStories` trae las historias en **orden topológico** intra-sprint
   (`StoriesBySprint` → `topoSortStories`, `tickets.go:857-942`). Un ciclo entre
   historias del sprint es un **error duro** (`ErrDepCycle`), no se degrada a orden
   por id (`tickets.go:895-942`).
3. Se construye el ticket: `sprintPreamble` (la cabecera "implementa todo el
   sprint en una pasada, una rama, un PR") + `renderSprintStories` (cada historia
   como una sección `### <id> — <title>`) (`native.go:1064-1079`,
   `native.go:1287-1303`).
4. Se dispara UN *run* y `MarkSprintRunning` graba el `run_id` en TODAS las
   historias del sprint (comparten un `run_id`) (`native.go:1244-1256`).

---

## Lane routing (enrutamiento por carril)

El *owner* de una historia define su carril (lane): el runner usa ese valor para
elegir el especialista por carril (`python-dev`, `react-dev`…); vacío cae a
`dev`.

- **Modo historia**: el `agent` del payload es directamente el `Owner` de la
  historia (`native.go:1002-1026`). Vacío → el runner usa `dev`.
- **Modo sprint**: `commonOwner` calcula el owner ÚNICO compartido por todas las
  historias del sprint (`native.go:1221`, `native.go:1267-1282`):
  - **Sprint mono-carril** (todas las historias del mismo owner) → se dispara como
    ESE especialista.
  - **Sprint multi-carril** (owners MIXTOS) → `commonOwner` devuelve `""` y
    **registra una advertencia**; el runner cae a `dev`. El sub-batching por
    carril está diferido a la Fase B2 (`native.go:1276-1279`).
  - Historias con owner vacío se tratan como `dev` para el acuerdo, así un sprint
    todo-sin-owner resuelve a `""` (default) sin un warning espurio de "mixed".

---

## El lazo de gating por merge (in_review → done)

Un *run* que termina solo significa que su PR está **ABIERTO**, no mergeado. Por
eso el trabajo pasa `running → in_review` (PR grabado) y se queda ahí hasta que
el PR **MERGEA**, momento en que avanza a `done` y desbloquea dependientes
(`tickets.go:1106-1112`).

### Parking: del run DONE a in_review

Cuando un *run* llega a `DONE`, `advanceRunning` lo enruta a
`parkOrFailStory`/`parkOrFailSprint` (`native.go:933-974`, `native.go:1116-1131`).
Antes de aparcar, `prGate` decide si el *run* produjo un PR usable (`native.go:830-852`):

- Si `gh == nil` (modo local sin GitHub), no se espera PR → se aparca tal cual.
- Con `gh != nil`, un *run* `DONE` DEBE producir una URL de PR de GitHub usable.
  Si el step `pr` falló, no hubo step `pr`, o el step tuvo éxito pero no grabó
  URL, entonces no hay nada que mergear nunca → aparcar en `in_review` colgaría
  para siempre (H1), así que el trabajo se marca **failed**
  (`native.go:843-851`, `native.go:854-873`).

### Reconcile: de in_review a done en el merge

`reconcileMerges` corre al inicio de cada ciclo (`native.go:655-688`). Para cada
historia `in_review` (agrupando un sprint goal-mode para reconciliarlo UNA vez,
ya que comparte un solo PR — `native.go:669-676`), aplica el `merge_mode` del
proyecto de ESA unidad y llama a `reconcileOne` (`native.go:693-741`):

- Si el PR está **mergeado** (por un humano en `manual`, o por nosotros en un
  ciclo previo) → `markMerged` lleva la unidad a `done` (`native.go:708-712`).
- Si **no** está mergeado y un humano lo **CERRÓ sin mergear** → es terminal: se
  marca **failed** (H2), siempre que el `gh` implemente el opcional
  `PRStateChecker` (`native.go:714-719`, `native.go:743-757`, `native.go:475-478`).
- Si `merge_mode != "auto"` (manual) → se espera a que un humano mergee en GitHub
  (`native.go:721-723`).
- Si `merge_mode == "auto"` → se mergea con `MergePR` (el *run* ya pasó gate +
  review). Un conflicto / branch-protection que hace fallar `MergePR` NO se
  resuelve reintentando, así que se acotan los intentos: tras `maxMergeFails = 3`
  fallos CONSECUTIVOS se marca la unidad **failed** (H2). Un éxito limpia el
  contador (`native.go:454-458`, `native.go:725-741`, `native.go:785-796`).

El estado al que avanza es **por unidad**: un sprint goal-mode avanza por
`sprint_id` (todas sus historias a la vez vía `MarkSprintDone`/`MarkSprintFailed`),
y una historia suelta por su `id` (`native.go:759-815`).

---

## Particionado por proyecto (multi-tenant)

Todo el ciclo opera **por proyecto** (audit A1/A2). El trabajo se agrupa por
`project_id` y cada grupo se evalúa con los ajustes de ESE proyecto
(`native.go:527-552`, `native.go:617-640`):

- `groupByProject` / `groupSprintsByProject` particionan tickets y sprints. Un
  `project_id` vacío (filas legacy sin backfill) cae al proyecto `default` para
  que aún disparen (`native.go:617-640`).
- Un proyecto python en `sprint+manual` y uno node en `story+auto` se manejan
  CORRECTAMENTE en el mismo ciclo (`native.go:531-538`).
- Los ajustes se leen frescos cada ciclo (cacheados solo dentro del ciclo), así
  que un toggle de `execution_unit`/`merge_mode` surte efecto sin reiniciar
  (`native.go:538-539`, `native.go:651-654`).
- El `project_id` de la historia se estampa en el payload del *run* para que el
  *run* de la fábrica quede scopeado (`native.go:1019-1026`, `native.go:1223-1231`).

El store soporta el scope con `ListStoriesByProject`, `ReadySprintsByProject`,
etc.; los `project_id` se migran/backfillean a `DefaultProjectID = "default"`
para que un backlog single-tenant sobreviva (`tickets.go:123-126`,
`tickets.go:175-232`).

---

## La máquina de estados de la historia

Los estados viven en la columna `status` (`tickets.go:17-28`). `ready` es
**derivado**, nunca almacenado. Las transiciones se guardan con la disciplina de
`legalSources` (`tickets.go:50-68`): cada estado destino escribible declara desde
qué estados almacenados puede llegar legalmente, y `transition` aplica ese
predicado `AND status IN (...)` para que una transición ilegal sea un no-op de 0
filas, traducido a `ErrIllegalTransition` (`tickets.go:436-475`).

Transiciones legales (`tickets.go:56-68`):

| Destino     | Fuentes legales                       | Disparador |
|-------------|---------------------------------------|------------|
| `running`   | `backlog`                             | `ClaimStory` (claim atómico) |
| `in_review` | `running`                             | run terminó, PR abierto (`MarkInReview`) |
| `done`      | `in_review`                           | PR mergeó (`MarkDone`) |
| `failed`    | `backlog`, `running`, `in_review`     | run terminal no-DONE, o PR cerrado/auto-merge agotado |

Notas de diseño:

- **`done` SOLO desde `in_review`**: el camino directo legacy `running → done` se
  prohíbe deliberadamente (`tickets.go:62-65`). Solo un PR mergeado (que primero
  aparca en `in_review`) llega a `done`. Esto cierra el bug B1: una historia que
  aún corre nunca se voltea a `done` por el merge de OTRA historia
  (`tickets.go:1073-1083`, `tickets.go:1122-1127`).
- **Estados terminales no se resucitan**: `done`/`failed` no son fuente legal de
  nada, así que un camino de completion stale no los arrastra de vuelta al lazo
  (`tickets.go:1085-1093`, `tickets.go:1129-1137`).
- **Compensación de claim (B3)**: `MarkBacklog` resetea SOLO una historia
  `running` con `run_id` vacío (reclamada-pero-no-disparada). Una historia que sí
  obtuvo `run_id` está ejecutando de verdad y no se reclama de vuelta
  (`tickets.go:528-553`). `UpdateStoryStatus` con destino `backlog` enruta aquí
  (`tickets.go:490-502`).
- **Salida del estado `failed`**: la máquina automática NO permite
  `failed → backlog`. La ÚNICA salida es la acción manual del usuario "Reencolar"
  (R2): `RequeueSprint` / `RequeueByRun` cruzan deliberadamente el edge prohibido
  y limpian `run_id`/`pr_url` para un fire limpio (`tickets.go:572-633`).

### Compensación B3/B4 en el scheduler

El scheduler complementa la máquina de estados con compensaciones ante fallos
parciales:

- **B3 (FireRun falla tras el claim)**: el claim ya volteó la historia a
  `running`. Si `FireRun` falla, se haría stranded (`running`, `run_id` vacío) y
  el lazo de completion la saltaría para siempre. Se compensa con `ResetClaim`
  (historia) / `ResetSprintClaim` (sprint) que la devuelven a `backlog`
  (`native.go:1027-1039`, `native.go:1232-1243`).
- **B4 (MarkRunning falla tras un FireRun exitoso)**: hay un *run* real
  ejecutando pero su `run_id` no se grabó. Se reintenta una vez; si sigue
  fallando, se resetea a `backlog` (el run se pierde pero la historia re-dispara
  en vez de quedar wedged) (`native.go:1044-1056`, `native.go:1244-1256`).
- **B4 (recuperación de stranded)**: si una historia/sprint corre con `run_id`
  vacío (la compensación se escapó, o un crash entre claim y record),
  `advanceRunningStories`/`advanceRunningSprints` la detectan y la resetean a
  `backlog` (`native.go:936-951`, `native.go:1105-1115`).

---

## Hallazgos de validación

Análisis adversarial del orquestador y el ticket store. Cada hallazgo cita
`file:line`.

- **"ready" es engañoso en modo sprint.** `fireByProject` siempre llama a
  `s.provider.Ready()` Y `s.provider.ReadySprints()` cada ciclo
  (`native.go:578-585`), pero en modo sprint las historias de un sprint NO
  aparecen en `Ready()` solo por estar en `backlog` con deps done — sí lo harían,
  porque `Store.Ready()` solo mira `status='backlog'` + `depsDone`
  (`tickets.go:811-849`) sin importar el `execution_unit`. Es decir, **las mismas
  historias que componen un sprint listo también aparecen como historias "ready"
  sueltas**. Lo que las salva del doble-fire es que en modo sprint `fireByProject`
  ignora `readyByProject[pid]` y solo dispara sprints (`native.go:599-611`), y que
  `ClaimSprint` y `Claim` compiten por las mismas filas. Pero el endpoint
  `GET /tickets?status=ready` que alimenta la UI mostrará esas historias como
  "ready" aunque el proyecto esté en modo sprint y nunca se vayan a disparar
  individualmente — la etiqueta "ready" no refleja la unidad de ejecución real.

- **`failed` no tiene salida legal automática.** `legalSources` no incluye
  ninguna transición HACIA `backlog` (`tickets.go:56-68`), y `done`/`failed` no
  son fuente de nada. Una historia/sprint en `failed` queda **terminal** hasta que
  un humano invoque `RequeueSprint`/`RequeueByRun` (`tickets.go:572-633`). Si esa
  ruta de UI/CLI no está cableada o el usuario no la conoce, el trabajo fallido se
  queda muerto en el board y, peor, **bloquea a todos sus dependientes para
  siempre** (un dependiente solo queda ready cuando su dep está en `done`,
  `tickets.go:1178-1193`; `failed` nunca llega a `done`). No hay reintento
  automático ni alerta — solo el log.

- **Desync historia/run cuando el kernel recupera un run internamente.** El
  scheduler decide el destino de una historia consultando `s.cp.RunStatus`
  (`native.go:952`, `native.go:1117`). Si el kernel recupera/reintenta un *run*
  internamente (cambia su estado sin que el scheduler intervenga), el scheduler
  solo ve el estado final. Pero el caso inverso es peor: una historia con `run_id`
  vacío se interpreta SIEMPRE como "stranded, resetear a backlog" (B4,
  `native.go:939-951`). Si el kernel disparó/recuperó un run para esa historia
  pero el `run_id` no llegó a grabarse en el ticket store, el scheduler la
  resetea a `backlog` y la **re-dispara**, produciendo un run duplicado para el
  mismo trabajo. No hay verificación de "¿existe ya un run para esta historia en
  el kernel?" antes del reset.

- **Mixed-lane degrada a `dev` en silencio (salvo un log).** `commonOwner`
  devuelve `""` y solo registra un `log.Printf` cuando un sprint tiene owners
  mixtos (`native.go:1267-1282`). El resultado: un sprint que mezcla, p.ej.,
  `python-dev` y `react-dev` se implementa COMPLETO por el agente `dev` genérico,
  perdiendo todo el routing por especialista. No hay error, ni flag en el ticket,
  ni nada visible en la UI — solo una línea de log que nadie lee. El sub-batching
  por carril está diferido a B2 (`native.go:1266`), pero mientras tanto un sprint
  mixto produce trabajo de peor calidad de forma callada.

- **El `merge_mode` por unidad se lee del proyecto, pero la primera lectura puede
  caer al default inseguro y NO se vuelve a intentar en el ciclo.** `modeFor`
  cachea el resultado por ciclo incluso cuando `ProjectSettings` **falló**
  (`native.go:508-525`): ante un error transitorio de la API, todo el trabajo del
  proyecto en ese ciclo se evalúa como `sprint`+`manual`. Para `manual` esto es
  seguro (no mergea), pero si el proyecto estaba en `auto`, **un blip de la API
  detiene los auto-merges de ese proyecto ese ciclo** sin señal alguna salvo el
  log (`native.go:512-514`). Se recupera el siguiente ciclo, pero el
  comportamiento "falla a manual" no es obvio para el operador.

- **`prGate` con fallo de inspección es permisivo: puede aparcar en `in_review`
  un run sin PR mergeable.** Si `RunPRResult` devuelve error, `prGate` cae al
  *probe* laxo `RunPRURL` y devuelve `ok=true` (`native.go:831-838`). Si ese probe
  también devuelve URL vacía pero sin error, el trabajo se aparca en `in_review`
  con `pr_url=""`. `reconcileOne` luego hace `return false` para PR vacío
  (`native.go:694-696`) — o sea, **se queda en `in_review` para siempre** sin
  avanzar ni fallar. El comentario asume que reconcile "reintentará el siguiente
  ciclo", pero si `RunPRResult` falla de forma persistente, nunca se promueve a la
  ruta H1 que lo marcaría failed.

- **`maxMergeFails` se cuenta en memoria y se pierde al reiniciar.** El contador
  `mergeFails` es un `map` en el struct del scheduler (`native.go:449-452`). Un
  reinicio del proceso (deploy, crash) **resetea los contadores a cero**, así que
  un PR con conflicto permanente vuelve a intentar auto-merge `maxMergeFails`
  veces de nuevo tras cada reinicio. En un entorno que reinicia seguido, el
  "fallar fuerte tras 3 intentos" (H2) nunca se alcanza.

- **PR cerrado-sin-merge solo se detecta si `gh` implementa `PRStateChecker`.**
  La detección de "humano cerró el PR" es vía type assertion opcional
  (`native.go:746-757`). Si el `MergeChecker` concreto NO implementa
  `PRClosed` (los fakes de test no lo hacen, y el comentario marca la
  implementación de producción como "FLAGGED for the github lane",
  `native.go:475-478`), un PR cerrado sin mergear **se sondea para siempre** en
  `manual` mode, sin avanzar nunca. El propio código admite que esta es la
  "pre-existing wait-forever behavior" (`native.go:471-474`).

- **Concurrencia entre schedulers: el reconcile no es exclusivo por unidad.**
  `RunOnce` puede ser llamado concurrentemente por varios schedulers sobre un
  store compartido (`native.go:448-452`). El claim de historias/sprints sí es
  atómico (`native.go:985`, `tickets.go:1018-1071`), pero `reconcileMerges` no
  toma ningún lock por PR: dos schedulers pueden ambos ver el mismo PR `in_review`
  en `auto` y ambos llamar `MergePR` (`native.go:728`). El segundo `gh pr merge`
  fallará (ya mergeado) y bumpeará `mergeFails` espuriamente, acercando a esa
  unidad a un `failed` indebido por una carrera benigna.

- **Un `project_id` vacío colapsa tenants distintos al `default`.** Tanto en
  agrupación (`native.go:617-640`) como en reconcile (`native.go:677-682`), un
  `project_id` vacío cae a `"default"`. Si por un bug de backfill historias de
  proyectos DISTINTOS quedaran con `project_id` vacío, todas se evaluarían bajo
  los ajustes del proyecto `default` — un cross-tenant silencioso. El backfill a
  `DefaultProjectID` (`tickets.go:227-232`) mitiga esto en migración, pero
  cualquier inserción posterior con `project_id` vacío reintroduce el riesgo (el
  default solo se aplica en `CreateStory`/`CreateSprint`, `tickets.go:336-338`,
  `tickets.go:287-289`).

- **`depsDone` trata "dep inexistente" como "no done", enmascarando datos
  corruptos.** Si una dep apunta a un id borrado/inexistente, `depsDone` devuelve
  `false` sin error (`tickets.go:1182-1184`). La historia **nunca** queda ready y
  desaparece efectivamente del flujo, sin señal. `ValidateDeps` lo detectaría
  (`tickets.go:699-725`), pero solo corre a nivel publish; un borrado posterior de
  una dep no re-valida, dejando dependientes en un deadlock silencioso.
