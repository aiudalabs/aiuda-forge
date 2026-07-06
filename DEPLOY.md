# Desplegar Forja en un VPS (para ~10 usuarios)

Guía paso a paso, sin ambigüedades. Todo corre en **un solo VPS** con docker-compose detrás
de **Caddy** (HTTPS automático) en **un solo dominio**. El cómputo pesado (escribir código) lo
hace GitHub (Copilot / GitHub Actions), no este servidor — por eso un VPS mediano alcanza.

---

## 0. Topología (qué corre y dónde)

```
                    https://forja.aiudalabs.com
                              │
                          ┌───┴────┐   (Caddy: TLS automático + proxy)
                          │ caddy  │   :80  :443
                          └───┬────┘
              /forge-api/* ───┤────── todo lo demás
                              │
                   ┌──────────┴──────────┐
             ┌─────┴─────┐         ┌──────┴──────┐
             │  control  │  :8080  │   console   │ :3000
             │ (API+WS,  │         │  (Next.js)  │
             │  agentes  │         └─────────────┘
             │  diseño)  │
             └─────┬─────┘
                   │ SQLite (volumen forge-data) + workdirs
                   └──────── egress-proxy (allowlist de salida)
```

El navegador habla **solo** con `https://forja.aiudalabs.com`:
- `/` → console
- `/forge-api/*` → control (Caddy le quita el prefijo `/forge-api`)
- `/forge-api/ws` → WebSocket del control (Caddy hace el upgrade solo)

---

## 1. Requisitos

