# Fluxo v2 — Arquitectura agent-native "orchestration-as-data"

**Fecha:** 2026-07-11 · **Autor:** diseño post-auditoría (`AUDIT-2026-07-11-adversarial-integral.md`) + discusión con el fundador.
**Objetivo:** una arquitectura que cumpla el principio **casi-cero-código-de-infraestructura** (el método vive en agents + skills +
markdown), con **UI + brain multi-tenant**, **deploy/preview en real-time tipo Lovable**, y que sea **realmente vendible** — y después
compararla con lo que tenemos hoy.

---

## 0. El principio, afilado (y su límite)

El deseo es "todo vive en agents, skills y markdown; casi cero infra". Correcto como **norte**, peligroso como **absoluto**. La
auditoría dejó una lección dura: *determinismo donde el gaming/el error es barato; agente donde hace falta juicio*. Si ponés la
**identidad, el aislamiento entre tenants, la máquina de estados y el ruteo de eventos** en manos de un LLM/markdown, no bajás
complejidad: reintroducís el flap (ARCH-2) y la corrupción cross-tenant (ARCH-1) **pero ahora no-deterministas**. Peor.

La reconciliación honesta del principio:

> **Cero código de infraestructura BESPOKE. El sustrato determinista NO se escribe — se ALQUILA como configuración declarativa
> (RLS, webhooks, preview deploys). Lo único que Fluxo escribe es el MÉTODO (markdown) y un pegamento fino. Los agentes hacen el
> TRABAJO y el JUICIO; nunca son la fuente de verdad del estado.**

Dos columnas, y la frontera entre ellas es el diseño entero:

| Determinista · declarativo · **alquilado** (no se escribe, se configura) | Agent · skill · **markdown** (lo único propio) |
|---|---|
| Identidad y tenant (OAuth + JWT con `tenant_id`) | El método: fases, qué hace cada rol, qué pregunta cada gate |
| **Aislamiento multi-tenant = RLS policies** (una regla, en la DB) | El trabajo: discovery, PRD, arquitectura, UI, código |
| Máquina de estados de stories (constraints/tabla de transiciones) | El juicio: verificación, review, art-direction |
| Ruteo de eventos (webhooks de GitHub, push, no polling) | La memoria: qué se escribe al brain y por qué (skill) |
| Deploy + preview real-time (plataforma) | El registro: cómo se presenta el conocimiento al cliente |

---

## 1. Las capas

```
 ┌───────────────────────────────────────────────────────────────────────────┐
 │  L3 · UI  (Next.js delgado — una VISTA sobre el brain, sin backend propio) │
 │  board · grafo de deps + click-para-despachar · brain explorer · preview   │
 │  live · gates conversacionales · lee Supabase con RLS + Realtime           │
 └───────────────▲───────────────────────────────────────────────▲───────────┘
                 │ realtime (websocket, sin polling)              │ embed
 ┌───────────────┴───────────────────────────────────────────────┴───────────┐
 │  L0 · SUSTRATO ALQUILADO (declarativo, cero código bespoke)                │
 │                                                                            │
 │  ┌─ Supabase ──────────────────┐   ┌─ GitHub ─────────┐  ┌─ Vercel/CF ──┐  │
 │  │ Postgres + RLS  = el BRAIN  │   │ repos (cliente)  │  │ preview deploy│  │
 │  │   multi-tenant (aislamiento │   │ Issues = backlog │  │ por branch/PR │  │
 │  │   = policy, no WHERE a mano)│   │ Actions = runtime│  │ URL viva +    │  │
 │  │ Realtime = proyección viva  │   │ del agente       │  │ logs stream   │  │
 │  │ Auth = GitHub OAuth → JWT   │   │ branch protection│  │ (Lovable-like)│  │
 │  │ Storage = artefactos        │   │ = gates          │  └──────────────┘  │
 │  │ Edge Functions = pegamento  │   └──────────────────┘                    │
 │  └─────────────────────────────┘                                          │
 └───────────────▲───────────────────────────────────────────────▲───────────┘
                 │ webhooks (push)                                │ dispatch / tokens
 ┌───────────────┴───────────────────────────────────────────────┴───────────┐
 │  L1 · MÉTODO  (registry: agents/*.md · skills/*.md · workflows/*.yaml)     │
 │  L2 · AGENTES (runtime):                                                    │
 │    · diseño → Claude Agent SDK (loop = SDK, no step-runner en Go)          │
 │    · ejecución → claude-code-action / Copilot en Actions                  │
 │    · "Maestro" = reconciliador DETERMINISTA (Edge Function, no LLM)       │
 └────────────────────────────────────────────────────────────────────────────┘
```

