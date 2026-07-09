"use client";

// REGISTRY — agentes / skills / workflows, no-code (doc 16 §2.5).
// Cableado contra GET/PUT/DELETE /registry/{agents,skills,workflows}.
// Modo mock: cae a datos de ejemplo de lib/mock cuando la API no responde.

import { useEffect, useMemo, useState } from "react";
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
  useTemplates,
  useTemplate,
  useSaveTemplate,
  useScaffoldProject,
  useSaveRegistryItem,
} from "@/lib/hooks";
import { useT } from "@/lib/i18n";
import { useActiveProject } from "@/lib/activeProject";
import type { RegistryKind } from "@/lib/types";
import { designAgentIds } from "./designAgents";

// Register only the YAML language to keep the bundle minimal.
SyntaxHighlighter.registerLanguage("yaml", yaml);

// ─────────────────────────────────────────────────────────────────────────────
// Tipos locales
// ─────────────────────────────────────────────────────────────────────────────

type ActiveTab = Exclude<RegistryKind, "skills"> | "templates";
type ItemModal =
  | { kind: RegistryKind; id: string; isNew: false }
  | { kind: RegistryKind; id: string; isNew: true }
  | null;

// ─────────────────────────────────────────────────────────────────────────────
// Componente raíz
// ─────────────────────────────────────────────────────────────────────────────