| | |
|---|---|
| **VPS** | 4 vCPU / 8 GB RAM / 40+ GB disco. Ubuntu 22.04/24.04. (Hostinger, DigitalOcean, etc.) |
| **Docker** | Docker Engine + el plugin `docker compose` v2 |
| **Dominio** | Un dominio/subdominio que controles, ej. `forja.aiudalabs.com` |
| **DNS** | Un registro **A**: `forja.aiudalabs.com → <IP pública del VPS>`, **antes** de arrancar (Caddy lo necesita para emitir el certificado TLS) |
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
git checkout feat/design-system      # (o main una vez mergeado)
cp .env.example .env
nano .env
```

Llena `.env` con esto. **Requerida** = sin ella no arranca bien.

### Básicas

| Variable | ¿Requerida? | Qué es / cómo obtenerla |
|---|---|---|
| `FORJA_DOMAIN` | **Sí** | El dominio público. Ej: `forja.aiudalabs.com`. (La usa Caddy + el override de prod.) |
| `VIBEFORGE_API_TOKEN` | **Sí** | Token de servicio interno. Generalo: `openssl rand -hex 32`. Sin esto la auth no se activa. |
| `VIBEFORGE_ADMIN_EMAIL` | **Sí** | Email del **primer usuario** (se crea en el primer boot). Ej: `noel@aiudalabs.com`. |
| `VIBEFORGE_ADMIN_PASSWORD` | **Sí** | Contraseña de ese primer usuario. Poné una fuerte; podés cambiarla después en la app. |
| `VIBEFORGE_GH_ORG` | **Sí** | Owner por defecto donde se crean los repos: tu usuario (`nmlemus`) o una org (`aiudalabs`). En la app cada proyecto puede elegir otro con el selector. |
| `GH_TOKEN` | Recomendada | Personal Access Token de GitHub (host). Lo usa el conductor como fallback si el token del tenant no alcanza. Crealo en github.com → Settings → Developer settings → Tokens (classic), scope `repo`. |
| `FORGE_WORKDIR` | **Sí** | Carpeta de trabajo de los runs. En Linux usá una ruta persistente, ej: `/opt/forge/runs`. (Se monta con la MISMA ruta en host y contenedor — no la cambies a algo raro.) |

### Las DOS credenciales de Claude — la parte importante (cero ambigüedad)

Forja usa Claude en **dos lugares distintos**, y son **credenciales diferentes**:

**A) Los agentes de DISEÑO** (los que generan brief/PRD/arquitectura/mockups/backlog, corriendo
dentro del contenedor `control`). Usan **UNA** de estas dos, según el modo:

| Variable | Cuándo se usa | Qué es / cómo obtenerla |
|---|---|---|
| `VIBEFORGE_AGENT_AUTH` | **Sí** | El **modo**. Dos valores posibles: `oauth_token` **o** `api_key`. Elegí uno (ver abajo). |
| `CLAUDE_CODE_OAUTH_TOKEN` | Solo si modo = `oauth_token` | Token de tu **suscripción Claude** (Pro/Max). Se genera con el CLI de Claude Code: `claude setup-token` (te imprime un token largo). **No** es una API key, **no** se paga por token — consume los límites de tu suscripción. |
| `ANTHROPIC_API_KEY` | Solo si modo = `api_key` | **API key de pago por uso** de Anthropic. Se saca en `console.anthropic.com` → API Keys (empieza con `sk-ant-...`). Se cobra por token consumido. |

> **¿Cuál elegir para 10 usuarios?**
> - `oauth_token` (una suscripción Pro/Max): **más barato**, pero **todos** los diseños de los 10
>   usuarios consumen los límites de **esa única suscripción** — si se agota, todos esperan al reset.
> - `api_key` (pago por uso): **escala mejor** para uso compartido (sin límite de suscripción
>   compartido), pero **pagás por token**. Recomendado para un deploy multi-usuario serio.

**B) El "Brain"** (el asistente/orquestador dentro de la app). **Siempre** usa `ANTHROPIC_API_KEY`
(usa la Messages API) y solo se activa si esa key está puesta.

| Variable | ¿Requerida? | Qué es |
|---|---|---|
| `ANTHROPIC_API_KEY` | Requerida **para el Brain** | La misma `sk-ant-...` de arriba. Si la dejás vacía, el Brain queda apagado (el resto funciona igual). |
| `BRAIN_MODEL` | No (default OK) | Modelo del Brain. Default `claude-opus-4-8` (mejor razonamiento). Dejalo así salvo que quieras cambiarlo. |

> **Resumen práctico de los tres tokens:**
> - `CLAUDE_CODE_OAUTH_TOKEN` = suscripción Claude → agentes de diseño en modo `oauth_token`.
> - `ANTHROPIC_API_KEY` = API de pago → agentes de diseño en modo `api_key` **y/o** el Brain.
> - `GH_TOKEN` = GitHub, nada que ver con Claude.
>
> Config típica multi-usuario: `VIBEFORGE_AGENT_AUTH=api_key` + `ANTHROPIC_API_KEY=sk-ant-...`
> (una sola key cubre los agentes de diseño Y el Brain), y dejás `CLAUDE_CODE_OAUTH_TOKEN` vacío.

### Operabilidad (ya traen buenos defaults — no hace falta tocarlas)

| Variable | Default | Qué hace |
|---|---|---|
| `VIBEFORGE_RETENTION_DAYS` | `30` | Borra eventos viejos + workdirs de runs terminados con más de N días (para que el disco no crezca infinito). `0` lo apaga. |
| `VIBEFORGE_GH_TIMEOUT_SEC` | `90` | Corta cualquier llamada `gh`/`git` colgada (para que un tenant no bloquee a los demás). |

**No pongas en `.env`**: `VIBEFORGE_PUBLIC_URL`, `VIBEFORGE_CONSOLE_URL`, `VIBEFORGE_CORS_ORIGIN`,
`NEXT_PUBLIC_VIBEFORGE_API_URL` — el override de producción (`deploy/docker-compose.prod.yml`) las
setea solo a partir de `FORJA_DOMAIN`.

---

## 3. DNS + firewall

1. **DNS**: creá el registro **A** `forja.aiudalabs.com → <IP del VPS>` y esperá a que propague
   (`dig +short forja.aiudalabs.com` debe devolver la IP del VPS).
2. **Firewall** (que solo 80/443/22 sean públicos; el 8080/3000 quedan internos):
   ```bash
   sudo ufw allow 22
   sudo ufw allow 80
   sudo ufw allow 443
   sudo ufw enable
   ```

---

## 4. Levantar todo

Desde la raíz del repo, con el `.env` lleno y el DNS ya apuntando:

```bash
docker compose -f docker-compose.yml -f deploy/docker-compose.prod.yml up -d --build
```

Esto construye y arranca: `caddy`, `control`, `console`, `orchestrator`, `egress-proxy`.
Caddy pide el certificado TLS a Let's Encrypt automáticamente (por eso el DNS tiene que estar
listo antes). Ver el estado:

```bash
docker compose ps
docker compose logs -f caddy      # deberías ver que obtiene el certificado
docker compose logs -f control    # "vibeforge control listening on :8080 ... 1 user(s)"
```

---

## 5. Primer arranque (en la app)

1. Abrí **`https://forja.aiudalabs.com`** → deberías ver la pantalla de login/entrada.
2. Logueate con `VIBEFORGE_ADMIN_EMAIL` / `VIBEFORGE_ADMIN_PASSWORD`.
3. **Conectar GitHub (la App de Forja)**: en la app, andá a Ajustes → conexión de GitHub, o abrí
   `https://forja.aiudalabs.com/forge-api/setup/github-app`. Eso te lleva al flujo de manifest de
   GitHub que **crea la GitHub App** y guarda sus credenciales en `/data/github-app.json`
   (dentro del volumen `forge-data`) — **no** es algo que pongas a mano en el `.env`.
