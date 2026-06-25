"use client";

// REGISTRY — agentes / skills / workflows, no-code (doc 16 §2.5).
// Cableado contra GET/PUT/DELETE /registry/{agents,skills,workflows}.
// Modo mock: cae a datos de ejemplo de lib/mock cuando la API no responde.

import { useEffect, useState } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { Light as SyntaxHighlighter } from "react-syntax-highlighter";
import yaml from "react-syntax-highlighter/dist/esm/languages/hljs/yaml";
import { githubGist } from "react-syntax-highlighter/dist/esm/styles/hljs";
import {
  useAgentPersona,
  useDeleteRegistryItem,
  useRegistryItem,
  useRegistryList,
  useSaveRegistryItem,
} from "@/lib/hooks";
import type { RegistryKind } from "@/lib/types";

// Register only the YAML language to keep the bundle minimal.
SyntaxHighlighter.registerLanguage("yaml", yaml);

// ─────────────────────────────────────────────────────────────────────────────
// Tipos locales
// ─────────────────────────────────────────────────────────────────────────────

type ActiveTab = RegistryKind;
type ItemModal =
  | { kind: RegistryKind; id: string; isNew: false }
  | { kind: RegistryKind; id: string; isNew: true }
  | null;

// ─────────────────────────────────────────────────────────────────────────────
// Componente raíz
// ─────────────────────────────────────────────────────────────────────────────

export function RegistryView() {
  const [tab, setTab] = useState<ActiveTab>("agents");
  const [modal, setModal] = useState<ItemModal>(null);

  const { data: listData, isLoading, isError } = useRegistryList(tab);
  const ids = listData?.ids ?? [];

  function openNew() {
    setModal({ kind: tab, id: "", isNew: true });
  }

  function openItem(id: string) {
    setModal({ kind: tab, id, isNew: false });
  }

  const TAB_LABELS: Record<ActiveTab, string> = {
    agents: "Agentes",
    skills: "Skills",
    workflows: "Workflows",
  };

  return (
    <div className="wrap">
      {/* Pestañas */}
      <div className="sectitle">
        <h2>Registry</h2>
        <span className="c">agentes · skills · workflows</span>
        <span className="sp" />
        {(["agents", "skills", "workflows"] as ActiveTab[]).map((k) => (
          <button
            key={k}
            className={`btn ghost sm${tab === k ? " on" : ""}`}
            onClick={() => setTab(k)}
          >
            {TAB_LABELS[k]}
          </button>
        ))}
        <button className="btn ghost sm" onClick={openNew}>
          + Nuevo
        </button>
      </div>

      {/* Lista */}
      {isLoading ? (
        <div className="placeholder">
          <div className="ph-ic">
            <span className="spin" />
          </div>
          Cargando {TAB_LABELS[tab].toLowerCase()}…
        </div>
      ) : isError ? (
        <div className="placeholder err">
          <div className="ph-ic">⚠</div>
          No se pudo conectar al registry.
        </div>
      ) : ids.length === 0 ? (
        <div className="placeholder">
          <div className="ph-ic">◆</div>
          Sin {TAB_LABELS[tab].toLowerCase()} todavía.
        </div>
      ) : (
        <div className="grid3">
          {ids.map((id) => (
            <ItemCard key={id} kind={tab} id={id} onOpen={openItem} />
          ))}
        </div>
      )}

      {/* Modal de edición */}
      <div className={`overlay ${modal ? "on" : ""}`} onClick={() => setModal(null)} />
      {modal && (
        <ItemEditorModal
          kind={modal.kind}
          id={modal.id}
          isNew={modal.isNew}
          onClose={() => setModal(null)}
        />
      )}
    </div>
  );
}

// ─────────────────────────────────────────────────────────────────────────────
// Tarjeta de ítem (título + primeras líneas del contenido)
// ─────────────────────────────────────────────────────────────────────────────

