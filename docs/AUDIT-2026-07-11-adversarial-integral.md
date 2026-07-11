# Auditoría adversarial integral — Fluxo (aiuda-forge)

**Fecha:** 2026-07-11 · **Alcance:** el PRODUCTO completo (multi-tenant, funcional, autónomo), no una lista de features.
**Método:** workflow multi-agente adversarial — 3 mapeadores de recon → 6 lentes en paralelo (cada una con abogado del
diablo + premortem "es 2027, Fluxo murió") → **cada hallazgo verificado por 2 verificadores adversariales** (uno intenta
refutarlo, otro cruza severidad) → dedup → ranking. **84 agentes, 0 errores, ~5M tokens.** De todos los hallazgos generados,
**32 sobrevivieron** a la verificación adversarial. Investigación **READ-ONLY**: los cambios se PROPONEN, no se mergean.

Estado del código auditado: `main` (HEAD ~`9d0eca3`/`3f338e8`), con #38/#39/#40 ya **mergeados** (la premisa del encargo
los daba como abiertos; la buildabilidad contract-driven y el runner efímero ya están en `main` — se validó su efecto real).

---

## VEREDICTO

> **¿Es Fluxo HOY un producto multi-tenant funcional y autónomo? → PARCIAL, inclinado a NO en las dos patas que definen la tesis.**

Desglosado, sin cariño:

| Dimensión | Veredicto | Evidencia |
|---|---|---|
| **Fábrica de diseño (idea→spec→backlog)** | ✅ **Funciona de verdad** | marketpty: 24/44 stories mergeadas; design.yaml corre las 6 fases gateadas y aterriza issues + deps nativas. Es el activo real. |
| **Multi-tenant (aislamiento en runtime)** | ⚠️ **PARCIAL con brechas CRÍTICAS** | El aislamiento se cerró en DATOS (columnas `project_id`) pero NO en runtime: escritura cross-tenant (ARCH-1), dispatch cross-tenant (ARCH-3), y un plano de control global sin authz (SEC-1/SEC-2). Dos clientes de pago en la misma instancia HOY se corrompen o se atacan entre sí. |
| **Autónomo (idea→PR sin niñera)** | ❌ **NO** | Cada proyecto real necesitó intervención manual (requeues por DB, cerrar draft PRs huérfanos, quitar labels `agent:running`, re-aplicar `claude.yml`). El gate de merge es verde-pero-vacío (AUTO-3): el humano sigue siendo el único QA de integración y visual. La promesa "quitar al humano" no está entregada. |
| **Escala a N tenants** | ❌ **Techo bajo** | Un solo SQLite con `SetMaxOpenConns(1)` + un único goroutine conductor serial que recorre TODOS los tenants con llamadas GitHub bloqueantes cada 25s (ARCH-4/5). Muere entre docenas de tenants, mucho antes de cualquier límite de CPU. |

**Traducción:** Fluxo es hoy un **acelerador de entrega de agencia, single-tenant / operador-confiable, que funciona** —
NO todavía un SaaS multi-tenant autónomo seguro. La distancia entre las dos cosas son ~6 fixes quirúrgicos de seguridad/aislamiento
(baratos, el código ya tiene las variantes correctas al lado) + una decisión de posicionamiento (matar el self-serve).

---

## Los 5 riesgos que MATAN el producto

1. **[NEGOCIO] El wedge ya es gratis y open-source.** El "discovery→PRD→arquitectura→UI→backlog gateado que aterriza en
   issues + dependencias nativas" que la GTM llama "lo menos disputado, nadie lo hace" es un clon funcional de **GitHub Spec Kit**
   (MIT, gratis, 90k★, `taskstoissues`, 29 agentes) — que shippeó **antes** de la fecha de la propia GTM. Y **GitHub Agent HQ**
   entrega "ejecución multi-modelo gobernada en tu propio GitHub" *dentro* de la suscripción Copilot ($10-19). El diferenciador
   cobrado y el upsell del conductor están **commoditizados a $0**. (GTM-1, GTM-3, GTM-4)

