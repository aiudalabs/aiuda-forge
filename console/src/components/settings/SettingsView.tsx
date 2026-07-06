"use client";

// SETTINGS — reorganizado por ALCANCE (auditoría UX 2026-07-04), en 4 secciones:
//  1. Conexiones (cuenta): GitHub (la puerta de entrada) · Canal Claude · Notificaciones.
//  2. Este proyecto: un PRESET maestro de autonomía (Manual/Asistido/Autónomo) que fija de
//     una vez dispatch_mode + merge_mode + workflow_approval, y un bloque "Avanzado" colapsado
//     con las perillas sueltas reetiquetadas como preguntas.
//  3. Cuenta y seguridad: cambio de password.
//  4. Avanzado / Legacy (colapsado): ejecutor self-hosted + Tickets (MCP) — solo modo local.
// La MECÁNICA de guardado NO cambió: la config de proyecto usa GET/PUT /projects/{id}/settings
// (su propio form/save), la global usa GET/PUT /settings (form/save aparte). El preset es azúcar
// sobre los mismos writes del proyecto. Los secretos llegan enmascarados (••••••••) y se
// devuelven sin cambio si el usuario no los edita, para no pisar el valor almacenado.

import { useEffect, useState } from "react";
import {
  useSettings,
  useSaveSettings,
  useProjectSettings,
  useSaveProjectSettings,
  useExecutors,
  useSetClaudeSecret,
  useGitHubStatus,
} from "@/lib/hooks";
import { useActiveProject } from "@/lib/activeProject";
import { useT } from "@/lib/i18n";
import { API_URL } from "@/lib/config";
import type { ProjectSettings, SettingsPayload } from "@/lib/types";
import { ConnectorsSection } from "./ConnectorsSection";
import { AccountSecurity } from "./AccountSecurity";

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

const MASKED = "••••••••";

function isMasked(v: string) {
  return v === MASKED;
}

// Presets de autonomía → los 3 campos del proyecto que FIJAN. execution_unit,
// executor, model_by_lane y max_concurrency NO los toca el preset (quedan en
// "Avanzado"). Si los valores actuales no matchean ninguno → "custom".
type Preset = "manual" | "assisted" | "autonomous" | "custom";

const PRESET_FIELDS: Record<
  Exclude<Preset, "custom">,
  Pick<ProjectSettings, "dispatch_mode" | "merge_mode" | "workflow_approval">
> = {
  manual: { dispatch_mode: "approve", merge_mode: "manual", workflow_approval: "manual" },
  assisted: { dispatch_mode: "auto", merge_mode: "manual", workflow_approval: "auto_if_safe" },
  autonomous: { dispatch_mode: "auto", merge_mode: "auto", workflow_approval: "auto_if_safe" },
};

function presetOf(f: ProjectSettings): Preset {
  for (const [name, v] of Object.entries(PRESET_FIELDS) as [
    Exclude<Preset, "custom">,
    (typeof PRESET_FIELDS)[keyof typeof PRESET_FIELDS],
  ][]) {
    if (
      f.dispatch_mode === v.dispatch_mode &&
      f.merge_mode === v.merge_mode &&
      f.workflow_approval === v.workflow_approval
    ) {
      return name;
    }
  }
  return "custom";
}

// ─────────────────────────────────────────────────────────────────────────────
// Componente raíz — compone las 4 secciones en orden de alcance.
// ─────────────────────────────────────────────────────────────────────────────

export function SettingsView() {
  return (
    <div className="settings-stack">
      <ConnectionsSection />
      <ProjectSettingsSection />
      <AccountSecurity />
      <GlobalLegacySection />
    </div>
  );
}

// ─────────────────────────────────────────────────────────────────────────────
// 1. CONEXIONES (cuenta) — GitHub (puerta de entrada) + Canal Claude + Notificaciones.
// ─────────────────────────────────────────────────────────────────────────────

function ConnectionsSection() {
  const t = useT();
  const { activeId } = useActiveProject();
  return (
    <div>
      <div className="sectitle">
        <h2>{t("settings.connections.title")}</h2>
        <span className="c">{t("settings.connections.subtitle")}</span>
      </div>
      <div className="grid3" style={{ gap: 16 }}>
        <GitHubSection projectId={activeId} />
        <ClaudeSecretSection projectId={activeId} />
      </div>
      <div style={{ marginTop: 18 }}>
        <ConnectorsSection />
      </div>
    </div>
  );
}

