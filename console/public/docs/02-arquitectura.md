# Arquitectura: GitHub ejecuta, Forja dirige

La decisión de arquitectura central de Forja: **no construir una fábrica de ejecución propia, sino dirigir la de GitHub**. GitHub ya tiene los primitivos de una fábrica de software — issues con dependencias nativas, agentes de código (Copilot, Claude, Codex), PRs, checks, secretos, billing. Forja aporta lo que GitHub no tiene: **el diseño gateado antes, y la política de orquestación durante**.

## Las dos mitades

```
┌────────────────────────  FORJA  ────────────────────────┐
│                                                          │
│  STUDIO (diseño)              CONDUCTOR (orquestación)   │
│  · personas del Registry      · proyección  (lee GitHub) │
│  · fases con gates humanos    · despacho    (elige qué,  │
│  · produce docs/ + backlog      cuándo y por qué canal)  │
│                               · política    (sprints,    │
│                                 autonomía, aprobaciones, │
│                                 auto-merge, concurrencia)│
└───────────────┬──────────────────────┬───────────────────┘
                │ publica              │ dirige
                ▼                      ▼
┌────────────────────────  GITHUB  ───────────────────────┐
│  repo · issues+deps · agentes · PRs · checks · secrets  │
│  (la ejecución ocurre AQUÍ, con las cuentas del usuario)│
└──────────────────────────────────────────────────────────┘
```

## La proyección: GitHub es la verdad

Forja **no mantiene un estado paralelo que pueda divergir**: el estado de cada story se *deriva* de GitHub en cada ciclo (issue cerrado → done; PR abierto que la referencia → in review; agente asignado o sesión activa → running; nada → backlog). El kanban de Tickets es una **proyección** de tu repo, no una segunda fuente de verdad.

- En producción, los **webhooks** de la GitHub App hacen la proyección instantánea.
- En local/desarrollo, un **poll cada 25 segundos** la mantiene al día.
- Autocorrección: si la proyección detecta inconsistencias (un PR mergeado cuyos issues no cerraron, una sesión de agente muerta), las repara sola.

## El despacho: la política que GitHub no tiene

GitHub sabe ejecutar una tarea; no sabe **cuál toca ahora**. Eso es del conductor:

- **Sprint como unidad** (goal-mode): todas las stories de un sprint → un agente → una rama → un PR. Menos PRs, más coherencia. (Configurable a story-por-story.)
- **Gating por dependencias**: un sprint no se despacha si depende de stories abiertas de otro sprint.
- **Ruteo de canal y modelo**: por proyecto o por lane (backend por un canal, frontend por otro; modelos frontera donde importa, económicos donde no).
- **Failover**: si el canal primario falla al despachar, se intenta el alterno automáticamente.

## Los canales de ejecución

| Canal | Quién paga | Fortaleza |
|---|---|---|
| **Copilot cloud agent** | créditos GitHub Copilot del usuario | Integración nativa total; itera en el PR con comentarios |
| **Claude Code (Actions)** | plan Claude del usuario ($0 marginal con Max) | Corre en TU repo con TU workflow; sin lock-in |
| Partner agents (Claude/Codex vía Agent HQ) | créditos GitHub | Asignar un issue al bot y listo |

La lección aprendida operando: **los canales fallan** (límites de sesión, incidencias del harness, créditos). Tener dos o más intercambiables por despacho no es lujo — es lo que mantiene la fábrica andando.

## Resiliencia (aprendida a golpes reales)

- **Checkpoint de rescate**: el canal Claude lleva un paso determinista post-agente que commitea, pushea y abre PR draft con lo que exista — una sesión que muere a mitad de sprint ya no pierde el trabajo.
- **Barrido de sesiones muertas**: una task de agente que terminó en error devuelve sus stories al backlog automáticamente.
- **Reencolar desde la UI**: cualquier story espejada puede volver a backlog con un click (⟲) y re-despacharse por otro canal.
- **Cierre de loop de merges**: si un PR mergeó pero sus `Closes #` no cerraron los issues (p. ej. escritos entre backticks), el conductor los cierra él mismo.

## El ejecutor legacy

Forja tiene un ejecutor propio anterior (sandbox docker local, gate anti-tamper). Está **apagado por defecto** — la ejecución GitHub-native lo reemplaza — pero puede reactivarse (`VIBEFORGE_LEGACY_FACTORY=1`) para entornos completamente self-hosted sin GitHub. El pipeline de **diseño** de Studio no depende de él y siempre corre en la infraestructura de Forja.
