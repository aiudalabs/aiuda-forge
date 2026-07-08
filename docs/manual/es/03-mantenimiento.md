# Mantenimiento y operación

Cómo mantener Fluxo corriendo en producción: actualizar, respaldar, mirar logs, y resolver los
problemas más comunes (varios aprendidos en carne propia).

> **Regla de oro operativa:** en producción **siempre** `docker compose` con **los dos archivos**
> (`-f docker-compose.yml -f deploy/docker-compose.prod.yml`) y **siempre con `-d`**. Omitir el
> override de prod o el `-d` es la causa #1 de los problemas de esta sección.

---

## 1. Actualizar a una versión nueva

```bash
cd /opt/aiuda-forge
git pull
docker compose -f docker-compose.yml -f deploy/docker-compose.prod.yml up -d --build
```

- **`-d`** → corre en background (no atado a tu terminal). Sin esto, si cerrás/titubea la
  terminal, los contenedores se caen.
- **`--build`** → recompila lo que cambió.
- **Los dos `-f`** → el override de prod es el que hornea la URL pública correcta en el console.

Para actualizar **solo un servicio** (ej. tras un cambio de front):

```bash
docker compose -f docker-compose.yml -f deploy/docker-compose.prod.yml up -d --build console
```

## 2. Backups (lo único irremplazable: los datos)

Todo el estado vive en el volumen Docker `aiuda-forge_forge-data`: las bases SQLite
(`vibeforge.db`, `tickets.db`, `projects.db`, `auth.db`, `billing.db`) + `github-app.json`.
**Respaldá ese volumen** con regularidad:

```bash
# Snapshot rápido del volumen a un tar con fecha
docker run --rm -v aiuda-forge_forge-data:/data -v /opt/backups:/backup alpine \
  tar czf /backup/forge-data-$(date +%F).tar.gz -C /data .
```

Programalo con `cron` (diario). Para **restaurar**, parás el stack, extraés el tar en el
volumen, y levantás de nuevo.

> El repo trae `deploy/backup-sqlite.sh` — un helper para respaldar las bases en caliente
> (SQLite `.backup`, consistente sin parar el servicio).

## 3. Logs

```bash
# Seguir el log de un servicio en vivo
docker logs -f aiuda-forge-control-1
docker logs -f aiuda-forge-console-1

# Todo el stack junto
docker compose -f docker-compose.yml -f deploy/docker-compose.prod.yml logs -f --tail 100
```

Al arrancar, `control` loguea algo como `vibeforge control listening on :8080 ... N user(s)` —
si ves eso, arrancó bien y leyó los datos.

## 4. Verificación de salud

```bash
docker compose -f docker-compose.yml -f deploy/docker-compose.prod.yml ps   # todos "Up"
curl -s -o /dev/null -w "%{http_code}\n" https://TU-DOMINIO/                 # 200 (console)
curl -s -o /dev/null -w "%{http_code}\n" https://TU-DOMINIO/forge-api/healthz # 200 (control)
```

## 5. Autenticación del servidor con GitHub (deploy key)

Para que `git pull` en el VPS no pida token cada vez, usá una **deploy key SSH read-only**:

```bash
# En el servidor:
ssh-keygen -t ed25519 -N "" -f /root/.ssh/deploy_fluxo -C "vps-deploy"
cat /root/.ssh/deploy_fluxo.pub   # copiá esta clave
# En GitHub: repo → Settings → Deploy keys → Add → pegá la clave, read-only.
# Apuntá el remoto por SSH usando esa clave:
cd /opt/aiuda-forge
git remote set-url origin git@github.com:aiudalabs/aiuda-forge.git
git config core.sshCommand "ssh -i /root/.ssh/deploy_fluxo -o IdentitiesOnly=yes"
git fetch origin   # debe funcionar sin pedir credenciales
```

---

## 6. Troubleshooting — los problemas comunes

### "Failed to fetch" / error de CORS al hacer login
**Síntoma:** el navegador intenta pegarle a `http://localhost:8080` y falla, o el CORS no
matchea.
**Causa:** el console se rebuildeó **sin el override de prod**, así que horneó la URL por
defecto (`localhost:8080`) en vez de `https://TU-DOMINIO/forge-api`. (`NEXT_PUBLIC_VIBEFORGE_API_URL`
se **hornea en el build** del console, no en runtime.)
**Fix:** rebuildeá el console **con los dos `-f`**:
```bash
docker compose -f docker-compose.yml -f deploy/docker-compose.prod.yml up -d --build console
```

### La UI se ve vacía / "todo desapareció"
**Casi siempre NO se perdió nada** — los datos viven en el volumen, intactos. Se ve vacía
porque `control`/`console` están **abajo** (durante un rebuild o porque corriste `up --build`
**sin `-d`** y la terminal titubeó). **Fix:** levantá en detached:
```bash
docker compose -f docker-compose.yml -f deploy/docker-compose.prod.yml up -d
```
Verificá que el volumen tiene las bases: `docker volume inspect aiuda-forge_forge-data`.

### "GitHub App not configured — run /setup/github-app first"
Si la App **ya estaba configurada**, este error suele ser **transitorio**: se disparó mientras
`control` reiniciaba. Esperá a que termine de arrancar y reintentá. Para confirmar que está
bien: `curl -s -o /dev/null -w "%{http_code}\n" https://TU-DOMINIO/forge-api/auth/github/start`
→ **302** significa configurada y OK. Si de verdad falta, verificá que `github-app.json` está en
el volumen y que `VIBEFORGE_GHAPP_CREDS=/data/github-app.json`.

### El certificado TLS no se emite
Caddy necesita que el **DNS resuelva** al servidor y los **puertos 80/443 abiertos**. Verificá
`dig +short TU-DOMINIO` y el firewall (`ufw status`). Mirá `docker logs aiuda-forge-caddy-1`.

### Un sprint no se despacha
Normalmente porque **la spec (`docs/`) todavía no está en `main`** (el `docs_pr` no se mergeó),
o hay **dependencias** sin cumplir, o falta el **canal** (permiso de Copilot / secret de
Claude). Miralo en **Agentes** y en Settings → Canal.

---

## 7. Escalado (cuando crezca)

- **Vertical primero:** más vCPU/RAM al servidor. Los agentes de diseño corren dentro de
  `control` y son lo que más consume — subí CPU antes que nada.
- **Retención:** `VIBEFORGE_RETENTION_DAYS` limpia runs/events viejos (evita que el disco y las
  bases crezcan sin control). GC de workdir + retención de eventos ya están en el engine.
- **A futuro (roadmap):** Postgres + claim con `SKIP LOCKED` para múltiples workers, imágenes de
  sandbox por-lane, y la capa de verificación E2E como check *required* por proyecto. Ver
  [`ROADMAP-v1.1-v1.4.md`](../../ROADMAP-v1.1-v1.4.md) y
  [el plan de verificación](../../PLAN-2026-07-07-verificacion-e2e-multiplataforma.md).

---

**Anterior:** [← Uso](02-uso-proyecto-e2e.md) · **Índice:** [Manual](../README.md)
