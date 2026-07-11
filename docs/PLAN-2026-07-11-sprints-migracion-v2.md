# Plan de sprints — migración de Fluxo v1 → v2 (agent-native)

**Fecha:** 2026-07-11 · **Insumos:** `AUDIT-2026-07-11-adversarial-integral.md`, `PLAN-2026-07-11-arquitectura-v2-agent-native.md`.
**Meta:** llevar Fluxo de "acelerador de agencia single-tenant que funciona pero necesita niñera" a "plano de control
multi-tenant seguro, con brain auditable, ejecución de factura única y preview en real-time" — el diseño de la landing/arquitectura.

## Principios de secuenciación (leer antes de crear los sprints)

1. **Endurecer ANTES de migrar.** Los 3 críticos + los altos de seguridad/aislamiento son quirúrgicos y viven en el Go de
   HOY. Se arreglan en horas y **bloquean un 2º tenant**. No esperamos la reescritura v2 para cerrarlos: Sprint 0 los tapa ya.
2. **Strangler, no big-bang.** Cada sprint deja el producto corriendo y vende por sí mismo. El kernel Go se estrangula, no se dinamita.
3. **El brain primero (es el moat).** Es aditivo, no toca el kernel vivo, y empieza a acumular el activo auditable desde el día 1.
4. **NO dogfoodear la migración con el factory v1.** Construir v2 —que arregla la corrupción cross-tenant y el flap— usando el
   factory v1 que TIENE esos bugs es circular y peligroso. v2 se construye a mano (con Claude Code), no vía el conductor v1.
5. **Determinismo donde el gaming/error es barato** (aislamiento, estado, liveness) **= código/RLS**; **juicio = agentes/markdown**.
   No mover identidad/aislamiento/estado a un LLM: reintroduce el flap y la fuga cross-tenant, pero no-deterministas.

Formato de historia: `[ID] título — closes:<hallazgo> · files:<archivos> · AC:<criterio verificable>`.

---

## SPRINT 0 — Hardening crítico (kernel Go de hoy · aditivo · NO bloquea v2)
**Objetivo:** parar la sangría de seguridad/aislamiento que bloquea un 2º tenant. Todo quirúrgico; la variante correcta ya existe al lado.
**Vende:** "Fluxo es seguro para poner dos clientes en la misma instancia." Prerequisito de cualquier onboarding externo.

- `[S0-01]` **SyncExternalStatus scoped por project_id** — closes:ARCH-1 · files:`tickets.go`,`projection.go`,`dispatch.go` ·
  AC: firma `SyncExternalStatus(projectID,id,...)`; SELECT/UPDATE/DELETE con `AND project_id=?`; test: el tick del proyecto A con
  story `S1-01` no toca la `S1-01` del proyecto B (status ni pr_url ni story_sessions).
- `[S0-02]` **Dispatch usa GetStoryInProject** — closes:ARCH-3 · files:`dispatch.go` · AC: `buildPrompt`/`issueNumbers` cargan por
  `(projectID,id)`; test: el prompt embebe el nº de issue del proyecto correcto aunque exista misma-id en otro tenant.