### L0 — Sustrato (lo que hoy Fluxo se construyó a mano, y que v2 alquila)

- **Supabase = el brain multi-tenant.** Postgres con **Row-Level Security**: cada fila lleva `tenant_id`/`project_id` y una *policy*
  `using (tenant_id = auth.jwt()->>'tenant')`. El aislamiento deja de ser un `WHERE project_id=?` que un dev olvida (ARCH-1/3) y pasa a
  ser **imposible de saltar**: la DB rechaza la lectura/escritura cruzada. **Una regla, declarativa, mata la clase entera de bugs.**
  - **Realtime**: la UI se suscribe a los cambios de `stories`/`runs`/`brain_events`. Adiós al conductor serial que pollea GitHub cada
    25s (ARCH-4) y al flap por lectura eventual (ARCH-2): el estado se empuja, no se deriva de una foto.
  - **Auth**: GitHub OAuth nativo → emite el JWT con el `tenant_id`. Cierra la costura de tokens hecha a mano.
  - **Storage**: mockups, snapshots de docs, screenshots de ui-verify.
  - **Edge Functions** (Deno/TS): el ÚNICO pegamento — receptor de webhooks, trigger de dispatch, minting de installation tokens,
    el reconciliador. Cientos de líneas, no 49k.
- **GitHub = sustrato de ejecución y entrega** (la apuesta que Fluxo ya tiene, se conserva): repos del cliente, **Issues = verdad del
  backlog**, **Actions = runtime del agente**, **branch protection = gates duros**, webhooks = eventos.
- **Vercel/Cloudflare = el "Lovable" real.** Preview deployment por branch/PR **out-of-the-box**: URL viva + logs de build en stream.
  El "deploy en real-time mostrando el end product" no se programa — es una integración de plataforma. La UI embebe el preview URL.

### L1 — Método (markdown/YAML: lo único verdaderamente propio)

- `agents/*.md` (roles), `skills/*.md` (capacidades reutilizables), `workflows/*.yaml` (ceremonias). **Ya existe en Fluxo** — pero en
  v2 es el PRODUCTO, no una capa de config sobre un kernel de Go.
- Nuevas skills clave:
  - **`brain-write`**: la skill que cada agente invoca para **append** al brain un evento estructurado (decisión, respuesta de gate,
    diseño rechazado, link requisito→issue→PR). Es la que hace real el moat "conocimiento auditable que no se pierde".
  - **`gate`**: define qué pregunta un gate y cómo se responde (aprobar / corregir / **responder conversacional**).
  - **`provisioning-contract`** + **`verify`**: los contratos de frontera y la verificación por-stack (ya existen como data).

### L2 — Agentes (runtime que ejecuta el método)

- **Diseño**: el **Claude Agent SDK** como loop del agente (reemplaza el step-runner de Go). El agente = system prompt (el rol .md) +
  skills + tools (MCP hacia el brain y GitHub). "El agente vive en markdown" se vuelve literal.
- **Ejecución**: `claude-code-action` / Copilot en Actions (ya está). Un lane = un canal, elegido por dato.
- **"Maestro" = reconciliador DETERMINISTA** (Edge Function, **no** un LLM): lee estado deseado (Issues + brain) vs real (webhooks de
  PR/checks/workflow_run) y emite la próxima acción. **Con histéresis desde el día 1**: una story solo se demota en un evento terminal
  explícito (`workflow_run: failed/cancelled`), nunca por "no vi PR en este tick". Esto es código — pequeño y con máquina de estados
  real — porque es exactamente donde el juicio de un LLM sería un desastre.

### L3 — UI (delgada, realtime, el brain como superficie de producto)

