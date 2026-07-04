"use client";

// StudioDocs (U1: Studio = Confluence) — renders a project's specs straight from
// its repo docs/ tree, the persistent source of truth that outlives an ephemeral
// design run. Left: a page tree; right: the rendered markdown. Always available
// for the active project, so "what we're building" never disappears.

import { useMemo, useState } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { Light as SyntaxHighlighter } from "react-syntax-highlighter";
import yaml from "react-syntax-highlighter/dist/esm/languages/hljs/yaml";
import { githubGist } from "react-syntax-highlighter/dist/esm/styles/hljs";
import { useActiveProject } from "@/lib/activeProject";
import { useProjectDocs, useProjectDoc } from "@/lib/hooks";
import { useT } from "@/lib/i18n";

SyntaxHighlighter.registerLanguage("yaml", yaml);

// Friendly titles + a logical reading order for the known design artifacts. Unknown
// files fall back to their filename and sort after the known ones. titleKey is an
// i18n key; null means use the raw filename.
const DOC_META: Record<string, { titleKey: string | null; icon: string; order: number }> = {
  "BRIEF.md": { titleKey: "studio.docs.title.brief", icon: "✦", order: 1 },
  "PRD.md": { titleKey: "studio.docs.title.prd", icon: "▤", order: 2 },
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

export function StudioDocs() {
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

  // Default selection: PRD if present, else the first file.
  const [selected, setSelected] = useState<string | null>(null);
  const active = selected ?? files.find((f) => f.name === "PRD.md")?.path ?? files[0]?.path ?? null;

  const { data: content, isLoading: docLoading } = useProjectDoc(projectId, active);

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
      <h2 className="docs-h1">
        {project.name}
        {project.repo && (
          <a className="docs-repo" href={project.repo} target="_blank" rel="noreferrer">
            {t("studio.docs.repo")}
          </a>
        )}
      </h2>

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
              {files.map((d) => (
                <button
                  key={d.path}
                  className={`docs-tree-item${active === d.path ? " on" : ""}`}
                  onClick={() => setSelected(d.path)}
                >
                  <span className="docs-tree-ic">{meta(d.name, d.path).icon}</span>
                  <span className="docs-tree-label">{metaTitle(d.name, d.path, t)}</span>
                  {isHtml(d.path) && <span className="docs-tree-badge">html</span>}
                </button>
              ))}
            </nav>
          )}
        </aside>

        {/* Reader */}
        <section className="docs-reader">
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
        </section>
      </div>
    </div>
  );
}
