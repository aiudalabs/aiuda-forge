# Despliegue de Fluxo

Guía oficial para instalar Fluxo en un servidor propio, de cero a funcionando. Tres etapas:
**0. Pre-despliegue** (qué necesitás) · **1. Despliegue** (instalar) · **2. Post-despliegue**
(dejarlo usable).

> Si solo querés probar en tu máquina, es lo mismo pero con `FLUXO_DOMAIN=localhost` y sin
> Caddy/TLS. Esta guía asume **producción en un servidor con dominio**.

---

## 0. Pre-despliegue — qué necesitás antes de empezar

Reuní estas 5 cosas. Sin ellas no arranca.

| # | Necesitás | De dónde sale / detalle |
|---|---|---|
| **A** | **Un servidor (VPS)** | Linux (Ubuntu 24.04 recomendado). **Mínimo 4 vCPU / 8 GB RAM / 40 GB disco.** Por qué 4 vCPU: los agentes de diseño (`claude -p`) corren *dentro* del contenedor `control` y compiten con la API. Para 1–10 usuarios alcanza. |
| **B** | **Un dominio** + acceso a su DNS | Ej. `fluxo.tudominio.com`. Necesitás poder crear un registro **A** apuntándolo al IP del servidor. Caddy emite el certificado TLS solo. |
| **C** | **Una cuenta de GitHub** (org o usuario) | Fluxo crea los repos de los proyectos ahí y opera vía una **GitHub App** que instalás en el post-despliegue. Ej. `aiudalabs` o tu usuario. |
| **D** | **Una credencial de Claude** | Los agentes de diseño la usan. Dos opciones (elegí una):<br>• **Suscripción** (Pro/Max): corré `claude setup-token` en tu máquina → te da un `CLAUDE_CODE_OAUTH_TOKEN`.<br>• **Pago por uso**: una `ANTHROPIC_API_KEY` de [console.anthropic.com](https://console.anthropic.com). *(El asistente "Brain" dentro de la app **solo** funciona con `ANTHROPIC_API_KEY`.)* |
| **E** | **Docker + git en el servidor** | Se instalan en el Paso 1.1. Docker Engine + el plugin `compose` + `git`. |

**Opcional (para cerrar el ciclo hasta el deploy de los proyectos):** una cuenta cloud
(Firebase/GCP o Supabase) para que los proyectos generados se desplieguen. No es necesaria
para levantar Fluxo; se conecta después, por proyecto.

---

## 1. Despliegue

### 1.1 Qué se instala (la topología)

Fluxo corre como **un stack de contenedores Docker** en un solo servidor, detrás de un
reverse-proxy con TLS. En producción son **4 servicios activos**:

| Servicio | Qué es | Puerto |
|---|---|---|
| **caddy** | Reverse-proxy + TLS automático. Un solo dominio: enruta `/forge-api/*` → control y todo lo demás → console. | 80, 443 (públicos) |
| **control** | El cerebro: API HTTP + el **Conductor** (orquesta los sprints en GitHub) + los **agentes de diseño** (`claude -p`) corren acá dentro. | 8080 (interno) |
| **console** | La UI web (Next.js). | 3000 (interno) |
| **egress-proxy** | Proxy de salida con allowlist (los sandboxes de agentes salen a internet solo por acá). | interno |

*(Hay un 5º servicio, `orchestrator`, que es el **factory legacy** pre-pivote. Está
**apagado por default** detrás del profile `legacy` — no lo levantes salvo pruebas del
factory v1.)*

Los datos persisten en un **volumen Docker** (`aiuda-forge_forge-data`) con las bases SQLite
(`vibeforge.db`, `tickets.db`, `projects.db`, `auth.db`, `github-app.json`).

### 1.2 Instalar Docker + git (en el servidor)

```bash
apt-get update -y && curl -fsSL https://get.docker.com | sh && apt-get install -y git
docker --version && docker compose version    # verificar
```

### 1.3 Clonar el repo

```bash
git clone https://github.com/aiudalabs/aiuda-forge.git /opt/aiuda-forge
cd /opt/aiuda-forge
```

*(Repo privado: usá un token o una deploy key. Ver §Mantenimiento para configurar una
deploy key SSH read-only y no tener que pasar el token en cada `git pull`.)*

### 1.4 Crear el `.env` — cada variable y de dónde sale

Copiá la plantilla y editá:

```bash
cp .env.example .env
nano .env
```

**Este es el detalle de cada variable — de dónde sacar el valor:**

| Variable | De dónde sale | Ejemplo / cómo |
|---|---|---|
| `FLUXO_DOMAIN` | **Tu dominio** (pre-req B) | `fluxo.tudominio.com` |
| `VIBEFORGE_ADMIN_EMAIL` | **Vos lo elegís** — el primer usuario admin | `admin@tudominio.com` |
| `VIBEFORGE_ADMIN_PASSWORD` | **Vos la elegís** (≥8 chars) | una contraseña fuerte |
| `VIBEFORGE_API_TOKEN` | **Lo generás** | `openssl rand -hex 32` → pegá el resultado |
| `VIBEFORGE_AGENT_AUTH` | **Elegís el modo** (pre-req D) | `oauth_token` (suscripción) o `api_key` (pago) |
| `CLAUDE_CODE_OAUTH_TOKEN` | Si `oauth_token`: **`claude setup-token`** en tu máquina | `sk-ant-oat...` |
| `ANTHROPIC_API_KEY` | Si `api_key`: **console.anthropic.com** | `sk-ant-...` (también habilita el Brain) |
| `VIBEFORGE_GH_ORG` | **Tu org/usuario de GitHub** (pre-req C) | `aiudalabs` |
| `GH_TOKEN` | **Personal Access Token** de GitHub (scope `repo`) | github.com → Settings → Developer settings |
| `PR_MODE` | **Dejar `github`** en producción (no tocar) | `github` |
| `FORGE_WORKDIR` | **Ruta de trabajo** de los agentes (persistente) | `/opt/forge/runs` (creá la carpeta: `mkdir -p /opt/forge/runs`) |
| `VIBEFORGE_RETENTION_DAYS` | Días antes de limpiar runs/events viejos (0 = nunca) | `30` |
| `VIBEFORGE_GH_TIMEOUT_SEC` | Timeout de las llamadas a `gh`/`git` | `90` |
| `BRAIN_MODEL` | El LLM del asistente Brain | `claude-opus-4-8` (default) |

Las URLs públicas (`VIBEFORGE_PUBLIC_URL`, `VIBEFORGE_CONSOLE_URL`, `VIBEFORGE_CORS_ORIGIN`,
`NEXT_PUBLIC_VIBEFORGE_API_URL`) **las setea solo** el override de producción a partir de
`FLUXO_DOMAIN` — no las toques a mano.

> ⚠️ **`NEXT_PUBLIC_VIBEFORGE_API_URL` se hornea en el build del console.** El override de
> prod ya lo pasa como build-arg desde `FLUXO_DOMAIN`. Por eso, si cambiás el dominio,
> **hay que rebuildear el console** (no alcanza reiniciar).

### 1.5 DNS — apuntar el dominio al servidor

En tu proveedor de DNS (GoDaddy, Cloudflare, Hostinger, etc.), creá **un registro A**:

```
Tipo: A · Nombre: fluxo (el subdominio) · Valor: <IP-del-servidor> · TTL: 600
```

Verificá que resuelve antes de levantar (Caddy necesita el DNS para emitir el TLS):

```bash
dig +short fluxo.tudominio.com    # debe devolver el IP del servidor
```

### 1.6 Abrir el firewall + levantar

```bash
ufw allow 22 && ufw allow 80 && ufw allow 443     # SSH + HTTP + HTTPS
cd /opt/aiuda-forge
docker compose -f docker-compose.yml -f deploy/docker-compose.prod.yml up -d --build
```

**El primer build tarda ~10–15 min** (compila Go + Next.js). Cuando termine, Caddy emite el
certificado TLS automáticamente al primer request. Verificá:

```bash
docker compose -f docker-compose.yml -f deploy/docker-compose.prod.yml ps   # todos "Up"
curl -s -o /dev/null -w "%{http_code}\n" https://fluxo.tudominio.com/        # 200
```

Si da 200 → **Fluxo está desplegado.** 🎉

---

## 2. Post-despliegue — dejarlo usable

Levantar el stack no alcanza: falta conectar GitHub (una vez) para que la fábrica pueda crear
repos y despachar sprints.

### 2.1 Primer login (admin)

Entrá a `https://fluxo.tudominio.com` → login con el `VIBEFORGE_ADMIN_EMAIL` /
`VIBEFORGE_ADMIN_PASSWORD` del `.env`.

### 2.2 Crear la GitHub App (una sola vez por instancia)

Fluxo opera sobre GitHub vía una **GitHub App**. En un deploy nuevo todavía no existe. En
**Settings → GitHub** vas a ver `⚠ App sin configurar` y un botón **"Configurar la App de
Fluxo"**:

1. Clic → te lleva al **manifest flow** de GitHub (formulario pre-llenado).
2. Elegí crearla bajo **tu org** (ej. `aiudalabs`) → **Create GitHub App**.
3. GitHub te redirige de vuelta a Fluxo y **guarda las credenciales solo** (en
   `github-app.json`, dentro del volumen).

### 2.3 Instalar la App en tu org

En Settings → GitHub, ahora aparece **"Instalar en una cuenta/org"** → clic → elegí tu org →
instalá. *(Instalar en una org siempre pasa por la pantalla de consentimiento de GitHub — es
su requisito, no se puede saltar.)*

### 2.4 Conectar tu GitHub (OAuth)

En Settings → GitHub → **"Conectar GitHub"** → autorizás. Esto vincula tu identidad de GitHub
a tu usuario de Fluxo (multi-tenant: cada usuario opera con sus propias credenciales).

### 2.5 Sembrar el secret de Claude para los proyectos (canal claude_action)

Para que el **código** se escriba en GitHub Actions con Claude, en **Settings → Canal Claude**
sembrás el secret en los repos (Fluxo no lo almacena; lo pone como secret del repo). *(Si vas
a usar Copilot como canal en vez de claude_action, este paso no aplica.)*

### 2.6 Verificación final

- Settings → GitHub muestra `✓ App configurada` y `🟢 Conectado`.
- Podés crear un proyecto de prueba (ver [Uso — un proyecto de punta a punta](02-uso-proyecto-e2e.md)).

---

## Resumen en 10 líneas

```bash
# 0. Pre: servidor Ubuntu + dominio + cuenta GitHub + credencial Claude
# 1. Deploy:
apt-get update -y && curl -fsSL https://get.docker.com | sh && apt-get install -y git
git clone https://github.com/aiudalabs/aiuda-forge.git /opt/aiuda-forge && cd /opt/aiuda-forge
cp .env.example .env && nano .env         # completar (ver tabla §1.4); openssl rand -hex 32 para el token
mkdir -p /opt/forge/runs                  # FORGE_WORKDIR
# DNS: registro A fluxo.tudominio.com -> IP del servidor
ufw allow 22 && ufw allow 80 && ufw allow 443
docker compose -f docker-compose.yml -f deploy/docker-compose.prod.yml up -d --build
# 2. Post: login admin -> Settings -> configurar+instalar GitHub App -> conectar GitHub
```

**Siguiente:** [Uso — un proyecto de punta a punta →](02-uso-proyecto-e2e.md)
