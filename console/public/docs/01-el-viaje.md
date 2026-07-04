# El viaje: de la idea al software

Forja convierte una idea en software funcionando, con humanos decidiendo y agentes ejecutando. Este documento recorre **el viaje completo de un proyecto**, paso a paso, tal como ocurre en el producto. Es la mejor forma de entender qué hace cada pieza.

## El mapa en una línea

```
Idea → Studio (diseño gateado) → Backlog → GitHub Issues → Agentes implementan → PRs → Merge → Cascada → Software
        [tu criterio]                        [tu repo]      [Copilot/Claude]           [tu review]
```

Forja **no ejecuta el código en sus servidores**: el desarrollo ocurre en TU GitHub, con TUS agentes (Copilot o Claude), en TU repo. Forja diseña, orquesta, vigila y te muestra todo.

---

## Paso 1 — Crear el proyecto

En **Studio**, describe qué quieres construir y crea el proyecto. Forja crea automáticamente un **repositorio privado en tu GitHub** con el nombre del proyecto.

> El repo nace vacío (solo un README). Todo lo demás — código, docs, agentes — llega en los pasos siguientes.

## Paso 2 — El diseño gateado (Studio)

Se lanza el **flujo de diseño**: una secuencia de fases donde agentes especializados (las *personas* del Registry) producen los documentos que definen el producto, y **tú apruebas cada fase antes de pasar a la siguiente**:

| Fase | Persona | Produce |
|---|---|---|
| Discovery | analyst | Brief: problema, usuarios, alcance |
| PRD | pm | Requisitos: qué se construye y por qué |
| Arquitectura | architect | Cómo: stack, módulos, esquema de datos |
| UI | designer | Design system: pantallas, tokens, estética |
| Mockups | designer | Prototipo HTML clickeable |
| Backlog | scrum-master | Épicas → sprints → stories con criterios de aceptación y dependencias |

En cada gate puedes **aprobar** o **rechazar con feedback** (el feedback re-ejecuta la fase incorporándolo). Este es el momento de mayor apalancamiento del viaje: un error corregido aquí cuesta una frase; el mismo error en código cuesta un sprint.

## Paso 3 — Publicar: el diseño viaja al repo

Al completar el diseño:

- Los **documentos** (`docs/PRD.md`, `docs/ARCHITECTURE.md`, `docs/DESIGN_SYSTEM.md`…) se suben al repo vía un **PR a `main`** que tú mergeas. Desde ese momento, cualquier agente que trabaje en el repo puede leerlos.
- El **backlog** (stories, sprints, dependencias) queda registrado en Forja, visible en **Tickets**.

## Paso 4 — Exportar el backlog a GitHub Issues

En **Tickets → Exportar a GitHub**, cada story se convierte en un **Issue real** en tu repo, con:

- El cuerpo completo: descripción, criterios de aceptación, lane (backend/frontend), sprint.
- **Dependencias nativas de GitHub** (`blocked-by`): el grafo de dependencias del backlog vive en GitHub, no solo en Forja.
- Labels por lane y sprint.

## Paso 5 — Instalar los agentes en el repo (scaffold)

En **Registry → Templates GitHub → Aplicar al proyecto**, Forja instala en el repo la *especialización*: los archivos que les dicen a los agentes de GitHub cómo trabajar en ese código:

- `AGENTS.md` — reglas generales que lee cualquier agente al abrir el repo.
- `.github/agents/python-dev.agent.md`, `react-dev.agent.md`… — el rol y los límites de cada agente por lane.
- `.github/instructions/*.instructions.md` — reglas automáticas por área de archivos.
- Workflows de calidad: **suite-integrity** (nadie borra tests), **claude-review** (review cruzado contra los criterios de aceptación), **ui-verify** (la app arranca y renderiza) y **claude.yml** (el canal de ejecución con Claude).

Estos archivos son **parte del repo**: versionados, editables (en GitHub o desde la tab Templates), y los respeta cualquier herramienta — Copilot, Claude, Codex.

## Paso 6 — Despachar trabajo a los agentes

En **Tickets**, las stories/sprints listos (dependencias cumplidas) muestran el botón **▶ Despachar**. Al pulsarlo eliges el **canal**:

- **Copilot cloud agent** — usa tu suscripción de GitHub Copilot. Cada sesión consume créditos.
- **Claude Code (Actions)** — usa tu plan de Claude vía el workflow del repo. Costo marginal $0 si tienes Claude Max/Pro.

El selector solo habilita los canales **realmente disponibles en tu GitHub** (Forja los verifica en vivo). Por defecto se despacha **el sprint completo como una unidad**: un agente implementa todas sus stories en una rama y abre **un solo PR** coherente.

## Paso 7 — Los agentes trabajan; tú miras

En **Agentes** ves las sesiones activas (con link directo a cada una) y la **cola de PRs**. Mientras el agente trabaja:

- Los checks de calidad corren en cada PR (integridad de tests, review cruzado, verificación de UI).
- Los PRs de agentes requieren tu **aprobación de workflows** la primera vez (o actívalo automático en Settings — Forja solo auto-aprueba PRs que no tocan los workflows de CI, por seguridad).

## Paso 8 — Review, merge y cascada

Revisas el PR en GitHub (o pides cambios comentando — el agente itera). Al **mergear**:

1. Los issues de las stories se cierran (Forja los cierra si el agente olvidó los `Closes #`).
2. Las stories pasan a **done** en el kanban.
3. Las dependencias se recalculan y **el siguiente sprint aparece como candidato** automáticamente.
4. Si activaste autonomía total, Forja lo despacha sola.

Ese es el loop. Se repite hasta que el backlog está vacío y el software, entregado.

---

## ¿Cuánta autonomía?

Todo el loop es gobernable por proyecto en **Settings**:

| Ajuste | Opciones | Con autonomía total |
|---|---|---|
| Despacho | tú confirmas / automático / apagado | Forja despacha sola lo que esté listo |
| Merge | tú mergeas / automático | Forja mergea PRs con checks verdes |
| Workflows | tú apruebas / auto si es seguro | Forja aprueba CI de PRs que no tocan workflows |
| Concurrencia | N agentes a la vez | limita el paralelismo |

En el extremo autónomo, tu único trabajo es **el diseño y los PRs que el reviewer marque**. En el extremo manual, confirmas cada paso. Empieza manual; suelta cuerda cuando confíes.