// GitHub: estado de conexión (GET /auth/github/status) + CTAs (conectar / instalar
// la App) + semáforos de capacidades del proyecto activo (reusa el probe de
// executors que consume el picker de canal: copilot y el secret Claude).
function GitHubSection({ projectId }: { projectId: string | null }) {
  const t = useT();
  const { data: status } = useGitHubStatus();
  const { data: executors } = useExecutors(projectId);
  const copilot = executors?.find((e) => e.id === "copilot");
  const claude = executors?.find((e) => e.id === "claude_action");

  return (
    <div className="card">
      <h3>{t("settings.github.title")}</h3>
      <div className="role">{t("settings.github.role")}</div>

      <div style={{ fontSize: 13, margin: "2px 0 10px" }}>
        {status?.connected ? (
          <span style={{ color: "var(--ok, #1a7a3a)" }}>
            🟢 {t("settings.github.connected", { login: status.login ?? "" })}
          </span>
        ) : (
          <span style={{ color: "var(--muted)" }}>⚪ {t("settings.github.notConnected")}</span>
        )}
      </div>

      <div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
        {status?.app_configured ? (
          // The instance App exists → users connect their own GitHub via OAuth.
          <a className="btn primary sm" href={`${API_URL}/auth/github/start`}>
            {status?.connected ? t("settings.github.reconnect") : t("settings.github.connect")}
          </a>
        ) : (
          // No instance App yet → the ONLY valid action is to create/configure it (the
          // manifest flow). "Connect GitHub" (OAuth) can't work without the App, so we
          // don't show it — it was the confusing dead button.
          <a className="btn primary sm" href={`${API_URL}/setup/github-app`} target="_blank" rel="noreferrer">
            {t("settings.github.setupApp")}
          </a>
        )}
      </div>
      <div style={{ fontSize: 11.5, color: "var(--ink4)", margin: "8px 0 0" }}>
        {status?.app_configured ? `✓ ${t("settings.github.appConfigured")}` : `⚠ ${t("settings.github.appMissing")}`}
      </div>

      {/* Semáforos de capacidades del proyecto activo. */}
      <div style={{ marginTop: 14, borderTop: "1px solid var(--line, #e6e6e6)", paddingTop: 10 }}>
        <div style={{ fontSize: 12, fontWeight: 700, color: "var(--ink2)", marginBottom: 6 }}>
          {t("settings.github.caps")}
        </div>
        {!projectId ? (
          <div style={{ fontSize: 12, color: "var(--ink4)" }}>{t("settings.github.capSelect")}</div>
        ) : (
          <div style={{ display: "flex", flexDirection: "column", gap: 4, fontSize: 12.5 }}>
            <Capability label={t("settings.github.capCopilot")} info={copilot} t={t} />
            <Capability label={t("settings.github.capClaude")} info={claude} t={t} />
          </div>
        )}
      </div>
    </div>
  );
}

function Capability({
  label,
  info,
  t,
}: {
  label: string;
  info?: { available: boolean; reason?: string };
  t: (k: string) => string;
}) {
  const on = info?.available === true;
  return (
    <div style={{ display: "flex", alignItems: "baseline", gap: 6 }}>
      <span>{on ? "🟢" : "⚪"}</span>
      <span style={{ fontWeight: 600 }}>{label}</span>
      <span style={{ color: on ? "var(--ok, #1a7a3a)" : "var(--ink4)" }}>
        {info === undefined ? "…" : on ? t("settings.github.capOn") : info.reason || t("settings.github.capOff")}
      </span>
    </div>
  );
}

// ─────────────────────────────────────────────────────────────────────────────
// 2. ESTE PROYECTO — preset de autonomía + perillas avanzadas (colapsadas).
// GET/PUT /projects/{id}/settings. Estado local + guardado independiente.
// ─────────────────────────────────────────────────────────────────────────────