export function RegistryView() {
  const t = useT();
  const [tab, setTab] = useState<ActiveTab>("agents");
  const [modal, setModal] = useState<ItemModal>(null);

  const isTemplates = tab === "templates";
  const { data: listData, isLoading, isError } = useRegistryList(isTemplates ? "agents" : tab);
  // Se muestran los agentes de MÉTODO (diseño + ceremonias, que el control/conductor corre
  // desde el registry) y se ocultan los lanes de EJECUCIÓN (código/review/verify), que viven
  // en .github/agents del repo scaffoldeado y correrían allá — editar su copia acá no cambia
  // nada vivo. La exclusión se DERIVA (denylist por familia de lane), no se enumera a mano:
  // ver ./designAgents.ts (por qué exclusión y no allowlist).
  const allIds = listData?.ids ?? [];
  const ids = tab === "agents" ? designAgentIds(allIds) : allIds;

  function openNew() {
    if (tab === "templates") return;
    setModal({ kind: tab, id: "", isNew: true });
  }

  function openItem(id: string) {
    if (tab === "templates") return;
    setModal({ kind: tab, id, isNew: false });
  }

  const TAB_LABELS: Record<ActiveTab, string> = {
    agents: t("registry.tab.agents"),
    workflows: t("registry.tab.workflows"),
    templates: t("registry.tab.templates"),
  } as Record<ActiveTab, string>;

  return (
    <div className="wrap">
      {/* Pestañas */}
      <div className="sectitle">
        <h2>{t("registry.title")}</h2>
        <span className="c">{t("registry.subtitle")}</span>
        <span className="sp" />
        {(["agents", "workflows", "templates"] as ActiveTab[]).map((k) => (
          <button
            key={k}
            className={`btn ghost sm${tab === k ? " on" : ""}`}
            onClick={() => setTab(k)}
          >
            {TAB_LABELS[k]}
          </button>
        ))}
        {tab !== "templates" && (
          <button className="btn ghost sm" onClick={openNew}>
            {t("registry.new")}
          </button>
        )}
      </div>

      {/* Lista */}
      {isTemplates ? (
        <TemplatesBrowser />
      ) : isLoading ? (
        <div className="placeholder">
          <div className="ph-ic">
            <span className="spin" />
          </div>
          {t("registry.loading", { kind: t(`registry.kind.${tab}`) })}
        </div>
      ) : isError ? (
        <div className="placeholder err">
          <div className="ph-ic">⚠</div>
          {t("registry.connectError")}
        </div>
      ) : ids.length === 0 ? (
        <div className="placeholder">
          <div className="ph-ic">◆</div>
          {t("registry.empty", { kind: t(`registry.kind.${tab}`) })}
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
  const t = useT();
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
    if (confirm(t("registry.confirmDelete", { kind, id }))) {
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
      {!content && <div className="role" style={{ color: "var(--ink4)", fontSize: 12 }}>{t("registry.cardLoading")}</div>}
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
  const t = useT();
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
            {t("registry.persona")}
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
  const t = useT();
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
      setSaveError(t("registry.idEmpty"));
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

  const kindLabel = t(`registry.kindLabel.${kind}`);

  return (
    <div
      className="modal on"
      role="dialog"
      aria-label={
        isNew
          ? t("registry.modal.newAria", { kind: kindLabel })
          : t("registry.modal.editAria", { kind: kindLabel })
      }
    >
      <div className="mh">
        <h3>
          {isNew
            ? t("registry.modal.newTitle", { kind: kindLabel })
            : t("registry.modal.editTitle", { kind: kindLabel, id: initialId })}
        </h3>
        <div style={{ display: "flex", gap: 8, alignItems: "center" }}>
          {/* Toggle vista/edición — solo en ítems existentes */}
          {!isNew && (
            <button
              className={`btn ghost sm${editMode ? " on" : ""}`}
              onClick={() => setEditMode((v) => !v)}
            >
              {editMode ? t("registry.view") : t("registry.edit")}
            </button>
          )}
          <button className="x" onClick={onClose} aria-label={t("registry.close")}>
            ✕
          </button>
        </div>
      </div>
      <div className="mb">
        {isNew && (
          <div className="field" style={{ marginBottom: 10 }}>
            <label>{t("registry.idLabel")}</label>
            <input
              className="inp mono"
              value={idInput}
              onChange={(e) => setIdInput(e.target.value)}
              placeholder={t("registry.idPlaceholder", { kind: kindLabel })}
            />
          </div>
        )}

        <div className="field">
          <label>
            {kind === "skills" ? t("registry.bodyLabelMarkdown") : t("registry.bodyLabelYaml")}
          </label>
          {isLoading ? (
            <div style={{ padding: 12, color: "var(--ink4)" }}>{t("registry.bodyLoading")}</div>
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
              {t("registry.cancel")}
            </button>
            <button
              className="btn primary"
              style={{ flex: 1 }}
              onClick={handleSave}
              disabled={save.isPending || isLoading}
            >
              {save.isPending ? t("registry.saving") : t("registry.saveAndValidate")}
            </button>
          </div>
        )}
        {editMode && (
          <p style={{ fontSize: 12, color: "var(--ink4)", marginTop: 12 }}>
            {t("registry.saveHint")}
          </p>
        )}
      </div>
    </div>
  );
}


// ─────────────────────────────────────────────────────────────────────────────
// Templates GitHub: lo que Fluxo instala en el repo de cada proyecto nuevo para
// que los agentes (Copilot / Claude / Codex) sepan CÓMO trabajar en ese código.
// Cards con nombre humano (mismo patrón visual que Agentes/Workflows); el path
// real queda como detalle. Editar → afecta proyectos futuros; el botón
// "Aplicar al proyecto" lo lleva al repo del proyecto activo.
// ─────────────────────────────────────────────────────────────────────────────

const STACK_LABELS: Record<string, string> = {
  _common: "registry.templates.groupCommon",
  "python-fastapi-react": "registry.templates.groupPython",
  "aiuda-flutter-firebase": "registry.templates.groupFlutter",
};

/** Traduce un path de template a título + descripción humanos. */
function templateMeta(path: string, t: (k: string, v?: Record<string, string>) => string) {
  const base = path.split("/").pop() ?? path;
  const agent = base.match(/^(.+)\.agent\.md\.tmpl$/);
  if (agent) return { title: agent[1], desc: t("registry.templates.descAgent", { name: agent[1] }) };
  const instr = base.match(/^(.+)\.instructions\.md\.tmpl$/);
  if (instr) return { title: t("registry.templates.titleInstr", { area: instr[1] }), desc: t("registry.templates.descInstr", { area: instr[1] }) };
  if (base.startsWith("AGENTS.md")) return { title: "AGENTS.md", desc: t("registry.templates.descAgentsMd") };
  if (base.startsWith("copilot-setup-steps")) return { title: t("registry.templates.titleSetup"), desc: t("registry.templates.descSetup") };
  if (base.startsWith("claude-review")) return { title: t("registry.templates.titleReview"), desc: t("registry.templates.descReview") };
  if (base.startsWith("claude.yml")) return { title: t("registry.templates.titleClaude"), desc: t("registry.templates.descClaude") };
  if (base.startsWith("suite-integrity")) return { title: t("registry.templates.titleSuite"), desc: t("registry.templates.descSuite") };
  if (base.startsWith("ui-verify")) return { title: t("registry.templates.titleUiVerify"), desc: t("registry.templates.descUiVerify") };
  return { title: base.replace(/\.tmpl$/, ""), desc: "" };
}

function TemplatesBrowser() {
  const t = useT();
  const { project } = useActiveProject();
  const { data: files, isLoading } = useTemplates();
  const [group, setGroup] = useState<string>("_common");
  const [sel, setSel] = useState<string | null>(null);
  const scaffold = useScaffoldProject(project?.id ?? null);

  const groups = useMemo(() => {
    const gs = new Set<string>();
    for (const f of files ?? []) {
      if (f === "README.md") continue;
      gs.add(f.split("/")[0]);
    }
    return Array.from(gs).sort();
  }, [files]);

  const visible = (files ?? []).filter((f) => f !== "README.md" && f.split("/")[0] === group);

  function applyToProject() {
    if (!project || group === "_common") return;
    if (!window.confirm(t("registry.templates.applyConfirm", { project: project.name }))) return;
    scaffold.mutate(group, {
      onSuccess: (r) => {
        let msg = t("registry.templates.applyDone", { written: String(r.written?.length ?? 0), skipped: String(r.skipped?.length ?? 0) });
        if (r.missing_vars?.length) {
          msg += "\n\n" + t("registry.templates.applyMissing", { vars: r.missing_vars.join(", ") });
        }
        window.alert(msg);
      },
      onError: (e) => window.alert(t("registry.templates.applyError") + "\n" + (e instanceof Error ? e.message : String(e))),
    });
  }

  if (isLoading) {
    return (
      <div className="placeholder">
        <div className="ph-ic"><span className="spin" /></div>
        {t("registry.templates.loading")}
      </div>
    );
  }

  return (
    <>
      {/* Qué es esto, en cristiano */}
      <div className="shellnote" style={{ marginBottom: 14 }}>
        {t("registry.templates.intro")}
      </div>

      {/* Grupos (stack) + acción de aplicar */}
      <div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 14, flexWrap: "wrap" }}>
        {groups.map((g) => (
          <button key={g} className={`btn ghost sm${group === g ? " on" : ""}`} onClick={() => setGroup(g)}>
            {STACK_LABELS[g] ? t(STACK_LABELS[g]) : g}
          </button>
        ))}
        <span className="sp" style={{ flex: 1 }} />
        {project && group !== "_common" && (
          <button className="btn primary sm" onClick={applyToProject} disabled={scaffold.isPending}>
            {scaffold.isPending
              ? t("registry.templates.applying")
              : t("registry.templates.apply", { project: project.name })}
          </button>
        )}
      </div>

      {/* Cards — mismo patrón que Agentes/Workflows */}
      <div className="grid3">
        {visible.map((f) => {
          const meta = templateMeta(f, t);
          return (
            <div key={f} className="card click" onClick={() => setSel(f)}>
              <h3><span>{meta.title}</span></h3>
              {meta.desc && <div className="role" style={{ fontSize: 12 }}>{meta.desc}</div>}
              <div className="role mono" style={{ fontSize: 10.5, opacity: 0.55, marginTop: 6 }}>{f}</div>
            </div>
          );
        })}
      </div>

      {/* Editor modal — mismo overlay/modal del Registry */}
      <div className={`overlay ${sel ? "on" : ""}`} onClick={() => setSel(null)} />
      {sel && <TemplateEditorModal path={sel} onClose={() => setSel(null)} />}
    </>
  );
}

function TemplateEditorModal({ path, onClose }: { path: string; onClose: () => void }) {
  const t = useT();
  const { data: content, isLoading } = useTemplate(path);
  const [draft, setDraft] = useState<string | null>(null);
  const [editMode, setEditMode] = useState(false);
  const save = useSaveTemplate();
  const meta = templateMeta(path, t);
  const isMarkdown = /\.md(\.tmpl)?$/.test(path);
  const body = draft ?? content ?? "";

  return (
    <div className="modal on" role="dialog" aria-label={meta.title}>
      <div className="mh">
        <h3>{meta.title}</h3>
        <div style={{ display: "flex", gap: 8, alignItems: "center" }}>
          <button className={`btn ghost sm${editMode ? " on" : ""}`} onClick={() => setEditMode((v) => !v)}>
            {editMode ? t("registry.view") : t("registry.edit")}
          </button>
          <button className="x" onClick={onClose} aria-label={t("registry.close")}>
            ✕
          </button>
        </div>
      </div>
      <div className="mb">
        <p className="c" style={{ margin: "0 0 4px", fontSize: 12.5 }}>{meta.desc}</p>
        <div className="role mono" style={{ fontSize: 10.5, opacity: 0.55, marginBottom: 12 }}>{path}</div>
        {isLoading ? (
          <div style={{ padding: 12, color: "var(--ink4)" }}>{t("registry.bodyLoading")}</div>
        ) : editMode ? (
          <textarea
            className="inp mono"
            style={{ minHeight: 380, resize: "vertical", fontFamily: "monospace", fontSize: 12 }}
            value={body}
            onChange={(e) => setDraft(e.target.value)}
            spellCheck={false}
          />
        ) : isMarkdown ? (
          <div className="artifact-md" style={{ maxHeight: 460, overflowY: "auto" }}>
            <ReactMarkdown remarkPlugins={[remarkGfm]}>{body}</ReactMarkdown>
          </div>
        ) : (
          <div className="reg-code-block" style={{ maxHeight: 460, overflowY: "auto" }}>
            <SyntaxHighlighter
              language="yaml"
              style={githubGist}
              customStyle={{ background: "var(--bg2)", fontSize: 12, margin: 0, padding: 14 }}
            >
              {body}
            </SyntaxHighlighter>
          </div>
        )}
        <p className="c" style={{ fontSize: 11.5, margin: "10px 0 12px" }}>{t("registry.templates.editNote")}</p>
        <div style={{ display: "flex", gap: 8, justifyContent: "flex-end" }}>
          <button className="btn ghost sm" onClick={onClose}>{t("registry.templates.close")}</button>
          <button
            className="btn primary sm"
            disabled={draft === null || save.isPending}
            onClick={() => save.mutate({ path, content: draft ?? "" }, { onSuccess: onClose })}
          >
            {save.isPending ? t("registry.templates.saving") : t("registry.templates.save")}
          </button>
        </div>
      </div>
    </div>
  );
}