2. **[SEGURIDAD] El plano de control es un blanco cross-tenant.** `PUT /registry/templates/file` no tiene ningún gate de rol/tenant:
   cualquier usuario registrado (o cualquiera que haga "Continue with GitHub") puede reescribir `claude.yml.tmpl` — el workflow de CI
   que Fluxo scaffoldea en **el repo de TODOS los tenants** — e inyectar un step que exfiltra `${{ secrets.CLAUDE_CODE_OAUTH_TOKEN }}`
   y un `GITHUB_TOKEN` con `contents:write` de cada víctima. Radio de explosión = toda la base de clientes. (SEC-1, amplificado por SEC-2/SEC-3)

3. **[ARQUITECTURA] Corrupción de datos cross-tenant en el hot-path.** La proyección escribe con `UPDATE stories SET status=?,
   pr_url=? WHERE id=?` **sin `project_id`**, contra una PK compuesta `(id, project_id)` con IDs que colisionan entre tenants —
   exactamente el patrón que el resto del store prohíbe explícitamente (`transitionScoped`/`SetStoryExternalRef` sí filtran por
   proyecto y citan "el incidente S11-01"). El tick de un tenant pisa el estado y el PR de otro. (ARCH-1, ARCH-3)

4. **[AUTONOMÍA] El humano pasó de autor a enfermero, y el gate no verifica.** El único mecanismo de rescate self-serve (Reencolar)
   rechaza el estado en el que realmente se traban (`running`), forzando ediciones a mano en `tickets.db` (AUTO-2). El gate de merge
   es **verde-pero-vacío**: `e2e-verify`/`provisioning-lint` son `continue-on-error` (reportan, no bloquean) y `ui-verify` se SKIPea
   siempre porque `{{app_path}}` nunca se renderiza — verificado en vivo en reservas-belleza PR #57 (AUTO-3). Es decir: se vende
   "calidad gobernada" y el humano sigue haciendo TODA la QA a mano. Y el FLAP `running↔ready` es un **patrón sistémico**, no un bug:
   liveness derivada de UNA lectura eventualmente-consistente que **defaultea a backlog**, aplicada con UPDATE crudo sin gate/histéresis/
   cooldown, y el mismo tick re-dispara un run pago fresco (ARCH-2). Cada fix del flap reveló el siguiente (label 2026-07-06 mató un
   síntoma y se volvió el bug de "eternamente stuck" dos días después).

5. **[ARQUITECTURA/CALIDAD] No hay historia de escala, y el "kernel sin metodología" es en parte mentira.** Un proceso, una conexión,
   un loop (ARCH-4/5). Y la jerarquía épica→sprint→story + el nombre del gate están **hardcodeados en Go** (`publish.go`, `gate.go`),
   violando la regla de oro #1 del propio `engine/CLAUDE.md`: cada stack/método nuevo es un release del kernel, no un edit de YAML (CQ-1).

---

## Los 32 hallazgos verificados (rankeados)

Estado: **CONFIRMED** = ningún verificador lo refutó · **PLAUSIBLE** = un verificador lo refutó/matizó, sobrevivió al otro.

### 🔴 CRÍTICOS

| ID | Estado | Hallazgo | Evidencia clave |
|---|---|---|---|
| **ARCH-1** | CONFIRMED | Corrupción de escritura cross-tenant: `SyncExternalStatus` hace un `UPDATE ... WHERE id=?` sin scope que el propio código prohíbe | `tickets.go:1125`; PK compuesta `tickets.go:525`; la guarda gemela advierte esto exacto en `tickets.go:1239-1241` |
| **SEC-1** | CONFIRMED | Endpoints `/registry/templates` sin authz de tenant/admin → cualquiera envenena el `claude.yml` de todos los tenants | `server.go:154-156` (sin wrapper de guarda) vs `server.go:192` (settings sí guardado); `registry.go:67-89` sin `requireRole`; `costura.go:59` auto-scaffold en `main` |
| **GTM-1** | PLAUSIBLE | El wedge entero ya existe gratis y open-source: GitHub Spec Kit | `GTM-2026-07-03:166` ("nadie lo hace") vs github/spec-kit (MIT, 90k★, `taskstoissues`, mismo orden de fases incl. "constitution") |