- Next.js que lee Supabase directo con RLS + Realtime. Casi no tiene backend propio: es una **vista sobre el brain**. Board, **grafo de
  dependencias con click-para-despachar**, **brain explorer** (timeline auditable por proyecto/cliente), **preview live embebido**,
  **gates conversacionales**. Español-first.

---

## 2. Cómo cada pieza del moat se vuelve real (y barata)

| Pieza vendible | Cómo se realiza en v2 | Qué bug/deuda de hoy elimina |
|---|---|---|
| **Brain multi-tenant auditable** | Tabla append-only `brain_events(tenant_id, project_id, kind, payload, actor, ts)` + RLS + Realtime; skill `brain-write` | ARCH-1 (envenenamiento cross-tenant) — RLS lo hace imposible |
| **Aislamiento real** | RLS policies (una por tabla) | ARCH-1/3, SEC-1/2/6 — se caen como clase |
| **Sin flap / liveness confiable** | Webhooks push + máquina de estados con histéresis en el Maestro | ARCH-2, AUTO-2/4 |
| **Deploy/preview tipo Lovable** | Vercel/CF preview por branch + embed | preview bespoke (`api/previews.go`) |
| **Una sola factura (managed keys)** | Keys en Supabase Vault; el dispatch las inyecta como secret efímero; metering por `tenant_id` | costura de tokens a mano (SEC-3) |
| **Gate que verifica de verdad** | Verify como job requerido en branch protection (no `continue-on-error`) + art-director sobre screenshot | AUTO-3 (gate verde-vacío) |
| **UI de equipo** | Grafo + realtime, sin conductor | UX-1 (la fábrica muda) |
| **Escala a N tenants** | Postgres gestionado + webhooks (sin loop serial ni SQLite de 1 conexión) | ARCH-4/5 |

---

## 3. Análisis adversarial de ESTA arquitectura (dónde muere v2)

No es gratis. Honestidad antes que entusiasmo:

1. **Lock-in de plataforma triple** (Supabase + Vercel + GitHub). Mitigación: los tres son estándar y semi-portables (Postgres es
   Postgres; Vercel↔Cloudflare; GitHub ya es del cliente). El trade real: 3 dependencias de plataforma **vs** 49k LOC de bugs propios
   que mantenés. Para un equipo chico, alquilar gana. Pero es una decisión estratégica explícita, no gratis.
2. **"Cero código" es aspiracional.** Seguís escribiendo: RLS (SQL), ~4-6 Edge Functions (webhook router, dispatch, token-mint,
   Maestro), y la UI. Es ~2-5k LOC **vs** 49k. El framing honesto es "casi-cero-BESPOKE", no "cero".
3. **RLS mal escrita = brecha silenciosa.** El aislamiento ahora vive en policies; una policy floja es tan peligrosa como el `WHERE`
   olvidado de hoy — pero es **auditables en un solo lugar** y testeables (pgTAP). Necesitás un test de fuga cross-tenant en CI (§1-bis).
4. **El Maestro sigue siendo el corazón delicado.** Reducido, pero la máquina de estados + histéresis hay que diseñarla bien una vez.
   La tentación de "que lo decida un agente" es la trampa: no.
5. **Managed keys = revendedor de compute** (margen fino, abuso, rate-limit por tenant). Real y ya discutido; el metering por
   `tenant_id` y cuotas duras son requisito, no adorno.
6. **Cold-start y rate-limits de GitHub Actions** no desaparecen: son del sustrato del cliente. El Maestro debe respetar backpressure.
7. **Migración con producto vivo**: marketpty + otros corren hoy sobre el kernel Go. No es un rewrite big-bang (ver §5, strangler).

---

## 4. Comparación con lo que tenemos HOY

