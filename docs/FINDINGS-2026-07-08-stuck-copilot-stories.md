# Hallazgos — stories atascadas en `running` tras muerte de Copilot (2026-07-08)

**Contexto:** reservas-belleza (demo). Se lanzó una iteración de rediseño de UI que produjo
5 stories visuales (`S-visual-5..9`, sprint `SP-visual-2`, lane flutter-dev). Copilot se
quedó sin créditos a mitad del sprint. Las 5 stories quedaron **atascadas en `running` y no
se liberan solas**, incluso después de minutos. Diagnóstico en vivo contra el VPS (main).

## Estado observado (read-only)
- Las 5 stories: `status = running`, y **todas con `pr_url = /pull/56`** — Copilot abrió
  **un único draft PR #56** ("entry screen onboarding", WIP, +980/−352) anclado a las 5.
- Los agent runs de Copilot terminaron en `failure`/`cancelled` (no colgados en `in_progress`).
- El Conductor tickea cada ~25s y consulta el estado de 3 tasks de Copilot. **La API devuelve
  404 para las 3:** `gh api agent task <uuid>: exit status 1: {"message":"not found"}`.
  (task ids: `c1d38e20-…`, `ceea33a3-…`, `5d809f99-…`)

## ⚠️ CAUSA RAÍZ REAL (descubierta al desatascarlo) — el label `agent:running`
Al intentar moverlas a `ready` se reveló que el ancla PERSISTENTE no era el 404 del sweep,
sino el **label `agent:running`** pegado en los 5 issues. La derivación de la proyección
(`projection.go` ~354) es: `target := backlog`; luego lo sube a `running` si hay **draft PR**,
**agent assignee**, **label `agent:running`**, o **sesión de task**. Los 5 issues tenían el
label puesto. El workflow lo pone al arrancar y lo quita en un step `if:always()` — pero
**Copilot murió (créditos) sin ejecutar esa limpieza**, así que el label quedó pegado →
`running` eterno. Había **3 anclas** que soltar: (1) draft PR #56 [ya cerrado], (2) la sesión
de task [DELETE en story_sessions], (3) **el label `agent:running` [el persistente]**. Recién
al quitar el label la proyección derivó a `backlog`. **El 404 del sweep es real pero
secundario:** aunque el sweep funcionara, el label igual trababa las stories.
- **Fix de raíz (además del de la Obs. D):** cuando una sesión de agente se determina muerta
  (task fail, run `failure`, o un Copilot sin cleanup), hay que **quitar el label
  `agent:running`** (limpieza GitHub-observable), no solo limpiar la sesión local. Hoy el label
  solo se quita desde el workflow del agente en `if:always()`; si el agente muere por fuera de
  ese path (créditos, kill), nadie lo quita → la story queda `running` para siempre. El
  Conductor debería quitar el label como parte del barrido de sesiones muertas.

## Por qué NO se auto-liberan (dos causas, cada una suficiente)