function ProjectSettingsSection() {
  const t = useT();
  const { project } = useActiveProject();
  const projectId = project?.id ?? null;
  const { data, isLoading, isError } = useProjectSettings(projectId);
  const save = useSaveProjectSettings(projectId);

  const [form, setForm] = useState<ProjectSettings | null>(null);
  const [saved, setSaved] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);

  // Re-sincronizar al cambiar de proyecto o cuando llegan los datos.
  useEffect(() => {
    if (data) setForm({ ...data });
  }, [data]);

  // Limpiar el form al cambiar de proyecto para no mostrar valores del anterior.
  useEffect(() => {
    setForm(null);
    setSaved(false);
    setSaveError(null);
  }, [projectId]);

  async function handleSave() {
    if (!form || !projectId) return;
    setSaveError(null);
    setSaved(false);
    try {
      await save.mutateAsync(form);
      setSaved(true);
      setTimeout(() => setSaved(false), 3000);
    } catch (err: unknown) {
      setSaveError(err instanceof Error ? err.message : String(err));
    }
  }

  const preset = form ? presetOf(form) : "custom";
  const applyPreset = (p: Exclude<Preset, "custom">) =>
    setForm((f) => (f ? { ...f, ...PRESET_FIELDS[p] } : f));

  return (
    <div style={{ marginTop: 22 }}>
      <div className="sectitle">
        <h2>{t("settings.project.title")}</h2>
        <span className="c">
          {project ? t("settings.project.execOf", { name: project.name }) : t("settings.project.execGeneric")}
        </span>
        <span className="sp" />
        <button
          className="btn primary sm"
          onClick={handleSave}
          disabled={save.isPending || !form || !projectId}
        >
          {save.isPending ? t("settings.saving") : t("settings.save")}
        </button>
      </div>

      {saved && (
        <div className="shellnote" style={{ borderColor: "var(--ok-line, #b8e4c5)", color: "var(--ok, #1a7a3a)" }}>
          {t("settings.project.savedNote")}
        </div>
      )}

      {saveError && (
        <div
          style={{
            padding: "8px 12px",
            background: "var(--err-soft, #fff0f0)",
            border: "1px solid var(--err-line, #f5c5c5)",
            borderRadius: 4,
            fontSize: 12,
            color: "var(--err, #c00)",
            marginBottom: 12,
          }}
        >
          {saveError}
        </div>
      )}

      {!projectId ? (
        <div className="placeholder">
          <div className="ph-ic">✦</div>
          {t("settings.project.selectPrompt")}
        </div>
      ) : isLoading || !form ? (
        <div className="placeholder">
          <div className="ph-ic"><span className="spin" /></div>
          {t("settings.project.loading")}
        </div>
      ) : isError ? (
        <div className="placeholder err">
          <div className="ph-ic">⚠</div>
          {t("settings.project.loadError")}
        </div>
      ) : (
        <>
          {/* Preset maestro de autonomía. */}
          <div className="card" style={{ marginBottom: 14 }}>
            <h3>{t("settings.preset.q")}</h3>
            <div className="seg" style={{ marginTop: 12 }}>
              {(["manual", "assisted", "autonomous"] as const).map((p) => (
                <button
                  key={p}
                  className={`seg-btn${preset === p ? " on" : ""}`}
                  onClick={() => applyPreset(p)}
                >
                  {t(`settings.preset.${p}`)}
                </button>
              ))}
            </div>
            <div className="role" style={{ marginTop: 10, minHeight: 0 }}>
              {t(`settings.preset.${preset}Desc`)}
            </div>
          </div>

          {/* Avanzado — perillas sueltas, colapsado por default. */}
          <details className="adv">
            <summary>{t("settings.preset.advanced")}</summary>
            <div className="grid3" style={{ gap: 16, marginTop: 12 }}>
              <DispatchSection
                mode={form.dispatch_mode}
                executor={form.executor}
                workflowApproval={form.workflow_approval}
                maxConcurrency={form.max_concurrency}
                onChange={(p) => setForm((f) => (f ? { ...f, ...p } : f))}
              />
              <MergeModeSection
                mode={form.merge_mode}
                onChange={(merge_mode) => setForm((f) => (f ? { ...f, merge_mode } : f))}
              />
              <ExecutionUnitSection
                unit={form.execution_unit}
                onChange={(execution_unit) => setForm((f) => (f ? { ...f, execution_unit } : f))}
              />
              <ModelByLaneSection
                map={form.model_by_lane}
                onChange={(model_by_lane) => setForm((f) => (f ? { ...f, model_by_lane } : f))}
              />
            </div>
          </details>
        </>
      )}

      <style jsx>{`
        .seg {
          display: inline-flex;
          flex-wrap: wrap;
          gap: 6px;
          border: 1px solid var(--stroke, #e6e6e6);
          border-radius: 10px;
          padding: 4px;
          background: var(--bg2, #faf8f4);
        }
        .seg-btn {
          padding: 8px 18px;
          border: none;
          border-radius: 7px;
          background: transparent;
          color: var(--ink2, #333);
          font-weight: 700;
          font-size: 13px;
          cursor: pointer;
        }
        .seg-btn.on {
          background: var(--accent, #e8440a);
          color: #fff;
        }
        .adv > summary {
          cursor: pointer;
          font-weight: 800;
          font-size: 14px;
          padding: 6px 0;
          list-style: revert;
        }
      `}</style>
    </div>
  );
}

