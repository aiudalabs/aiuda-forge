# Plan — C6b: fusión Studio ↔ Especificación (una sola vista)

**Fecha:** 2026-07-06 · **Checkpoint previo:** tag `v1.1.0-spec-rework`.
**Objetivo:** eliminar la dualidad de tabs "Especificación" (StudioDocs) vs "Diseño" (StudioView). Una sola vista: la **Especificación** es el hogar; el **pipeline** (fases/gates/generación en vivo) aparece **inline como estado** cuando hay un run activo. Esto mata la confusión "Especificación muestra lo viejo / Diseño lo nuevo".

## Regla de oro (NO romper)
El flujo **aprobar/rechazar gates** (`PhasePanel`, `useApprove`/`useReject`) es **crítico** — si se rompe, el usuario no puede aprobar diseños. Preservarlo EXACTO. Typecheck verde tras cada paso. Commit incremental. Mantener i18n (es/en/pt) para todo string nuevo.

## Estado actual
- `Studio.tsx` — shell con 2 tabs: `spec`→`<StudioDocs/>`, `design`→`<StudioView/>`. Sin proyecto → `<StudioEntry/>`.
- `StudioView.tsx` (**1061 líneas**, monolito) contiene:
  - `StudioView` — lista de runs + `ProjectCard` + `NewProjectModal`.
  - `ProjectDetail` (l.320) — **el pipeline**: stepper de fases + `PhasePanel`s + relaunch + `IterationModal`.
  - `PhasePanel` (l.584) — artefacto de la fase + **gate aprobar/rechazar** (crítico).
  - `PhaseStatusBadge`, `MockupsArtifact`, `BacklogArtifact`, `IterationModal`, `NewProjectModal`.
- `StudioDocs.tsx` — la Especificación: ya tiene chips de versión, refine por doc, CR a nivel proyecto (C4), chip/banner de run activo (C6a), changelog, toggle Work/Published (D3), y `onOpenPipeline` (hoy cambia al tab design).

## Fase 1 — Modularizar StudioView (el archivo es demasiado grande)
Extraer, SIN cambiar comportamiento, a archivos separados bajo `src/components/studio/`:
- `PhasePanel.tsx` ← `PhasePanel` + `PhaseStatusBadge` + `MockupsArtifact` + `BacklogArtifact`.
- `DesignPipeline.tsx` ← `ProjectDetail` renombrado a `DesignPipeline`, con prop `{ runId: string }`; hace su propio `useDesignRun(runId)`, stepper, `PhasePanel`s, aprobar/rechazar, live-log. **Autocontenido.**
- `StudioModals.tsx` ← `NewProjectModal` (y `IterationModal` si aún se usa).
- `StudioView.tsx` queda como la **lista/entrada** delgada (o se absorbe en Fase 3).
Verificación: typecheck + la vista Diseño se ve/funciona idéntica (aprobar un gate sigue andando). Commit.

## Fase 2 — Pipeline reutilizable
`DesignPipeline` debe renderizarse solo (dado un `runId`), sin depender del estado de `StudioView`. Probar embebiéndolo temporalmente. Commit.

## Fase 3 — Fusionar en la Especificación
- `Studio.tsx`: quitar los 2 tabs co-iguales. Render único: `<StudioDocs/>` (+ `<StudioEntry/>` si no hay proyecto).
- `StudioDocs.tsx`: cuando hay run activo (`useActiveDesignRun`), renderizar `<DesignPipeline runId={activeRun.id}/>` **inline** — como panel arriba de los docs (o expandible desde el banner/"Ver pipeline"). Cuando NO hay run activo, solo los docs.
- Reconciliar duplicados: StudioDocs ya tiene el modal de CR (C4) y el banner de aprobación. La CR/relaunch del `IterationModal` viejo se unifica con la de StudioDocs (una sola).
- `onOpenPipeline` deja de cambiar de tab: hace scroll/expande el panel inline.
Verificación: typecheck + E2E manual — con un run AWAITING, el panel aparece en la Especificación y **aprobar el gate funciona**; sin run, solo docs. Commit.

## Fase 4 — Limpieza
- Quitar código muerto (tabs, imports, StudioView si quedó vacío, `studio.tab.*` i18n si ya no se usan).
- Confirmar que no quedó ninguna referencia rota. Typecheck + `npm run build` si aplica. Commit.

## Criterio de aceptación
1. Una sola vista; no hay tab "Diseño" separado.
2. Con run activo: pipeline inline en la Especificación; **aprobar/rechazar gates funciona igual que antes**.
3. Sin run: solo la Especificación (docs + versiones + refine + CR + changelog).
4. Typecheck verde; i18n completo; commits incrementales.
5. Ningún archivo nuevo monstruoso: StudioView desglosado en módulos de <~350 líneas.
