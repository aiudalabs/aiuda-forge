"use client";

// DocsView — the product documentation INSIDE the site. On-philosophy: the content
// itself lives as plain .md files (public/docs/*.md, editable without a rebuild),
// rendered here as a Confluence-style page tree + markdown reader. These are docs
// about the TOOL (aiuda-forge), distinct from a project's specs (which live in Studio).

import { useEffect, useState } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";

interface DocPage {
  file: string;
  title: string;
  icon: string;
}

// The page set (also the reading order). Content is authored as the matching .md.
const PAGES: DocPage[] = [
  { file: "01-filosofia.md", title: "Filosofía", icon: "✦" },
  { file: "02-arquitectura.md", title: "Arquitectura", icon: "◫" },
  { file: "03-kernel.md", title: "Kernel de workflows", icon: "⚙" },
  { file: "04-orquestador.md", title: "Orquestador", icon: "⟳" },
  { file: "05-diseno-y-fabrica.md", title: "Diseño y Fábrica", icon: "▤" },
  { file: "06-seguridad-resiliencia.md", title: "Seguridad y Resiliencia", icon: "🛡" },
];

export function DocsView() {
  const [active, setActive] = useState<string>(PAGES[0].file);
  const [content, setContent] = useState<string>("");
  const [state, setState] = useState<"loading" | "ok" | "missing">("loading");

  useEffect(() => {
    let alive = true;
    setState("loading");
    fetch(`/docs/${active}`, { cache: "no-store" })
      .then((r) => (r.ok ? r.text() : Promise.reject(new Error(String(r.status)))))
      .then((t) => {
        if (!alive) return;
        // A 404 in dev returns the index.html shell; guard against that.
        if (t.trimStart().startsWith("<!DOCTYPE") || t.trimStart().startsWith("<html")) {
          setState("missing");
          return;
        }
        setContent(t);
        setState("ok");
      })
      .catch(() => alive && setState("missing"));
    return () => {
      alive = false;
    };
  }, [active]);

  return (
    <div className="wrap">
      <div className="eyebrow acc">Documentación de la herramienta</div>
      <h2 className="docs-h1">aiuda-forge</h2>

      <div className="docs-layout">
        <aside className="docs-tree">
          <div className="docs-tree-head">Guía</div>
          <nav>
            {PAGES.map((p) => (
              <button
                key={p.file}
                className={`docs-tree-item${active === p.file ? " on" : ""}`}
                onClick={() => setActive(p.file)}
              >
                <span className="docs-tree-ic">{p.icon}</span>
                <span className="docs-tree-label">{p.title}</span>
              </button>
            ))}
          </nav>
        </aside>

        <section className="docs-reader">
          {state === "loading" ? (
            <div className="placeholder">
              <span className="spin" /> cargando…
            </div>
          ) : state === "missing" ? (
            <div className="placeholder">
              Esta página aún se está generando. Vuelve en un momento.
            </div>
          ) : (
            <article className="docs-md artifact-md">
              <ReactMarkdown remarkPlugins={[remarkGfm]}>{content}</ReactMarkdown>
            </article>
          )}
        </section>
      </div>
    </div>
  );
}