- `[S0-03]` **/registry/** y /settings mutables tras rol operador/admin** — closes:SEC-1,SEC-2 · files:`server.go`,`registry.go`,`httpx` ·
  AC: usuario no-admin recibe 403 en `PUT /registry/templates/file` y `PUT /settings`; templates que van a repos de tenant son
  read-only deployados con el binario (no editables por tenant); test de autorización.
- `[S0-04]` **GET /projects/{id}/settings con ownership** — closes:SEC-6 · files:`server.go`,`access.go` · AC: sesión ajena → 403.
- `[S0-05]` **Llave de la GitHub App fuera de plaintext** — closes:SEC-3 · files:`app.go`,`ghauth.go` · AC: PEM/secrets desde
  secret-manager/env cifrado, en memoria; sin archivo 0600 servible; documentar rotación.
- `[S0-06]` **Histéresis anti-flap en la democión running→backlog** — closes:ARCH-2 · files:`projection.go`,`tickets.go`,`dispatch.go` ·
  AC: la democión exige N observaciones negativas o dwell mínimo; el re-dispatch se gatea por ausencia de `story_session` viva (no por
  `status==backlog`); no se borra la sesión en una proyección backlog cruda; test: un read-lag de 1 tick NO dispara un 2º run.
- `[S0-07]` **Gate de verificación deja de ser verde-vacío** — closes:AUTO-3 · files:`costura.go`,`registry/templates/**` · AC:
  `scaffoldVarsFor` puebla `app_path`/`design_tokens`; `ui-verify` deja de SKIPear; `e2e-verify` corre; (seguimiento en S4: quitar
  `continue-on-error`). Test: un repo scaffoldeado tiene `app_path` no vacío.
- `[S0-08]` **Requeue desde `running`** — closes:AUTO-2 · files:`server.go`,`tickets.go` · AC: transición legal `running→backlog` con
  confirmación del operador; endpoint acepta `running`; test.

**Definition of done Sprint 0:** `go test ./...` verde en `engine`; **test de fuga cross-tenant en CI** (dos proyectos, misma story id).

---

## SPRINT 1 — Brain a Postgres+RLS (el moat · aditivo · no toca el kernel vivo)
**Objetivo:** dejar de perder el conocimiento; empezar el activo auditable. Levantar el sustrato Supabase.
**Vende:** "registro auditable y presentable al cliente" — el diferenciador central de la landing.

- `[S1-01]` **Provisionar Supabase** — infra · AC: proyecto Postgres + Auth (GitHub OAuth) + Vault; envs; entorno de dev reproducible.
- `[S1-02]` **Schema `brain_events` (append-only) + RLS** — files:`supabase/migrations`,`_common/.fluxo` · AC: columnas
  `tenant_id,project_id,kind,payload,actor,ts`; policy `using (tenant_id = auth.jwt()->>'tenant')`; pgTAP: lectura cross-tenant rechazada.
- `[S1-03]` **Skill `brain-write` + tool MCP** — files:`registry/skills/brain-write.md`,`registry/…` · AC: un agente appendea una
  decisión/respuesta-de-gate/diseño-rechazado con un solo call; queda con provenance.
- `[S1-04]` **Provenance requisito→issue→PR al brain** — files:`costura.go`/hook,`publish` · AC: al publicar backlog y al mergear PR se
  escribe el link requisito↔issue↔PR; test: la cadena es reconstruible por query.
- `[S1-05]` **Brain explorer en la consola (realtime)** — files:`console/src/app/brain`,`lib` · AC: timeline por proyecto leyendo
  Supabase con RLS+Realtime; sin backend propio nuevo.
- `[S1-06]` **Test de fuga cross-tenant del brain en CI** — closes:(pre-req del moat) · AC: CI falla si una policy deja leer otro tenant.

---

## SPRINT 2 — Aislamiento a RLS + UI realtime (mata la clase de bug)
**Objetivo:** mover el estado de tickets a Postgres+RLS; el aislamiento pasa a ser una regla declarativa, no un WHERE a mano.
**Vende:** multi-tenant seguro de verdad + board en vivo sin polling.

- `[S2-01]` **Migrar `stories/runs/events/sprints` a Postgres + RLS** — closes:ARCH-1,ARCH-3,ARCH-5,SEC-1,SEC-2,SEC-6 (estructural) ·
  AC: cada tabla con `tenant_id`+policy; índices; pgTAP de aislamiento.
- `[S2-02]` **Borrar el scoping a mano** — files:`tickets.go`,`access.go` · AC: se eliminan los `UPDATE … WHERE id=?` crudos; el acceso
  va por RLS; lint/test que prohíbe `status='` y WHERE sin tenant fuera del helper.
- `[S2-03]` **UI board/grafo por Realtime** — closes:ARCH-4 (parcial) · AC: la consola se suscribe; se retira el polling del board.
- `[S2-04]` **Shadow-write/lectura durante la transición** — AC: feature-flag doble-escritura SQLite↔Postgres para no romper prod; plan de corte.
- `[S2-05]` **Test de fuga cross-tenant (stories/runs) en CI** — AC: verde obligatorio para mergear.

---

## SPRINT 3 — Maestro reconciliador (mata el flap de raíz)
**Objetivo:** reemplazar el conductor serial que pollea GitHub por un reconciliador determinista dirigido por webhooks, con histéresis.
**Vende:** "corre sola sin niñera" — menos intervenciones por proyecto (métrica).

- `[S3-01]` **Edge Function receptora de webhooks firmados** — files:`supabase/functions/webhook` · AC: recibe PR/checks/workflow_run;
  verifica firma; idempotente.
- `[S3-02]` **Máquina de estados en datos + reconciliador determinista** — closes:ARCH-2 · AC: la democión solo por evento terminal
  explícito (`workflow_run: failed/cancelled`); histéresis/cooldown; no-LLM.
- `[S3-03]` **Re-dispatch gateado por sesión viva** — closes:ARCH-2 · AC: no re-dispara si hay `story_session` activa; test read-lag.
- `[S3-04]` **Apagar el loop serial** — closes:ARCH-4 · AC: el conductor 25s se retira o queda como safety-sweep de baja frecuencia.
- `[S3-05]` **Liveness de Copilot por workflow_run** — closes:AUTO-4 · AC: 404 transitorio no marca "sin veredicto"; se deriva del run.

---

## SPRINT 4 — Ejecución v2 (factura única · gate real · preview Lovable)
**Objetivo:** managed keys, verificación que bloquea, y preview por branch.
**Vende:** el modelo de negocio (factura única) + calidad gobernada de verdad + "muestra el end-product".

- `[S4-01]` **Managed keys en Vault + inyección efímera + metering por tenant** — closes:SEC-3 (cierre) · AC: el job recibe la key como
  secret efímero; metering por `tenant_id`; cuota/abuso.
- `[S4-02]` **Verify como check REQUERIDO** — closes:AUTO-3 (cierre) · AC: e2e/ui-verify salen de `continue-on-error` y son required en
  branch protection; un PR con verify roja NO mergea.
- `[S4-03]` **Preview por branch (Vercel/CF) + embed** — files:`console`,`supabase/functions` · AC: cada PR tiene URL viva + logs; embebida en la UI.
- `[S4-04]` **Failover de canal por crédito/agotamiento** — AC: si un canal se agota, cae al alterno con señal en UI.

---

## SPRINT 5 — Agentes en markdown + método sin Go (cumplir la regla de oro)
**Objetivo:** el runtime de los agentes de diseño en el Agent SDK; sacar la metodología del Go.
**Vende:** "editá YAML, no código" deja de ser mentira; cada stack/método nuevo no es un release del kernel.

- `[S5-01]` **Agent SDK como runtime de diseño** — AC: los agentes de fase corren por SDK (rol .md + skills + tools MCP); se retira el step-runner Go.
- `[S5-02]` **Jerarquía y gate a data del registry** — closes:CQ-1 · files:`publish.go`→schema,`gate.go`→`gate_file` por step · AC:
  épica/sprint/story y nombre del gate salen de datos; un método kanban (sin sprints) no requiere tocar Go.
- `[S5-03]` **Resolver `$step.output.text` + limpiar config muerta** — closes:D2,D5,CQ-3 · AC: inputs de design.yaml no-op eliminados;
  `approval:risk-policy`/`skills`/`prompt` implementados o quitados.

---

## SPRINT 6 — Onboarding + GTM (llegar a vendible)
**Objetivo:** cerrar el last-mile del journey y el modelo de cobro de agencia.
**Vende:** el funnel completo: signup → primer proyecto → primer PR firmado.

- `[S6-01]` **Wizard de onboarding** — closes:UX-4,UX-3 · AC: Continue with GitHub → instalar App → semáforos de capacidad REALES →
  preset de autonomía → primer proyecto; no se puede lanzar un design run sin canal de ejecución listo.
- `[S6-02]` **Capabilities project-scoped** — closes:UX-2 · AC: probe del secret del repo + Copilot real; campo `copilot`; sin falso-verde.
- `[S6-03]` **Billing de agencia (factura única) + metering visible** — AC: plan Team; overage de compute transparente; BYO-key en Estudio+.
- `[S6-04]` **i18n completo + rebrand** — closes:UX-6 · AC: es/en; docs sin "Forja"; strings externalizados.

---

## Ruta crítica y dependencias

```
S0 (hardening, independiente)  ─┐
                                 ├─►  puede correr YA, en paralelo a S1
S1 (brain+RLS, aditivo) ─► S2 (aislamiento+realtime) ─► S3 (Maestro) ─► S4 (ejecución) ─► S6 (GTM)
                                                          S5 (método) ── paralelo a S3/S4
```
- **S0** no depende de la decisión de plataforma → arranca ya.
- **S1** requiere la decisión de plataforma (Supabase) → confirmar antes.
- **S2** depende de S1 (Postgres levantado). **S3** depende de S2 (estado en Postgres). **S4** depende de S3. **S6** cierra.

## Lo que NO hacemos todavía (deferido explícito)
- Sharding/multi-región: recién con docenas de tenants. Por ahora Postgres gestionado alcanza.
- Sandboxear los agentes de diseño en contenedor: después de que el runtime SDK esté estable.
- Multi-app / full-stack por proyecto: después de que el motor de una-app sea sólido.

## Última milla — piezas que el ejemplo end-to-end (Doña Rosa: app móvil + web) EXPONE

Verificación del ejemplo contra el código: el spine **diseño → PR → preview web** está bien considerado; la **"última milla a un
producto que el cliente usa" tiene gaps reales** que la arquitectura no nombraba. Rankeados por cuánto muerden:

- `[LM-1]` **Publicación móvil a tiendas — falta casi entero.** HOY `build-apk.yml` produce un APK firmado con clave **debug** como
  artefacto interno (`build-apk.yml.tmpl:62-64`: *"Sin keystore… firma con la clave debug… La firma de release real… es un follow-up;
  iOS… queda diferido"*). Falta: firma de release (keystore en secrets), **iOS** (runner macOS + signing Apple), y submission (App Store
  Connect / Play Console, metadata, review). Interino realista: **Firebase App Distribution / link de prueba** (aún sin cablear). · S4→S6.
- `[LM-2]` **Infra + entornos del app DEL CLIENTE — no existe.** No hay provisioning del backend del cliente (crear su Firebase/Supabase,
  entornos dev/preview/prod, secrets de la app, DNS/dominio como `bella-turnos.web.app`). El preview web (Vercel) y `e2e-verify` corren
  contra un backend **efímero/emulador**; publicar a prod necesita infra persistente. La arquitectura provisiona el sustrato de FLUXO
  (Supabase del brain), NO el del cliente. · S4.
- `[LM-3]` **Verificación VISUAL real (no solo "arranca") — aspiracional.** `ui-verify` usa Playwright/chromium y su propio comentario
  dice *"prueba que la app ARRANCA; nadie juzgaba si se VE"* (`ui-verify.yml.tmpl:177`). Falta el **juez-visión** (art-director vs
  mockup) y, para móvil, un device/emulador real (hoy es Flutter-web renderizado en chromium). Cierra AUTO-3 **de verdad**. · S4.
- `[LM-4]` **Multi-superficie completa — parcial.** El stack `aiuda-flutter-firebase` cubre customer-app + admin + backend, pero
  `ui-verify` valida **UN solo app primario** (`app_path` default `apps/customer`, `README:66`); el provider-app / web-admin no tienen
  verify propio, y la buildabilidad multi-app es fase-2. · S5.
- `[LM-5]` **Integración cross-lane e2e — no es un gate.** El grafo ordena lanes y `e2e-verify` corre el backend, pero *"el app
  desplegado habla con el backend desplegado"* no se verifica; el contrato de frontera (`provisioning.yaml`) es lint **estático**. · S4.
- `[LM-6]` **Change-requests con infra/terceros** (el "recordatorio por WhatsApp" del cierre): features nuevas necesitan servicios +
  secrets (WhatsApp Business API, push) que nadie provisiona — el mismo patrón de LM-2 a nivel feature. · backlog.

**Conclusión de la verificación:** la arquitectura consideró bien la **fábrica** (idea→código→PR→preview); **NO** consideró la
**entrega/operación del producto** (tiendas, infra y entornos del cliente, verificación visual/integración real). El ejemplo de Rosa es
honesto solo hasta el paso 7 (web preview); los pasos 8 (tiendas) y "publicado a prod" son objetivo, no realidad. Estas 6 piezas se
suman al plan (mayormente S4, algunas S5/S6).

## Decisiones abiertas (bloquean S1+)
1. **Apuesta de plataforma:** ¿Supabase (Postgres+RLS+Auth+Realtime+Vault) + Vercel/CF para preview? ¿o neutral/self-host Postgres?
2. **Punto de arranque:** ¿S0 (hardening) primero —recomendado— o directo a S1 (brain)?
3. **Cuenta/infra:** quién provisiona Supabase/Vercel y con qué billing.
