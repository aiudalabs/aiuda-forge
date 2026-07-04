# Agentes y templates: quién trabaja y con qué reglas

En Forja hay **dos familias de agentes**, y viven en lugares distintos a propósito.

## 1. Las personas de diseño (viven en Forja)

Son los agentes de Studio: analyst, pm, architect, designer, scrum-master. Corren en la infraestructura de Forja durante el flujo de diseño y producen los documentos del proyecto.

- Se ven y editan en **Registry → Agents** (su persona en markdown) y **Registry → Workflows** (el flujo de fases).
- Editar una persona **cambia cómo diseña** a partir del siguiente run: si quieres que tu architect siempre exija un esquema de datos versionado, edítalo ahí.

## 2. Los agentes de ejecución (viven en TU repo)

Los que escriben código — python-dev, react-dev, flutter-dev — **no son procesos de Forja**: son *instrucciones que viajan con el repo* y que cualquier agente de GitHub (Copilot, Claude, Codex) lee al trabajar en él:

| Archivo en el repo | Qué hace |
|---|---|
| `AGENTS.md` | Lo primero que lee **cualquier** agente: reglas generales, mapa del proyecto, comandos de validación |
| `.github/agents/python-dev.agent.md` | El rol del agente backend: qué toca, qué no, cómo valida |
| `.github/agents/react-dev.agent.md` | Ídem frontend |
| `.github/instructions/backend.instructions.md` | Reglas automáticas para archivos de `src/**` |
| `.github/workflows/*` | Los checks de calidad y el canal Claude |

Ventajas de que vivan en el repo: están **versionados** (cambiarlos es un commit revisable), funcionan con **cualquier herramienta** (hoy Copilot, mañana lo que sea), y el agente que trabaja el repo los tiene siempre frescos — sin depender de que Forja esté en línea.

## Los Templates GitHub: el molde

**Registry → Templates GitHub** es el molde del que nacen esos archivos. Está organizado por stack:

- **Común a todos** — AGENTS.md, canal Claude, QA de integridad y review.
- **Python + React** — agentes y setup para FastAPI + React.
- **Flutter + Firebase** — agentes y setup para apps móviles.

Cada card se abre, se lee renderizada (markdown o YAML resaltado) y se **edita**. Dos alcances:

1. **Editar el template** → todos los proyectos que crees a partir de ahora nacen con tu versión. Es tu *estándar de casa*.
2. **"Aplicar al proyecto"** → empuja la versión actual de los templates al repo del proyecto activo (solo actualiza lo que cambió).

## ¿Y si quiero un cambio SOLO en un proyecto?

Edita el archivo directamente **en el repo del proyecto** (en GitHub): `tu-repo/.github/agents/python-dev.agent.md`. Es un archivo normal — el siguiente agente que trabaje lo leerá cambiado. El template de Forja no se entera (y "Aplicar al proyecto" lo sobreescribiría, así que si personalizas un proyecto, evita re-aplicar ese archivo).

## El setup del entorno (la pieza delicada)

`copilot-setup-steps.yml` es el único template que **ejecuta** (prepara Python/Node/Postgres antes de que el agente trabaje) y por eso el único que puede romperse con cambios del harness de GitHub. Reglas aprendidas operándolo:

- **Sin checkout propio** — el harness de GitHub clona por su cuenta; un checkout extra mata la sesión.
- **Sin caches que dependan de archivos del repo** — el workspace puede estar vacío en la fase de setup.
- **Todo con guards** — cada paso verifica que su carpeta/archivo exista antes de correr; un repo a medio construir no debe romper el setup.
- **Mínimo necesario** — cuanto menos haga este archivo, menos puede fallar.
