# Desplegar Fluxo en un VPS (para ~10 usuarios)

Guía paso a paso, sin ambigüedades. Todo corre en **un solo VPS** con docker-compose detrás
de **Caddy** (HTTPS automático) en **un solo dominio**. El cómputo pesado (escribir código) lo
hace GitHub (Copilot / GitHub Actions), no este servidor — por eso un VPS mediano alcanza.

> **Estado (2026-07-09):** esta guía cubre TODO lo que agregaron los PRs #15→#29 (ceremonias de
> sprint, `release`/previews servidos, digest diario, la retro que **edita el método**, art-director).
> Si venías de una versión vieja del deploy, lee §7 (registry escribible) y §10 (imagen del sandbox):
> son los dos cambios que rompen si no los aplicás.

---

## 0. Topología (qué corre, en qué red, sobre qué volumen)

```
                         https://fluxo.aiudalabs.com
                                    │  :80 :443
                            ┌───────┴────────┐
                            │     caddy      │   TLS automático (Let's Encrypt)
                            └───────┬────────┘   red: forge-internal
                    /forge-api/* ───┤─────── todo lo demás
                                    │
                     ┌──────────────┴───────────────┐
              ┌──────┴───────┐               ┌───────┴───────┐
              │   control    │  :8080        │    console    │ :3000
              │ API + WS     │               │   (Next.js)   │
              │ agentes de   │               └───────────────┘
              │ DISEÑO (claude,│              red: forge-internal
              │  dentro del   │
              │  contenedor)  │
              └──┬────────┬───┘
   volúmenes:    │        │   red: forge-internal
   forge-data ───┤        └──────────► egress-proxy (tinyproxy, allowlist)
   (6 SQLite)    │                         redes: forge-internet + vibeforge-egress
   forge-previews┘                                        ▲
   (sitios /pv)                                           │ HTTPS_PROXY
                    DooD (socket del host)                │
              control ── docker run ──►  vibeforge-agent:local  (build del `release`:
                                          npm ci / build / firebase)  red: vibeforge-egress
                                          (SIN internet directo; sale sólo por el proxy)

  Redes:
    forge-internal  → control ↔ console ↔ caddy (sin salida a internet)
    forge-internet  → SOLO el egress-proxy (única salida real)
    vibeforge-egress→ interna, sin gateway: los sandboxes salen SOLO por el egress-proxy
  Volúmenes:
    forge-data      → los 6 SQLite (vibeforge, tickets, projects, auth, brain, billing)
    forge-previews  → los sitios estáticos que publica el paso `release` (servidos en /pv/)
    caddy-data/-config (solo prod) → certificados TLS de Caddy
```

El navegador habla **solo** con `https://fluxo.aiudalabs.com`:
- `/` → console
- `/forge-api/*` → control (Caddy le quita el prefijo `/forge-api`)
- `/forge-api/ws` → WebSocket del live-log (Caddy hace el upgrade solo)
- `/forge-api/pv/<token>/` → **preview** de un `release` (el control mina la URL sobre
  `VIBEFORGE_PUBLIC_URL`, que en prod es `.../forge-api`, así que **no hace falta ninguna ruta
  extra** en Caddy — viaja por el mismo `/forge-api/*`).

---

## 1. Requisitos

| | |
|---|---|
| **VPS** | 4 vCPU / 8 GB RAM / 40+ GB disco. Ubuntu 22.04/24.04. |
| **Docker** | Docker Engine + el plugin `docker compose` v2. El control usa el **socket del host** (DooD) para lanzar el sandbox del `release`, así que el socket tiene que estar disponible. |
| **Dominio** | Un dominio/subdominio que controles, ej. `fluxo.aiudalabs.com` |
| **DNS** | Un registro **A**: `fluxo.aiudalabs.com → <IP pública del VPS>`, **antes** de arrancar (Caddy lo necesita para emitir el TLS) |
| **GitHub** | El `gh` CLI autenticado en el VPS (`gh auth login`) para el fallback del conductor, y la cuenta/org donde se crearán los repos |

