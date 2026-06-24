# LOG — aiuda-factory-ui

- Leídos los 4 docs de diseño (15 mockup · 16 spec · 17 plan de conexión · 14 contrato API).
- Scaffold Next.js (App Router + TS), TanStack Query, sin framework CSS (tokens de marca).
- Capa de cliente `lib/api.ts` con auto-detección mock/real (health `/healthz`) + datos del mockup.
- Layout: sidebar (Board·Tickets·Studio·Registry·Gasto·Settings) + topbar (costo · modo · campana · proyecto).
- Board cableado: stat cards, lista de runs, live-log (WS), drawer (timeline · eventos · diff · costo), aprobar/rechazar/cancelar/retry/borrar, pausar/reanudar fábrica.
- Shells fieles al mockup (Tickets · Studio · Registry · Gasto · Settings) con TODO(endpoint).
- README con cómo correr (mock/real) + cómo levantar el control-plane.

## T4 — Wiring del resto de secciones (2026-06-23)

Cableadas contra los endpoints reales con fallback mock intacto:

### Registry (`/registry`)
- GET `/registry/{kind}` → `useRegistryList` → lista de IDs con tabs agents/skills/workflows.
- GET `/registry/{kind}/{id}` → `useRegistryItem` → preview en tarjeta + textarea en modal.
- PUT `/registry/{kind}/{id}` → `useSaveRegistryItem` → errores 400 mostrados inline.
- DELETE `/registry/{kind}/{id}` → `useDeleteRegistryItem` → con confirmación.
- Mock: `mockRegistryIds` + `mockRegistryContent` en `lib/mock.ts`.

### Settings (`/settings`)
- GET `/settings` → `useSettings` → formulario reactivo (MCP, agent auth, sandbox, merge policy).
- PUT `/settings` → `useSaveSettings` → secretos enmascarados devueltos sin cambio si no se editan.
- Mock: `mockSettings` en `lib/mock.ts`.

### Spend/Gasto (`/spend`)
- GET `/metrics` → `useMetrics` → total_cost_usd, acceptance_rate, cost-per-accepted-change, barras por workflow y paso.
- `getSpendToday()` actualizado para mapear `total_cost_usd` desde `/metrics`.
- Mock: `mockMetrics` en `lib/mock.ts`.

### Tickets (`/tickets`)
- GET `/tickets` del orquestador → `useTickets` → tabla + grafo de dependencias.
- Orquestador en `ORCHESTRATOR_URL` (:9090, env-overridable), con sondeo propio `/healthz` y fallback mock.
- Los `run_id` abren el `RunDrawer` del Board.
- Mock: `mockOrchestratorTickets` en `lib/mock.ts`.

### api.ts:211 (rejectStep)
- Confirmado cableado: `POST /runs/{id}/steps/{step}/reject` con `{ reason }`. No era stub — ya estaba apuntando al endpoint real.

### Sidebar projects selector
- Dejado como mock/TODO per instrucciones; no existe endpoint `/projects`.

### Build gate
- `npm run lint` → 0 errores (warning pre-existente de fuente custom).
- `npm run build` → 9 rutas generadas, 0 type errors.

### Commits
- `5a7f786` feat(core): types + mock + API + hooks para Registry/Settings/Metrics/Tickets
- `0e1516f` feat(registry): cablear RegistryView contra GET/PUT/DELETE /registry/{kind}
- `5378137` feat(settings): cablear SettingsView contra GET/PUT /settings
- `a78182b` feat(spend): cablear SpendView contra GET /metrics
- `e086e0b` feat(tickets): cablear TicketsView contra GET /tickets del orquestador

### Decisiones tomadas (UX ambigua)
- **Registry — modal unificado**: un solo modal editor de texto crudo (YAML/md) en vez de formularios de campos distintos por tipo. Fiel al mockup ("YAML ⇄") y más simple.
- **DepGraph**: grafo lineal raíz → hijos en vez de SVG D3. Suficiente para el mockup, sin deps pesadas.
- **tokens en getSpendToday**: el contrato GET /metrics no incluye tokens; se devuelve "—" en lugar de inventar un valor.