// ─────────────────────────────────────────────────────────────────────────────
// 4. AVANZADO / LEGACY — ejecutor self-hosted + Tickets (MCP), colapsado.
// Único consumidor del form GLOBAL (GET/PUT /settings): mcp · agent_auth · sandbox.
// ─────────────────────────────────────────────────────────────────────────────

function GlobalLegacySection() {
  const t = useT();
  const { data, isLoading, isError } = useSettings();
  const save = useSaveSettings();

  const [form, setForm] = useState<SettingsPayload | null>(null);
  const [saved, setSaved] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);

  useEffect(() => {
    if (data && !form) setForm(structuredClone(data));
  }, [data, form]);

  async function handleSave() {
    if (!form) return;
    setSaveError(null);
    setSaved(false);
    try {
      await save.mutateAsync(form);
      setSaved(true);
      setTimeout(() => setSaved(false), 3000);
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : String(err);
      setSaveError(msg);
    }
  }

  return (
    <details className="adv" style={{ marginTop: 24 }}>
      <summary>
        <span className="adv-title">{t("settings.legacy.sectionTitle")}</span>
        <span className="adv-sub">{t("settings.legacy.sectionSubtitle")}</span>
      </summary>

      <div className="sectitle" style={{ marginTop: 14 }}>
        <span className="sp" />
        <button className="btn primary sm" onClick={handleSave} disabled={save.isPending || !form}>
          {save.isPending ? t("settings.saving") : t("settings.save")}
        </button>
      </div>

      {saved && (
        <div className="shellnote" style={{ borderColor: "var(--ok-line, #b8e4c5)", color: "var(--ok, #1a7a3a)" }}>
          {t("settings.savedNote")}
        </div>
      )}
      {saveError && (
        <div
          style={{
            padding: "8px 12px",
            background: "var(--err-soft, #fff0f0)",
            border: "1px solid var(--err-line, #f5c5c5)",
            borderRadius: 4,
            fontSize: 12,
            color: "var(--err, #c00)",
            marginBottom: 12,
          }}
        >
          {saveError}
        </div>
      )}

      {isLoading || !form ? (
        <div className="placeholder">
          <div className="ph-ic"><span className="spin" /></div>
          {t("settings.loading")}
        </div>
      ) : isError ? (
        <div className="placeholder err">
          <div className="ph-ic">⚠</div>
          {t("settings.loadError")}
        </div>
      ) : (
        <>
          <div
            style={{
              display: "flex",
              alignItems: "baseline",
              gap: 10,
              flexWrap: "wrap",
              margin: "0 0 12px",
            }}
          >
            <h3 style={{ margin: 0, fontSize: 14 }}>{t("settings.legacy.title")}</h3>
            <span style={{ color: "var(--ink4)", fontSize: 12 }}>{t("settings.legacy.note")}</span>
          </div>
          <div className="grid3" style={{ gap: 16 }}>
            <AgentAuthSection
              auth={form.agent_auth}
              onChange={(agent_auth) => setForm((f) => (f ? { ...f, agent_auth } : f))}
            />
            <SandboxSection
              sandbox={form.sandbox}
              onChange={(sandbox) => setForm((f) => (f ? { ...f, sandbox } : f))}
            />
            <McpSection
              connections={form.mcp}
              onChange={(mcp) => setForm((f) => (f ? { ...f, mcp } : f))}
            />
          </div>
        </>
      )}

      <style jsx>{`
        .adv > summary {
          cursor: pointer;
          padding: 8px 0;
          list-style: revert;
        }
        .adv-title {
          font-weight: 900;
          font-size: 16px;
          letter-spacing: -0.3px;
        }
        .adv-sub {
          font-family: var(--mono);
          font-size: 12px;
          color: var(--ink4);
          margin-left: 10px;
        }
      `}</style>
    </details>
  );
}