### 🟠 ALTOS

| ID | Estado | Hallazgo | Evidencia clave |
|---|---|---|---|
| **ARCH-2** | CONFIRMED | El FLAP es patrón sistémico: derivación memoryless que defaultea a backlog + UPDATE sin gate + re-dispatch en el mismo tick | `projection.go:354`, `tickets.go:1103-1135`, `app.go:804→846→852`, `dispatch.go:188-198` |
| **ARCH-3** | CONFIRMED | Dispatch cross-tenant: `Dispatch`/`issueNumbers` re-cargan con `GetStory(id)` sin scope | `dispatch.go:446,488`; `GetStory`→`WHERE id=? LIMIT 1` (`tickets.go:1143-1165`), su propio doc advierte el "incidente S11-01" |
| **ARCH-4** | CONFIRMED | Techo de escala: un goroutine conductor serial recorre TODOS los tenants con GitHub bloqueante por tick | `app.go:787-862` (`Projects.List()` de todos + `for` serial); ticker fijo 25s `app.go:647` |
| **ARCH-5** | CONFIRMED | SQLite `SetMaxOpenConns(1)` + scans por-tenant sin índice serializan todo el sistema | `tickets.go:400-405`; `WHERE project_id=?` sin índice (solo PK `(id,project_id)`, leftmost=id) + N+1 `loadDeps` |
| **SEC-2** | CONFIRMED | `PUT /settings` muta config global del plano de control (MCP, auth de agente, merge policy) sin authz | `server.go:165-166` bare; `putSettings` `server.go:1241-1258` sin `requireRole` |
| **SEC-3** | CONFIRMED | La llave maestra de la GitHub App (PEM + client/webhook secret) en un archivo plaintext en el host | `app.go:56-62` (0600, sin KMS/rotación); mintea tokens admin+secrets+contents de TODA instalación (`tokens.go:82-132`) |
| **UX-1** | CONFIRMED | La fábrica queda muda en el clímax: default `dispatch_mode='approve'` + cero handoff Studio→ejecución | `projects.go:167,452`; `app.go:835` (`continue` si no es auto); `StudioDocs.tsx:190,341` no linkea a `/tickets` |
| **AUTO-2** | CONFIRMED | Reencolar solo rescata `failed`; el estado real de trabado (`running`) está bloqueado → ediciones manuales de DB | `server.go:505-513`; `legalSources` sin arista `running→backlog`; `FINDINGS-2026-07-08:100-102` |
| **AUTO-3** | CONFIRMED | Gate de merge verde-pero-vacío: verificación de integración y visual nunca bloquean el merge | `e2e-verify.yml.tmpl:29` `continue-on-error`; `ui-verify` SKIP por `{{app_path}}` no renderizado (`costura.go:193-217`); vivo en `ANALYSIS-2026-07-08:96-107` |
| **AUTO-4** | CONFIRMED | Liveness de Copilot sobre una API preview frágil; un 404 = "sin veredicto" → sesiones muertas nunca se liberan | `projection.go:296-304`; `FINDINGS-2026-07-08:36-51` (Bug A + Bug B verificados en vivo) |
| **CQ-1** | CONFIRMED | El refactor de "kernel sin metodología" nunca ocurrió: jerarquía épica/sprint/story y nombre del gate hardcodeados en Go | `publish.go:22-56,135-199`; `gate.go:36 const GateFile`; el propio `CLAUDE.md` lo lista como PENDIENTE |
| **GTM-3** | PLAUSIBLE | Fluxo es una TERCERA factura obligatoria sobre las dos suscripciones que exige que el comprador ya tenga | `GTM:143,151,124-126`; Copilot Pro $10 / Business $19 llena el "hueco" nativamente |
| **GTM-4** | PLAUSIBLE | Cliff de activación: no puede entregar su promesa core para la mayoría del ICP, y el canal primario (Copilot) está roto hoy | `GTM:180-182`; `CLAUDE.md` pendiente #1 (Agent tasks API 403); `ADR:176-179` |
| **UX-2** | PLAUSIBLE | La señal `claude` de pre-vuelo es un falso-verde: chequea el env del server de Fluxo, no el secret del repo | `onboarding.go:73` (env del server) vs `dispatch.go:193` (secret del repo); sin campo `copilot` |
| **UX-3** | PLAUSIBLE | "Instalar la GitHub App" —paso obligatorio del journey— no está secuenciado ni verificado; el login OAuth solo ya pone `github:true` | `onboarding.go:68-72` (solo token OAuth); `StudioEntry.tsx:101` solo ofrece OAuth, nunca instalar App |
| **CQ-2** | PLAUSIBLE | `legalSources` es un falso choke-point único: 16 literales SQL de status crudos saltan las constantes tipadas y la tabla central | `tickets.go:24-36` (constantes) vs 16 sitios raw `status='...'` (`1349,1405,...,2424`) |

