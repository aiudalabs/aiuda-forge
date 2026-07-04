# GTM 2026-07-03 — Onboarding (auth + setup) y monetización de aiuda-forge en cloud

**Autor:** analista de producto/GTM · **Insumos:** ADR-2026-07-03-studio-first-github-native.md,
PLAN-2026-07-03-pivot-github-native.md, CLAUDE.md raíz + búsqueda web de precios (julio 2026).

## Resumen ejecutivo (la opinión, sin rodeos)

**Auth:** GitHub OAuth como identidad primaria (matar email+password como onboarding de usuario),
**más** una GitHub App instalada por org para el trabajo pesado automatizable, **más** un token
user-to-server que sale del mismo flujo OAuth para las dos cosas que la App NO puede hacer:
despachar Copilot agent-tasks y leer el billing de Copilot. No es estética: es la única combinación
que cubre todo el producto sin pedir un PAT a mano.

**Monetización:** monetizable, pero NO como vibe-coder barato. El comprador real es la agencia dev /
fundador técnico / PYME con equipo que ya vive en GitHub y quiere specs de calidad + ejecución
gobernada + trazabilidad, en su propio GitHub, sin lock-in del modelo de ejecución. Modelo:
suscripción por asiento + runs de diseño medidos (nuestro único COGS variable real). El wedge es la
fábrica de specs gateada que exporta a GitHub Issues — vendible incluso antes de que el conductor esté listo.

---

# PARTE 1 — Autenticación + setup inicial

## 1. La auth debe ser: GitHub OAuth primario; email+password muere

Todo el producto vive en GitHub (repos del cliente, issues = fuente de verdad del backlog, PRs =
entregable, agentes en Actions). Pedir email+password y *después* conectar GitHub es fricción doble y
una identidad que no aporta nada. **Login con GitHub (OAuth) como identidad primaria.** El
email+password actual (seed de admin por env) se conserva solo como acceso de operador/soporte interno.

Razón no cosmética: el token user-to-server que necesitamos sí o sí para las agent-tasks **es un
subproducto del flujo OAuth**. Si la identidad ya es GitHub, obtener ese token es gratis en el mismo
paso. Con email+password habría que forzar un OAuth extra después. Un solo camino.

## 2. GitHub App vs OAuth App vs PAT — y el gap que obliga a usar los dos

No es elegir uno: es una **GitHub App que emite ambos tipos de token**.

