# LOG — aiuda-factory-ui

- Leídos los 4 docs de diseño (15 mockup · 16 spec · 17 plan de conexión · 14 contrato API).
- Scaffold Next.js (App Router + TS), TanStack Query, sin framework CSS (tokens de marca).
- Capa de cliente `lib/api.ts` con auto-detección mock/real (health `/healthz`) + datos del mockup.
- Layout: sidebar (Board·Tickets·Studio·Registry·Gasto·Settings) + topbar (costo · modo · campana · proyecto).
- Board cableado: stat cards, lista de runs, live-log (WS), drawer (timeline · eventos · diff · costo), aprobar/rechazar/cancelar/retry/borrar, pausar/reanudar fábrica.
- Shells fieles al mockup (Tickets · Studio · Registry · Gasto · Settings) con TODO(endpoint).
- README con cómo correr (mock/real) + cómo levantar el control-plane.
