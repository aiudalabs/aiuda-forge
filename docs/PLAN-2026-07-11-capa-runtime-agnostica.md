# Capa de ejecución agnóstica — Runtime × Provider × ExecEnv

**Fecha:** 2026-07-11 · **Insumos:** `AUDIT-2026-07-11-adversarial-integral.md`, `PLAN-2026-07-11-arquitectura-v2-agent-native.md`,
referencia externa: **multica** (OSS, `github.com/multica-ai/multica` — plataforma vendor-neutral de runtimes de agentes; **no es nuestra**,
la usamos como prueba de que el patrón funciona, no como dependencia).

## Por qué este doc

El componente de ejecución de Fluxo hoy está cableado a **2 executors** (`copilot | claude_action`) con lógica por-canal en Go
(`conductor/dispatch.go`): un `switch` en `fireChannel`, un `otherExecutor` binario, `if`s por-canal en `capacityBlock`, y el preámbulo
del runner efímero horneado en el código. Eso:

1. **Viola la regla de oro #1 del propio kernel** (`engine/CLAUDE.md`: *"Zero methodology in code. The executor is generic: there is
   NO `if step.id == …`"*). Sumar un 3er canal (Cursor, Codex, opencode-en-Actions, un runner self-hosted, un docker aislado para E2E)
   exige editar Go + recompilar + redeployar.
2. **Lee liveness de forma frágil** (AUTO-4: el 404 de la Agent-tasks API de Copilot se trata como "sin veredicto" → sesión colgada).
3. **Confunde tres ejes distintos** en un solo string.

## El modelo: tres ejes ortogonales

Tomado del modelo de multica (Runtime / Provider / ExecEnv), que ya lo prueba con ~14 CLIs y runtimes local+cloud:

| Eje | Pregunta | Valores | Hoy en Fluxo |
|---|---|---|---|
| **Runtime** | ¿DÓNDE corre el agente? | `github_actions` · `local_daemon` · `docker_isolated` (E2E) · `cloud` | `dispatch.go` (Actions) **y** `cmd/worker`+`internal/sandbox` (kernel v1) — **dos universos separados** |
| **Provider** | ¿QUÉ CLI de agente? | `claude` · `copilot` · `codex` · `opencode` · `cursor` · … | `registry/backends/*.yaml` (solo lo usa el factory local) **y** el `switch` de `fireChannel` |
| **ExecEnv** | ¿cómo se AÍSLA la tarea? | runner de Actions · dir local · contenedor egress-deny | `internal/sandbox` (v1) — no conectado al path GitHub-native |

**Fluxo ya tiene las piezas — fragmentadas.** Este trabajo las **unifica bajo una sola interfaz** (`Runtime`) + un **registro de
providers en data**. No es greenfield: es consolidar `sandbox` + `worker` + `backends.yaml` + `dispatch.go`.

## La frontera con Fluxo (lo que NO se toca)

Multica despacha **tickets independientes** (un issue = una task). Fluxo despacha por **sprint (goal-mode: el sprint entero, en orden de
deps, un branch, un PR)** o por story, con el grafo `blocked_by`. **Esa decomposición + orquestación es diferenciación de Fluxo y se
queda ARRIBA de la capa de runtime.**

```
┌─ FLUXO (el moat) ─ decide la UNIDAD: sprint / story / orden-de-deps / goal-mode ─┐
└──────────────┬───────────────────────────────────────────────────────────────────┘
               │ entrega una UNIDAD DE TRABAJO = { prompt, repo, contexto, lane }
┌──────────────▼─ CAPA DE RUNTIME (agnóstica, este doc) ────────────────────────────┐
│ Policy elige (runtime, provider) por lane + fallback ORDENADO                      │
│ Runtime.Dispatch → Provider (data) arma la invocación → ExecEnv aísla → sessionRef │
└───────────────────────────────────────────────────────────────────────────────────┘
```

