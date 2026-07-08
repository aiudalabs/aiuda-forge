# Manual oficial — Fluxo (by AIuda Labs)

**Fluxo** es una **fábrica autónoma de software**: convierte una idea en un producto real
—diseño gateado por humanos → backlog → Pull Requests en GitHub que un agente de IA
implementa, verifica y despliega— con la mínima intervención humana.

Este manual cubre **desplegar, usar y mantener** el sistema. Disponible en:

| Idioma | Carpeta |
|---|---|
| 🇪🇸 Español | [`es/`](es/) — versión de referencia |
| 🇬🇧 English | [`en/`](en/) — *(en preparación)* |

## Índice del manual (es)

| # | Doc | Para quién |
|---|---|---|
| 0–2 | [**Despliegue**](es/01-despliegue.md) — pre-requisitos, instalación paso a paso, post-despliegue | Quien instala/opera la infraestructura |
| 3 | [**Uso — un proyecto de punta a punta**](es/02-uso-proyecto-e2e.md) — crear, diseñar, aprobar gates, change requests, refine, tickets manuales, sprints | Quien usa Fluxo para construir productos |
| 4 | [**Mantenimiento y operación**](es/03-mantenimiento.md) — actualizar, backups, logs, troubleshooting, escalado | Quien mantiene el sistema en producción |
| — | [**Arquitectura**](../ARCHITECTURE.md) · [**Verificación E2E**](../PLAN-2026-07-07-verificacion-e2e-multiplataforma.md) | Referencia técnica profunda |

> **Nota de vocabulario:** el producto se llama **Fluxo**; los identificadores internos
> (módulo Go `forge`, kernel `vibeforge`, prefijo de env `VIBEFORGE_*`, ruta `/forge-api`)
> conservan el nombre técnico histórico — no se renombran.