### 🟡 MEDIOS

| ID | Estado | Hallazgo |
|---|---|---|
| **SEC-4** | CONFIRMED | Manifest de la GitHub App pide permisos no-mínimos (`administration:write`, `secrets:write`, `workflows:write`) |
| **SEC-5** | PLAUSIBLE | `workflow_approval auto_if_safe` solo inspecciona `.github/workflows/**`, pero el CI ejecuta scripts del repo fuera de ese path |
| **SEC-6** | CONFIRMED | `GET /projects/{id}/settings` filtra la postura operativa de un tenant sin chequeo de ownership |
| **UX-4** | CONFIRMED | No existe wizard de onboarding y "launch" no gatea en nada: se puede correr un design run completo con cero canal de ejecución listo |
| **UX-5** | PLAUSIBLE | La razón del capacity-block en auto-dispatch es solo-log: nunca llega a la UI |
| **ARCH-6** | PLAUSIBLE | Los gates fail-open degradan a inseguro justo bajo la presión de rate-limit que crea la escala |
| **CQ-3** | CONFIRMED | Config decorativa muerta: `approval: risk-policy` en el step de PR se descarta en silencio y engaña al lector |
| **CQ-4** | PLAUSIBLE | `tickets.go` es un God file de 2526 líneas (state machine + publish + claim/reset + sprint bulk + sessions + codegraph) |
| **AUTO-6** | PLAUSIBLE | Los repos ya scaffoldeados requieren re-aplicar `claude.yml` a mano para ganar los steps del label de liveness |

### ⚪ BAJOS

| ID | Estado | Hallazgo |
|---|---|---|
| **UX-6** | CONFIRMED | i18n a medias y los docs shippeados aún llevan la marca VIEJA ("Forja", no "Fluxo") |
| **UX-7** | CONFIRMED | Un fallo parcial de launch orfana el proyecto y atrapa el retry detrás de un 409 |
| **CQ-5** | CONFIRMED | `publish.go` reimplementa `strings.Contains`/`Index` a mano con el paquete `strings` ya importado |
| **SEC-7** | PLAUSIBLE | El login OAuth entrega el token de sesión en el fragment de la URL de redirect → fuga a logs de proxy e historial |

---

## Premortems por lente ("es 2027, Fluxo murió")

- **NEGOCIO:** el wedge se commoditizó antes de la primera factura. GitHub shipeó Spec Kit (gratis) + Agent HQ (bundleado en Copilot),
  así que "la fábrica de specs gateada con ejecución gobernada en tu GitHub" fue un `pip install` + un feature nativo. Nunca hubo un
  cliente de pago fuera de aiudalabs: quedó siendo una herramienta interna de agencia disfrazada de SaaS, y pedirle a un shop LATAM de
  3 devs una TERCERA factura ($49/asiento) sobre Copilot + Claude mató el funnel en el cliff de activación.

