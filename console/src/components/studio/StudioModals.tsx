"use client";

// Modales del Studio: nuevo proyecto (crea repo + lanza el diseño) e iteración
// (change request → workflow "iterate" sobre un proyecto ya publicado).

import { useEffect, useState } from "react";
import { ApiError } from "@/lib/api";
import { useActiveProject } from "@/lib/activeProject";
import { useCreateDesignRun, useCreateIterationRun, useCreateProject } from "@/lib/hooks";
import { useT } from "@/lib/i18n";

// ─────────────────────────────────────────────────────────────────────────────
// Modal: nueva iteración — el change request que dispara el workflow "iterate"
// ─────────────────────────────────────────────────────────────────────────────

export function IterationModal({
  projectId,
  repo,
  onClose,
  onCreated,
}: {
  projectId: string;
  repo: string;
  onClose: () => void;
  onCreated: (id: string) => void;
}) {
  const t = useT();
  const [changeRequest, setChangeRequest] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const createIterationRun = useCreateIterationRun();

  // Cerrar con Escape.
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") onClose();
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    const trimmed = changeRequest.trim();
    if (!trimmed) {
      setError(t("studio.iteration.validation"));
      return;
    }
    setBusy(true);
    try {
      const run = await createIterationRun.mutateAsync({
        project_id: projectId,
        repo,
        changeRequest: trimmed,
      });
      onCreated(run.id);
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="modal on" role="dialog" aria-modal="true" aria-labelledby="it-title">
      <div className="mh">
        <h3 id="it-title">{t("studio.iteration.title")}</h3>
        <button className="x" onClick={onClose} aria-label={t("studio.modal.close")}>
          ✕
        </button>
      </div>
      <form className="mb" onSubmit={handleSubmit}>
        <p style={{ fontSize: 12, color: "var(--ink4)", margin: "0 0 12px" }}>
          {t("studio.iteration.hint")}
        </p>
        <div className="field">
          <label htmlFor="it-cr">{t("studio.iteration.label")}</label>
          <textarea
            id="it-cr"
            className="inp"
            style={{ resize: "vertical", minHeight: 120 }}
            value={changeRequest}
            onChange={(e) => setChangeRequest(e.target.value)}
            placeholder={t("studio.iteration.placeholder")}
            autoFocus
            required
          />
        </div>

        {error && (
          <div
            style={{
              padding: "8px 12px",
              background: "var(--accent-soft)",
              border: "1px solid var(--accent-line)",
              borderRadius: 4,
              fontSize: 12,
              color: "var(--accent)",
              marginBottom: 10,
            }}
          >
            {error}
          </div>
        )}

        <div style={{ display: "flex", gap: 10 }}>
          <button type="button" className="btn ghost" style={{ flex: 1 }} onClick={onClose}>
            {t("studio.iteration.cancel")}
          </button>
          <button type="submit" className="btn primary" style={{ flex: 1 }} disabled={busy}>
            {busy ? t("studio.iteration.launching") : t("studio.iteration.launch")}
          </button>
        </div>
      </form>
    </div>
  );
}