// ─────────────────────────────────────────────────────────────────────────────
// Sub-secciones
// ─────────────────────────────────────────────────────────────────────────────

function McpSection({
  connections,
  onChange,
}: {
  connections: SettingsPayload["mcp"];
  onChange: (v: SettingsPayload["mcp"]) => void;
}) {
  const t = useT();
  // Which row is expanded for editing (only one at a time). Keeps the list compact
  // and scannable when there are many connections.
  const [editing, setEditing] = useState<number | null>(null);

  const patch = (i: number, p: Partial<SettingsPayload["mcp"][number]>) => {
    const next = [...connections];
    next[i] = { ...next[i], ...p };
    onChange(next);
  };
  const remove = (i: number) => {
    onChange(connections.filter((_, j) => j !== i));
    if (editing === i) setEditing(null);
  };
  const add = () => {
    const next = [...connections, { name: "", url: "", token: "" }];
    onChange(next);
    setEditing(next.length - 1); // open the new row for editing
  };

  return (
    <div className="card">
      <h3>{t("settings.mcp.title")}</h3>
      <div className="role">{t("settings.mcp.role")}</div>

      <ul className="mcp-list">
        {connections.map((conn, i) => {
          // Telegram is configured in the dedicated "Notificaciones" section (v1.3); hide
          // it here so its token isn't edited in two places. Index preserved.
          if (conn.name.toLowerCase() === "telegram") return null;
          const open = editing === i;
          return (
            <li key={i} className="mcp-item">
              <div className="mcp-row">
                <span className="mcp-name">{conn.name || t("settings.mcp.untitled")}</span>
                <span className="mcp-url mono">{conn.url || "—"}</span>
                <span className={`mcp-tok${conn.token ? " on" : ""}`}>
                  {conn.token ? "● token" : "—"}
                </span>
                <button className="link" onClick={() => setEditing(open ? null : i)}>
                  {open ? t("settings.mcp.collapse") : t("settings.mcp.edit")}
                </button>
                <button className="link danger" onClick={() => remove(i)}>
                  {t("settings.mcp.delete")}
                </button>
              </div>
              {open && (
                <div className="mcp-edit">
                  <div className="field">
                    <label>{t("settings.mcp.name")}</label>
                    <input className="inp" value={conn.name} onChange={(e) => patch(i, { name: e.target.value })} />
                  </div>
                  <div className="field">
                    <label>{t("settings.mcp.url")}</label>
                    <input className="inp mono" value={conn.url} onChange={(e) => patch(i, { url: e.target.value })} />
                  </div>
                  <div className="field">
                    <label>
                      {t("settings.mcp.token")}{" "}
                      {isMasked(conn.token) && (
                        <span style={{ color: "var(--ink4)", fontSize: 11 }}>{t("settings.masked")}</span>
                      )}
                    </label>
                    <input
                      className="inp mono"
                      type="password"
                      placeholder={isMasked(conn.token) ? t("settings.maskedPlaceholder") : ""}
                      value={isMasked(conn.token) ? "" : conn.token}
                      // Empty field → restore the mask so the stored secret isn't wiped.
                      onChange={(e) => patch(i, { token: e.target.value || MASKED })}
                    />
                  </div>
                </div>
              )}
            </li>
          );
        })}
      </ul>

      <button className="btn ghost sm" style={{ marginTop: 10 }} onClick={add}>
        {t("settings.mcp.add")}
      </button>
    </div>
  );
}

