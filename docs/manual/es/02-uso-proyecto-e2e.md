# Uso — un proyecto de punta a punta

Cómo se usa Fluxo para llevar **una idea → un producto real en GitHub**. Primero el camino
feliz (§1–§6), después **todos los flujos alternativos** que vas a necesitar en la práctica
(§7: change request, refine, ticket manual, sacar algo de `failed`, etc.).

> **Modelo mental.** Fluxo tiene dos mitades:
> 1. **Studio (diseño)** — vos + los agentes de diseño definen QUÉ construir, fase por fase,
>    y vos aprobás cada fase (gate). El resultado son documentos (`docs/`) + un backlog.
> 2. **Conductor (ejecución)** — una vez aprobado el diseño, el backlog se vuelve Issues de
>    GitHub y el Conductor **despacha sprints**: un agente de IA (Copilot o Claude) abre PRs,
>    se verifican, y se mergean. GitHub es la fuente de verdad; Fluxo lo proyecta.

---

## 1. Crear el proyecto

En **Studio** (arriba en la nav) → **Nuevo proyecto**:
- **Nombre** del proyecto.
- **Owner / GitHub space** — bajo qué org/usuario se crea el repo (ej. `aiudalabs` o tu usuario).
- **Instrucciones / brief** — describí la idea en lenguaje natural. Cuanto más claro el
  problema y el usuario objetivo, mejor arranca el descubrimiento.

Al crear, Fluxo crea el repo en GitHub y arranca el **pipeline de diseño**.

## 2. Las fases de diseño (el pipeline)

El diseño avanza en fases; cada una produce **un documento** y se **congela en un gate**
esperando tu aprobación. El orden (SDLC):

| # | Fase | Produce | Pregunta que responde |
|---|---|---|---|
| 1 | **Descubrimiento** | `docs/BRIEF.md` | ¿Qué problema, para quién? |
| 2 | **Constitución** | `docs/CONSTITUTION.md` | Decisiones bloqueadas: stack, modelo de negocio, presupuestos |
| 3 | **PRD** | `docs/PRD.md` | Los requisitos (cada uno con su escenario GIVEN/WHEN/THEN) |
| 4 | **Modelo de datos** | `docs/DATA_MODEL.md` | Entidades, campos, máquinas de estado, accesos |
| 5 | **Arquitectura** | `docs/ARCHITECTURE.md` + `docs/provisioning.yaml` | El CÓMO + el **contrato de frontera** (roles, índices, permisos) |
| 6 | **UI / Pantallas** | `docs/UI_SCREENS.md` | Las pantallas y flujos |
| 7 | **Mockups** | `docs/mockups/*.html` | Un HTML clickeable por superficie |
| 8 | **Backlog** | épicas + sprints + stories | El plan de trabajo (lean, MVP primero) |
| → | **docs_pr** | PR que mergea `docs/` a `main` | Deja la spec canónica en `main` |
| → | **handoff** | publica el backlog | Dispara la ejecución |

Podés **ver cada documento en pantalla completa** y **abrir los mockups en una pestaña
nueva** para clickearlos como una UI real.

## 3. Aprobar (o rechazar) un gate

En cada fase, en el panel de la fase, tenés:
- **Aprobar** → pasa a la fase siguiente.
- **Pedir cambios (Change Request)** → escribís feedback y la fase **se re-ejecuta con tu
  feedback inyectado** (el agente reescribe el documento incorporándolo). Se repite hasta que
  apruebes.

