# Plan — Pipeline de diseño cíclico (brief → sprint), humano-opcional, cero hardcode

**Fecha:** 2026-07-05 · **Alcance:** SOLO la parte de diseño (idea → brief → PRD → … → backlog/stories dev-ready). El plano de build (despachar stories a GitHub) se difiere.

**Tesis:** si el diseño produce documentación bien elaborada y curada, construir encima es fácil. Atacamos Forja por partes, empezando por el diseño.

---

## 1. Principios (no negociables)

1. **Cero metodología en el código.** El engine es un ejecutor genérico de pasos YAML. Etapas, personas y skills viven en `registry/` como YAML + markdown, editables sin recompilar. *(El plano de diseño ya cumple — `design.yaml` es datos. El hardcode a eliminar está en el plano de build `conductor/dispatch.go`, fuera de alcance.)*
2. **Método en UN solo lugar.** El método vive en el archivo `registry/skills/<x>.md` y se inyecta en la persona en runtime (`manifest.go:67-81`, confirmado). La persona queda **delgada** (rol + cómo trabaja) — NO se inlinea la skill. *(Deuda: `pm.md` y `architect.md` hoy duplican la skill inline con un comentario falso; des-duplicar.)*
3. **Humano opcional.** Aprobar, editar o corregir en cada gate — solo si quiere. Modo auto por proyecto.
4. **Diseño cíclico + delta full.** El diseño no "termina": se cierra una versión y se vuelve. Cada iteración es un **delta** sobre la verdad canónica. Adoptamos el modelo delta COMPLETO (schema requirement+scenario, semántica ADDED/MODIFIED/REMOVED) desde el inicio — **git provee los `changes/`/`archive/` gratis** (delta = diff del PR; apply = merge a `main`).

## 2. Fases del pipeline (objetivo)

Revisión SDLC completa. **Se agregan** `constitution` y `data-model`. **NO son fase** (a propósito, para no crecer en docs): *clarify* (es capacidad del gate, cross-cutting) y *test-strategy* (los escenarios GIVEN/WHEN/THEN embebidos en cada requisito YA son el contrato de aceptación → alimentan Wave-V).

| # | Etapa | Persona | Skill (método) | Lee | Produce | Nuevo |
|---|-------|---------|----------------|-----|---------|-------|
| 1 | discovery | `analyst` | `discovery-brief-template` | idea | `docs/BRIEF.md` | |
| 2 | **constitution** | `principles` | `opinionated-defaults` | brief | `docs/CONSTITUTION.md` | ✅ |
| 3 | prd | `pm` | `prd-template` (scenarios) | brief, constitution | `docs/PRD.md` | |
| 4 | **data-model** | `data-modeler` | `data-model-template` | prd, constitution | `docs/DATA_MODEL.md` | ✅ |
| 5 | architecture | `architect` | `architecture-template` | prd, data-model | `docs/ARCHITECTURE.md` | |
| 6 | ui | `ux` | `ui-screens-template` | prd, architecture | `docs/UI_SCREENS.md` | ✅ skill |
| 7 | mockups | `designer` | `mockup-html`, `design-system` | UI_SCREENS, PRD | `docs/mockups/index.html` | |
| 8 | backlog | `scrum-master` | `story-template` (scenarios), `po-checklist`, `sharding-method` | prd, arch, data-model | `docs/backlog.yaml` | |
| 9 | handoff | *(ticket_publish)* | — | backlog | stories en el store | |
| 10 | docs_pr | *(pr)* | — | docs/ | PR → `main` (= apply del delta) | |

**Constitución** = doc pequeño y durable de decisiones bloqueadas (stack, modelo de negocio, presupuestos no-funcionales: seguridad/performance/i18n). Es el ancla estable del modelo delta — el único doc que no crece por-feature; las fases lo *leen* en vez de re-enviar prosa.

## 3. Modelo delta (full, desde el inicio)

- **Verdad canónica** = `docs/` en `main`.
- **Delta** = diff del PR de una design-run o iteración. Git ES el store de cambios.
- **Apply/archive** = merge a `main`. No se construye nada extra.
- **Schema** (robado de OpenSpec): requisitos como MUST/SHALL, cada uno con ≥1 `#### Scenario:` (GIVEN/WHEN/THEN); en iteraciones se marcan `## ADDED / MODIFIED / REMOVED Requirements`.
- **`validate`** (paso nuevo, CÓDIGO — determinista, vive en el kernel): lintea el schema; un spec malformado falla rápido (mata D2/#16 "encadena vacío").
- Los escenarios **son** el contrato de aceptación → heredados por las stories → alimentan la verificación Wave-V (matan el hash frágil de `.vibeforge-gate`).

## 4. Ciclo + gate humano-opcional

```
        ┌──────────────── nueva iteración / feature (change-request) ─────────────┐
        ▼                                                                         │
brief → constitution → PRD → data-model → architecture → UI → mockups → backlog ──┘
              (delta ADDED/MODIFIED/REMOVED sobre lo canónico)
```

- **v1:** anillo completo desde la idea.
- **Iteración N:** `iteration-planner` lee lo canónico + change-request → emite el delta (requisitos que cambian, sprints/stories nuevos). El PR muestra exacto qué se agrega/modifica/quita.
- **Gate modo `{auto | avisar | bloquear}`** por proyecto (D-C: bloquear en BRIEF/PRD/backlog, avisar en el resto). En cualquier gate: editar el doc, responder preguntas (#3), o rechazar-con-feedback.

## 5. Plan para movernos rápido

**Fase 0 — Método + flujo (markdown/YAML, sin recompilar) — EN CURSO.**
- Nuevas skills: `opinionated-defaults`, `ui-screens-template`, `data-model-template`.
- Nuevas personas delgadas: `principles`, `data-modeler`.
- `design.yaml` reescrito al flujo de 10 pasos + inputs corregidos a `$step.output.text` (arregla #16/D2).
- `prd-template` y `story-template` al schema requirement+scenario.

**Fase 1 — Delta + validate + ciclo en GUI + gate opcional (CÓDIGO, ~1-2 sem).**
- Paso `validate` (lint del schema) en el kernel.
- Modos de gate `{auto,avisar,bloquear}` + editar-y-aprobar inline.
- Wirear `iterate.yaml` en Studio ("Nueva iteración").
- Des-duplicar `pm.md`/`architect.md` (quitar el método inline; dejar la skill).

**Fase 2 — Handoff dev-ready + conversational gates.**
- Invocar `story-detailer` (stories dev-ready antes del build).
- Respuesta conversacional a open-questions del gate (#3).

**Fase 3 — A producción.** Deploy del pipeline de diseño (VPS + Caddy) para usarlo en real.

## 6. Decisiones (resueltas 2026-07-05)

- **D-A** Fase de modelo de datos: ✅ agregar `data-model` (entre PRD y arquitectura). Además se agrega `constitution`.
- **D-B** Profundidad delta: ✅ FULL desde el inicio (schema + semántica ADDED/MODIFIED/REMOVED; git como store).
- **D-C** Gate por defecto: ✅ bloquear en BRIEF/PRD/backlog, avisar en el resto; configurable por proyecto.
