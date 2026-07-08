# Documentación — Fluxo (aiuda-forge)

Índice de los documentos de diseño, planes y auditorías. Fluxo es una **fábrica autónoma de
software** (idea → diseño gateado → PRs en GitHub), vendida por AIuda Labs. Monorepo:
`engine/` (Go, kernel "cero metodología en código") + `console/` (Next.js).

> **Convención de nombres:** `TIPO-YYYY-MM-DD-tema.md`. Tipos: `ADR` (decisión de arquitectura),
> `PLAN` (plan de trabajo/diseño), `ROADMAP` (secuencia de features), `AUDIT`/`UI-AUDIT`
> (hallazgos), `GTM` (go-to-market). Los sin fecha son vivos (se actualizan).

## Empezá acá

| Doc | Qué es |
|---|---|
| [ARCHITECTURE.md](ARCHITECTURE.md) | Arquitectura del engine — las 4 capas, el kernel, dónde vive cada cosa. **Leer primero.** |
| [ADR-2026-07-03-studio-first-github-native.md](ADR-2026-07-03-studio-first-github-native.md) | La decisión fundacional: el diseño es el producto, la ejecución (GitHub-native) es intercambiable. |
| [PLAN-2026-07-03-pivot-github-native.md](PLAN-2026-07-03-pivot-github-native.md) | El pivote de "fábrica propia" a "Studio + Conductor sobre GitHub". |

## Diseño del producto y el pipeline

| Doc | Qué es |
|---|---|
| [PLAN-2026-07-05-pipeline-diseno-ciclico.md](PLAN-2026-07-05-pipeline-diseno-ciclico.md) | El pipeline de diseño (brief → sprint), humano-opcional, sin hardcode. |
| [PLAN-2026-07-05-brain-orquestador-maestro.md](PLAN-2026-07-05-brain-orquestador-maestro.md) | El Brain como orquestador maestro. |
| [PLAN-2026-07-06-C6b-fusion-studio-especificacion.md](PLAN-2026-07-06-C6b-fusion-studio-especificacion.md) | Fusión Studio ↔ Especificación en una sola vista. |
| [PLAN-2026-07-06-vista-spec-unificada.md](PLAN-2026-07-06-vista-spec-unificada.md) | Vista Especificación unificada + versionado + refine. |

## Verificación, calidad y multi-plataforma

| Doc | Qué es |
|---|---|
| [**PLAN-2026-07-07-verificacion-e2e-multiplataforma.md**](PLAN-2026-07-07-verificacion-e2e-multiplataforma.md) | **El plan central actual:** de "verificar artefactos" a "verificar comportamiento". Capa E2E + provisioning-linter, contrato de stack multi-plataforma, imágenes (costo/propiedad), conexión de proveedores cloud, y roadmap por sprints/sesiones. Nace del incidente E2E que expuso 8 bugs invisibles a los tests unitarios. |
| [AUDIT-2026-06-25.md](AUDIT-2026-06-25.md) | Auditoría adversarial del engine. |
| [UI-AUDIT-2026-07-03.md](UI-AUDIT-2026-07-03.md) | Auditoría de la UI de la consola post-pivote. |

## Diseño visual / UI

| Doc | Qué es |
|---|---|
| [ROADMAP-design-system.md](ROADMAP-design-system.md) | Arreglar la blandura estética en la raíz (design system). |

## Roadmaps de features

| Doc | Qué es |
|---|---|
| [ROADMAP-v1.1-v1.4.md](ROADMAP-v1.1-v1.4.md) | Roadmap de funcionalidades v1.1 → v1.4. |
| [ROADMAP-v1.3-channels.md](ROADMAP-v1.3-channels.md) | Canales + import de tickets. |
| [PLAN-2026-07-04-plan-de-trabajo.md](PLAN-2026-07-04-plan-de-trabajo.md) | Plan de trabajo post-auditoría de 9 agentes. |

## Negocio

| Doc | Qué es |
|---|---|
| [GTM-2026-07-03-onboarding-monetizacion.md](GTM-2026-07-03-onboarding-monetizacion.md) | Onboarding (auth + setup) y monetización en cloud. |

---

**Otras fuentes de verdad (fuera de `docs/`):**
- `CLAUDE.md` (raíz) — contexto de proyecto + estado de las waves de remediación + backlog vivo.
- `engine/CLAUDE.md` — **la constitución del kernel** (cero metodología en código; qué va en Go vs en DATA).
- `engine/registry/` — la metodología como DATA: `workflows/*.yaml`, `agents/*.md`, `templates/github-native/**`.