- **PRODUCTO/UX:** el demo era mágico y el último kilómetro invisible. El tenant nuevo llegaba idea→PRD/arquitectura/mockups hermosos,
  aprobaba los gates, publicaba el backlog — y **no pasaba nada**, porque encender un canal de ejecución era un paso manual, sin
  secuenciar, enterrado en Settings. La única señal de pre-vuelo (`caps.claude`) era un falso-verde que miraba el env del server de
  Fluxo, no el repo del cliente. Conclusión del usuario: "solo hace docs". Churn en el trial.

- **ARQUITECTURA:** murió en la costura entre "anda con 2 proyectos en demo" y "corre para 50 tenants de pago". Todo el sistema
  multi-tenant era un SQLite tras `SetMaxOpenConns(1)` y un goroutine conductor serial con GitHub bloqueante cada 25s. Los UPDATE
  `WHERE id=?` sin scope dejaban que el tick de un tenant corrompiera las stories del otro. Sin historia de escala horizontal: todo el
  estado y todo el scheduling en un proceso, una conexión, un loop.

- **CALIDAD:** el "kernel determinista que no sabe nada de metodología, todo el método vive en el registry" era el moat entero y la
  pitch de venta — y nunca fue verdad. La jerarquía épica→sprint→story está horneada en `publish.go`, el nombre del gate es un `const`,
  16 literales de status a mano en SQL. "Editá YAML, no código" se volvió una mentira que los clientes notaron.

- **SEGURIDAD:** un solo tenant armó el plano de control compartido. El editor de templates nunca se re-gateó para el pivote
  multi-tenant: cualquier usuario reescribía el `claude.yml` que Fluxo scaffoldea en TODOS los repos, e inyectaba un step que exfiltraba
  el token de Claude y un `GITHUB_TOKEN` con `contents:write` de cada víctima. Amplificado por la llave maestra de la App en un archivo
  plaintext en el host. Aislamiento airtight en la capa de DATOS, ungoverned en el registry y la llave maestra.

- **AUTONOMÍA:** la "fábrica autónoma" nunca corrió sola: cada proyecto real necesitó un humano para destrabarlo. La maquinaria de
  recuperación perseguía síntomas más rápido de lo que convergía (el label que mató el flap se volvió el bug de "eternamente stuck" dos
  días después). Y el gate de merge era verde-pero-vacío, así que el humano revisaba cada PR a mano igual. Los usuarios hicieron la
  cuenta: niñear stories trabadas + revisar cada PR sin verificar era más lento que correr Claude Code a mano. El humano pasó de autor a enfermero.

---

## LA recomendación (una sola)

> **Repivotear Fluxo de "SaaS horizontal self-serve" a "plano de control de entrega para agencias LATAM, español-first",
> vendido como engagement de equipo/agencia (o done-with-you) — y ANTES de meter un segundo tenant, cerrar las ~6 brechas
> quirúrgicas de aislamiento/seguridad. Dejar de competir con GitHub en ejecución y en autonomía-total; vender los GATES
> HUMANOS y la TRAZABILIDAD cliente-facing como el producto, no como una limitación.**

### Qué MATAR primero
1. **El tier self-serve de $49/asiento** y la ambición de SaaS horizontal "ahora". La captura de valor está invertida (regala el
   diferenciador, cobra la commodity) y es una tercera factura para un trabajo que GitHub ya hace en una. El propio ADR dice: productizar
   *después* de N casos entregados.
2. **La ambición de "dueño de la ejecución"** (el conductor multi-modelo como diferenciador cobrado). Eso es Agent HQ, bundleado. Concedelo.
3. **La narrativa de autonomía-total** ("quitamos al humano"). No está entregada y el mercado castiga la promesa incumplida. Reencuadrá:
   los gates humanos SON el producto para una agencia que le rinde cuentas a un cliente.
4. **El scope-creep de multi-app / full-stack** hasta que el motor de una-app sea sólido.