4. **Instalá la App** en tu cuenta/org (`nmlemus` y/o `aiudalabs`) para que Forja pueda crear
   repos y despachar trabajo ahí.
5. (Opcional) **Secret de Claude para los repos**: en Ajustes → Canal Claude, sembrás el
   `CLAUDE_CODE_OAUTH_TOKEN` como secret del repo para que el workflow `claude.yml` escriba código
   en GitHub Actions. Forja **no** lo almacena, solo lo siembra.
6. Ya podés crear tu primer proyecto desde `/` (nombre + owner + descripción).

---

## 5.1 Cómo entra un usuario nuevo (Pedro) — sin setear nada por detrás

El admin (arriba) solo hace falta **una vez** para conectar la GitHub App de la instancia.
De ahí en más, **cualquiera al que le pases el link entra solo**:

1. Abre `https://forja.aiudalabs.com` → **Continuar con GitHub**.
2. GitHub le pide autorizar la App → vuelve logueado (la cuenta se crea sola, no necesita tu
   admin ni que le setees nada).
3. Cae en la pantalla de crear proyecto, que ya muestra **sus** organizaciones (nombre +
   orgs). Crea el proyecto → el repo se crea en **su** cuenta/org, con **su** token.
4. Diseña. Los agentes de diseño usan el Claude **del operador** (compartido) — Pedro no
   configura ninguna credencial de IA.

Si alguien entra con email/contraseña (o su GitHub no está conectado), la pantalla le muestra
una tarjeta **"Conectá tu GitHub"** para hacerlo en 10 segundos. No hay pasos ocultos.

---

## 6. Webhooks de GitHub (recomendado, no bloqueante)

Sin webhooks, Forja sincroniza el estado con un poll cada 25s (funciona, pero gasta más rate-limit
de GitHub). Para activarlos: en la config de la GitHub App (en github.com), poné el webhook URL en
`https://forja.aiudalabs.com/forge-api/` (el receptor ya existe) y activá los eventos de issues/PRs.
El secret del webhook va en la variable de entorno `VIBEFORGE_GITHUB_WEBHOOK_SECRET` del control
(agregala al `.env` si activás webhooks).

---

## 7. Backups (cron)

El script hace un backup consistente de las SQLite sin apagar nada:

```bash
crontab -e
# cada hora, guardando en /opt/forge-backups:
0 * * * * cd /opt/aiuda-forge && ./engine/scripts/backup-db.sh /opt/forge-backups aiuda-forge_forge-data "$(date +\%Y\%m\%d-\%H\%M)"
```
(El nombre del volumen `aiuda-forge_forge-data` sale de `docker volume ls`.)

---

## 8. Verificar que quedó bien

```bash
curl -s https://forja.aiudalabs.com/forge-api/healthz          # -> 200
curl -s https://forja.aiudalabs.com/ -o /dev/null -w '%{http_code}\n'   # -> 200
```
En el browser: login OK, el live-log de un run se actualiza en tiempo real (eso confirma que el
WebSocket pasa por Caddy).

---

## 9. Actualizar a una versión nueva

```bash
cd /opt/aiuda-forge
git pull
docker compose -f docker-compose.yml -f deploy/docker-compose.prod.yml up -d --build
```
Los datos persisten en el volumen `forge-data`.

---

## 10. Qué NO cubre este deploy (límites conocidos, honestos)

- **Una sola instancia del control** (escalar vertical, no horizontal). No corras 2 réplicas del
  `control` — comparten estado en memoria y en SQLite de un solo escritor. Alcanza de sobra para
  ~10 usuarios; para 30–50+ hace falta Postgres + separar el conductor (ver el review de
  escalabilidad). No lo necesitás ahora.
- **Los agentes de diseño corren dentro del contenedor `control`** (con un cap de 3 vCPU / 4 GB para
  no ahogar el host). Un pico de muchos diseños simultáneos compite por esa CPU. Aceptable a 10
  usuarios; el sandbox por-run real es un follow-up.
- El costo de Claude (diseño + Brain) es tuyo — dimensioná según elijas `oauth_token` vs `api_key`.