> 💡 **Responder preguntas del diseño (workaround actual):** si una fase te deja *preguntas
> abiertas*, respondelas **en el mismo cuadro de "Pedir cambios"** — el agente las incorpora al
> re-ejecutar. (La respuesta conversacional dedicada está en el backlog: [#3].)

## 4. Del diseño al código (handoff)

Cuando aprobás la última fase:
1. **docs_pr** — se abre un PR que mergea toda la `docs/` a `main`. **Importante:** los sprints
   NO se despachan hasta que ese PR esté en `main` (correct-by-construction: sin la spec en
   `main`, la fábrica no tendría contexto). El PR puede ser auto-mergeable.
2. **handoff** — el backlog se **exporta a GitHub Issues** con sus dependencias nativas, y el
   Conductor empieza.

## 5. El Conductor despacha los sprints

Con el diseño en `main`, el Conductor (tick cada ~25s, proyectando desde GitHub):
- Calcula los **candidatos** despachables (respetando dependencias entre stories/sprints).
- **Despacha** cada story a un canal: **Copilot** o **claude_action** (Claude Code en Actions).
- El agente **abre un PR** que implementa la story.
- Corren los **checks** (tests, `provisioning-lint`, `e2e-verify` — la capa de verificación).
- Según la autonomía configurada: **auto-merge** (si es seguro) o espera tu aprobación del PR.
- Al mergear, cierra el Issue y libera las stories que dependían de esta.

## 6. Seguir el progreso (Tickets / Agentes)

- **Tickets** (kanban estilo JIRA) — cada story es una card con estado, sprint, lane (owner),
  conteo de ACs, y links reales al PR y al run. Columnas colapsables; filtros por
  búsqueda/estado/sprint/lane.
- **Agentes** — las sesiones activas, la cola de PRs, y aprobar workflows cuando hace falta.

---

## 7. Flujos alternativos (lo que vas a usar de verdad)

### 7a. Change Request sobre un documento ya aprobado
En la **Especificación**, en el header del proyecto o por documento, **"Change Request"** →
escribís qué cambiar → **re-ejecuta esa fase** con el feedback. Se genera una **nueva versión**
del doc (hay historial + time-travel por chips de versión). Al aprobar, un nuevo `docs_pr`
lleva el cambio a `main`.

### 7b. Refine conversacional de un documento
En la Especificación, el **chat del Brain** (el textarea) apunta a un documento y lo **refina
conversacionalmente** (C3): "hacé el PRD más específico en la parte de pagos", "agregá una
pantalla de onboarding". Es más quirúrgico que un Change Request de fase completa. Para
mockups, el refine **nombra la pantalla objetivo** para editar ese archivo.

### 7c. Agregar un ticket / story manualmente
En **Tickets** → **Nueva story**: título, descripción, sprint, lane, dependencias. Se crea con
el `project_id` correcto y se puede **exportar a GitHub Issue** (con sus deps). El Conductor la
toma como cualquier otra story.

### 7d. Sacar una story/sprint de `failed` (Reencolar)
Si una story quedó **`failed`** (ej. un error transitorio, un límite de sesión de Claude):
- En la card (o en el drawer del run) → **Reencolar** → transición `failed → backlog`, limpia
  `run_id`/`pr_url`, y el Conductor la **dispara fresco**.
- También hay **reencolar un sprint completo** en bloque.

### 7e. Reintentar una fase de diseño que falló
Si una fase de diseño falla (ej. el backlog se pasó del timeout), aparece **`failed` en rojo
pero relanzable inline** — la re-ejecutás y arranca con presupuesto fresco. Un fallo de una
fase **no tumba** todo el run (tiene reintentos).

### 7f. Nueva iteración (ciclo continuo)
Terminado un ciclo, **"Nueva iteración"** vuelve a abrir el pipeline de diseño sobre el
proyecto existente (`iterate.yaml`) para la siguiente tanda de features — la spec en `main`
es el punto de partida.

### 7g. Elegir/forzar el canal de ejecución
Por proyecto (o por despacho puntual) podés elegir el **canal** (Copilot vs claude_action) y el
**modelo por lane**. Si un canal no está disponible (falta permiso/secret), Fluxo lo detecta
con un probe real y podés hacer **failover** al otro.

### 7h. Cambiar la autonomía (cuánto humano-en-el-loop)
En **Settings → autonomía**: desde "todo manual" (aprobás cada PR) hasta "auto-merge si es
seguro" (nunca auto-mergea si toca `.github/workflows/**`). Elegí según tu confianza en el
proyecto.

---

## Resumen del ciclo

```
idea → [Studio] descubrimiento→constitución→PRD→datos→arquitectura→UI→mockups→backlog
        (aprobás cada gate; change request / refine cuando haga falta)
     → docs_pr (spec a main) → handoff (backlog → GitHub Issues)
     → [Conductor] despacha sprints → agente abre PR → checks/verificación → merge
     → Tickets muestra el progreso; Reencolar lo que falle; Nueva iteración para seguir
```

**Anterior:** [← Despliegue](01-despliegue.md) · **Siguiente:** [Mantenimiento →](03-mantenimiento.md)