**Clave: el runtime es agnóstico a la granularidad.** Recibe un prompt y lo corre — no sabe ni le importa si el prompt es una story o un
sprint entero. Por eso Fluxo puede alimentarlo con la unidad que su orquestación decidió, y multica con tickets sueltos: **misma capa
abajo, distinta inteligencia de decomposición arriba.**

## Contrato del `Runtime` (Go)

```go
// Runtime = DÓNDE corre un agente. Implementaciones: github_actions, local_daemon,
// docker_isolated, cloud. El Dispatcher de Fluxo depende SOLO de esta interfaz.
type Runtime interface {
    ID() string
    // Dispatch corre una unidad de trabajo con el provider indicado y devuelve una
    // referencia de sesión estable (la que Liveness luego consulta).
    Dispatch(ctx context.Context, work WorkUnit, provider Provider) (SessionRef, error)
    // Probe reporta si este runtime PUEDE correr ese provider AHORA (capacidad).
    // Motivo visible si no; "" si sí. Fail-open ante error transitorio.
    Probe(ctx context.Context, provider Provider, repo string) (ok bool, reason string)
    // Liveness deriva el estado real de una sesión desde la fuente ROBUSTA del
    // runtime (workflow_run en Actions, exit-code en local, …) — NO de una API frágil.
    Liveness(ctx context.Context, ref SessionRef) (State, error) // running|done|failed|lost
    Isolation() Isolation // none | actions | docker_egress_deny | …
}

// WorkUnit es lo que Fluxo entrega — granularity-agnostic (story o sprint).
type WorkUnit struct {
    Prompt  string
    Repo    string
    Issues  []int             // para señales observables (agent:running, Closes #n)
    Lane    string
    Context map[string]string // module map, provisioning contract, etc.
}
```

`Policy` sigue eligiendo `(runtime, provider)` por lane, pero los ids son **abiertos** (cualquier runtime/provider registrado), no 2
strings. `otherExecutor` (swap binario) → una **lista de fallback ordenada** en la Policy/registry.

## Provider registry (data) — `registry/providers/*.yaml`

El "QUÉ CLI y cómo" sale de datos, no de Go. Cada provider declara su invocación, **dónde vive la credencial y de quién es el gasto**,
cómo se prueba capacidad, cómo se lee liveness, y su preámbulo de prompt (en markdown, no en Go):

```yaml
# registry/providers/claude.yaml
id: claude
runtimes: [github_actions, local_daemon, docker_isolated]   # dónde puede correr
invoke:
  github_actions: { workflow: claude.yml, inputs: { prompt: $prompt, issues: $issues } }
  local:         { argv: [claude, -p, --output-format, stream-json, --permission-mode, acceptEdits] }
credential: { source: repo_secret, name: CLAUDE_CODE_OAUTH_TOKEN, owner: client }  # ← en el org del CLIENTE
capacity_probe: repo_secret_exists
liveness: workflow_run            # cierra AUTO-4: deriva del run, no del 404
running_signal: label:agent:running
prompt_preamble: claude_ephemeral.md   # la disciplina del runner efímero, en MARKDOWN
```
```yaml
# registry/providers/copilot.yaml
id: copilot
runtimes: [github_actions]
invoke: { github_actions: { api: agent_tasks, model: $model } }
credential: { source: client_copilot, owner: client }   # el Copilot del cliente, su gasto
capacity_probe: none
liveness: workflow_run            # NO la Agent-tasks API (404 frágil)
```
```yaml
# registry/providers/codex.yaml   — sumar un CLI = un YAML, CERO Go
id: codex
runtimes: [local_daemon, docker_isolated]
invoke: { local: { argv: [codex, exec, --json] } }
credential: { source: local_env, name: OPENAI_API_KEY, owner: client }
capacity_probe: cli_on_path
liveness: process_exit
```

## Matriz credencial-por-runtime (tu punto: la plata vive en el org del cliente)