### Qué ARREGLAR YA (bloqueante para cualquier 2º tenant — todo quirúrgico, el código ya tiene la variante correcta al lado)
- **ARCH-1 + ARCH-3**: agregar `AND project_id=?` a `SyncExternalStatus` y usar `GetStoryInProject` en el path de dispatch (ambas
  variantes scoped **ya existen** — `transitionScoped`, `GetStoryInProject`). Es threading de un `projectID` que la proyección ya tiene.
- **SEC-1 + SEC-2 + SEC-6**: gatear `/registry/**` y `/settings` (mutaciones) detrás de un rol operador/admin de instancia (no per-tenant);
  hacer los templates que van a repos de tenant **read-only, deployados con el binario**. Es el fix más barato con el mayor radio de daño evitado.
- **SEC-3**: mover PEM/secrets a un secret manager, cargar en memoria, rotación.
- **ARCH-2 (el flap, la causa de fondo recurrente)**: nunca dejar que UNA lectura demote `running→backlog`; exigir N observaciones
  negativas consecutivas o un dwell mínimo (histéresis/cooldown), y gatear el re-dispatch en la ausencia de un `story_session` vivo, no
  solo en `status==backlog`. No borrar la sesión en una proyección backlog cruda.
- **AUTO-3 (el gate verde-vacío mina lo único que vendés)**: poblar `app_path`/`design_tokens` en `scaffoldVarsFor`; una vez que
  `e2e/ui-verify` corran verde de verdad, sacarlos de `continue-on-error`. Sin esto, "calidad gobernada" es falso.

### Qué DOBLAR
- **La fábrica de specs gateada como ENTREGABLE de cliente**: brief vago en español → discovery→PRD→arquitectura→UI→backlog gateado que
  aterriza como Issues con grafo `blocked_by`, **con un trail de trazabilidad requisito→issue→PR que la agencia le muestra al cliente**.
  Eso es lo que Spec Kit (CLI solo-dev, un IDE) NO da: plano de control hosted + gates de gobernanza + artefacto auditable presentable.
