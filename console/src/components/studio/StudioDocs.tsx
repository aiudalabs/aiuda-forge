"use client";

// StudioDocs (U1: Studio = Confluence) — renders a project's specs straight from
// its repo docs/ tree, the persistent source of truth that outlives an ephemeral
// design run. Left: a page tree; right: the rendered markdown. Always available
// for the active project, so "what we're building" never disappears.

import { useEffect, useMemo, useState } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { Light as SyntaxHighlighter } from "react-syntax-highlighter";
import yaml from "react-syntax-highlighter/dist/esm/languages/hljs/yaml";
import { githubGist } from "react-syntax-highlighter/dist/esm/styles/hljs";
import { useActiveProject } from "@/lib/activeProject";
import { useProjectDocs, useProjectDoc, useDocHistory, useDesignRuns, useRerunStep, useCreateIterationRun, useActiveDesignRun } from "@/lib/hooks";
import { useT } from "@/lib/i18n";

SyntaxHighlighter.registerLanguage("yaml", yaml);

// Friendly titles + a logical reading order for the known design artifacts. Unknown
// files fall back to their filename and sort after the known ones. titleKey is an
// i18n key; null means use the raw filename.
const DOC_META: Record<string, { titleKey: string | null; icon: string; order: number }> = {
  "BRIEF.md": { titleKey: "studio.docs.title.brief", icon: "✦", order: 1 },
  "CONSTITUTION.md": { titleKey: "studio.docs.title.constitution", icon: "⬡", order: 1.5 },
  "PRD.md": { titleKey: "studio.docs.title.prd", icon: "▤", order: 2 },
  "DATA_MODEL.md": { titleKey: "studio.docs.title.dataModel", icon: "▦", order: 2.5 },
  "ARCHITECTURE.md": { titleKey: "studio.docs.title.architecture", icon: "◫", order: 3 },
  "UI_SCREENS.md": { titleKey: "studio.docs.title.uiScreens", icon: "▢", order: 4 },
  "DESIGN_SYSTEM.md": { titleKey: "studio.docs.title.designSystem", icon: "◈", order: 5 },
  "backlog.yaml": { titleKey: "studio.docs.title.backlog", icon: "☰", order: 6 },
  "SESSION.md": { titleKey: "studio.docs.title.session", icon: "◷", order: 7 },
};

function meta(name: string, path?: string) {
  // HTML files are mockup surfaces — show them with the mockup icon regardless
  // of their filename (index.html, passenger-app.html, driver-app.html, …).
  if (path && isHtml(path)) return { titleKey: null, icon: "▨", order: 5.5 };
  return DOC_META[name] ?? { titleKey: null, icon: "·", order: 99 };
}

// Resolve a doc's display title: translated for known files, clean filename otherwise.
function metaTitle(name: string, path: string, t: (k: string) => string): string {
  const m = meta(name, path);
  if (m.titleKey) return t(m.titleKey);
  // For HTML mockup files: strip extension for a cleaner label ("passenger-app").
  if (isHtml(path)) return name.replace(/\.html?$/i, "");
  return name;
}

function isMarkdown(path: string) {
  return path.toLowerCase().endsWith(".md");
}

// Which design phase (step) produces each doc — so the refine on a doc re-runs the
// right phase. Html mockups → the mockups phase.
const DOC_STEP: Record<string, string> = {
  "BRIEF.md": "discovery",
  "CONSTITUTION.md": "constitution",
  "PRD.md": "prd",
  "DATA_MODEL.md": "data_model",
  "ARCHITECTURE.md": "architecture",
  "UI_SCREENS.md": "ui",
  "backlog.yaml": "backlog",
};
function stepForDoc(name: string, path: string): string | null {
  if (isHtml(path)) return "mockups";
  return DOC_STEP[name] ?? null;
}

function isHtml(path: string) {
  const p = path.toLowerCase();
  return p.endsWith(".html") || p.endsWith(".htm");
}

