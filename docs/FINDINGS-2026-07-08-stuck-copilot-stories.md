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

### Observación D — 404 en `gh api agent task <id>` (¿endpoint o id?)
No es un 403 de permiso (el permiso Copilot de la App está habilitado) — es 404 "not found".
Hay que confirmar si el problema es el *endpoint* que arma el código, el *formato del id*
(session id vs task id), o que la task **expira/se borra** tras fallar. Lead de investigación.

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
- **Observación D:** arreglar la consulta de estado de la task (endpoint/id) para que el
  barrido tenga datos reales.
- **Failover por saldo (E):** opcional — detectar "sin créditos" como canal no disponible.