- **Answering conversacional de los open-questions de cada gate** (backlog #3) — el gate ES el producto; hacelo excelente.
- **Español-first en la elicitación** — donde las herramientas GitHub-native, inglés-first, subatienden.
- **El motor de recuperación** hasta que converja (menos intervenciones por proyecto, medido), no whack-a-mole.

### Qué DIFERIR (real, pero no bloquea el motion de agencia)
- Postgres + SKIP-LOCKED + sharding (ARCH-4/5): es un techo real, pero recién muerde con docenas de tenants — que no vas a tener hasta
  tener casos. Agregá el índice `stories(project_id)` ahora (barato) y planeá el resto para cuando el pipeline lo exija.
- El refactor "kernel 100% methodology-free" (CQ-1): deuda real, no bloquea el motion de agencia (una metodología, un stack a la vez).

---

## El ICP más vendible (determinado con evidencia)

**ICP recomendado:** la **agencia / dev shop boutique LATAM (español-first), 3-20 devs, que entrega software a la medida a clientes
externos de pago sobre GitHub** — vendida como engagement de equipo/agencia (**Team $199-500/mo o done-with-you**), NO como asientos
self-serve de $49. Paso 0: aiudalabs lo dogfoodea en su propia entrega (como dice el ADR: productizar recién tras N casos). Los primeros
compradores externos son agencias LATAM pares que confían en la relación con aiudalabs.

**Por qué (el valor que sobrevive a la commoditización para ESTE comprador):** no es el artefacto de spec (Spec Kit lo da gratis) ni la
ejecución model-agnostic (Agent HQ la da en la sub de Copilot que ya pagan) — es la **metodología de entrega hosted, multi-tenant y
gateada que produce un entregable de trazabilidad CLIENTE-FACING (trail requisito→issue→PR) en español**, contra el cual la agencia
factura y que le muestra al cliente. La agencia (1) tiene clientes externos que exigen proceso y trazabilidad → el audit trail es valor
de confianza facturable, no vitamina; (2) opera repos multi-tenant de clientes → el plano hosted + gates gobernados le ganan a un CLI
per-dev; (3) es español-first, donde las tools GitHub-native subatienden; (4) el producto **ya funciona** para este workflow (marketpty: 24/44).

**Disposición a pagar:** real pero con forma de **equipo/agencia, NO de asiento**. Rechazar el tier $49/asiento (WTP ~$0 una vez que el
comprador ve Spec Kit + Agent HQ). Defensible: plan Team/Agencia **$199-500/mo** (ancla con el Team $199 de la GTM, subcotiza el Devil
Team $500). Se justifica por (a) desplazamiento de labor (escribir 40 issues bien especificados + grafo de deps a mano = varios dev-days/
proyecto) y (b) trazabilidad cliente-facing que gana/retiene confianza y se adjunta a la factura. Medir solo el COGS real (design-runs de
Anthropic) como overage; **nunca revender compute de ejecución**. Primeros dólares como **done-with-you / servicio productizado**, no funnel de tarjeta.

**El wedge (una frase):** *"Convertí el brief de tu cliente en un backlog gobernado y trazable que tu cliente firma — en español, en su
propio GitHub."* El diferenciador vs Spec Kit gratis NO es el artefacto (idéntico) sino el envoltorio: plano hosted + gates humanos +
trazabilidad presentable + elicitación español-first. NO liderar con el conductor multi-modelo (eso es la commodity de Agent HQ) — es upsell.

### Competencia (julio 2026, con precio)

| Competidor | Posicionamiento | Precio | Amenaza |
|---|---|---|---|
| **GitHub Spec Kit (+Copilot)** | Toolkit SDD open-source; brief→spec→backlog ordenado por deps, 29 agentes | **Gratis** (corre sobre Copilot $10-19 que ya tienen) | **Existencial** — clon casi exacto del wedge, gratis, el SDD que más crece en 2026 |
| **GitHub Agent HQ / Copilot agent** | Agentes multi-modelo ejecutando en tu propio repo, con mission control | Sin surcharge sobre Copilot $10-19; créditos ~$0.01 | **Existencial al conductor** — ES "ejecución gobernada model-agnostic en tu GitHub", en una factura. No competir acá |
| **Lovable / Bolt / v0** | Vibe-coding idea→app, cero-SDLC | $20-25 | Baja para el ICP agencia; dueños del segmento indie → **rechazar ese segmento, no pelearlo** |
| **Devin (Cognition)** | Ingeniero de software autónomo, metered | Core $20 PAYG; Team $500/mo | Media — ancla el techo "ingeniero autónomo", ahora dentro de Agent HQ; sin fábrica de specs gateada |
| **Factory.ai / Cursor** | Droids agénticos / IDE agéntico | Factory $20/$100; Cursor $20/$40-120 | Media-baja — productividad dev, no gobernanza de entrega a cliente |

### ICPs a RECHAZAR
- **Indie hacker / founder no-técnico** (idea→app): dueño de Lovable/Bolt/v0 a $20-25 sin fricción GitHub; los prerequisitos de Fluxo son un cliff fatal.
- **Dev solo que solo quiere mejores specs**: Spec Kit da el backlog idéntico gratis en su IDE; no hay trabajo pago acá.
- **Enterprise / plataforma**: presión build-vs-buy, estandariza en Agent HQ+Spec Kit, ciclos largos de SSO/seguridad, Fluxo no tiene moat ni SOC2/escala.
- **SaaS horizontal self-serve a $49/asiento** (la tesis primaria de la propia GTM): captura de valor invertida, tercera factura, y el ADR mismo dice que no debería existir standalone hasta tener casos.

---

*Generado por auditoría adversarial multi-agente (84 agentes, 6 lentes, 2× verificación adversarial por hallazgo). Read-only:
los fixes están PROPUESTOS, no aplicados. La landing page propuesta vive en `/landing/index.html`.*
