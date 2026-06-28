# v1.3 — Canales + import de tickets (diseño aprobado)

Estado: **Fase 1 aprobada** (2026-06-28). Telegram end-to-end + import de GitHub,
con identidad mapeada usuario externo → sesión (auditable, respeta roles v1.2).

## Objetivo
Operar la fábrica desde canales externos (notificaciones salientes + comandos
entrantes) e importar tickets externos al backlog. El contenido lo sigue generando
el modelo; los canales transportan eventos y comandos.

## Arquitectura (3 piezas, minimal)
```
SALIDA:  store.emitTx → bus.drain → ChannelDelivery → Connector.Notify(event)
         (hook nuevo en internal/api/bus.go; el bus ya recorre cada evento persistido)
ENTRADA: POST /webhooks/{connector} → VerifySignature(HMAC/secret)
         → resolver (usuario externo → sesión aiuda-forge) + (canal → proyecto)
         → Brain.Send(projectID, rol_real, texto) → responder al canal
         (aprobaciones de acciones mutantes vía botones inline del canal)
IMPORT:  POST /projects/{id}/import/{source} → ImportRunner (espejo de publish.go)
         → mapea issues → Story/Epic/Sprint con external_ref (dedup idempotente)
```

### Abstracciones
- **`Connector` interface**: `Notify(event)`, `HandleInbound(payload)`, `VerifySignature(req)`.
  Un archivo por conector (`internal/channels/telegram/…`).
- **Config de canal**: tabla `project_channels(project_id, connector, target, events, created_at)`
  + token del conector en `settings.MCP[connector]` (enmascarado `••••••••`, registrado en
  la capa de redacción de logs `agent.RegisterSecret`).
- **`external_ref`** en `stories`: `UNIQUE` cuando no vacío. Formato `github:owner/repo#N`.
  Reimportar el mismo issue = no-op.

### Identidad entrante (decisión aprobada)
El conector vincula cada usuario externo (Telegram) a una cuenta aiuda-forge por email
(`/link <email>` con verificación) y actúa con su **rol real** del proyecto. Respeta
multi-tenant y el gating de roles v1.2. El service-token queda sólo para canales de
sistema/CI.

### Canal → proyecto
Mapeo explícito por comando `/link <project>` en el canal (guardado en `project_channels`).
Sin mapeo → el conector responde "canal no vinculado".

## Análisis adversarial (riesgos y mitigaciones)
1. **5 conectores a medias** → construir UN conector end-to-end (Telegram) + UNA fuente de
   import (GitHub) primero; replicar después. Spine extensible, no N integraciones.
2. **Webhook entrante no autenticado** → verificar firma/secreto (HMAC) en todo `/webhooks/*`;
   ruta pública pero verificada, nunca ejecuta sin validar.
3. **Atribución de identidad** → usuario externo → sesión real (no service-token anónimo).
4. **Canal → proyecto ambiguo** → mapeo explícito por `/link`, nunca implícito.
5. **Secretos** → reutilizar enmascarado de settings + `RegisterSecret` (no se loguean).

## Orden de build (suite verde + commit por paso)
- **Fase 1**:
  1. `external_ref` + import de GitHub issues (self-contained). [#57]
  2. Spine de conectores + config `project_channels`. [#58]
  3. Telegram salida: notificaciones de eventos. [#59]
  4. Telegram entrada: comandos → Brain + aprobaciones por botones. [#60]
  5. Console: UI de canales + disparo de import. [#61]
- **Fase 2**: Slack (OAuth + signing secret).
- **Fase 3**: JIRA import + Confluence (push de specs).

## Fuera de alcance de Fase 1 → BACKLOG
Slack, JIRA, Confluence; OAuth multi-workspace; import bidireccional (sync de vuelta el
estado al issue externo); traducción del contenido de agentes.
