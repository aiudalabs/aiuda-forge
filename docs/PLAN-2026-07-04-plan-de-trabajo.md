# Plan de trabajo — 2026-07-04 (post-auditoría de 9 agentes)

Origen: doble ronda de agentes (QA, backlog, bug New-story, seguridad engine, consola funcional,
GTM/competencia, UX en vivo, análisis Spec Kit, diseño del ciclo continuo). Informes registrados en
`~/.devtrace/decisions/aiuda-forge.md` (entradas 2026-07-04). Estado vivo en la task list de la sesión.

## En curso
1. **[SEGURIDAD] C1 + A1** — ownership en toda la superficie de tickets (cross-tenant CONFIRMADO:
   `GET /stories` sin filtro, mutaciones sin propiedad; patrón `access.go`, 404 no 403, service token
   intacto) + callback OAuth sin `state` no emite sesión (login CSRF, `ghauth.go:180`). *Agente en worktree.*
2. **[BUG] New story completo** — project_id del proyecto activo + campo descripción (usar `body`) +
   export a GitHub Issues al crear story manual (best-effort, idempotente por `external_ref`); sin esto
   el conductor jamás despacha stories manuales. *Agente en worktree.*
5. **[INVESTIGACIÓN] Cerebro/grafo de código** — paisaje de repo-maps/grafos para LLMs (Aider, SCIP,
   Potpie, Cognee, stack-graphs, contexto nativo de Copilot/Claude) para diseñar el cerebro de Forja.

## Siguiente
3. **Ciclo continuo F1** (1–2 días) — botón "Nueva iteración" → `POST /runs workflow:iterate`
   (`iterate.yaml` + `iteration-planner` YA existen, desconectados de la UI); reporte de ids `skipped`
   en publish; ocultar "Relanzar diseño" en publicados (es una trampa: regenera todo y las colisiones
   se descartan en silencio); iterate basado en `main`.
4. **Ciclo continuo F2** — `docs_pr` en iterate + `docs/iterations/<slug>.md` append-only (nunca
   reescribir el PRD monolítico; patrón BMAD brownfield, dolor #1 de la comunidad Spec Kit); historial
   de iteraciones en Studio.
6. **Settings reestructura** — por alcance: Conexiones (GitHub + Canal Claude + notifs) ARRIBA →
   proyecto con preset maestro de autonomía [Manual · Asistido · Autónomo] + avanzado colapsado en
   lenguaje humano → cuenta → legacy colapsado. Copy "factory"→Forja.
7. **Onboarding wizard** — `/onboarding` (setup_url del manifest, hoy 404); 5 pantallas GTM; StudioEntry
   chequea `/auth/github/status` antes de crear y ofrece remediación (H1).
8. **Spec Kit vía (a)** — vampirizar (MIT): S1 gate conversacional con el motor `clarify` (≤5 preguntas
   con recomendación, reintegración al doc) = pendiente #3; S2 checklists auto-validados por fase;
   S3 paso `analyze` + `constitution` por proyecto. NO ejecutar spec-kit como motor (altitud per-feature,
   acopla a roadmap GitHub). Clon de referencia en el scratchpad de la sesión.
9. **Brain** — recablear tools a GitHub-native (issues/PRs/sesiones) o retirar del nav; hoy diagnostica
   problemas fantasma del ejecutor legacy apagado.
10. **Móvil + menores** — Settings apila <700px (controles de autonomía fuera de pantalla en 390px),
    header/toolbars responsive, nav con labels/aria; M2 (probe fallido → picker vacío sin razón),
    M3 (KPI Open PRs desde `/projects/{id}/prs`); #17 hydration, EnsureDevBranch vestigial, "— tok",
    i18n Docs/Spend/Registry, screenshots sueltos en repo root, limpiar ~17 ítems de CLAUDE.md
    (obsoletos por el pivote / resueltos sin marcar), decidir `engine/vendor/` (commit vs gitignore),
    push de la rama (48 commits adelante).

## Decisiones de producto que enmarcan el plan
- **Diferencial declarado**: Forja vive en UI — visual, con clics, móvil, para quien no usa terminal
  (vs Spec Kit/CLI). Toda decisión de UX se mide contra ese estándar.
- **Moat real** (GTM 2026-07): diseño gobernado aguas arriba del issue + español-first LATAM + "tu repo,
  tu compute, tu agente"; el conductor NO es el moat (GitHub Agent HQ). Tier Agency = revenue real.
- **Ciclo continuo**: unidad de iteración = change request → delta (nuevas stories con deps a lo
  existente); docs/ en `main` es la verdad del QUÉ; GitHub (issues/PRs) es la verdad del ESTADO.
- **Cerebro** (en diseño): grafo producto↔código (story↔PR↔archivos↔decisiones) mantenido por el engine
  vía webhooks post-merge; contexto quirúrgico inyectado en cada issue al despachar ("archivos a tocar").
