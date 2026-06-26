"use client";

// StudioDocs (U1: Studio = Confluence) — renders a project's specs straight from
// its repo docs/ tree, the persistent source of truth that outlives an ephemeral
// design run. Left: a page tree; right: the rendered markdown. Always available
// for the active project, so "what we're building" never disappears.

import { useMemo, useState } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import type { DocEntry } from "@/lib/types";
import { useActiveProject } from "@/lib/activeProject";
import { useProjectDocs, useProjectDoc } from "@/lib/hooks";

// Friendly titles + a logical reading order for the known design artifacts. Unknown
// files fall back to their filename and sort after the known ones.
const DOC_META: Record<string, { title: string; icon: string; order: number }> = {
  "BRIEF.md": { title: "Brief", icon: "✦", order: 1 },
  "PRD.md": { title: "PRD · Requisitos", icon: "▤", order: 2 },
  "ARCHITECTURE.md": { title: "Arquitectura", icon: "◫", order: 3 },
  "UI_SCREENS.md": { title: "Pantallas UI", icon: "▢", order: 4 },
  "backlog.yaml": { title: "Backlog", icon: "☰", order: 5 },
  "mockups": { title: "Mockups", icon: "▨", order: 6 },
  "SESSION.md": { title: "Sesión de diseño", icon: "◷", order: 7 },
};

function meta(name: string) {
  return DOC_META[name] ?? { title: name, icon: "·", order: 99 };
}

function isMarkdown(path: string) {
  return path.toLowerCase().endsWith(".md");
}

export function StudioDocs() {
  const { project, isLoading: projLoading } = useActiveProject();
  const projectId = project?.id ?? null;
  const { data: docs, isLoading, isError } = useProjectDocs(projectId);

  // Files only (dirs like mockups need their own viewer — handled as a note for now),
  // sorted by the logical reading order.
  const files = useMemo(() => {
    const list = (docs ?? []).filter((d) => d.type === "file");
    return [...list].sort((a, b) => meta(a.name).order - meta(b.name).order);
  }, [docs]);

  const dirs = useMemo(() => (docs ?? []).filter((d) => d.type === "dir"), [docs]);

  // Default selection: PRD if present, else the first file.
  const [selected, setSelected] = useState<string | null>(null);
  const active = selected ?? files.find((f) => f.name === "PRD.md")?.path ?? files[0]?.path ?? null;

  const { data: content, isLoading: docLoading } = useProjectDoc(projectId, active);

  if (projLoading) {
    return (
      <div className="wrap">
        <div className="placeholder">
          <span className="spin" /> cargando proyecto…
        </div>
      </div>
    );
  }

  if (!project) {
    return (
      <div className="wrap">
        <div className="placeholder">Selecciona un proyecto para ver su especificación.</div>
      </div>
    );
  }

  return (
    <div className="wrap">
      <div className="eyebrow acc">Especificación del producto</div>
      <h2 className="docs-h1">
        {project.name}
        {project.repo && (
          <a className="docs-repo" href={project.repo} target="_blank" rel="noreferrer">
            ↗ repo
          </a>
        )}
      </h2>

      <div className="docs-layout">
        {/* Page tree */}
        <aside className="docs-tree">
          <div className="docs-tree-head">Páginas</div>
          {isLoading ? (
            <div className="docs-tree-empty">
              <span className="spin" /> cargando…
            </div>
          ) : files.length === 0 ? (
            <div className="docs-tree-empty">
              Aún no hay specs en el repo.
              <br />
              Se generan en la fase de diseño.
            </div>
          ) : (
            <nav>
              {files.map((d) => (
                <button
                  key={d.path}
                  className={`docs-tree-item${active === d.path ? " on" : ""}`}
                  onClick={() => setSelected(d.path)}
                >
                  <span className="docs-tree-ic">{meta(d.name).icon}</span>
                  <span className="docs-tree-label">{meta(d.name).title}</span>
                </button>
              ))}
              {dirs.map((d) => (
                <div key={d.path} className="docs-tree-item dir" title="Carpeta — visor próximamente">
                  <span className="docs-tree-ic">{meta(d.name).icon}</span>
                  <span className="docs-tree-label">{meta(d.name).title}</span>
                  <span className="docs-tree-badge">carpeta</span>
                </div>
              ))}
            </nav>
          )}
        </aside>

        {/* Reader */}
        <section className="docs-reader">
          {isError ? (
            <div className="placeholder err">No se pudieron leer los docs del repo.</div>
          ) : !active ? (
            <div className="placeholder">Selecciona una página.</div>
          ) : docLoading ? (
            <div className="placeholder">
              <span className="spin" /> cargando documento…
            </div>
          ) : isMarkdown(active) ? (
            <article className="docs-md artifact-md">
              <ReactMarkdown remarkPlugins={[remarkGfm]}>{content ?? ""}</ReactMarkdown>
            </article>
          ) : (
            <pre className="docs-code">{content ?? ""}</pre>
          )}
        </section>
      </div>
    </div>
  );
}
