"use client";

// SETTINGS — dos planos (Wave 2, multi-tenant):
//  · GLOBAL (por instancia): conexiones (MCP) · auth del agente · sandbox. GET/PUT /settings.
//  · PER-PROYECTO (proyecto activo): unidad de ejecución · modo de merge.
//    GET/PUT /projects/{id}/settings. execution_unit + merge_mode + merge_policy
//    SALIERON del plano global — ahora viven por proyecto.
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

  if (isLoading) {
    return (
      <div className="wrap">
        <div className="placeholder">
          <div className="ph-ic"><span className="spin" /></div>
          {t("settings.loading")}
        </div>
      </div>
    );
  }

  if (isError || !form) {
    return (
      <div className="wrap">
        <div className="placeholder err">
          <div className="ph-ic">⚠</div>
          {t("settings.loadError")}
        </div>
      </div>
    );
  }

  return (
    <div className="wrap">
      <div className="sectitle">
        <h2>{t("settings.title")}</h2>
        <span className="c">{t("settings.subtitle")}</span>
        <span className="sp" />
        <button className="btn primary sm" onClick={handleSave} disabled={save.isPending}>
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

      {/* GLOBAL: conexiones · seguridad · sandbox (por instancia). */}
      <div className="grid3" style={{ gap: 16 }}>
        {/* MCP Connections */}
        <McpSection
          connections={form.mcp}
          onChange={(mcp) => setForm((f) => f ? { ...f, mcp } : f)}
        />

        {/* Agent Auth */}
        <AgentAuthSection
          auth={form.agent_auth}
          onChange={(agent_auth) => setForm((f) => f ? { ...f, agent_auth } : f)}
        />

        {/* Sandbox */}
        <SandboxSection
          sandbox={form.sandbox}
          onChange={(sandbox) => setForm((f) => f ? { ...f, sandbox } : f)}
        />
      </div>

      {/* PER-PROYECTO: unidad de ejecución + modo de merge del proyecto activo. */}
      <ProjectSettingsSection />
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
  return (
    <div className="card">
      <h3>{t("settings.mcp.title")}</h3>
      <div className="role">{t("settings.mcp.role")}</div>
      {connections.map((conn, i) => (
        <div key={i} style={{ marginTop: 10 }}>
          <div className="field">
            <label>{t("settings.mcp.name")}</label>
            <input
              className="inp"
              value={conn.name}
              onChange={(e) => {
                const next = [...connections];
                next[i] = { ...next[i], name: e.target.value };
                onChange(next);
              }}
            />
          </div>
          <div className="field">
            <label>{t("settings.mcp.url")}</label>
            <input
              className="inp mono"
              value={conn.url}
              onChange={(e) => {
                const next = [...connections];
                next[i] = { ...next[i], url: e.target.value };
                onChange(next);
              }}
            />
          </div>
          <div className="field">
            <label>{t("settings.mcp.token")} {isMasked(conn.token) && <span style={{ color: "var(--ink4)", fontSize: 11 }}>{t("settings.masked")}</span>}</label>
            <input
              className="inp mono"
              type="password"
              placeholder={isMasked(conn.token) ? t("settings.maskedPlaceholder") : ""}
              value={isMasked(conn.token) ? "" : conn.token}
              onChange={(e) => {
                const next = [...connections];
                // Si el usuario borra el campo → devolver la máscara para no pisar el real.
                next[i] = { ...next[i], token: e.target.value || MASKED };
                onChange(next);
              }}
            />
          </div>
        </div>
      ))}
      <button
        className="btn ghost sm"
        style={{ marginTop: 10 }}
        onClick={() => onChange([...connections, { name: "", url: "", token: "" }])}
      >
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