| Dimensión | HOY (v1, auditado) | v2 (agent-native) | Veredicto |
|---|---|---|---|
| **Código de infra propio** | ~49k LOC Go (kernel, store, conductor, tokens, gate, scaffold) | ~2-5k LOC (Edge Functions + RLS + UI); método en markdown | **Inversión total** — se BORRA el sustrato construido |
| **Método** | Parcialmente en registry, **parcialmente hardcodeado en Go** (publish.go, gate.go — CQ-1) | 100% en registry (agents/skills/yaml); la regla de oro se cumple de verdad | v2 cumple la promesa que v1 rompe |
| **Aislamiento multi-tenant** | Columnas `project_id` + scoping a mano → **ARCH-1/3 cross-tenant**, SEC-1/2/6 | RLS declarativa (una regla/tabla) | v2 mata la clase de bug |
| **Estado / liveness** | Derivado de 1 lectura eventual del label, UPDATE crudo, loop serial 25s → **flap ARCH-2** | Webhooks push + máquina de estados con histéresis | v2 elimina el patrón |
| **Persistencia** | SQLite `MaxOpenConns(1)`, sin índice `project_id` → **ARCH-4/5** | Postgres gestionado + RLS + realtime | v2 escala |
| **Brain** | Existe (`brain/`, vista consola) pero sobre store frágil y envenenable | `brain_events` append-only + RLS + skill `brain-write` = moat de producto | v2 lo vuelve vendible |
| **Deploy/preview** | `api/previews.go` bespoke | Vercel/CF preview por branch (plataforma) | v2 es "Lovable" de verdad, sin código |
| **Ejecución (agentes)** | step-runner en Go + claude-code-action | Agent SDK (diseño) + Actions (ejecución) | v2 = "el agente vive en markdown" |
| **Gate de verificación** | verde-pero-vacío (`continue-on-error`, ui-verify SKIP) — **AUTO-3** | job requerido en branch protection | v2 gatea de verdad |
| **Facturación** | BYO-keys (tercera factura) | Managed keys, factura única (Vault + metering) | v2 = el modelo del fundador |
| **Onboarding** | sin wizard, launch sin gate (UX-4) | wizard sobre Supabase Auth + semáforos reales | v2 cierra el cliff |
| **Superficie de seguridad** | App key plaintext (SEC-3), registry sin authz (SEC-1) | Supabase Vault + RLS + templates read-only del binario | v2 cierra las brechas |

**Lectura de una línea:** hoy Fluxo **construyó el sustrato** (su store, su conductor, su aislamiento, su minting) — y ahí viven TODOS
los bugs críticos de la auditoría. v2 = **borrá el sustrato que construiste, alquilalo como config declarativa, quedate con el método.**
Y hay un argumento de dogfooding brutal: **Fluxo ya tiene un stack profile `react-supabase`** — v2 es Fluxo construido con el mismo stack
que Fluxo le vende a sus clientes.

---

## 5. Camino de migración (strangler, no big-bang)

No se reescribe todo de una. Orden por valor/riesgo:

1. **Brain primero, a Postgres+RLS** (nuevo, aditivo): `brain_events` + skill `brain-write` + brain explorer en la UI. Es el moat y no
   toca el kernel vivo. Empezás a acumular el activo auditable ya.
2. **UI realtime sobre Supabase** para el board/grafo (lee la nueva DB en paralelo al kernel viejo). El usuario ve la ergonomía.
3. **Aislamiento a RLS**: migrar `stories`/`runs`/`events` a Postgres con policies; el test de fuga cross-tenant en CI. Aquí se caen
   ARCH-1/3 y SEC-1/2/6 de golpe.
4. **Maestro reconciliador** (Edge Function) reemplaza el conductor serial: webhooks + histéresis. Se cae el flap.
5. **Preview a Vercel/CF**; **managed keys a Vault**; **verify como job requerido**.
6. **Agent SDK** para los agentes de diseño; retirar el step-runner de Go.
7. Lo que queda de Go: se apaga o se reduce a un par de funciones. `publish.go`/`gate.go` hardcodeados → data en registry (CQ-1 muere solo).

Cada paso es vendible por sí mismo y deja el producto corriendo. El kernel Go se estrangula, no se dinamita.

---

## 6. La pregunta abierta que decide todo

**¿El `brain/` de hoy ya captura decisiones + respuestas de gates + diseños rechazados + provenance, o es un store de artefactos?**
Si es lo primero, v2 es una **re-plataformización** del moat que ya tenés (menor riesgo). Si es lo segundo, el moat es **roadmap**, y v2
es también el momento de construirlo bien por primera vez. Eso cambia qué vendés HOY vs qué prometés.