// MockupFrame renders an HTML mockup inline (sandboxed iframe) with a button to
// open it full-screen in its own tab — "como una UI funcional".
function MockupFrame({ content, t }: { content: string; t: (k: string) => string }) {
  function openInTab() {
    const blob = new Blob([content], { type: "text/html" });
    const url = URL.createObjectURL(blob);
    window.open(url, "_blank", "noopener");
    // Revoke shortly after the tab has had time to load it.
    setTimeout(() => URL.revokeObjectURL(url), 30_000);
  }
  return (
    <div className="docs-mockup">
      <div className="docs-mockup-bar">
        <span className="docs-mockup-tag">{t("studio.docs.mockup.tag")}</span>
        <button className="btn ghost sm" onClick={openInTab}>
          {t("studio.docs.mockup.openFullscreen")}
        </button>
      </div>
      <iframe
        className="docs-mockup-frame"
        srcDoc={content}
        sandbox="allow-scripts allow-popups allow-forms"
        title="Mockup"
      />
    </div>
  );
}

export function StudioDocs({ onOpenPipeline }: { onOpenPipeline?: () => void } = {}) {
  const t = useT();
  const { project, isLoading: projLoading } = useActiveProject();
  const projectId = project?.id ?? null;
  const { data: docs, isLoading, isError } = useProjectDocs(projectId);

  // Subdirectory entries are expanded by the API (one level), so all entries are
  // files. Sort by the logical reading order defined in DOC_META.
  const files = useMemo(() => {
    const list = (docs ?? []).filter((d) => d.type === "file");
    return [...list].sort((a, b) => meta(a.name, a.path).order - meta(b.name, b.path).order);
  }, [docs]);

  // Group the html mockups under one collapsible "Mockups" node (order 5.5, between
  // UI and Backlog) instead of listing each html file loose in the tree.
  const treeNodes = useMemo(() => {
    const htmls = files.filter((f) => isHtml(f.path));
    type Node = { order: number; doc?: (typeof files)[number]; mockups?: typeof files };
    const nodes: Node[] = files
      .filter((f) => !isHtml(f.path))
      .map((f) => ({ order: meta(f.name, f.path).order, doc: f }));
    if (htmls.length) nodes.push({ order: 5.5, mockups: htmls });
    return nodes.sort((a, b) => a.order - b.order);
  }, [files]);

  // Default selection: PRD if present, else the first file.
  const [selected, setSelected] = useState<string | null>(null);
  const active = selected ?? files.find((f) => f.name === "PRD.md")?.path ?? files[0]?.path ?? null;

  // Version time-travel: `ver` is a commit sha (null = current/latest). History is
  // fetched only for markdown/yaml docs, not html mockups.
  const [ver, setVer] = useState<string | null>(null);
  useEffect(() => setVer(null), [active]);
  const [mockOpen, setMockOpen] = useState(false);
  useEffect(() => {
    if (active && isHtml(active)) setMockOpen(true);
  }, [active]);

  // Refine (C3): resolve the project's latest design/iterate run — that's the run a
  // per-doc refine re-runs (D1). The doc maps to its phase; the feedback is injected.
  const { data: designRuns } = useDesignRuns();
  const activeRun = useActiveDesignRun(projectId);
  const awaitingCount = (activeRun?.phases ?? []).filter((p) => p.gateStatus === "AWAITING").length;
  const rerun = useRerunStep();
  const [refine, setRefine] = useState("");
  const activeFile = files.find((fl) => fl.path === active) ?? null;
  const activeStep = activeFile ? stepForDoc(activeFile.name, activeFile.path) : null;
  // The refine targets the LATEST run of this project that actually CONTAINS the doc's
  // phase — not just the newest run (e.g. a "mockups-only" run has no data_model step).
  const refineRun = useMemo(() => {
    if (!activeStep) return null;
    return (
      (designRuns ?? [])
        .filter((r) => r.project_id === projectId && (r.phases ?? []).some((p) => p.stepId === activeStep))
        .sort((a, b) => b.created_at - a.created_at)[0] ?? null
    );
  }, [designRuns, projectId, activeStep]);
  function doRefine() {
    if (!refine.trim() || !refineRun || !activeStep) return;
    rerun.mutate([refineRun.id, activeStep, refine.trim()], { onSuccess: () => setRefine("") });
  }

  // Change Request (C4): a PROJECT-level action (may touch several docs) — lives in
  // the header, not inside a phase. Plans a delta backlog on top of the shipped product.
  const createCR = useCreateIterationRun();
  const [crOpen, setCrOpen] = useState(false);
  const [crText, setCrText] = useState("");
  function submitCR() {
    if (!crText.trim() || !project?.repo || !projectId) return;
    createCR.mutate(
      { project_id: projectId, repo: project.repo, changeRequest: crText.trim() },
      {
        onSuccess: () => {
          setCrOpen(false);
          setCrText("");
        },
      },
    );
  }
  const { data: history } = useDocHistory(projectId, active && !isHtml(active) ? active : null);

  const { data: content, isLoading: docLoading } = useProjectDoc(projectId, active, ver ?? "design");

  if (projLoading) {
    return (
      <div className="wrap">
        <div className="placeholder">
          <span className="spin" /> {t("studio.docs.loadingProject")}
        </div>
      </div>
    );
  }

  if (!project) {
    return (
      <div className="wrap">
        <div className="placeholder">{t("studio.docs.selectProject")}</div>
      </div>
    );
  }

  return (
    <div className="wrap">
      <div className="eyebrow acc">{t("studio.docs.eyebrow")}</div>
      <h2 className="docs-h1" style={{ marginBottom: 8 }}>
        {project.name}
        {project.repo && (
          <a className="docs-repo" href={project.repo} target="_blank" rel="noreferrer">
            {t("studio.docs.repo")}
          </a>
        )}
      </h2>
      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: 10,
          padding: "0 0 14px",
          marginBottom: 20,
          borderBottom: "1px solid var(--stroke)",
        }}
      >
        <span
          style={{
            fontFamily: "var(--mono)",
            fontSize: 11.5,
            color: "var(--ink4)",
            display: "inline-flex",
            alignItems: "center",
            gap: 6,
          }}
        >
          <span style={{ width: 6, height: 6, borderRadius: "50%", background: "var(--emerald)" }} />
          {t("studio.docs.onBranch")}
        </span>
        {activeRun && onOpenPipeline && (
          <button
            className="btn ghost sm"
            onClick={onOpenPipeline}
            style={{ display: "inline-flex", alignItems: "center", gap: 7 }}
          >
            <span style={{ width: 7, height: 7, borderRadius: "50%", background: awaitingCount > 0 ? "var(--accent)" : "var(--navy)" }} />
            {awaitingCount > 0 ? t("studio.docs.awaitingN", { n: awaitingCount }) : t("studio.docs.runActive")} · {t("studio.docs.openPipeline")}
          </button>
        )}
        <div style={{ flex: 1 }} />
        {project.repo && (
          <button className="btn primary sm" onClick={() => setCrOpen(true)}>
            {t("studio.view.newIteration")}
          </button>
        )}
      </div>

      <div className="docs-layout">
        {/* Page tree */}
        <aside className="docs-tree">
          <div className="docs-tree-head">{t("studio.docs.pages")}</div>
          {isLoading ? (
            <div className="docs-tree-empty">
              <span className="spin" /> {t("studio.docs.loading")}
            </div>
          ) : files.length === 0 ? (
            <div className="docs-tree-empty">
              {t("studio.docs.empty.line1")}
              <br />
              {t("studio.docs.empty.line2")}
            </div>
          ) : (
            <nav>
              {treeNodes.map((n) =>
                n.doc ? (
                  <button
                    key={n.doc.path}
                    className={`docs-tree-item${active === n.doc.path ? " on" : ""}`}
                    onClick={() => setSelected(n.doc!.path)}
                  >
                    <span className="docs-tree-ic">{meta(n.doc.name, n.doc.path).icon}</span>
                    <span className="docs-tree-label">{metaTitle(n.doc.name, n.doc.path, t)}</span>
                  </button>
                ) : (
                  <div key="mockups-group">
                    <button className="docs-tree-item" onClick={() => setMockOpen((o) => !o)}>
                      <span className="docs-tree-ic">{mockOpen ? "▾" : "▸"}</span>
                      <span className="docs-tree-label">{t("studio.docs.title.mockups")}</span>
                      <span className="docs-tree-badge">{n.mockups!.length}</span>
                    </button>
                    {mockOpen &&
                      n.mockups!.map((h) => (
                        <button
                          key={h.path}
                          className={`docs-tree-item${active === h.path ? " on" : ""}`}
                          style={{ paddingLeft: 32 }}
                          onClick={() => setSelected(h.path)}
                        >
                          <span className="docs-tree-ic">▨</span>
                          <span className="docs-tree-label" style={{ fontSize: 12.5 }}>
                            {h.name.replace(/\.html?$/i, "")}
                          </span>
                        </button>
                      ))}
                  </div>
                ),
              )}
            </nav>
          )}
        </aside>

        {/* Reader */}
        <section className="docs-reader">
          {active && !isHtml(active) && history && history.length > 1 && (
            <div className="chips" style={{ marginBottom: 12 }}>
              {history.map((h, i) => {
                const vnum = history.length - i;
                const isLatest = i === 0;
                const on = isLatest ? ver === null || ver === h.sha : ver === h.sha;
                return (
                  <button
                    key={h.sha}
                    className={`chip${on ? " on" : ""}`}
                    style={{ fontFamily: "var(--mono)", fontSize: 11.5, padding: "4px 10px" }}
                    title={`${h.message} · ${new Date(h.date).toLocaleString()}`}
                    onClick={() => setVer(isLatest ? null : h.sha)}
                  >
                    v{vnum}
                  </button>
                );
              })}
              {ver !== null && (
                <span style={{ color: "var(--ink4)", fontSize: 12, alignSelf: "center" }}>
                  {t("studio.docs.viewingOld")}
                </span>
              )}
            </div>
          )}
          {isError ? (
            <div className="placeholder err">{t("studio.docs.readError")}</div>
          ) : !active ? (
            <div className="placeholder">{t("studio.docs.selectPage")}</div>
          ) : docLoading ? (
            <div className="placeholder">
              <span className="spin" /> {t("studio.docs.loadingDoc")}
            </div>
          ) : isHtml(active) ? (
            <MockupFrame content={content ?? ""} t={t} />
          ) : isMarkdown(active) ? (
            <article className="docs-md artifact-md">
              <ReactMarkdown remarkPlugins={[remarkGfm]}>{content ?? ""}</ReactMarkdown>
            </article>
          ) : (
            <SyntaxHighlighter
              language="yaml"
              style={githubGist}
              customStyle={{
                background: "var(--bg2)",
                border: "1px solid var(--stroke)",
                borderRadius: "var(--r)",
                fontSize: 12.5,
                lineHeight: 1.6,
                margin: 0,
                padding: "16px 18px",
                fontFamily: "var(--mono)",
              }}
              wrapLongLines={false}
            >
              {content ?? ""}
            </SyntaxHighlighter>
          )}
          {active && activeStep && refineRun && ver === null && (
            <div style={{ marginTop: 14, borderTop: "1px solid var(--stroke)", paddingTop: 14 }}>
              <div style={{ fontFamily: "var(--display)", fontWeight: 700, fontSize: 13, marginBottom: 8 }}>
                {t("studio.docs.refine.label")}
              </div>
              <div style={{ display: "flex", gap: 8 }}>
                <input
                  style={{ flex: 1, background: "#fff", border: "1px solid var(--stroke-strong)", borderRadius: 10, padding: "10px 12px", font: "inherit", color: "var(--ink)" }}
                  placeholder={t("studio.docs.refine.placeholder")}
                  value={refine}
                  onChange={(e) => setRefine(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === "Enter") doRefine();
                  }}
                />
                <button className="btn primary sm" disabled={!refine.trim() || rerun.isPending} onClick={doRefine}>
                  {rerun.isPending ? t("studio.docs.refine.sending") : t("studio.docs.refine.send")}
                </button>
              </div>
            </div>
          )}
        </section>
      </div>
      {crOpen && (
        <div
          onClick={() => setCrOpen(false)}
          style={{ position: "fixed", inset: 0, background: "rgba(13,13,15,.42)", display: "grid", placeItems: "center", padding: 20, zIndex: 50 }}
        >
          <div className="card" onClick={(e) => e.stopPropagation()} style={{ width: "min(560px,94vw)" }}>
            <h3 style={{ fontSize: 18 }}>{t("studio.iteration.title")}</h3>
            <p className="role">{t("studio.iteration.hint")}</p>
            <textarea
              value={crText}
              onChange={(e) => setCrText(e.target.value)}
              placeholder={t("studio.iteration.placeholder")}
              style={{ width: "100%", minHeight: 90, background: "var(--bg)", border: "1px solid var(--stroke-strong)", borderRadius: 10, padding: 11, font: "inherit", color: "var(--ink)", resize: "vertical", marginTop: 6 }}
            />
            <div style={{ display: "flex", justifyContent: "flex-end", gap: 8, marginTop: 14 }}>
              <button className="btn ghost" onClick={() => setCrOpen(false)}>
                {t("studio.iteration.cancel")}
              </button>
              <button className="btn primary" disabled={!crText.trim() || createCR.isPending} onClick={submitCR}>
                {createCR.isPending ? t("studio.iteration.launching") : t("studio.iteration.launch")}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
