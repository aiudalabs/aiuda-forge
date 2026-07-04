# Operación: settings, costos y troubleshooting

## Los settings que gobiernan tu proyecto

Todo en **Settings → Proyecto**, por proyecto:

| Setting | Qué controla | Recomendación |
|---|---|---|
| **Unidad de ejecución** | sprint (1 PR por sprint) o story (1 PR por story) | sprint — menos PRs, más coherencia |
| **Modo de merge** | manual (tú) o auto (checks verdes → merge) | manual hasta confiar; auto después |
| **Despacho** | approve (tú confirmas) / auto / off | approve al principio |
| **Aprobación de workflows** | manual / auto si es seguro | auto_if_safe (la política protege el CI) |
| **Concurrencia máxima** | cuántas stories con agente a la vez | 1–2 si los sprints comparten módulos |
| **Ejecutor** | canal por defecto (Copilot / Claude Actions) | el que tengas contratado; se puede elegir por despacho |
| **Modelo por lane** | qué modelo usa cada lane en el canal Copilot | frontera para lanes críticos |

## Los costos, sin sorpresas

- **Diseño (Studio)**: corre en Forja con modelos frontera. Es el costo de Forja.
- **Ejecución**: corre en TU GitHub con TUS cuentas:
  - Canal Copilot → consume **créditos de tu suscripción Copilot** (facturación por uso de GitHub).
  - Canal Claude Actions → consume **tu plan de Claude** (con Max/Pro, costo marginal cero; ojo a los límites de sesión de 5 horas).
- La vista **Spend** muestra el gasto de diseño y, cuando el proyecto vive en una organización, el consumo de Copilot/Actions del ciclo desde la facturación de GitHub.

## Troubleshooting: los casos que vas a ver

**"El sprint quedó en running pero no pasa nada."**
La sesión del agente murió (límite de sesión de Claude, créditos, incidencia). Abre la story → **⟲ Reencolar** → **▶ Despachar** y elige canal (el picker te dice cuáles están disponibles). El trabajo parcial pusheado no se pierde.

**"Mergeé y el kanban no se movió."**
La proyección se actualiza cada ~25s en local (instantánea con webhooks en producción). Si un issue no cerró con el merge, el conductor lo cierra en el siguiente ciclo. Botón de sync manual en Tickets si tienes prisa.

**"El PR del agente no corre los checks."**
Están esperando aprobación de workflows (protección de GitHub para PRs de bots). Apruébalos desde la vista **Agentes** (botón ✓) o activa `auto_if_safe` en Settings.

**"Copilot falla al arrancar la sesión."**
Tres causas históricas, todas con fix permanente en los templates: checkout propio en el setup (eliminado), caches que dependen del repo (eliminados), pasos sin guards (guardeados). Si aparece una nueva: el log de la sesión en GitHub la muestra, y el sprint se reencola por el otro canal mientras tanto.

**"Claude (Actions) terminó 'success' pero sin PR."**
Casi siempre límite de sesión del plan. El **checkpoint de rescate** habrá pushado el trabajo y abierto un PR draft con lo que existía. Reencola lo que falte cuando el límite se resetee (o despacha por Copilot).

**"Quiero que el agente haga X de forma particular."**
- Para todos tus proyectos futuros: edita el template (Registry → Templates GitHub).
- Solo para este proyecto: edita el archivo en el repo (`.github/agents/…`, `AGENTS.md`).
- Solo para un despacho: escribe el matiz en la story antes de despachar (los criterios de aceptación del issue son la especificación que el agente sigue).

## El orden de un proyecto nuevo (chuleta)

1. Studio: crear proyecto → diseñar con gates → aprobar backlog.
2. Mergear el PR de docs (queda `docs/` en `main`).
3. Tickets → **Exportar a GitHub** (issues + dependencias).
4. Registry → Templates → **Aplicar al proyecto** (agentes + checks al repo).
5. Si usarás el canal Claude: añadir el secret `CLAUDE_CODE_OAUTH_TOKEN` al repo (una vez).
6. Tickets → **▶ Despachar** el primer sprint → elegir canal.
7. Revisar y mergear PRs; la cascada hace el resto.