function AgentAuthSection({
  auth,
  onChange,
}: {
  auth: SettingsPayload["agent_auth"];
  onChange: (v: SettingsPayload["agent_auth"]) => void;
}) {
  const t = useT();
  return (
    <div className="card">
      <h3>{t("settings.auth.title")}</h3>
      <div className="role">{t("settings.auth.role")}</div>
      <div className="field" style={{ marginTop: 10 }}>
        <label>{t("settings.auth.mode")}</label>
        <select
          className="inp"
          value={auth.mode}
          onChange={(e) => onChange({ ...auth, mode: e.target.value })}
        >
          {/* subscription = default del backend (settings.go). Estaba ausente en el
              select (bug L5): un round-trip lo pisaba con oauth_token. */}
          <option value="subscription">{t("settings.auth.optSubscription")}</option>
          <option value="oauth_token">oauth_token (Max)</option>
          <option value="api_key">api_key</option>
        </select>
      </div>
      <div className="field">
        <label>
          {t("settings.auth.secret")} {isMasked(auth.secret) && <span style={{ color: "var(--ink4)", fontSize: 11 }}>{t("settings.masked")}</span>}
        </label>
        <input
          className="inp mono"
          type="password"
          placeholder={isMasked(auth.secret) ? t("settings.maskedPlaceholder") : ""}
          value={isMasked(auth.secret) ? "" : auth.secret}
          onChange={(e) => onChange({ ...auth, secret: e.target.value || MASKED })}
        />
      </div>
    </div>
  );
}

function ExecutionUnitSection({
  unit,
  onChange,
}: {
  unit: ProjectSettings["execution_unit"];
  onChange: (v: ProjectSettings["execution_unit"]) => void;
}) {
  const t = useT();
  return (
    <div className="card">
      <h3>{t("settings.exec.q")}</h3>
      <div className="role">
        {t("settings.exec.role")}
      </div>
      <div className="field" style={{ marginTop: 10 }}>
        <label>{t("settings.exec.mode")}</label>
        <select
          className="inp"
          value={unit}
          onChange={(e) => onChange(e.target.value as ProjectSettings["execution_unit"])}
        >
          <option value="sprint">{t("settings.exec.optSprint")}</option>
          <option value="story">{t("settings.exec.optStory")}</option>
        </select>
      </div>
      <div className="role" style={{ marginTop: 8, fontSize: 12 }}>
        {unit === "sprint"
          ? t("settings.exec.helpSprint")
          : t("settings.exec.helpStory")}
      </div>
    </div>
  );
}

function MergeModeSection({
  mode,
  onChange,
}: {
  mode: ProjectSettings["merge_mode"];
  onChange: (v: ProjectSettings["merge_mode"]) => void;
}) {
  const t = useT();
  return (
    <div className="card">
      <h3>{t("settings.merge.q")}</h3>
      <div className="role">
        {t("settings.merge.role")}
      </div>
      <div className="field" style={{ marginTop: 10 }}>
        <label>{t("settings.merge.mode")}</label>
        <select
          className="inp"
          value={mode}
          onChange={(e) => onChange(e.target.value as ProjectSettings["merge_mode"])}
        >
          <option value="manual">{t("settings.merge.optManual")}</option>
          <option value="auto">{t("settings.merge.optAuto")}</option>
        </select>
      </div>
      <div className="role" style={{ marginTop: 8, fontSize: 12 }}>
        {mode === "auto"
          ? t("settings.merge.helpAuto")
          : t("settings.merge.helpManual")}
      </div>
    </div>
  );
}