function ItemCard({
  kind,
  id,
  onOpen,
}: {
  kind: RegistryKind;
  id: string;
  onOpen: (id: string) => void;
}) {
  const { data: content } = useRegistryItem(kind, id);
  const deleteItem = useDeleteRegistryItem(kind);

  // Extrae la primera línea no vacía del YAML/markdown como subtítulo.
  const preview = content
    ? content
        .split("\n")
        .filter((l) => l.trim() && !l.startsWith("id:") && !l.startsWith("#"))
        .slice(0, 2)
        .join(" · ")
    : "";

  function handleDelete(e: React.MouseEvent) {
    e.stopPropagation();
    if (confirm(`¿Borrar ${kind}/${id}?`)) {
      deleteItem.mutate(id);
    }
  }

  return (
    <div className="card click" onClick={() => onOpen(id)}>
      <h3 style={{ display: "flex", justifyContent: "space-between" }}>
        <span>{id}</span>
        <button
          className="btn ghost sm"
          style={{ fontSize: 11, padding: "0 6px" }}
          onClick={handleDelete}
          disabled={deleteItem.isPending}
        >
          ✕
        </button>
      </h3>
      {preview && (
        <div className="role mono" style={{ fontSize: 11.5, wordBreak: "break-all" }}>
          {preview}
        </div>
      )}
      {!content && <div className="role" style={{ color: "var(--ink4)", fontSize: 12 }}>Cargando…</div>}
    </div>
  );
}

// ─────────────────────────────────────────────────────────────────────────────
// Vista renderizada — YAML resaltado o markdown según kind
// ─────────────────────────────────────────────────────────────────────────────

function RenderedView({
  kind,
  id,
  content,
}: {
  kind: RegistryKind;
  id: string;
  content: string;
}) {
  // Persona del agente (solo para agents).
  const { data: persona } = useAgentPersona(kind === "agents" ? id : null);

  if (kind === "skills") {
    // Skills son markdown puro.
    return (
      <div className="artifact-md" style={{ maxHeight: 460, overflowY: "auto" }}>
        <ReactMarkdown remarkPlugins={[remarkGfm]}>{content}</ReactMarkdown>
      </div>
    );
  }

  // Agents y workflows: YAML resaltado.
  return (
    <div>
      <div className="reg-code-block">
        <SyntaxHighlighter
          language="yaml"
          style={githubGist}
          customStyle={{
            background: "var(--bg2)",
            border: "1px solid var(--stroke)",
            borderRadius: "var(--r)",
            fontSize: 12,
            lineHeight: 1.6,
            margin: 0,
            padding: "12px 14px",
            overflowX: "auto",
            maxHeight: 320,
            fontFamily: "var(--mono)",
          }}
          wrapLongLines={false}
        >
          {content}
        </SyntaxHighlighter>
      </div>

      {/* Persona del agente: markdown bajo el YAML */}
      {kind === "agents" && persona && (
        <div style={{ marginTop: 16 }}>
          <div
            style={{
              fontSize: 10,
              fontWeight: 700,
              textTransform: "uppercase",
              letterSpacing: "0.08em",
              color: "var(--ink4)",
              marginBottom: 8,
            }}
          >
            Persona
          </div>
          <div
            className="artifact-md"
            style={{
              background: "var(--bg2)",
              border: "1px solid var(--stroke)",
              borderRadius: "var(--r)",
              maxHeight: 260,
              overflowY: "auto",
              padding: "12px 14px",
            }}
          >
            <ReactMarkdown remarkPlugins={[remarkGfm]}>{persona}</ReactMarkdown>
          </div>
        </div>
      )}
    </div>
  );
}

// ─────────────────────────────────────────────────────────────────────────────
// Modal editor de ítem — con toggle vista/edición
// ─────────────────────────────────────────────────────────────────────────────

