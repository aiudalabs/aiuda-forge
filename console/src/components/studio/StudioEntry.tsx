"use client";

// StudioEntry (U5) — the conversational "what do you want to build?" entry shown
// when there is no project yet. It's the obvious, can't-miss starting point: you
// describe the idea, name the repo, and one button creates the project AND kicks
// off the design run — then the app drops you straight into the design flow.

import { useEffect, useState } from "react";
import { ApiError } from "@/lib/api";
import { useActiveProject } from "@/lib/activeProject";
import { useCreateDesignRun, useCreateProject, useGithubOrgs, useCapabilities } from "@/lib/hooks";
import { API_URL } from "@/lib/config";
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
  const [projectName, setProjectName] = useState("");
  const [repoInput, setRepoInput] = useState("");
  const [touchedRepo, setTouchedRepo] = useState(false);
  const [org, setOrg] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const createProject = useCreateProject();
  const createDesignRun = useCreateDesignRun();
  const { setActiveId } = useActiveProject();
  const { data: orgs = [] } = useGithubOrgs();
  const { data: caps } = useCapabilities();
  const t = useT();

  // Repo name auto-derives from the project name (slugified) until the user edits it.
  const repoName = touchedRepo ? repoInput : slugify(projectName);
  // Default the owner to the first available once the list loads.
  useEffect(() => {
    if (!org && orgs.length) setOrg(orgs[0]);
  }, [org, orgs]);

  async function launch(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    const trimmedIdea = idea.trim();
    const finalProjectName = projectName.trim();
    const finalRepo = slugify(repoName);
    if (!trimmedIdea) {
      setError(t("studio.entry.validation.idea"));
      return;
    }
    if (!finalProjectName || !finalRepo) {
      setError(t("studio.entry.validation.name"));
      return;
    }
    setBusy(true);
    try {
      const proj = await createProject.mutateAsync({ name: finalProjectName, description: trimmedIdea, org, repoName: finalRepo });
      setActiveId(proj.id);
      await createDesignRun.mutateAsync({
        project_id: proj.id,
        repo: proj.repo,
        instructions: trimmedIdea,
      });
      onLaunched?.();
    } catch (err: unknown) {
      if (err instanceof ApiError && err.status === 409) {
        setError(t("studio.entry.repoExists", { name: finalRepo }));
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

        {caps && !caps.github && (
          <a
            href={`${API_URL}/auth/github/start`}
            style={{ display: "block", textDecoration: "none", margin: "0 0 20px", padding: "16px 18px", border: "1px solid var(--accent-line)", background: "var(--accent-soft)", borderRadius: 14 }}
          >
            <div style={{ fontFamily: "var(--display)", fontWeight: 700, fontSize: 15, color: "var(--ink)" }}>
              ⚡ {t("studio.entry.connectGithub")}
            </div>
            <div style={{ fontSize: 13, color: "var(--ink3)", marginTop: 4 }}>{t("studio.entry.connectGithubSub")}</div>
            <span className="btn primary sm" style={{ marginTop: 12, display: "inline-block" }}>
              {t("studio.entry.connectGithubCta")}
            </span>
          </a>
        )}

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

          <input
            className="entry-name-inp"
            style={{ width: "100%", padding: "12px 14px", border: "1px solid var(--stroke-strong)", borderRadius: 12, fontFamily: "var(--display)", fontSize: 14, background: "#fff", color: "var(--ink)" }}
            value={projectName}
            onChange={(e) => setProjectName(e.target.value)}
            placeholder={t("studio.entry.projectNamePlaceholder")}
            spellCheck={false}
          />

          <div className="entry-row">
            <div className="entry-name">
              <select
                value={org}
                onChange={(e) => setOrg(e.target.value)}
                title={t("studio.entry.orgTitle")}
                style={{ border: "none", background: "transparent", fontFamily: "var(--mono)", fontSize: 13, color: "var(--ink2)", cursor: "pointer", outline: "none", maxWidth: 140 }}
              >
                {orgs.map((o) => (
                  <option key={o} value={o}>
                    {o}
                  </option>
                ))}
              </select>
              <span className="entry-name-pre" style={{ padding: "0 2px" }}>/</span>
              <input
                className="entry-name-inp"
                value={repoName}
                onChange={(e) => {
                  setTouchedRepo(true);
                  setRepoInput(e.target.value);
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
