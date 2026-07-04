# Calidad y seguridad: confiar sin ingenuidad

Los agentes de código son buenos y van a mejorar — pero un sistema serio no se construye sobre "confía en el agente". Forja usa **verificación en capas**: determinista donde el fallo es barato de detectar y probable, agéntica donde hace falta juicio, y humana donde importa de verdad.

## Las capas, de abajo arriba

### 1. Piso determinista (no requiere juicio, no se puede engañar fácil)

- **suite-integrity** — en cada PR cuenta los marcadores de test (`def test_`, `it(`, `testWidgets(`) y **falla si bajan**. Ataca el reward-hack número uno de los agentes: borrar el test que no pasa.
- **ui-verify** — en PRs de frontend, construye la app, la arranca y verifica con un browser real que la pantalla renderiza sin errores de JavaScript. "Los tests pasan" es una señal; "la app abre" es otra mejor.

### 2. Verificación agéntica (juicio barato)

- **claude-review** — un modelo *distinto* al que implementó revisa el diff contra los **criterios de aceptación** de las stories del PR, y deja una review formal: request-changes si hay un bloqueador, approve con notas si no. El review cruzado (un modelo revisando a otro) evita el punto ciego de auto-revisarse.

### 3. Gate humano (donde importa)

- **El diseño**: cada fase de Studio espera tu aprobación. Es el gate de mayor apalancamiento.
- **El merge**: por defecto, ningún PR entra a `main` sin tu click. (Puedes delegarlo con `merge: auto` — solo mergea PRs con todos los checks verdes y sin review negativa.)

## La política de aprobación de workflows

GitHub deja los CI checks de un PR de agente en estado *"awaiting approval"* — una protección real: un agente malicioso podría modificar el CI para exfiltrar secretos. Forja la respeta con una política precisa:

- **Manual** (default): tú apruebas cada uno (botón en la vista Agentes).
- **Auto si es seguro**: Forja aprueba automáticamente **solo si el diff del PR no toca `.github/workflows/**`**. Un PR que modifica el CI siempre espera a un humano — incluso si el click viene de la UI de Forja, la política se aplica igual.

## Qué puede tocar cada quien

- Los **agentes de ejecución** trabajan con los permisos de TU canal (tu Copilot, tu plan Claude) en TU repo — Forja no les presta credenciales propias.
- El **conductor** opera con permisos acotados de la GitHub App: issues, PRs, actions, contents del repo del proyecto. Los webhooks van firmados (HMAC).
- Las **personas de diseño** corren en la infraestructura de Forja y solo producen documentos — no tocan repos de código durante el diseño.

## Cuando algo falla (y algo siempre falla)

| Fallo | Qué pasa |
|---|---|
| La sesión del agente muere (límite, error) | El **checkpoint de rescate** ya pushó el trabajo parcial; el barrido devuelve las stories a backlog |
| El PR mergeó pero los issues no cerraron | El conductor los cierra y la cascada sigue |
| Un canal está caído | Failover automático al alterno en el despacho; o elige tú el canal en el picker |
| Una story quedó colgada | **⟲ Reencolar** en su drawer: vuelve a backlog, sesión limpia, lista para re-despachar |

La filosofía: **ningún fallo de agente debe costar más que re-despachar**. El trabajo pusheado nunca se pierde; el estado siempre se puede reconciliar desde GitHub, que es la verdad.
