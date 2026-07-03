"use client";

// SETTINGS — dos planos (Wave 2, multi-tenant), ordenados por lo que el usuario busca:
//  1. PER-PROYECTO (arriba, proyecto activo): unidad de ejecución · modo de merge ·
//     despacho a agentes (dispatch/executor/workflow_approval/max_concurrency) · modelo
//     por lane. GET/PUT /projects/{id}/settings. Se auto-gestiona carga/errores y su
//     propio guardado.
//  2. GLOBAL (abajo, por instancia): Tickets (MCP) + el "ejecutor legacy (self-hosted)"
//     (auth del agente · sandbox), agrupado y separado porque solo aplica al modo fábrica
//     local — la ejecución GitHub-native no lo usa. GET/PUT /settings.
// Conectores (Telegram) y Cuenta/seguridad (cambio de password) se renderizan DESPUÉS,
// como componentes hermanos en app/settings/page.tsx.
// Los secretos llegan enmascarados (••••••••) y se devuelven sin cambio si el usuario
// no los edita, para no pisar el valor almacenado.

import { useEffect, useState } from "react";
import {
  useSettings,
  useSaveSettings,
  useProjectSettings,
  useSaveProjectSettings,
} from "@/lib/hooks";
import { useActiveProject } from "@/lib/activeProject";
import { useT } from "@/lib/i18n";
import type { ProjectSettings, SettingsPayload } from "@/lib/types";

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

const MASKED = "••••••••";

function isMasked(v: string) {
  return v === MASKED;
}

// ─────────────────────────────────────────────────────────────────────────────
// Componente raíz
// ─────────────────────────────────────────────────────────────────────────────

export function SettingsView() {
  const t = useT();
  const { data, isLoading, isError } = useSettings();
  const save = useSaveSettings();

  // Estado local mutable del formulario.
  const [form, setForm] = useState<SettingsPayload | null>(null);
  const [saved, setSaved] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);

  // Inicializar el formulario con los datos del servidor.
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
    <div className="settings-stack">
      {/* PER-PROYECTO va PRIMERO: es lo que el usuario busca (cómo se ejecuta su
          proyecto). Se auto-gestiona su propia carga/errores y su guardado, así que
          un fallo del fetch global de abajo no lo deja en blanco. */}
      <ProjectSettingsSection />

      {/* GLOBAL (por instancia): conexiones y ejecutor legacy self-hosted. Renderiza
          su propio estado de carga/error inline en vez de tumbar toda la página. */}
      <div style={{ marginTop: 24 }}>
        <div className="sectitle">
          <h2>{t("settings.global.title")}</h2>
          <span className="c">{t("settings.global.subtitle")}</span>
          <span className="sp" />
          <button
            className="btn primary sm"
            onClick={handleSave}
            disabled={save.isPending || !form}
          >
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
            {/* Tickets (MCP) — conexión de backlog por instancia. */}
            <div className="grid3" style={{ gap: 16 }}>
              <McpSection
                connections={form.mcp}
                onChange={(mcp) => setForm((f) => f ? { ...f, mcp } : f)}
              />
            </div>

            {/* Ejecutor legacy (self-hosted): auth del agente + sandbox. Solo aplica al
                modo fábrica local; la ejecución GitHub-native no las usa. Separado y al
                final para que no se confunda con la config activa del proyecto. */}
            <div style={{ marginTop: 18 }}>
              <div
                style={{
                  display: "flex",
                  alignItems: "baseline",
                  gap: 10,
                  flexWrap: "wrap",
                  margin: "0 0 12px",
                  paddingTop: 8,
                  borderTop: "1px solid var(--line, #e6e6e6)",
                }}
              >
                <h3 style={{ margin: 0, fontSize: 14 }}>{t("settings.legacy.title")}</h3>
                <span style={{ color: "var(--ink4)", fontSize: 12 }}>
                  {t("settings.legacy.note")}
                </span>
              </div>
              <div className="grid3" style={{ gap: 16 }}>
                <AgentAuthSection
                  auth={form.agent_auth}
                  onChange={(agent_auth) => setForm((f) => f ? { ...f, agent_auth } : f)}
                />
                <SandboxSection
                  sandbox={form.sandbox}
                  onChange={(sandbox) => setForm((f) => f ? { ...f, sandbox } : f)}
                />
              </div>
            </div>
          </>
        )}
      </div>
    </div>
  );
}

// ─────────────────────────────────────────────────────────────────────────────
// Sección PER-PROYECTO — execution_unit + merge_mode del proyecto activo
// (GET/PUT /projects/{id}/settings). Estado local + guardado independiente del
// formulario global, porque pega contra otro endpoint.
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
        <div className="grid3" style={{ gap: 16 }}>
          <ExecutionUnitSection
            unit={form.execution_unit}
            onChange={(execution_unit) =>
              setForm((f) => (f ? { ...f, execution_unit } : f))
            }
          />
          <MergeModeSection
            mode={form.merge_mode}
            onChange={(merge_mode) => setForm((f) => (f ? { ...f, merge_mode } : f))}
          />
          <DispatchSection
            mode={form.dispatch_mode}
            executor={form.executor}
            workflowApproval={form.workflow_approval}
            maxConcurrency={form.max_concurrency}
            onChange={(p) => setForm((f) => (f ? { ...f, ...p } : f))}
          />
          <ModelByLaneSection
            map={form.model_by_lane}
            onChange={(model_by_lane) => setForm((f) => (f ? { ...f, model_by_lane } : f))}
          />
        </div>
      )}
    </div>
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
          // Telegram is configured in the dedicated "Conectores" section (v1.3); hide
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
      <h3>{t("settings.exec.title")}</h3>
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
      <h3>{t("settings.merge.title")}</h3>
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
      <h3>{t("settings.dispatch.title")}</h3>
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