- **Installation token (server-to-server):** todo lo automatizable sin humano — crear repos, issues,
  dependencias, push de scaffold, secrets, dispatch/approve de workflows, leer PRs/checks.
  Da multi-tenant limpio (una instalación por org), webhooks firmados nativos, permisos finos, sin PAT
  personal del operador (cierra backlog #1).
- **User-to-server token (del OAuth de la misma App):** lo que GitHub PROHÍBE con installation token.

**Gap crítico (confirmado julio 2026):** la Agent tasks API NO acepta installation tokens de GitHub
App. GitHub soporta PAT (classic/fine-grained), tokens OAuth y user-to-server, pero *"GitHub App
installation access tokens are not supported… coming soon"*. Es deliberado: mantienen una credencial
humana en la cadena mientras madura el modelo de seguridad
([Changelog 13-may-2026](https://github.blog/changelog/2026-05-13-start-copilot-cloud-agent-tasks-via-the-rest-api/);
[Changelog 4-jun-2026](https://github.blog/changelog/2026-06-04-agent-tasks-rest-api-now-available-for-copilot-pro-pro-and-max/)).
Lo mismo con el **billing/usage de Copilot**: es scope de usuario/owner, no lo lee una installation.

Conclusión: el conductor **necesita almacenar y refrescar el token user-to-server de cada usuario**
para despachar con `model=…` y leer costos. No hay forma 100% headless hoy. Diseñar para esto desde
el día 1, no como parche. Nota: el canal **claude-code-action** corre dentro de Actions con un secret
del repo (installation token alcanza) → degrada con gracia si el token de usuario falta/expira. Otro
argumento para no apostar todo a Copilot.

### Tabla operación → mecanismo → permiso exacto

| Operación del producto | Mecanismo | Permiso / scope exacto |
|---|---|---|
| Crear repo en la org del usuario | Installation token | `Administration: write` (repo) o creación a nivel org |
| Push del scaffold + branches | Installation token | `Contents: write` |
| Escribir `.github/workflows/**` (QA, claude-review) | Installation token | `Workflows: write` (permiso especial, aparte de Contents) |
| Crear issues + ACs (body checklist) | Installation token | `Issues: write` |
| Dependencias nativas `blocked_by` | Installation token | `Issues: write` |
| Poner secrets por repo (ej. `CLAUDE_CODE_OAUTH_TOKEN`) | Installation token | `Secrets: write` |
| Recibir eventos (PR merged, issue closed) | GitHub App (webhooks firmados) | suscripción a eventos; `X-Hub-Signature-256` |
| Leer PRs / checks / mergear | Installation token | `Pull requests: write`, `Checks: read` |
| Dispatch de workflow / re-run tras `action_required` | Installation token | `Actions: write` |
| **Despachar Copilot agent-task con `model`** | **User-to-server / PAT** | **NO installation** — scope de usuario Copilot |
| **Asignar issue a `@copilot` (GraphQL)** | **User-to-server** | token de usuario |
| **Leer Copilot billing / premium usage** | **User-to-server / owner** | scope de billing, no installation |

## 3. Wizard de onboarding (pantalla a pantalla) — objetivo <5 min

Estándar a batir: Vercel/Railway. Diferencia honesta: ellos despliegan *su* infra; nosotros
dependemos de prerequisitos de terceros (Copilot/Claude). No igualamos los 5 min si el usuario NO
tiene Copilot coding agent habilitado. Estrategia: **verificar temprano y degradar con gracia**;
nunca dejar que descubra el bloqueo en el minuto 20.

- **P1 — Landing / "Continue with GitHub".** Un botón. OAuth. Al volver ya tenemos identidad + token
  user-to-server (scopes agent-tasks + billing pedidos en el consent).
- **P2 — Instalar la GitHub App.** Elegir cuenta/org destino y alcance de repos. Emite la installation.
  Copy honesto: "los repos son y quedan tuyos".
- **P3 — Chequeo de capacidades (auto-verificado, en vivo), con semáforos:**
  - ¿Copilot con **coding agent** habilitado en la org? (gap ⚠️ del PoC: `suggestedActors` sin @copilot).
    Si no → link a Settings→Copilot→Coding agent.
  - ¿Token de usuario con scope agent-tasks vivo? (verde/rojo).
  - ¿Canal claude_action disponible? (opcional: guardar `CLAUDE_CODE_OAUTH_TOKEN` como secret).
  - Regla de oro: si Copilot no está habilitado, NO bloqueamos → marcamos rojo y default a claude_action.
- **P4 — Org destino + preset de autonomía.** Piloto automático / Copiloto (default) / Manual (§3 PLAN).
- **P5 — "Crear tu primer proyecto" → Studio.** Cae en discovery. Diseñar corre en NUESTRO engine
  (tokens Anthropic), sin prerequisitos externos → valor antes que fricción.

Insight: el valor (diseño en Studio) llega antes que la fricción (Copilot/token). Un usuario sin
Copilot igual llega a un backlog exportable y ya está enganchado. Eso salva el funnel.

## 4. Riesgos y fricciones del setup

1. **El usuario paga Copilot/Claude aparte** → cliff de activación (signups sin coding agent habilitado).
   Mitigación: degradar a claude_action; medir % de signups que *pueden ejecutar*.
2. **Dependencia del token de usuario** para agent-tasks y billing → almacenar cifrado + refrescar; si
   expira, el canal Copilot cae en silencio. Mitigación: monitor de salud + fallback claude_action.
3. **Secrets org-level vs repo-level, y usuarios sin org.** Copilot en repos personales tiene otra
   superficie; secrets por repo escalan mal a N repos. Mitigación: preferir secrets org-level; personal = 2ª clase.

---

# PARTE 2 — Monetización

## Comparables de precio (julio 2026, citados)

| Producto | Qué es | Precio 2026 |
|---|---|---|
| **Lovable** | Vibe-coding idea→app | Free $0 · **Pro $25/mo** (100 créditos) · Business $50/mo · Enterprise custom — [No Code MBA](https://www.nocode.mba/articles/lovable-pricing), [eesel](https://www.eesel.ai/blog/lovable-pricing) |
| **Replit** | Vibe-coding + hosting | **Core $25/mo** (+$25) · Pro $100/mo (hasta 15 builders, reemplazó Teams el 20-feb-2026) · agente ~$0.25/checkpoint — [Automation Atlas](https://automationatlas.io/answers/replit-agent-pricing-explained-2026/) |
| **Cursor** | IDE agéntico | Hobby $0 · **Pro $20** · Pro+ $60 · Ultra $200 · Teams Standard $40/user, Premium $120/user — [No Code MBA](https://www.nocode.mba/articles/cursor-pricing), [dev.to](https://dev.to/rahulxsingh/cursor-pricing-in-2026-hobby-pro-pro-ultra-teams-and-enterprise-plans-explained-4b89) |
| **GitHub Copilot** | Autocompletar + coding agent | Free · **Pro $10** (+$15) · Pro+ $39 (+$70) · Max $100 (+$200) · Business $19/seat · usage-based desde 1-jun-2026 (1 crédito=$0.01) — [GitHub Blog](https://github.blog/news-insights/company-news/github-copilot-is-moving-to-usage-based-billing/), [pecollective](https://pecollective.com/tools/github-copilot-pricing/) |
| **Devin (Cognition)** | Ingeniero AI autónomo | **Core $20/mo** pay-as-you-go ($2.25/ACU ≈15 min) · Team $500/mo (250 ACU @ $2.00) · Enterprise custom — [VentureBeat](https://venturebeat.com/programming-development/devin-2-0-is-here-cognition-slashes-price-of-ai-software-engineer-to-20-per-month-from-500), [CostBench](https://costbench.com/software/ai-coding-assistants/devin-ai/) |
| **Factory.ai** | Droids agénticos (Series C $150M, val $1.5B) | **Pro $20** · Plus $100 · Max $200 · Teams/Enterprise custom, token-based — [docs.factory.ai](https://docs.factory.ai/pricing), [tech-insider](https://tech-insider.org/factory-ai-150-million-series-c-khosla-coding-droids-2026/) |

Lectura: vibe-coding e IDE agénticos anclan la entrada en **$20-25/mo**; el "ingeniero autónomo"
premium (Devin Team) en **$500/mo**. Hueco entre medio para "diseño gobernado + ejecución trazable"
que nadie ocupa limpio.

## 1. ¿Es monetizable? ¿Quién paga?

Sí, pero el comprador NO es el indie hacker vibe-coder (ya lo tienen Lovable/Replit, más barato y sin
exigir GitHub). Nuestro comprador es quien el vibe-coding deja insatisfecho:

- **Agencia dev / software house LATAM** (el caso aiudalabs): entrega a clientes, necesita specs de
  calidad, trazabilidad requisito→código, ejecución auditable. Vive en GitHub. **Mayor disposición a pagar.**
- **Fundador técnico / PYME con 1-3 devs** que quiere convertir idea en backlog buildable con gates humanos.

**Dolor que paga:** no es "generame una app" (commodity). Es *"tengo que entregar software de calidad,
con proceso, en mi propio GitHub, sin amarrarme a un modelo de ejecución, y sin escribir 40 issues
bien especificados a mano"*. Lovable/Replit son cero-SDLC, cero-trazabilidad — débiles justo ahí.

## 2. Pricing concreto

Principio: el COGS de ejecución lo paga el usuario (su Copilot/su Claude). Nuestro COGS real = tokens
Anthropic en Studio (runs de diseño) + infra. Por eso **no revendemos créditos de ejecución** —
suscripción por asiento con los **runs de diseño como componente medido** (único costo variable
nuestro). Modelo más limpio que los competidores: el margen no depende de arbitrar compute.

| Tier | Precio | Incluye | Lógica |
|---|---|---|---|
| **Free** | $0 | 1 proyecto, solo diseño (Studio→backlog), export con watermark | Enganche: ver el backlog de calidad antes de pagar |
| **Pro** | **$49/mo por asiento** | Proyectos ilimitados, ~10 runs de diseño/mo incluidos (luego $X/run), export GitHub/Linear/JIRA, conductor 1 proyecto activo, presets de autonomía | Sobre el vibe-coder ($20-25) porque no es lo mismo; medimos COGS con el run |
| **Team** | **$199/mo** (hasta 5 asientos) | Proyectos compartidos, conductor multi-proyecto, gates conversacionales, prioridad, roles | Comprador agencia; ancla frente a Devin Team ($500) |
| **Agency / Enterprise** | custom | Multi-org, SSO, casos de estudio, soporte, SLA | aiudalabs y similares |

Por qué suscripción + run medido y no otros: por-proyecto-activo castiga al que diseña mucho y ejecuta
poco (nuestro usuario ideal explora ideas); margen-sobre-créditos de ejecución es imposible (el usuario
paga eso directo a GitHub/Anthropic); flat puro deja plata con el heavy user de Studio. El run de
diseño medido alinea precio con nuestro único costo variable.

## 3. Wedge de entrada

**Vender PRIMERO la fábrica de specs gateada que exporta a GitHub Issues con dependencias nativas — a
agencias LATAM, español-first.** Razones:

- Lo más diferenciado y menos disputado: nadie hace discovery→PRD→arquitectura→UI→backlog gateado que
  aterriza en 44 issues + 73 dependencias nativas (PoC). Copilot/Devin arrancan *desde* el issue.
- No requiere el conductor terminado: el export ya funciona (F0, PoC 44/44 + 73/73). Time-to-revenue corto.
- Encaja con el GTM del ADR: agencia primero (casos de estudio) → producto standalone después.

El conductor multi-canal anti-lock-in y el tracking JIRA-like son la **expansión** (upsell), no el
wedge. Vender el paquete completo de una diluye el mensaje.

## 4. Amenazas — los 3 riesgos letales

1. **GitHub lo hace nativo.** Agent HQ / mission control ya orquesta; que sume "issues bien
   especificados desde un brief + scheduling por dependencias" no es descabellado (deps y agent-tasks
   ya son suyas). Moat honesto: metodología de diseño gateada (BMAD-style) + verificación cross-model +
   español-first LATAM. Moat de *producto y proceso*, defendible pero no infinito — ventana de meses,
   no años. Por eso el wedge de agencia (relación + casos + idioma) importa más que la feature.
2. **Cliff de activación.** Exige GitHub + Copilot/Claude pago + coding agent habilitado + token vivo.
   Si un % alto de signups no puede ejecutar, el funnel muere en P3. Mitigación: degradar a
   claude_action; métrica norte = "% de signups que llegan a un PR".
3. **La calidad del diseño no se percibe como pagable.** Si un buen prompt en Claude Code da el 80% de
   la spec gratis, ¿por qué pagar $49? Mitigación: diferencial *demostrable* — trazabilidad
   requisito→issue→PR, gates que atrapan errores (el reviewer que cazó el gate-sham en run_bebf25),
   verificación que el prompt suelto no da. Sin casos de estudio, es vitamina, no analgésico.

## Veredicto

Monetizable como herramienta de agencia/equipo técnico a ~$49-199/mo, no como vibe-coder de $20. Wedge:
la spec factory gateada + export a GitHub, a agencias LATAM. Los tres asesinos (GitHub-nativo, cliff de
activación por prerequisitos de terceros, calidad de diseño no percibida como pagable) se atacan con lo
mismo: metodología + verificación probadas en casos reales de aiudalabs, español-first, antes de productizar.
