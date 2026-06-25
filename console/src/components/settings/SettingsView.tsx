"use client";

// SETTINGS — conexiones · seguridad · política (doc 16 §2.7).
// Cableado contra GET/PUT /settings. Los secretos llegan enmascarados (••••••••) y se
// devuelven sin cambio si el usuario no los edita, para no pisar el valor almacenado.

import { useEffect, useState } from "react";
import { useSettings, useSaveSettings } from "@/lib/hooks";
import type { SettingsPayload } from "@/lib/types";

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
          Cargando configuración…
        </div>
      </div>
    );
  }

  if (isError || !form) {
    return (
      <div className="wrap">
        <div className="placeholder err">
          <div className="ph-ic">⚠</div>
          No se pudo cargar la configuración.
        </div>
      </div>
    );
  }

  return (
    <div className="wrap">
      <div className="sectitle">
        <h2>Settings</h2>
        <span className="c">conexiones · seguridad · política</span>
        <span className="sp" />
        <button className="btn primary sm" onClick={handleSave} disabled={save.isPending}>
          {save.isPending ? "Guardando…" : "Guardar"}
        </button>
      </div>

      {saved && (
        <div className="shellnote" style={{ borderColor: "var(--ok-line, #b8e4c5)", color: "var(--ok, #1a7a3a)" }}>
          Configuración guardada.
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

        {/* Merge Policy */}
        <MergePolicySection
          policy={form.merge_policy}
          onChange={(merge_policy) => setForm((f) => f ? { ...f, merge_policy } : f)}
        />

        {/* Sandbox */}
        <SandboxSection
          sandbox={form.sandbox}
          onChange={(sandbox) => setForm((f) => f ? { ...f, sandbox } : f)}
        />

        {/* Unidad de ejecución */}
        <ExecutionUnitSection
          unit={form.execution_unit}
          onChange={(execution_unit) => setForm((f) => f ? { ...f, execution_unit } : f)}
        />
      </div>
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
  return (
    <div className="card">
      <h3>Tickets (MCP)</h3>
      <div className="role">El backlog vive en JIRA/GitHub; la fábrica los lee por MCP.</div>
      {connections.map((conn, i) => (
        <div key={i} style={{ marginTop: 10 }}>
          <div className="field">
            <label>Nombre</label>
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
            <label>URL</label>
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
            <label>Token {isMasked(conn.token) && <span style={{ color: "var(--ink4)", fontSize: 11 }}>(enmascarado)</span>}</label>
            <input
              className="inp mono"
              type="password"
              placeholder={isMasked(conn.token) ? "Sin cambios — dejar vacío para preservar" : ""}
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
        + Añadir conexión
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
  return (
    <div className="card">
      <h3>Auth del agente</h3>
      <div className="role">Cómo corre Claude dentro del sandbox.</div>
      <div className="field" style={{ marginTop: 10 }}>
        <label>Modo</label>
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
          Secret {isMasked(auth.secret) && <span style={{ color: "var(--ink4)", fontSize: 11 }}>(enmascarado)</span>}
        </label>
        <input
          className="inp mono"
          type="password"
          placeholder={isMasked(auth.secret) ? "Sin cambios — dejar vacío para preservar" : ""}
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
  unit: SettingsPayload["execution_unit"];
  onChange: (v: string) => void;
}) {
  return (
    <div className="card">
      <h3>Unidad de ejecución</h3>
      <div className="role">
        Cómo la fábrica agrupa el trabajo en PRs.
      </div>
      <div className="field" style={{ marginTop: 10 }}>
        <label>Modo</label>
        <select className="inp" value={unit} onChange={(e) => onChange(e.target.value)}>
          <option value="sprint">por sprint — 1 PR por sprint (goal mode)</option>
          <option value="story">por story — 1 PR por story</option>
        </select>
      </div>
      <div className="role" style={{ marginTop: 8, fontSize: 12 }}>
        {unit === "sprint"
          ? "Cada sprint se implementa en un solo run, sobre una rama, como un PR coherente."
          : "Cada story se implementa por separado: un run y un PR por story."}
      </div>
    </div>
  );
}

function MergePolicySection({
  policy,
  onChange,
}: {
  policy: SettingsPayload["merge_policy"];
  onChange: (v: SettingsPayload["merge_policy"]) => void;
}) {
  return (
    <div className="card">
      <h3>Política de merge</h3>
      <div className="role">Por nivel de riesgo.</div>
      <div className="field" style={{ marginTop: 10 }}>
        <label>Bajo riesgo</label>
        <select
          className="inp"
          value={policy.low_risk}
          onChange={(e) => onChange({ ...policy, low_risk: e.target.value })}
        >
          <option value="automerge">automerge</option>
          <option value="human_gate">human_gate</option>
        </select>
      </div>
      <div className="field">
        <label>Auth / pagos / migrac.</label>
        <select
          className="inp"
          value={policy.high_risk}
          onChange={(e) => onChange({ ...policy, high_risk: e.target.value })}
        >
          <option value="human_gate">human_gate</option>
          <option value="automerge">automerge</option>
        </select>
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
  return (
    <div className="card">
      <h3>Sandbox</h3>
      <div className="role">Aislamiento de la ejecución.</div>
      <div className="field" style={{ marginTop: 10 }}>
        <label>Runtime</label>
        <input
          className="inp"
          value={sandbox.runtime}
          onChange={(e) => onChange({ ...sandbox, runtime: e.target.value })}
        />
      </div>
      <div className="field">
        <label>Imagen del agente</label>
        <input
          className="inp mono"
          value={sandbox.image}
          onChange={(e) => onChange({ ...sandbox, image: e.target.value })}
        />
      </div>
    </div>
  );
}
