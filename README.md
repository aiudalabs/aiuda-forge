# aiuda-factory-ui

Frontend de **Aiuda Factory** — la UI de una fábrica de software autónoma que corre sobre el
kernel de ejecución [vibeforge-v2](../vibeforge). Es un **cliente puro**: render del contrato de
la API + eventos del bus. Ninguna lógica de negocio del flujo vive aquí — toda acción = un
endpoint del contrato (ver `docs/comparativa/14_API_CONTRACT_v2.md`).

Identidad visual de [aiudalabs.com](https://aiudalabs.com): Satoshi · Instrument Serif ·
JetBrains Mono · cream `#FAF8F4` · accent `#E8440A`. Portado fiel del mockup
`15_UI_MOCKUP_AIUDA_FACTORY.html`.

## Stack

- **Next.js** (App Router) + **TypeScript**
- **TanStack Query** — estado de servidor (runs, metrics, control)
- **WebSocket** (`/ws`) — live-log y actualización en vivo del board
- CSS con los tokens de marca (variables CSS, sin framework)

## Correr

```bash
npm install
npm run dev          # http://localhost:3000
```

Por defecto la UI apunta al control-plane en `http://localhost:8080`. Si **no responde**, la UI
arranca en **MODO MOCK** automáticamente (datos de ejemplo del mockup) para que se pueda
construir/ver sin el backend arriba. Un badge en la topbar indica el modo (`● mock` / `● en vivo`).

### Variables de entorno

Copia `.env.example` a `.env.local`:

```bash
# URL del control-plane (kernel API). Default: http://localhost:8080
NEXT_PUBLIC_VIBEFORGE_API_URL=http://localhost:8080

# Fuerza modo mock aunque la API responda (demo / desarrollo de UI). "1" para forzar.
NEXT_PUBLIC_FORCE_MOCK=
```

## Modo real — levantar el control-plane

El Board está **100% respaldado** por la API del kernel hoy (doc 17 §1). Para verlo en vivo,
levanta el control-plane de `vibeforge-v2`:

```bash
# en el repo del kernel (vibeforge-v2)
go run ./cmd/control        # expone :8080 — /runs, /ws, /control/pause|resume, …
```

Con eso arriba, la UI detecta la API (health `/healthz`), cambia a **modo en vivo**, y el Board:
lista runs (`GET /runs`), abre el detalle (`GET /runs/{id}`), reproduce eventos
(`GET /runs/{id}/events?after=`), recibe push por WS (`/ws`), y opera runs (lanzar/cancelar/
retry/aprobar/rechazar/borrar + pausar/reanudar la fábrica).

## Qué está cableado vs. shell

| Sección | Estado | Respaldo |
|---|---|---|
| **Board · Runs** | ✅ **cableado en vivo** | `/runs*`, `/ws`, `/control/*` (existe hoy) |
| **Tickets** | 🔌 shell | `TODO: GET /tickets` (orquestador, MCP) |
| **Studio** | 🔌 shell | `TODO: servicio Studio` (fases · handoff) |
| **Registry** | 🔌 shell | `TODO: CRUD /registry/{agents,skills,workflows}` |
| **Gasto** | 🔌 shell | `TODO: GET /metrics · /analytics` (desglose) |
| **Settings** | 🔌 shell | `TODO: store + GET/PUT /settings` |

Cada shell lleva un comentario `TODO(endpoint)` con el endpoint que consumirá cuando exista
(plan de conexión: doc 17). Las shells son fieles al mockup y navegables, listas para cablear.

## Gates

```bash
npm run build        # producción
npm run lint         # eslint (next/core-web-vitals)
```

## Estructura

```
src/
  app/                 # rutas (App Router): board (/), tickets, studio, registry, spend, settings
  components/
    board/             # Board cableado: StatCards, RunCard, RunDrawer, LiveLog, DiffBox, …
    tickets|studio|registry|spend|settings/   # shells fieles al mockup
    Sidebar · Topbar · Logo
  lib/
    api.ts             # capa única de cliente (HTTP) + auto mock/real
    types.ts           # tipos del contrato (runs/steps/events)
    hooks.ts           # TanStack Query + tiempo real (WS)
    ws.ts              # conexión única al event bus
    mock.ts            # datos de ejemplo (= los del mockup)
    config.ts          # VIBEFORGE_API_URL, flags
```

## Referencias de diseño

- `15_UI_MOCKUP_AIUDA_FACTORY.html` — referencia visual exacta
- `16_UI_FUNCTIONAL_SPEC.md` — spec funcional (6 secciones, §10 revisión)
- `17_UI_BACKEND_CONNECTION_PLAN.md` — qué está respaldado hoy vs. falta
- `14_API_CONTRACT_v2.md` — contrato de endpoints + eventos
