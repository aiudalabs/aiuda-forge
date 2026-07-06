"use client";

// StudioEntry (U5) — the conversational "what do you want to build?" entry shown
// when there is no project yet. It's the obvious, can't-miss starting point: you
// describe the idea, name the repo, and one button creates the project AND kicks
// off the design run — then the app drops you straight into the design flow.

import { useState } from "react";
import { ApiError } from "@/lib/api";
import { useActiveProject } from "@/lib/activeProject";
import { useCreateDesignRun, useCreateProject } from "@/lib/hooks";
import { useT } from "@/lib/i18n";

// Slugify an idea/name into a valid repo name (lowercase, dashes).
function slugify(s: string): string {
  return s
    .toLowerCase()
    .normalize("NFD")
    .replace(/[̀-ͯ]/g, "")
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 40);
}

const EXAMPLES = [
  "Un marketplace que conecta clientes con proveedores de servicios locales verificados.",
  "Una app de tareas colaborativa para equipos pequeños, con tableros y recordatorios.",
  "Un CRM simple para freelancers: contactos, propuestas y seguimiento de pagos.",
];

export function StudioEntry({ onLaunched }: { onLaunched?: () => void }) {
  const [idea, setIdea] = useState("");
  const [name, setName] = useState("");
  const [touchedName, setTouchedName] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const createProject = useCreateProject();
  const createDesignRun = useCreateDesignRun();
  const { setActiveId } = useActiveProject();
  const t = useT();

  // Auto-suggest the repo name from the idea until the user edits it themselves.
  const repoName = touchedName ? name : slugify(name || idea.split(/[.\n]/)[0] || "");

  async function launch(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    const trimmedIdea = idea.trim();
    const finalName = slugify(repoName);
    if (!trimmedIdea) {
      setError(t("studio.entry.validation.idea"));
      return;
    }
    if (!finalName) {
      setError(t("studio.entry.validation.name"));
      return;
    }
    setBusy(true);
    try {
      const proj = await createProject.mutateAsync({ name: finalName, description: trimmedIdea });
      setActiveId(proj.id);
      await createDesignRun.mutateAsync({
        project_id: proj.id,
        repo: proj.repo,
        instructions: trimmedIdea,
      });
      onLaunched?.();
    } catch (err: unknown) {
      if (err instanceof ApiError && err.status === 409) {
        setError(t("studio.entry.repoExists", { name: finalName }));
      } else {
        setError(err instanceof Error ? err.message : String(err));
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="entry">
      <div className="entry-inner">
        <div className="entry-brand">
          <span className="logo">
            <span className="b">&lt;</span>
            <span className="word">
              <span className="ai">ai</span>
              <span className="uda">uda</span>
            </span>
            <span className="b">/&gt;</span>
          </span>
        </div>
        <h1 className="entry-h1">{t("studio.entry.h1")}</h1>
        <p className="entry-sub">{t("studio.entry.sub")}</p>

        <form className="entry-form" onSubmit={launch}>
          <textarea
            className="entry-idea"
            value={idea}
            onChange={(e) => setIdea(e.target.value)}
            placeholder={t("studio.entry.ideaPlaceholder")}
            autoFocus
            rows={3}
          />

          <div className="entry-examples">
            {EXAMPLES.map((ex, i) => (
              <button key={i} type="button" className="entry-chip" onClick={() => setIdea(ex)}>
                {ex.split(",")[0].slice(0, 42)}…
              </button>
            ))}
          </div>

          <div className="entry-row">
            <div className="entry-name">
              <span className="entry-name-pre">{t("studio.entry.repoPre")}</span>
              <input
                className="entry-name-inp"
                value={repoName}
                onChange={(e) => {
                  setTouchedName(true);
                  setName(e.target.value);
                }}
                placeholder={t("studio.entry.namePlaceholder")}
                spellCheck={false}
              />
            </div>
            <button type="submit" className="entry-go" disabled={busy}>
              {busy ? t("studio.entry.launching") : t("studio.entry.start")}
            </button>
          </div>

          {error && <div className="entry-err">{error}</div>}
        </form>
      </div>
    </div>
  );
}