Instalar Docker en Ubuntu:
```bash
curl -fsSL https://get.docker.com | sh
sudo usermod -aG docker $USER   # relogueá después de esto
```

---

## 2. El archivo `.env` — CADA variable explicada

Cloná el repo y copiá la plantilla:
```bash
git clone https://github.com/aiudalabs/aiuda-forge.git
cd aiuda-forge
cp .env.example .env
nano .env
```

### Tabla completa de variables

Leyenda **Lee**: *control* = el contenedor `control` (API + agentes de diseño + conductor);
*console* = build de Next.js; *caddy* = reverse proxy (solo prod). **Prod?** = obligatoria en producción.

| Variable | Lee | Default | Prod? | Qué es |
|---|---|---|---|---|
| `FLUXO_DOMAIN` | caddy, prod override | — | **Sí** | Dominio público. Ej: `fluxo.aiudalabs.com`. |
| `VIBEFORGE_API_TOKEN` | control | — | **Sí** | Token de servicio interno. `openssl rand -hex 32`. Sin esto la auth no se activa. |
| `VIBEFORGE_ADMIN_EMAIL` | control | — | **Sí** | Email del primer usuario (se crea al primer boot). |
| `VIBEFORGE_ADMIN_PASSWORD` | control | — | **Sí** | Contraseña de ese primer usuario. |
| `VIBEFORGE_GH_ORG` | control | — | **Sí** | Owner por defecto para crear repos (`nmlemus` u org `aiudalabs`). |
| `GH_TOKEN` | control | vacío | Recom. | PAT de GitHub (host), fallback del conductor. Scope `repo`. |
| `FORGE_WORKDIR` | control (DooD) | `/tmp/vibeforge-runs` | **Sí** | Carpeta de runs, montada en la **misma ruta** host↔contenedor. En prod usá algo persistente: `/opt/forge/runs`. |
| `PR_MODE` | control | `github` | **Sí** | `github` = abre+mergea PRs reales (el `docs_pr` del diseño DEBE llegar a `main`). `local` = solo tests offline. |
| **AI — agentes de diseño** | | | | |
| `VIBEFORGE_AGENT_AUTH` | control | `oauth_token` | **Sí** | Modo: `oauth_token` (suscripción) **o** `api_key` (pago por uso). |
| `CLAUDE_CODE_OAUTH_TOKEN` | control | vacío | si modo=oauth | Token de tu suscripción Claude (`claude setup-token`). |
| `ANTHROPIC_API_KEY` | control | vacío | si modo=api_key **o** Brain | API key `sk-ant-…`. Cubre diseño en modo `api_key` **y** activa el Brain. |
| `BRAIN_MODEL` | control | `claude-opus-4-8` | No | Modelo del Brain. |
| **Engine / sandbox** | | | | |
| `VIBEFORGE_ENGINE` | control | `claude` | No | Backend de agente. |
| `VIBEFORGE_SANDBOX` | control | `docker` | **Sí** | Runtime del sandbox. `docker` = aísla el build del `release` (no corre en el host). |
| `VIBEFORGE_AGENT_IMAGE` | control | `vibeforge-agent:local` | **Sí** | Imagen del sandbox donde corre el build del `release`. **Hay que construirla** (§10). |
| **Previews (`release` #23)** | | | | |
| `VIBEFORGE_PREVIEW_SECRET` | control | *(random)* | **Sí** | Clave HMAC de los tokens de preview. Si está vacía se genera una random por proceso → **todos los links de preview mueren en cada restart**. `openssl rand -hex 32`. |
| `VIBEFORGE_PREVIEWS_DIR` | control | `/previews` (compose) | — | Dónde viven los sitios de preview. El compose ya lo apunta al volumen `forge-previews`. |
| `VIBEFORGE_RELEASE_BUILD_TIMEOUT_MIN` | control | `10` | No | Timeout de un build de release. |
| `VIBEFORGE_RELEASE_MAX_MB` | control | `200` | No | Tope del artefacto publicado. |
| `VIBEFORGE_RELEASE_KEEP` | control | `5` | No | Previews retenidos por proyecto (GC de los viejos). |
| **Ceremonias / digest** | | | | |
| `VIBEFORGE_DIGEST_CRON` | control | `0 13 * * *` | No | Cron del digest diario. **Se evalúa en UTC** (la TZ del contenedor NO importa). `0 13 * * *` = 13:00 UTC = 08:00 Panamá. El token de Telegram y el canal por proyecto se configuran **en la app**, no acá. |
| `VIBEFORGE_CONDUCTOR_GROOM` | control | `1` | No | JIT grooming pre-dispatch (#18). `0` lo apaga. |
| `VIBEFORGE_AGENT_TIMEOUT_MIN` | control | `20` | No | Wall-clock del agente de diseño. Subilo para diseños grandes. |
| `VIBEFORGE_AGENT_IDLE_TIMEOUT_MIN` | control | `8` | No | Watchdog de inactividad del agente (#26). |
| **Operabilidad** | | | | |
| `VIBEFORGE_RETENTION_DAYS` | control | `30` | No | Borra eventos + workdirs de runs terminados > N días. `0` apaga. (No toca los previews.) |
| `VIBEFORGE_GH_TIMEOUT_SEC` | control | `90` | No | Corta llamadas `gh`/`git` colgadas. |
| `VIBEFORGE_GITHUB_WEBHOOK_SECRET` | control | vacío | si usás webhooks | Secret del webhook de la GitHub App (§6.4). *(Antes estaba en `.env` pero no llegaba al contenedor; ahora sí.)* |

**No pongas en `.env`** (los setea el override de prod a partir de `FLUXO_DOMAIN`):
`VIBEFORGE_PUBLIC_URL`, `VIBEFORGE_CONSOLE_URL`, `VIBEFORGE_CORS_ORIGIN`, `NEXT_PUBLIC_VIBEFORGE_API_URL`.

### Las tres credenciales, en una línea
- `CLAUDE_CODE_OAUTH_TOKEN` = suscripción Claude → agentes de diseño en modo `oauth_token`.
- `ANTHROPIC_API_KEY` = API de pago → diseño en modo `api_key` **y/o** el Brain.
- `GH_TOKEN` = GitHub (nada que ver con Claude).

> Config típica multi-usuario: `VIBEFORGE_AGENT_AUTH=api_key` + `ANTHROPIC_API_KEY=sk-ant-…`
> (una sola key cubre diseño Y Brain), `CLAUDE_CODE_OAUTH_TOKEN` vacío.

---

## 3. DNS + firewall

1. **DNS**: creá el registro **A** `fluxo.aiudalabs.com → <IP del VPS>` (`dig +short fluxo.aiudalabs.com`
   debe devolver la IP).
2. **Firewall** (solo 80/443/22 públicos; 8080/3000 quedan internos):
   ```bash
   sudo ufw allow 22 && sudo ufw allow 80 && sudo ufw allow 443 && sudo ufw enable
   ```

---

## 4. Construir la imagen del sandbox (una vez) y levantar todo

El paso `release` (previews) corre `npm ci`/`npm run build`/`firebase deploy` **dentro** de la imagen
del sandbox `vibeforge-agent:local`. Esa imagen **no** la construye el compose (el control la lanza por
DooD contra el daemon del host), así que hay que construirla una vez **antes** del primer `up`:

```bash
./engine/scripts/build-sandbox-images.sh    # crea vibeforge-agent:local (+ forge-gate:local legacy)
```

Después, con el `.env` lleno y el DNS apuntando:

```bash
docker compose -f docker-compose.yml -f deploy/docker-compose.prod.yml up -d --build
```

Levanta `caddy`, `control`, `console`, `egress-proxy`. Ver estado:

```bash
docker compose ps
docker compose logs -f caddy      # debe obtener el certificado TLS
docker compose logs -f control    # "vibeforge control listening on :8080 ... 1 user(s)"
```

---

## 5. Primer arranque (en la app)

1. Abrí **`https://fluxo.aiudalabs.com`** → login.
2. Logueate con `VIBEFORGE_ADMIN_EMAIL` / `VIBEFORGE_ADMIN_PASSWORD`.
3. **Conectar la GitHub App**: Ajustes → conexión de GitHub, o `…/forge-api/setup/github-app`.
   Crea la GitHub App (manifest flow) y guarda sus credenciales en `/data/github-app.json`
   (volumen `forge-data`) — no va en el `.env`.
4. **Instalá la App** en tu cuenta/org para que Fluxo cree repos y despache trabajo.
5. (Opcional) **Secret de Claude para los repos** (§6.1).
6. Creá tu primer proyecto desde `/`.

### 5.1 Cómo entra un usuario nuevo (Pedro)
El admin solo hace falta **una vez** para conectar la App. De ahí, cualquiera con el link:
**Continuar con GitHub** → autoriza → cae en crear proyecto con **sus** orgs → el repo se crea en
**su** cuenta con **su** token. Los agentes de diseño usan el Claude del operador (compartido).

---

## 6. Requisitos POR PROYECTO que el deploy no puede darte (GitHub-native)

El deploy monta la instancia; algunas cosas viven **en el repo del proyecto** o en los ajustes del
proyecto y las configura cada usuario:

### 6.1 Secret de Claude en el repo del proyecto — **`CLAUDE_CODE_OAUTH_TOKEN`**
Los workflows que Fluxo escaffoldea en cada repo (`claude.yml` = escribir código, `claude-review.yml`
= review, `ui-verify.yml` = verificación visual con el **art-director**) usan
`${{ secrets.CLAUDE_CODE_OAUTH_TOKEN }}`. Sembralo desde **Ajustes → Canal Claude** (Fluxo lo pone
como secret del repo; **no** lo almacena). Sin ese secret, Copilot/Claude Actions no arrancan.

> **Nota:** el secret del repo es `CLAUDE_CODE_OAUTH_TOKEN`, **no** `ANTHROPIC_API_KEY`. La API key
> `ANTHROPIC_API_KEY` es para los agentes de diseño + el Brain **en el servidor** (§2), no en el repo.

### 6.2 GitHub App instalada
En la cuenta/org donde se crean los repos (paso 4 de §5). Sin instalación, no hay tokens de
instalación para crear repos ni despachar Copilot.

### 6.3 `firebase_token` — solo si `release_target=firebase`
El paso `release` puede publicar una preview en **Firebase Hosting** en vez de servirla como sitio
estático. Para eso, en los **ajustes del proyecto** hay que cargar el `firebase_token` del proyecto
(`firebase login:ci`). El control lo inyecta como `FIREBASE_TOKEN` dentro del sandbox (nunca lo loguea).
El sandbox sale a `*.googleapis.com` por el egress-proxy (ya está en la allowlist). Si el proyecto
usa `release_target=static` (default), no hace falta nada de esto.

### 6.4 Webhooks (opcional, recomendado)
Sin webhooks Fluxo poll-ea cada 25s. Para activarlos: en la config de la GitHub App poné el webhook URL
en `https://fluxo.aiudalabs.com/forge-api/` y el secret en `VIBEFORGE_GITHUB_WEBHOOK_SECRET` del `.env`.

---

## 7. El registry es ESCRIBIBLE (la retro edita el método) — leer esto

Desde el PR #27, la ceremonia de **retro** puede mejorar el propio método: su paso `registry_apply`
**escribe** archivos bajo `engine/registry/{agents,skills,workflows}/` (validado — allowlist de ids, sin
escape de path, sin auto-editar el método de la retro — y auditado con un evento por escritura), pero
solo después de que un humano **aprueba** la retro del sprint.

Por eso el `docker-compose.yml` monta el registry **read-write** (antes decía, en falso, "el engine solo
LEE el registry"). Si lo dejaras `:ro`, la retro falla con un error claro
(`registry_apply: write <kind> "<id>": … read-only file system`) — **falla limpia, sin panic**, y no
estampa la retro (nada queda a medias).

> **⚠️ Loop de un solo VPS:** el registry se bind-montea desde el checkout git (`./engine/registry`), así
> que una retro aprobada **deja cambios SIN COMMITEAR** en el working tree de `/opt/aiuda-forge`. Eso es
> **deseable** (el rastro git de cómo evolucionó el método es valioso), no un accidente. Operativamente:
> ```bash
> cd /opt/aiuda-forge && git status            # verás cambios bajo engine/registry/
> git add engine/registry && git commit -m "retro(SP<n>): método editado"
> ```
> **Antes de un `git pull` de upgrade** (§9), commiteá o descartá esos cambios, o el pull choca.

---

## 8. Verificar que quedó bien

```bash
curl -s https://fluxo.aiudalabs.com/forge-api/healthz          # -> 200
curl -s https://fluxo.aiudalabs.com/ -o /dev/null -w '%{http_code}\n'   # -> 200
```
En el browser: login OK y el live-log de un run se actualiza en tiempo real (confirma el WebSocket por Caddy).

---

## 9. Upgrade a una versión nueva — **backup → pull → build → up**

Las migraciones de todas las DBs corren **automáticas e idempotentes al boot** (dentro del `Open()` de
cada store: `ALTER TABLE ADD COLUMN` guardados que ignoran "duplicate column"). No hay paso manual de
migración. Lo nuevo de #15→#29 ya está cubierto: columna `kind` (#17), `screen_key` (#20), columnas de
ceremonia `planned_at`/`reviewed_at`/`retro_at` + `*_run_id` (#24/#25/#27), `release_target`/`firebase_token`
(#23), `digest_channel`/`last_digest_at` (#19), `planning_mode`/`review_mode`/`retro_mode`. Aun así,
**siempre hacé backup antes** por las dudas:

```bash
cd /opt/aiuda-forge
# 1) BACKUP consistente de los 6 SQLite (sin apagar nada; usa sqlite3 .backup):
./engine/scripts/backup-db.sh /opt/forge-backups aiuda-forge_forge-data "$(date +%Y%m%d-%H%M)"
# 2) (si hubo retros) commiteá los cambios del registry — ver §7:
git status && git add engine/registry && git commit -m "retro: método editado" || true
# 3) PULL:
git pull
# 4) reconstruí la imagen del sandbox SOLO si cambió su Dockerfile:
./engine/scripts/build-sandbox-images.sh
# 5) UP (reconstruye control/console/caddy y aplica migraciones al boot):
docker compose -f docker-compose.yml -f deploy/docker-compose.prod.yml up -d --build
```
Los datos persisten en `forge-data`; los previews en `forge-previews`.

Backup por cron (además del pre-upgrade):
```bash
crontab -e
0 * * * * cd /opt/aiuda-forge && ./engine/scripts/backup-db.sh /opt/forge-backups aiuda-forge_forge-data "$(date +\%Y\%m\%d-\%H\%M)"
```

---

## 10. La imagen del sandbox (`vibeforge-agent:local`) — detalle

`engine/deploy/agent/Dockerfile` = **node LTS + npm + git + python3 + firebase-tools**. Es donde corre,
aislado y sin acceso directo a internet, el build del paso `release`:
- **static**: `npm ci && npm run build` (salida `dist/` o `build/`), o copia tal cual si ya hay `index.html`.
- **firebase**: `firebase hosting:channel:deploy … --json --non-interactive`.

La red del sandbox es `vibeforge-egress` (interna, sin gateway); sale **solo** por el `egress-proxy`,
cuya allowlist (`engine/deploy/egress-proxy/filter`) permite:
`api.anthropic.com`, `registry.npmjs.org`, `pypi.org`, `files.pythonhosted.org`, y **`*.googleapis.com`**
(este último para el deploy a Firebase: hosting upload + lookup de proyecto + refresh de token OAuth;
solo lo usa el target firebase). Construila con `./engine/scripts/build-sandbox-images.sh`.

---

## 11. SMOKE CHECKLIST — 15 minutos post-deploy (probar TODO lo nuevo)

Corré esto con un **proyecto de juguete** (ej. "RutaViva-smoke") apenas termina el deploy. Cada paso
prueba una feature de #15→#29; si todos pasan, el deploy soporta el código.

- [ ] **0. Salud** — `curl …/forge-api/healthz` → 200; login en la UI OK.
- [ ] **1. Proyecto en ceremonia total** — creá el proyecto y poné, en Ajustes del proyecto,
      `planning_mode`, `review_mode`, `retro_mode` = **ceremony** (así se ejercitan las 3 ceremonias).
- [ ] **2. Design run corto** — lanzá el diseño; mirá el **live-log en tiempo real** (WebSocket por Caddy).
- [ ] **3. Responder un gate** — en una fase con open-questions, usá el verbo **`answer`** (#22):
      respondé sin rechazar y confirmá que la fase incorpora la respuesta.
- [ ] **4. `/flow` en vivo** — abrí la vista **Flow** (#28) y confirmá que el grafo del proyecto se
      **actualiza solo** conforme avanzan fases/gates/sprints. *(Verificación pendiente de #28 — reportar
      si el refresh en vivo no se ve.)*
- [ ] **5. Un sprint de una story** — dejá que el conductor despache una story y mergee su PR
      (Copilot/Claude Action con el secret `CLAUDE_CODE_OAUTH_TOKEN` del repo, §6.1).
- [ ] **6. `release` static + preview por token** — corré un `release` (target static): debe publicar el
      sitio y darte una URL `…/forge-api/pv/<token>/`. Abrila (el token dura ~10 min) y confirmá que
      **carga desde el volumen** `forge-previews`. Reiniciá el control (`docker compose restart control`)
      y volvé a mintar un token: la preview **sigue ahí** (persistencia + `VIBEFORGE_PREVIEW_SECRET` fijo).
- [ ] **7. Digest manual por el Brain** — con `ANTHROPIC_API_KEY` puesto, pedile al **Brain** el
      `daily_digest` (herramienta read-only): debe devolver el resumen de 4 secciones (📈/🚧/⏭️/💰).
- [ ] **8. Retro que EDITA el registry (la prueba del bug #1 arreglado)** — completá review→retro de un
      sprint con una **propuesta de juguete** (ej. tocar una skill), **aprobala**, y confirmá:
      `docker compose exec control ls -la /app/registry/skills` muestra el archivo modificado, y en el
      host `git -C /opt/aiuda-forge status` muestra el cambio sin commitear (§7). **Si esto escribe en vez
      de fallar con "read-only file system", el registry quedó bien montado RW.**

---

## 12. Qué NO cubre este deploy (límites conocidos, honestos)

- **Una sola instancia del control** (escalar vertical, no horizontal). No corras 2 réplicas: comparten
  estado en memoria + SQLite de un solo escritor. Para 30–50+ hace falta Postgres + separar el conductor.
- **Los agentes de diseño corren dentro del contenedor `control`** (cap 3 vCPU / 4 GB). Un pico de muchos
  diseños simultáneos compite por esa CPU. El único cómputo que sí se aísla en sandbox es el build del `release`.
- El costo de Claude (diseño + Brain) es tuyo — dimensioná según `oauth_token` vs `api_key`.