| Runtime | Credencial típica | Dónde vive | Gasto de | Aislamiento |
|---|---|---|---|---|
| `github_actions` | `CLAUDE_CODE_OAUTH_TOKEN` / Copilot del cliente | **repo/org del cliente** | **cliente** (BYO, cero COGS Fluxo) | runner de Actions |
| `local_daemon` | env local del operador | máquina del operador | operador | dir + git |
| `docker_isolated` | env inyectado / vendorizado | contenedor efímero | según provider | **egress-deny (E2E)** |
| `cloud` (opcional) | key efímera de Vault | Fluxo | **Fluxo (upsell)** | sandbox cloud |

→ **"Dónde vive la plata" es una propiedad del RUNTIME.** El default (`github_actions`) es BYO-en-el-org-del-cliente — corrige mi error
anterior de poner managed-keys como default. Managed-keys queda como propiedad del runtime `cloud`, **opcional**, y ahí vive el upsell.

## Refactor de `dispatch.go` (de `switch` a genérico)

```
ANTES  fireChannel(): switch executor { case "claude_action": DispatchWorkflow(claude.yml); default: CreateAgentTask }
       otherExecutor(): claude_action ⇄ copilot   (binario)
       capacityBlock(): if executor=="claude_action" { RepoSecretExists(...) }
       + preámbulo efímero horneado en Go

DESPUÉS Dispatcher carga runtime := runtimes[pol.RuntimeFor(lane)]; provider := providers[pol.ProviderFor(lane)]
        ref, err := runtime.Dispatch(ctx, work, provider)   // genérico
        capacidad: runtime.Probe(ctx, provider, repo)         // declarado por provider.capacity_probe
        liveness (en la proyección): runtime.Liveness(ref)    // declarado por provider.liveness
        fallback: pol.Fallback (lista) en vez de otherExecutor
        preámbulo: provider.prompt_preamble (markdown)
```

## Qué cierra / mejora

- **AUTO-4** (liveness frágil de Copilot): `liveness: workflow_run` declarado → 404 transitorio no cuelga la sesión.
- **Regla de oro del kernel**: `fireChannel`/`otherExecutor`/`capacityBlock` dejan de ser lógica-por-canal en Go.
- **Extensibilidad real**: `engine local / docker aislado / E2E / Cursor / Codex / opencode` = un `Runtime` y/o un YAML de provider. Cero Go por canal nuevo.
- **Corrige el GTM/landing**: default = ejecución en el GitHub del cliente con SUS keys (cero COGS); managed/factura-única = runtime cloud opcional.
- **Unifica los dos universos** (kernel v1 `worker`+`sandbox` y GitHub-native `dispatch`) bajo una interfaz.

## Multica: qué tomamos y qué NO

- **Tomamos (patrón):** la separación Runtime × Provider × ExecEnv; providers como data; runtime local (daemon) + cloud + aislado;
  auto-detección de CLIs; liveness robusta por-runtime.
- **NO tomamos:** su modelo de trabajo (tickets independientes). Fluxo conserva sprints/goal-mode/deps/gates arriba — es su diferenciación.
- **NO dependemos** de multica (no es nuestro). Interop futura opcional (es OSS), no en el plan.

## Cambios al sprint plan (`PLAN-2026-07-11-sprints-migracion-v2.md`)

- **S3/S4 "ejecución"** se reencuadra: *"abstraer `Runtime` × `Provider` (data-driven) + unificar sandbox+worker+dispatch"*, NO "integrar multica".
- **S4-01 managed keys** pasa de default a **propiedad del runtime `cloud` (opcional)**.
- Nueva historia en **S5 (método sin Go)**: `registry/providers/*.yaml` + `fireChannel`/`otherExecutor`/`capacityBlock` → genéricos (cumple la regla de oro).
- **S4-04 failover**: la lista de fallback pasa al registry (por lane), no el swap binario en Go.
