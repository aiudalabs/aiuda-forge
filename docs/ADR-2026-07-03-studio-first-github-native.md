# ADR 2026-07-03 — Studio-first: el diseño es el producto, la ejecución es intercambiable

**Estado:** aceptado (dirección estratégica) · **Autores:** Noel + Claude (análisis adversarial)
**Contexto previo:** AUDIT-2026-06-25.md, ROADMAP-v1.1-v1.4.md, Waves R/V/D en CLAUDE.md

## Decisión

1. **Concentrar la inversión de desarrollo en Studio** (fases de diseño gateadas: discovery →
   PRD → arquitectura → UI → mockups → backlog) y en la **capa de verificación** (Wave V:
   reviewer contra ACs, suite-integrity, ui-verify). Eso es lo diferencial y lo vendible.
2. **Tratar la capa de ejecución como commodity intercambiable.** El kernel propio
   (tickets-store, state machine, reaper, sandbox) se mantiene en modo conservación para uso
   interno, pero NO se le agrega funcionalidad nueva. El objetivo a mediano plazo es que la
   ejecución pueda correr sobre agentes de terceros (GitHub Copilot coding agent, Claude
   Code, Multica, OpenCode) dirigidos por un **"conductor" delgado** (~10% del engine actual)
   que solo aporte lo que ellos no tienen: política de secuenciación por sprint y gates.
3. **Go-to-market: agencia primero, producto después.** aiuda-forge es el acelerador interno
   de aiudalabs (vender software terminado a PYMES/agencias LATAM, español-first); el Studio
   se productiza standalone solo después de N proyectos entregados que sirvan de casos de
   estudio.

## Por qué (hallazgos, julio 2026)

### 1. El mercado horizontal ya tiene dueños

