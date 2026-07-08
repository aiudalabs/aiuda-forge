# Plan — Rebanada C: vista Especificación unificada + versionado + refine

**Fecha:** 2026-07-06 · **Alcance:** consolidar la vista de la Especificación (un solo hogar), con versionado por documento sacado del history de la rama `design`, refinación conversacional por doc, Change Request a nivel proyecto, y los mockups agrupados. Cierra el modelo que validamos en el mockup.

**Depende de:** Rebanada A (i18n) y B (re-run con feedback) — ya hechas. El refine usa `rerunStep(run, step, feedback)` de B.

---

## 1. Lo que YA existe (reusar, no reinventar)

- **`StudioDocs.tsx`** ya ES la vista Especificación: `docs-layout` (tree 248px + contenido), `DOC_META` (íconos/orden), visor markdown + mockups en iframe.
- **Endpoints de docs con `?ref=`**: `listProjectDocs(projectId, ref)` y `getProjectDoc(projectId, path, ref)` — hoy default `"dev"` (rama vestigial). Cambiar a `design` = un default.
- **`github.ReadFile(repo, path, ref)`** — leer un doc en cualquier commit/rama (para ver una versión vieja).
- **Commits a `design`** en cada aprobación de fase (Rebanada e51fb03) — la data del history YA se está generando.
- **`rerunStep(run, step, feedback)`** (B) — el backbone del refine.

## 2. Lo que falta

### Backend (chico)
1. **Default de la vista a `design`** (era `dev`) en `listProjectDocs`/`getProjectDoc`.
2. **Helper `github.ListCommitsForPath(repo, branch, path)`** → `gh api repos/{slug}/commits?sha=<branch>&path=<path>` → lista `{sha, date, message}`. Es el history/versiones de un doc.
3. **Endpoint `GET /projects/{id}/docs/history?path=&ref=design`** → usa el helper. Devuelve las versiones de un doc.
   (Leer una versión puntual ya sale con `getProjectDoc(..., ref=<sha>)`.)

### UI
4. **Chips/timeline de versión por doc** (usa el endpoint history). v1 si nunca cambió; v1·v2·v3 si evolucionó; click = time-travel (lee ese `sha`).
5. **Refine-chat por doc** — input debajo del doc → `rerunStep(run, stepId, feedback)`. **Decisión D1: qué run.**
6. **Change Request a nivel proyecto** — mover el botón al header de la vista (no dentro de una fase), su modal ya existe (renombrado en A).
7. **Mockups agrupados** — nodo colapsable "Mockups" en `StudioDocs` (los html van adentro), en vez de 5 sueltos.
8. **Consolidación** — decidir la relación Studio (pipeline/run) ↔ Especificación (**D4**).

## 3. Sub-rebanadas (orden de menor a mayor riesgo)

- **C1 — Backend del versionado.** Default `design` + `ListCommitsForPath` + endpoint `/docs/history`. Testeable (helper + handler). Sin UI todavía.
- **C2 — Chips/timeline de versión.** UI en `StudioDocs`: badge de versión por doc + selector de versiones + time-travel (lee `ref=<sha>`).
- **C3 — Refine-chat por doc.** Input conversacional debajo del doc → `rerunStep` con feedback. Resuelve D1.
- **C4 — CR a nivel proyecto.** Reubicar el botón + scope claro.
- **C5 — Mockups agrupados.** Nodo colapsable en el tree.
- **C6 — Consolidación / navegación.** Fusionar o clarificar Studio ↔ Especificación (D4).

Cada sub-rebanada se commitea sola. C1 y C5 son las más seguras; C3 y C6 las que tienen decisión de fondo.

## 4. Decisiones abiertas (a resolver antes de C3/C6)

- **D1 — ¿El refine sobre un doc re-corre cuál run?** La Especificación es repo-backed (no atada a un run); el re-run necesita un `runId`. Propuesta: **el último run de diseño del proyecto** (design o iterate). Si no hay run activo, el refine crea uno. — el punto más delicado.
- **D2 — Granularidad del versionado.** ¿Por commit, o agrupado por CR (varios docs suben juntos)? Propuesta: **por commit para el doc** (v1/v2/v3) + el **changelog agrupa por CR** a nivel proyecto (el commit lleva en su mensaje qué lo originó).
- **D3 — ¿La vista lee `design` (working) o `main` (released)?** Propuesta: **`design` con indicador**; `main` es "publicado" (lo que sale por el PR final). Quizás un toggle "trabajo / publicado".
- **D4 — ¿Studio y Especificación se fusionan?** Propuesta: **una sola vista "Especificación"**; el pipeline/gates/generación-en-vivo aparece como *estado* cuando hay un run activo. Studio (el pipeline suelto) deja de ser una sección aparte. — refactor de nav, el más grande.

## 5. Nota de despliegue

C necesita ver los commits a `design` de verdad → **al empezar C conviene rebuildear el control** (hoy corre binario viejo) y verificar A+B+C end-to-end vivos, con un run de diseño real que commitee a `design`.