function ItemEditorModal({
  kind,
  id: initialId,
  isNew,
  onClose,
}: {
  kind: RegistryKind;
  id: string;
  isNew: boolean;
  onClose: () => void;
}) {
  const { data: remoteContent, isLoading } = useRegistryItem(kind, isNew ? null : initialId);
  const save = useSaveRegistryItem(kind);

  const [idInput, setIdInput] = useState(initialId);
  const [body, setBody] = useState("");
  const [saveError, setSaveError] = useState<string | null>(null);
  // Default: rendered view; switch to edit only on demand.
  const [editMode, setEditMode] = useState(isNew);

  // Cuando llega el contenido del servidor, lo ponemos en el editor.
  useEffect(() => {
    if (remoteContent !== undefined) setBody(remoteContent);
  }, [remoteContent]);

  // Cierra con Escape.
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") onClose();
    }
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [onClose]);

  async function handleSave() {
    setSaveError(null);
    const targetId = idInput.trim();
    if (!targetId) {
      setSaveError("El ID no puede estar vacío.");
      return;
    }
    try {
      await save.mutateAsync({ id: targetId, body });
      setEditMode(false); // vuelve a la vista renderizada tras guardar
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : String(err);
      setSaveError(msg);
    }
  }

  const kindLabel = kind === "agents" ? "agente" : kind === "skills" ? "skill" : "workflow";

  return (
    <div className="modal on" role="dialog" aria-label={`${isNew ? "Nuevo" : "Editar"} ${kindLabel}`}>
      <div className="mh">
        <h3>{isNew ? `Nuevo ${kindLabel}` : `${kindLabel} · ${initialId}`}</h3>
        <div style={{ display: "flex", gap: 8, alignItems: "center" }}>
          {/* Toggle vista/edición — solo en ítems existentes */}
          {!isNew && (
            <button
              className={`btn ghost sm${editMode ? " on" : ""}`}
              onClick={() => setEditMode((v) => !v)}
            >
              {editMode ? "Ver" : "Editar"}
            </button>
          )}
          <button className="x" onClick={onClose} aria-label="Cerrar">
            ✕
          </button>
        </div>
      </div>
      <div className="mb">
        {isNew && (
          <div className="field" style={{ marginBottom: 10 }}>
            <label>ID</label>
            <input
              className="inp mono"
              value={idInput}
              onChange={(e) => setIdInput(e.target.value)}
              placeholder={`nombre-del-${kindLabel}`}
            />
          </div>
        )}

        <div className="field">
          <label>
            {kind === "skills" ? "Contenido (markdown)" : "Definición (YAML)"}
          </label>
          {isLoading ? (
            <div style={{ padding: 12, color: "var(--ink4)" }}>Cargando…</div>
          ) : editMode ? (
            <textarea
              className="inp mono"
              style={{ minHeight: 280, resize: "vertical", fontFamily: "monospace", fontSize: 12 }}
              value={body}
              onChange={(e) => setBody(e.target.value)}
            />
          ) : (
            <RenderedView kind={kind} id={initialId} content={body} />
          )}
        </div>

        {saveError && (
          <div
            style={{
              padding: "8px 12px",
              background: "var(--err-soft, #fff0f0)",
              border: "1px solid var(--err-line, #f5c5c5)",
              borderRadius: 4,
              fontSize: 12,
              color: "var(--err, #c00)",
              marginBottom: 10,
              whiteSpace: "pre-wrap",
            }}
          >
            {saveError}
          </div>
        )}

        {editMode && (
          <div style={{ display: "flex", gap: 10, marginTop: 8 }}>
            <button className="btn ghost" style={{ flex: 1 }} onClick={onClose}>
              Cancelar
            </button>
            <button
              className="btn primary"
              style={{ flex: 1 }}
              onClick={handleSave}
              disabled={save.isPending || isLoading}
            >
              {save.isPending ? "Guardando…" : "Validar y guardar"}
            </button>
          </div>
        )}
        {editMode && (
          <p style={{ fontSize: 12, color: "var(--ink4)", marginTop: 12 }}>
            El servidor valida el schema; si el cuerpo es inválido verás el error arriba.
            Los cambios son efectivos en el próximo run.
          </p>
        )}
      </div>
    </div>
  );
}