- **Vibe-coding (idea→app para no técnicos):** Replit pasó de $300M a **$525M de revenue
  anualizado** en abril 2026 ([Sacra](https://sacra.com/c/replit/)); Lovable tocó **$400M ARR**
  en febrero 2026 con la mitad del Fortune 500 usándolo
  ([MindStudio](https://www.mindstudio.ai/blog/lovable-vs-replit-agent)). Competir ahí de
  frente es inviable para un equipo pequeño.
- **Ticket→PR (ejecución agéntica):** GitHub Copilot coding agent toma un issue asignado y
  devuelve un PR nativo, corriendo en Actions
  ([GitHub Blog](https://github.blog/ai-and-ml/github-copilot/assigning-and-completing-issues-with-coding-agent-in-github-copilot/)),
  con "mission control" para orquestar sesiones paralelas
  ([GitHub Blog](https://github.blog/ai-and-ml/github-copilot/how-to-orchestrate-agents-using-mission-control/))
  y una app de escritorio anunciada en Build 2026 con worktrees aislados por sesión
  ([Digital Applied](https://www.digitalapplied.com/blog/github-copilot-app-agent-native-desktop-orchestration-2026)).
- **Agentes-como-compañeros open source:** [Multica](https://github.com/multica-ai/multica)
  (Go+Next.js, ~25k★ en jun-2026) regala la capa "asignar issues a agentes, squads, progreso
  en tiempo real", compatible con Claude Code, Codex, Copilot CLI, OpenCode y más.
- **La tendencia de valor** señalada por los analistas: de generar código → a **coordinar el
  ciclo completo con verificación**
  ([CIO](https://www.cio.com/article/4134741/how-agentic-ai-will-reshape-engineering-workflows-in-2026.html),
  [HCLTech](https://www.hcltech.com/trends-and-insights/autonomous-software-factory-agentic-ai-sdlc),
  [PwC](https://www.pwc.com/m1/en/publications/2026/docs/future-of-solutions-dev-and-delivery-in-the-rise-of-gen-ai.pdf)).
  El stack por fase del SDLC ya está mapeado tool-por-tool
  ([MetaCTO](https://www.metacto.com/blogs/mapping-ai-tools-to-every-phase-of-your-sdlc)):
  cada pieza genérica de nuestro engine tiene un equivalente mejor financiado o gratis.

### 2. La tesis de modelos, actualizada

Tesis original: "frontera para diseñar (Studio), baratos/locales para implementar".

- **Lo que se sostiene:** frontera en diseño/definición es el mayor apalancamiento — una spec
  excelente hace que un modelo mediano implemente bien; una spec mala multiplica el costo
  aguas abajo. Lovable/Replit son débiles exactamente ahí (cero SDLC, cero trazabilidad
  requisito→código).
- **Lo que 2026 invalidó:** "implementación barata" como ahorro. El costo real no son los
  tokens sino los **loops de fallo** (evidencia interna: run_bebf25, 2026-07-03 — el
  story-detailer preguntó en vez de draftear, el dev declinó, el gate-sham pasó, el reviewer
  lo cazó; dinero quemado sin una línea de código útil). Los modelos abiertos cerraron mucho
  la brecha (GLM-5.2 lidera agentic coding en LiveBench; Qwen3-Coder 69.6% SWE-bench
  Verified — [HuggingFace](https://huggingface.co/blog/daya-shankar/open-source-llms)) pero
  el gap frontera sigue siendo real en razonamiento multi-paso
  ([MindStudio](https://www.mindstudio.ai/blog/best-open-source-llms-agentic-coding-2026)),
  que es justo lo que "implementa el sprint entero" exige.
- **Tesis corregida:** frontera para **diseñar y VERIFICAR**; el modelo del medio es
  intercambiable y se rutea por paso (barato para pasos mecánicos, frontera o casi-frontera
  para implementar-con-juicio, frontera siempre en el reviewer). El `backendloader` ya lo
  permite; es política de ruteo, no re-arquitectura.

### 3. Qué hace y qué NO hace GitHub Copilot coding agent (la pieza que decidió el "conductor")

SÍ hace:
- Issue asignado → PR, en background sobre Actions
  ([Docs](https://docs.github.com/copilot/concepts/agents/coding-agent/about-coding-agent)).
- Sesiones paralelas con steering (mission control / Agent HQ).
- **Dependencias entre issues ya son GA y nativas:** "blocked by"/"blocking" (hasta 50 por
  issue) con icono en boards, **API y webhooks completos**
  ([Changelog ago-2025](https://github.blog/changelog/2025-08-21-dependencies-on-issues/),
  [Docs](https://docs.github.com/en/issues/tracking-your-work-with-issues/using-issues/creating-issue-dependencies))
  y flags en el CLI (`--add-blocked-by`, jun-2026,
  [Changelog](https://github.blog/changelog/2026-06-10-manage-sub-issues-types-and-dependencies-from-github-cli/)).
  Nuestro `deps: [S1-01]` mapea 1:1 a metadata nativa.

NO hace (= exactamente nuestro orquestador, lo único que vale la pena conservar):
- **No es un scheduler:** nada dispara el siguiente issue cuando su bloqueador mergea; la
  guía oficial dice "usa flujos secuenciales, particiona con cuidado" — el humano (o una
  automatización TUYA) decide qué asignar y cuándo.
- **No tiene modo sprint:** issue→PR atomizado; nuestro goal-mode (N stories → un branch →
  un PR coherente) se aproxima publicando el sprint como UN issue con las stories en el body.
- **No tiene nuestros gates de política:** "no dispares SP2 hasta que docs+gate estén en dev"
  (#19), suite-integrity, reviewer cross-model contra ACs, merge_mode por proyecto.

**Conclusión operativa:** el conductor delgado = GitHub App/Action que escucha webhooks
(PR merged / issue closed), consulta el grafo de dependencias nativo, y asigna el siguiente
issue al agente elegido. Todo lo pesado y propio (tickets-store, reaper, sandbox) se vuelve
primitiva de GitHub (issues + dependencies + Actions + branch protection).

### 4. Evidencia interna del costo del engine propio

- 3 waves de auditoría de seguridad/robustez (AUDIT-2026-06-25: C1-C5, B1-B5, H1-H4, A1-A5).
- Wave R completa por incidentes de resiliencia en vivo (stories failed permanentes, requeue).
- 2026-07-03: run_77b5 muerto por exit 127 del sandbox; run_bebf25 envenenado por un persona
  desalineado con el modo sprint + gate-sham `true` (Wave V pendiente); stories huérfanas en
  `running` sobre un run CANCELLED (gap R3-variante). Cada uno de estos es mantenimiento del
  commodity, no del diferencial.

## Consecuencias

- **Se sigue invirtiendo en:** Studio (metodología, personas, gates conversacionales #3),
  verificación (Wave V), export del backlog a GitHub Issues/Linear/JIRA, el conductor delgado.
- **Se congela (conservación, no features):** kernel de workflows, tickets-store propio,
  sandbox propio, scheduler nativo. Fixes de bugs solo si bloquean el uso interno.
- **PoC inmediata:** exportar el backlog real de marketpty a GitHub Issues con dependencias
  nativas y asignar SP1 a Copilot en un repo de prueba (resultados en la sección siguiente).

## Referencias completas

- PwC — [Agentic SDLC 2026](https://www.pwc.com/m1/en/publications/2026/docs/future-of-solutions-dev-and-delivery-in-the-rise-of-gen-ai.pdf)
- HCLTech — [The autonomous software factory](https://www.hcltech.com/trends-and-insights/autonomous-software-factory-agentic-ai-sdlc)
- Microsoft — [AI-led SDLC on Azure + GitHub](https://techcommunity.microsoft.com/blog/appsonazureblog/an-ai-led-sdlc-building-an-end-to-end-agentic-software-development-lifecycle-wit/4491896)
- CIO — [How agentic AI will reshape engineering workflows in 2026](https://www.cio.com/article/4134741/how-agentic-ai-will-reshape-engineering-workflows-in-2026.html)
- Sacra — [Replit revenue](https://sacra.com/c/replit/) · MindStudio — [Lovable vs Replit](https://www.mindstudio.ai/blog/lovable-vs-replit-agent)
- HuggingFace — [Best open-source LLMs 2026](https://huggingface.co/blog/daya-shankar/open-source-llms) · MindStudio — [Open LLMs for agentic coding](https://www.mindstudio.ai/blog/best-open-source-llms-agentic-coding-2026)
- Multica — [github.com/multica-ai/multica](https://github.com/multica-ai/multica)
- GitHub — [About Copilot coding agent](https://docs.github.com/copilot/concepts/agents/coding-agent/about-coding-agent) ·
  [Assigning issues to the agent](https://github.blog/ai-and-ml/github-copilot/assigning-and-completing-issues-with-coding-agent/) ·
  [Mission control](https://github.blog/ai-and-ml/github-copilot/how-to-orchestrate-agents-using-mission-control/) ·
  [Dependencies on issues (GA)](https://github.blog/changelog/2025-08-21-dependencies-on-issues/) ·
  [Deps en gh CLI](https://github.blog/changelog/2026-06-10-manage-sub-issues-types-and-dependencies-from-github-cli/) ·
  [Creating issue dependencies](https://docs.github.com/en/issues/tracking-your-work-with-issues/using-issues/creating-issue-dependencies)
- MetaCTO — [AI tools per SDLC phase](https://www.metacto.com/blogs/mapping-ai-tools-to-every-phase-of-your-sdlc)

## PoC — resultados (2026-07-03)

Repo de prueba: **[nmlemus/forge-copilot-poc](https://github.com/nmlemus/forge-copilot-poc)** (privado).
Se exportó el backlog REAL de marketpty (44 stories, 12 sprints, 73 aristas de dependencia)
leído de `GET /tickets?project=marketpty-597c85e3` del control-plane.

**Funcionó (y valida el ADR):**
- ✅ **44/44 issues creadas** vía `gh api` con título `S1-NN — …`, body = user-story + ACs como
  checklist `- [ ]`, labels por sprint (`SP1…SP12`), lane (`lane:python-dev`/`lane:react-dev`)
  y epic (`E1…`). El mapeo Studio→GitHub es 1:1 y trivial.
- ✅ **73/73 dependencias nativas** creadas vía
  `POST /repos/{repo}/issues/{n}/dependencies/blocked_by` con `{"issue_id": <id>}`.
  GitHub las muestra como "Blocked" en boards/issues y las expone por API+webhooks.
- ✅ **El "conductor" es trivial:** un script de ~15 líneas (issues abiertas cuyos bloqueadores
  están todos cerrados) reproduce el ready-set del orquestador nativo: devolvió exactamente
  las 6 stories de SP1 + S1-31 como READY y las 37 restantes BLOCKED con sus bloqueadores.
  Ese script + un webhook (`issues.closed` / `pull_request.merged`) + un assign = todo el
  scheduler que hace falta.

**Gaps confirmados (= el valor que queda de nuestro lado):**
- ⚠️ **Copilot coding agent no estaba asignable** en la cuenta (`suggestedActors` solo devolvió
  al humano): requiere plan Copilot Pro/Business con el coding agent habilitado
  (Settings → Copilot → Coding agent). Paso manual pendiente para la parte final de la demo;
  la asignación en sí es `assign @copilot` en la UI o vía GraphQL una vez habilitado.
- ⚠️ **La política de sprint NO es expresable con deps nativas** (reproduce el hallazgo #21 de
  la UI): S1-31 (design system, sin deps, SP9) sale READY nativamente aunque el goal-mode por
  sprint diga que no debe dispararse hasta SP8. El gating por sprint, el goal-mode
  (N stories → 1 PR) y los gates de verificación siguen siendo lógica NUESTRA — el conductor.
- ⚠️ `gh` CLI local (2.64, 2024) no trae los flags `--add-blocked-by` (v2.94+, jun-2026);
  la API REST funciona igual con cualquier versión.

**Conclusión del PoC:** la mitad commodity del engine (tickets-store, estado, grafo) se
reemplaza con primitivas nativas de GitHub HOY, sin fricción. Lo que no se reemplaza es
exactamente lo que el ADR decide conservar: secuenciación por sprint, goal-mode y
verificación. Dirección confirmada.