### Bug A — el barrido trata el 404 como "sin veredicto" y no libera
`internal/conductor/projection.go` (barrido de sesiones muertas): solo marca una sesión
muerta si el estado es **explícitamente** `failed`/`cancelled`/`error`. Cuando la lectura
del estado **falla** (acá, 404 "not found"), hace `state = ""` ("best-effort: sin veredicto
no tocamos nada") → la story **nunca** se marca muerta → queda en `running` indefinidamente.
- **El fondo:** un 404 "not found" para una task que ESTABA corriendo significa que la task
  **ya no existe** (Copilot la limpió tras fallar) — es decir, la sesión está **muerta de
  hecho**. Tratar "task inexistente" como "no sé, no toco" es lo que deja la story colgada.
- **Fix propuesto:** distinguir *error transitorio* (red/5xx/timeout → reintentar, no tocar)
  de *task definitivamente ausente* (404 not-found → tratar como muerta → liberar la story).

### Bug B — el barrido solo libera stories SIN PR
Aunque el barrido obtuviera veredicto de muerte, solo devuelve a `backlog` las stories **sin
PR**. Las 5 cuelgan del draft #56 → quedarían atascadas igual. Una sesión muerta cuyo único
"progreso" es un **draft WIP abandonado** debería ser recuperable (p. ej. si el PR sigue
`draft` y su agente murió, liberar la story y/o cerrar el draft).

### Observación C — un PR de Copilot anclado a 5 stories
La proyección asoció las 5 stories del sprint al **mismo** PR #56. Si esto es esperado
(Copilot hace 1 PR por tanda) o una mis-asociación, el efecto es que **un solo PR muerto
bloquea 5 stories**. Vale entender la regla de anclaje story↔PR de Copilot.

### Observación D — por qué el 404 (causa raíz, no es sobre los issues)
La consulta es (`internal/github/dispatch.go` `AgentTaskState`):
```
GET /agents/repos/{owner}/{repo}/tasks/{uuid}   (X-GitHub-Api-Version: 2026-03-10)
```
Cuatro puntos que explican el 404:
1. **Son dos APIs distintas.** Los issues viven en `/repos/{o}/{r}/issues/{n}` — existen, están
   bien. El barrido NO consulta el issue: consulta el registro de la **agent-task de Copilot**
   en `/agents/repos/.../tasks/{uuid}`. Que el issue exista no dice nada del registro de la task.
2. **Es una API en PUBLIC-PREVIEW** pinneada a `2026-03-10` (el código lo dice: *"the Agent
   tasks public-preview shape we tested live"*). Las preview de GitHub cambian sin aviso →
   síntoma clásico: *funcionó al probarlo, ahora 404* porque el path / esquema de id / conducta
   derivaron.
3. **La task es efímera.** El uuid se extrae de la URL web de la sesión (`github.com/.../tasks/
   <uuid>`, `taskIDRe`). Muy probablemente, cuando el run de Copilot termina (completed/failed/
   sin créditos), ese registro deja de ser consultable por id → 404. O sea el 404 es, de hecho,
   **señal de que la task murió** — pero el código lo trata como "sin veredicto".
4. **No es 403.** El permiso Copilot de la App está habilitado (hay test separado para el 403).
   Es 404 "not found": el registro no está en ese path. Problema distinto (endpoint/preview/
   efímero), no de permiso.

**El defecto de fondo es DE DÓNDE saca la señal de vida.** El barrido depende de esta preview
frágil; cuando da 404 no puede distinguir "muerta de verdad" de "la API se rompió" → no toca
nada → la story cuelga. **Y la señal confiable ya existe y la observamos:** `gh run list`
mostró el workflow **"Running Copilot cloud agent" → `failure`/`cancelled`** — observable,
estable, no-preview. Para **claude_action** el barrido ya NO usa ninguna API frágil: usa el
label GitHub-observable **`agent:running`** (resuelto 2026-07-06), justo para evitar esto. Para
Copilot sigue colgado de la Agent Tasks preview API.
- **Fix de raíz:** derivar vida/muerte de Copilot del **estado del workflow run** (`failure`/
  `cancelled` = muerto) o de un **label observable**, NO de `/agents/.../tasks/{uuid}`. Mismo
  patrón que ya se aplicó a claude_action.

### Observación E — el agotamiento de créditos no dispara failover de canal
El probe de canal verifica que existan workflow+secret, no el saldo de créditos. Copilot
"sin créditos" no se detecta como canal caído → no hay failover automático a Claude. (Baja
prioridad en este setup: el dispatch es **manual** — el humano elige el canal por clic.)

## Cómo resolver AHORA (operativo, para este demo)
Como el dispatch es manual, no hay loop; pero **las 5 no se liberan solas**. Palancas:
1. **El draft PR #56 es el ancla de las 5.** Cerrarlo (sin mergear) es lo que debería
   devolver las stories a un estado despachable — *a confirmar según la lógica de proyección
   ante un PR cerrado-no-mergeado*.
2. Si cerrar #56 no las libera (por el Bug A/B), haría falta un **requeue manual** de las 5
   (`running → backlog`), hoy no cubierto por el botón Reencolar (que es `failed → backlog`).
3. Con las stories en `backlog`/`ready`, el humano las **despacha a claude_action** (canal
   ya listo: `CLAUDE_CODE_OAUTH_TOKEN` sembrado + `claude.yml` presente).

## Cómo resolver DE RAÍZ (Fluxo)
- **Bug A:** en el barrido, tratar 404/not-found de la Agent tasks API como **sesión muerta**
  (con reintento corto para descartar transitorios), no como "sin veredicto".
- **Bug B:** permitir liberar/recuperar una story cuya sesión murió aunque tenga un **draft
  WIP** (o cerrar el draft huérfano automáticamente).
- **Reencolar desde `running`:** permitir el requeue manual de una story `running` atascada
  (transición `running → backlog`), no solo desde `failed`.
- **Observación D (el fix clave):** dejar de depender de la Agent Tasks preview API para la
  liveness de Copilot. Derivar vida/muerte del **estado del workflow run** ("Running Copilot
  cloud agent" → `failure`/`cancelled`) o de un **label observable** — el MISMO patrón que ya
  se aplicó a claude_action (`agent:running`, 2026-07-06). Esto arregla A y D de un saque: un
  run `failure` es un veredicto de muerte inequívoco y estable, sin 404 ni preview frágil.
- **Failover por saldo (E):** opcional — detectar "sin créditos" como canal no disponible.