// Despacho a agentes de GitHub (pivote F2): cuánta autonomía tiene el conductor
// y por qué canal ejecuta.
function DispatchSection({
  mode,
  executor,
  workflowApproval,
  maxConcurrency,
  onChange,
}: {
  mode: ProjectSettings["dispatch_mode"];
  executor: ProjectSettings["executor"];
  workflowApproval: ProjectSettings["workflow_approval"];
  maxConcurrency: ProjectSettings["max_concurrency"];
  onChange: (
    p: Partial<
      Pick<
        ProjectSettings,
        "dispatch_mode" | "executor" | "workflow_approval" | "max_concurrency"
      >
    >,
  ) => void;
}) {
  const t = useT();
  return (
    <div className="card">
      <h3>{t("settings.dispatch.q")}</h3>
      <div className="role">{t("settings.dispatch.role")}</div>
      <div className="field" style={{ marginTop: 10 }}>
        <label>{t("settings.dispatch.mode")}</label>
        <select
          className="inp"
          value={mode}
          onChange={(e) => onChange({ dispatch_mode: e.target.value as ProjectSettings["dispatch_mode"] })}
        >
          <option value="approve">{t("settings.dispatch.optApprove")}</option>
          <option value="auto">{t("settings.dispatch.optAuto")}</option>
          <option value="off">{t("settings.dispatch.optOff")}</option>
        </select>
      </div>
      <div className="field" style={{ marginTop: 10 }}>
        <label>{t("settings.dispatch.executor")}</label>
        <select
          className="inp"
          value={executor}
          onChange={(e) => onChange({ executor: e.target.value as ProjectSettings["executor"] })}
        >
          <option value="copilot">{t("settings.dispatch.optCopilot")}</option>
          <option value="claude_action">{t("settings.dispatch.optClaude")}</option>
        </select>
      </div>
      <div className="role" style={{ marginTop: 8, fontSize: 12 }}>
        {mode === "auto"
          ? t("settings.dispatch.helpAuto")
          : mode === "off"
            ? t("settings.dispatch.helpOff")
            : t("settings.dispatch.helpApprove")}
      </div>

      <div className="field" style={{ marginTop: 12 }}>
        <label>{t("settings.dispatch.workflowApproval")}</label>
        <select
          className="inp"
          value={workflowApproval}
          onChange={(e) =>
            onChange({ workflow_approval: e.target.value as ProjectSettings["workflow_approval"] })
          }
        >
          <option value="manual">{t("settings.dispatch.wfOptManual")}</option>
          <option value="auto_if_safe">{t("settings.dispatch.wfOptAutoIfSafe")}</option>
        </select>
      </div>
      <div className="role" style={{ marginTop: 8, fontSize: 12 }}>
        {workflowApproval === "auto_if_safe"
          ? t("settings.dispatch.wfHelpAutoIfSafe")
          : t("settings.dispatch.wfHelpManual")}
      </div>

      <div className="field" style={{ marginTop: 12 }}>
        <label>{t("settings.dispatch.maxConcurrency")}</label>
        <input
          className="inp"
          type="number"
          min={0}
          step={1}
          value={maxConcurrency}
          onChange={(e) => {
            // Vacío o inválido → 0 (sin límite). Nunca negativo.
            const n = Math.floor(Number(e.target.value));
            onChange({ max_concurrency: Number.isFinite(n) && n > 0 ? n : 0 });
          }}
        />
      </div>
      <div className="role" style={{ marginTop: 8, fontSize: 12 }}>
        {t("settings.dispatch.maxConcurrencyHint")}
      </div>
    </div>
  );
}

// Ruteo de modelo por lane (frontera donde importa, barato donde no): pares
// lane → modelo que el conductor pasa a la Agent tasks API. Vacío = auto.
function ModelByLaneSection({
  map,
  onChange,
}: {
  map: Record<string, string>;
  onChange: (v: Record<string, string>) => void;
}) {
  const t = useT();
  const rows = Object.entries(map);
  const setRow = (oldLane: string, lane: string, model: string) => {
    const next: Record<string, string> = {};
    for (const [k, v] of rows) {
      if (k === oldLane) {
        if (lane.trim() !== "") next[lane.trim()] = model;
      } else {
        next[k] = v;
      }
    }
    onChange(next);
  };
  return (
    <div className="card">
      <h3>{t("settings.modelByLane.title")}</h3>
      <div className="role">{t("settings.modelByLane.role")}</div>
      <div style={{ marginTop: 10, display: "flex", flexDirection: "column", gap: 8 }}>
        {rows.length === 0 && (
          <div className="role" style={{ fontSize: 12 }}>{t("settings.modelByLane.empty")}</div>
        )}
        {rows.map(([lane, model], i) => (
          <div key={i} style={{ display: "flex", gap: 6 }}>
            <input
              className="inp mono"
              style={{ flex: 1, fontSize: 12 }}
              value={lane}
              placeholder="python-dev"
              onChange={(e) => setRow(lane, e.target.value, model)}
            />
            <input
              className="inp mono"
              style={{ flex: 1.4, fontSize: 12 }}
              value={model}
              placeholder="claude-sonnet-4.6"
              onChange={(e) => setRow(lane, lane, e.target.value)}
            />
            <button
              className="btn ghost sm"
              title={t("settings.modelByLane.remove")}
              onClick={() => {
                const next = { ...map };
                delete next[lane];
                onChange(next);
              }}
            >
              ✕
            </button>
          </div>
        ))}
        <button
          className="btn ghost sm"
          style={{ alignSelf: "flex-start" }}
          onClick={() => {
            if (!("" in map)) onChange({ ...map, "": "" });
          }}
        >
          {t("settings.modelByLane.add")}
        </button>
      </div>
      <div className="role" style={{ marginTop: 8, fontSize: 12 }}>
        {t("settings.modelByLane.hint")}
      </div>
    </div>
  );
}

