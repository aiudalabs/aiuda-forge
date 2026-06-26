# Engines y portabilidad

aiuda-forge trata al modelo (hoy **Claude Code**) como un *engine intercambiable*: un
"LLM headless con herramientas". Esta página explica **cómo se invoca hoy**, **por qué
así** y **qué habría que tocar** para usar mañana otro engine (opencode, codex, cursor…).

## Cómo se llama al engine hoy

En cada paso `type: agent`, el backend arma este comando (`engine/internal/agent/claude.go`):

```
claude -p --output-format stream-json --verbose --permission-mode acceptEdits \
       --model <id> --allowedTools read,edit,write,bash \
       --append-system-prompt "<persona.md (+ skills)>" \
       "<prompt>"
```

El mapeo desde el **registry** es:

| Pieza del agente (registry) | Cómo llega al engine |
|---|---|
| `persona` (`<id>.md`) + `skills:` | `--append-system-prompt` *(string)* |
| `role:` + ticket / instructions / feedback | dentro del `<prompt>` |
| `tools: [read, edit, write, bash]` | `--allowedTools` |
| `model:` | `--model` |

Corre **dentro del sandbox docker** (fábrica) o **en el host** (diseño).

## La decisión clave: la persona viaja como string

aiuda-forge **NO usa el mecanismo nativo de agentes/skills del engine**. No invoca
subagentes de `.claude/agents` ni un "Skill tool". En cambio, **carga él mismo** la
persona y los skills (lee los `.md`) y se los pasa al engine como un **string de
system-prompt**.

Esto es deliberado y es lo correcto para la portabilidad: si dependiéramos de los
agentes/skills *nativos* de un engine, quedaríamos **casados con ese engine**. opencode,
codex y cursor no tienen el mismo mecanismo. Un "slot de system prompt", en cambio, lo
tiene cualquiera. **La lógica vive en los `.md` del registry; el engine solo ejecuta.**

> Históricamente los `skills:` se declaraban pero nunca se inyectaban (config muerta), y
> el encadenado de contexto entre fases de diseño (`$step.text`) resolvía a `""` en
> silencio. Ambos eran bugs de **nuestra capa de ensamblado**, no del engine — ya
> corregidos: los skills se cargan desde `registry/skills/<id>.md` y se inyectan al
> system-prompt, y `$step.<k>` ahora cae a `$step.output.<k>`.

## El *seam*: la interfaz `Backend`

Toda la dependencia del engine pasa por una sola interfaz
(`engine/internal/agent/backend.go`):

```go
type Backend interface {
    Run(ctx, prompt string, opts Options, onEvent func(Event)) (Result, error)
}
```

`EngineMode` selecciona la implementación: `"echo"` (FakeBackend, para tests) o
`"claude"` (ClaudeBackend real). **Agregar un engine = una implementación más + un
EngineMode más.** El registry, el kernel de workflows y el orquestador **no se tocan**.

## Qué es específico de Claude (y hay que reescribir por engine)

| Acoplamiento | Dónde | Qué cambia por engine |
|---|---|---|
| Flags del CLI | `claude.go` | `-p`, `--output-format`, `--append-system-prompt`, `--allowedTools`, `--permission-mode`, `--model` |
| Parseo de salida | `parseStreamLine` | el schema `stream-json` (assistant/result, usage, total_cost_usd) |
| Env de auth | `childEnv` | `ANTHROPIC_API_KEY` / `CLAUDE_CODE_OAUTH_TOKEN` |
| Egress allowlist | `egress.go` | apunta a `api.anthropic.com` |
| **Model IDs** | los `.yaml` de agentes | `claude-sonnet-4-6`, `claude-opus`… son de Anthropic |
| Imagen del sandbox | — | debe traer el binario del engine |

**El catch:** las personas asumen las capacidades del engine (tools `read/edit/write/bash`,
modo `acceptEdits`, que el agente vendoriza dependencias). Otro engine debe ofrecer
**semántica equivalente de tools y permisos**, y los model IDs necesitan **aliasing
por-engine** (un `model: opus` lógico → el id real de cada proveedor).

## Cómo añadir un engine nuevo (receta)

1. Implementar `XBackend` (satisface `Backend`): traducir `(prompt, systemPrompt,
   allowedTools, model)` a los flags de *ese* CLI y parsear *su* salida a `Result`
   (texto, éxito, tokens/coste si los reporta).
2. Registrar un `EngineMode` (`"opencode"`, `"codex"`…) en `app.go`.
3. Mapear auth (sus env vars) y el egress allowlist (sus endpoints).
4. Alias de modelos: `opus`/`sonnet` lógicos → el id del proveedor.

Hecho eso, **el resto del sistema —registry, kernel, orquestador, fábrica— funciona sin
cambios**, porque nunca dependió del engine: solo de la interfaz `Backend`.

## Resumen

El diseño portable **ya existe** (la interfaz `Backend`), y "la persona como string" es
la decisión correcta para no casarse con un engine. Lo que faltaba era completar nuestra
capa de ensamblado (skills, contexto) — ya cerrado. Para un engine nuevo se escribe **un
backend** y se configura auth/egress/modelos; nada del cerebro de la herramienta cambia.