function SandboxSection({
  sandbox,
  onChange,
}: {
  sandbox: SettingsPayload["sandbox"];
  onChange: (v: SettingsPayload["sandbox"]) => void;
}) {
  const t = useT();
  return (
    <div className="card">
      <h3>{t("settings.sandbox.title")}</h3>
      <div className="role">{t("settings.sandbox.role")}</div>
      <div className="field" style={{ marginTop: 10 }}>
        <label>{t("settings.sandbox.runtime")}</label>
        <input
          className="inp"
          value={sandbox.runtime}
          onChange={(e) => onChange({ ...sandbox, runtime: e.target.value })}
        />
      </div>
      <div className="field">
        <label>{t("settings.sandbox.image")}</label>
        <input
          className="inp mono"
          value={sandbox.image}
          onChange={(e) => onChange({ ...sandbox, image: e.target.value })}
        />
      </div>
    </div>
  );
}


// ─────────────────────────────────────────────────────────────────────────────
// Canal Claude: siembra del secret CLAUDE_CODE_OAUTH_TOKEN en el repo del
// proyecto. El token viaja directo a GitHub (cifrado con la public key del
// repo) — Fluxo no lo guarda. El probe de canales lo verifica en vivo.
// ─────────────────────────────────────────────────────────────────────────────

function ClaudeSecretSection({ projectId }: { projectId: string | null }) {
  const t = useT();
  const [token, setTokenValue] = useState("");
  const [done, setDone] = useState(false);
  const { data: executors } = useExecutors(projectId);
  const save = useSetClaudeSecret(projectId);
  const claude = executors?.find((e) => e.id === "claude_action");

  return (
    <div className="card">
      <h3>{t("settings.claude.title")}</h3>
      <div className="role">{t("settings.claude.role")}</div>
      {claude && (
        <div style={{ fontSize: 12, margin: "8px 0" }}>
          {claude.available ? (
            <span style={{ color: "var(--ok, #1a7a3a)" }}>🟢 {t("settings.claude.ready")}</span>
          ) : (
            <span style={{ color: "var(--muted)" }}>⛔ {claude.reason}</span>
          )}
        </div>
      )}
      <div className="field" style={{ marginTop: 8 }}>
        <label>{t("settings.claude.tokenLabel")}</label>
        <input
          className="inp mono"
          type="password"
          value={token}
          onChange={(e) => { setTokenValue(e.target.value); setDone(false); }}
          placeholder={t("settings.claude.tokenPlaceholder")}
        />
      </div>
      <p className="c" style={{ fontSize: 11.5, margin: "6px 0 10px" }}>{t("settings.claude.hint")}</p>
      <button
        className="btn primary sm"
        disabled={!token.trim() || save.isPending || !projectId}
        onClick={() =>
          save.mutate(token.trim(), {
            onSuccess: () => { setTokenValue(""); setDone(true); },
            onError: (e) => window.alert(t("settings.claude.error") + "\n" + (e instanceof Error ? e.message : String(e))),
          })
        }
      >
        {save.isPending ? t("settings.claude.saving") : done ? t("settings.claude.saved") : t("settings.claude.save")}
      </button>
    </div>
  );
}
