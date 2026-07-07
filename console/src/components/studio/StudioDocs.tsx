"use client";

// StudioDocs — el Studio como WORKSPACE full-height (rediseño 2026-07-06, aprobado por
// mockup). Shell = topbar fija · cuerpo [riel | main] · drawer de ACTIVIDAD al pie.
// El riel lista las FASES del run (stepper vertical) y los DOCUMENTOS del repo; el main
// muestra el doc seleccionado o, si elegís una fase, su PhasePanel (artefacto +
// aprobar/rechazar). El live-log ya NO vive dentro del PhasePanel: es el terminal del
// drawer de Actividad al pie (colapsable), alimentado por useLiveEvents(run, isRunning).
// El flujo de gates (PhasePanel/useApprove/useReject) NO cambia.

import { useEffect, useMemo, useRef, useState } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { Light as SyntaxHighlighter } from "react-syntax-highlighter";
import yaml from "react-syntax-highlighter/dist/esm/languages/hljs/yaml";
import { githubGist } from "react-syntax-highlighter/dist/esm/styles/hljs";
import { useActiveProject } from "@/lib/activeProject";
import {
  useProjectDocs,
  useProjectDoc,
  useDocHistory,
  useDesignRuns,
  useRerunStep,
  useActiveDesignRun,
  useDesignLog,
  useDesignRun,
  useDeleteRun,
  useCreateDesignRun,
  useLiveEvents,
} from "@/lib/hooks";
import { IterationModal } from "./StudioModals";
import { LiveLog } from "@/components/board/LiveLog";
import { useT } from "@/lib/i18n";
import { PhasePanel } from "./PhasePanel";
import { phaseLabel, phaseState, PHASE_ICON, activePhaseIndex } from "./phaseHelpers";
import { useRouter } from "next/navigation";

SyntaxHighlighter.registerLanguage("yaml", yaml);

// Friendly titles + a logical reading order for the known design artifacts.
// Iconos calcados del mockup para los docs que muestra (◈ discovery, § constitution,
// ◇ prd); el resto conserva glifos coherentes. order = orden de lectura del riel.
const DOC_META: Record<string, { titleKey: string | null; icon: string; order: number }> = {
  "BRIEF.md": { titleKey: "studio.docs.title.brief", icon: "◈", order: 1 },
  "CONSTITUTION.md": { titleKey: "studio.docs.title.constitution", icon: "§", order: 1.5 },
  "PRD.md": { titleKey: "studio.docs.title.prd", icon: "◇", order: 2 },
  "DATA_MODEL.md": { titleKey: "studio.docs.title.dataModel", icon: "▦", order: 2.5 },
  "ARCHITECTURE.md": { titleKey: "studio.docs.title.architecture", icon: "◫", order: 3 },
  "UI_SCREENS.md": { titleKey: "studio.docs.title.uiScreens", icon: "▢", order: 4 },
  "DESIGN_SYSTEM.md": { titleKey: "studio.docs.title.designSystem", icon: "❖", order: 5 },
  "backlog.yaml": { titleKey: "studio.docs.title.backlog", icon: "☰", order: 6 },
  "SESSION.md": { titleKey: "studio.docs.title.session", icon: "◷", order: 7 },
};

function meta(name: string, path?: string) {
  if (path && isHtml(path)) return { titleKey: null, icon: "▨", order: 5.5 };
  return DOC_META[name] ?? { titleKey: null, icon: "·", order: 99 };
}

function metaTitle(name: string, path: string, t: (k: string) => string): string {
  const m = meta(name, path);
  if (m.titleKey) return t(m.titleKey);
  if (isHtml(path)) return name.replace(/\.html?$/i, "");
  return name;
}

function isMarkdown(path: string) {
  return path.toLowerCase().endsWith(".md");
}

// Which design phase (step) produces each doc — so the refine on a doc re-runs the
// right phase. Mirrors registry/workflows/design.yaml — keep in sync.
const DOC_STEP: Record<string, string> = {
  "BRIEF.md": "discovery",
  "CONSTITUTION.md": "constitution",
  "PRD.md": "prd",
  "DATA_MODEL.md": "data_model",
  "ARCHITECTURE.md": "architecture",
  "UI_SCREENS.md": "ui",
  // DESIGN_SYSTEM.md lo escribe el agente `designer` en la fase `mockups`
  // (registry/agents/designer.md) — refinarlo re-corre esa fase.
  "DESIGN_SYSTEM.md": "mockups",
  "backlog.yaml": "backlog",
};
function stepForDoc(name: string, path: string): string | null {
  if (isHtml(path)) return "mockups";
  return DOC_STEP[name] ?? null;
}

function changelogLabel(msg: string): string {
  return msg.replace(/^design:\s*publish\s*/i, "").replace(/\s*\(\w+_gate approved\)\s*$/i, "");
}

function isHtml(path: string) {
  const p = path.toLowerCase();
  return p.endsWith(".html") || p.endsWith(".htm");
}

// MockupFrame renders an HTML mockup inline (sandboxed iframe) with a button to open it
// full-screen in its own tab — "como una UI funcional".
function MockupFrame({ content, t }: { content: string; t: (k: string) => string }) {
  function openInTab() {
    const blob = new Blob([content], { type: "text/html" });
    const url = URL.createObjectURL(blob);
    window.open(url, "_blank", "noopener");
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
  const router = useRouter();

  // Subdirectory entries are expanded by the API (one level), so all entries are files.
  const files = useMemo(() => {
    const list = (docs ?? []).filter((d) => d.type === "file");
    return [...list].sort((a, b) => meta(a.name, a.path).order - meta(b.name, b.path).order);
  }, [docs]);

  // Group the html mockups under one collapsible "Mockups" node.
  const treeNodes = useMemo(() => {
    const htmls = files.filter((f) => isHtml(f.path));
    type Node = { order: number; doc?: (typeof files)[number]; mockups?: typeof files };
    const nodes: Node[] = files
      .filter((f) => !isHtml(f.path))
      .map((f) => ({ order: meta(f.name, f.path).order, doc: f }));
    if (htmls.length) nodes.push({ order: 5.5, mockups: htmls });
    return nodes.sort((a, b) => a.order - b.order);
  }, [files]);

  // Selección del main: un doc (selected) O una fase (selPhase). Excluyentes.
  const [selected, setSelected] = useState<string | null>(null);
  const [selPhase, setSelPhase] = useState<number | null>(null);
  const [showLog, setShowLog] = useState(false);
  const [fullscreen, setFullscreen] = useState(false);
  const [railOpen, setRailOpen] = useState(true);
  const [actOpen, setActOpen] = useState(true);
  const active = selected ?? files.find((f) => f.name === "PRD.md")?.path ?? files[0]?.path ?? null;

  function pickDoc(path: string) {
    setSelected(path);
    setSelPhase(null);
    setShowLog(false);
  }
  function pickPhase(i: number) {
    setSelPhase(i);
    setShowLog(false);
  }

  const [ver, setVer] = useState<string | null>(null);
  useEffect(() => setVer(null), [active]);
  const [mockOpen, setMockOpen] = useState(false);
  useEffect(() => {
    if (active && isHtml(active)) setMockOpen(true);
  }, [active]);

  // Run de diseño activo + sus fases EN VIVO (useDesignRun refetchea cada 3s).
  const activeRun = useActiveDesignRun(projectId);
  const { data: liveRun } = useDesignRun(activeRun?.id ?? null);
  const run = liveRun ?? activeRun ?? null;
  const phases = run?.phases ?? [];
  const awaitingCount = phases.filter((p) => p.gateStatus === "AWAITING").length;
  const isTerminal = run ? run.status === "DONE" || run.status === "FAILED" || run.status === "CANCELLED" : false;
  const publishedBacklog = phases.some((p) => p.stepId === "handoff" && phaseState(p) === "approved");

  // Actividad en vivo: el terminal del drawer al pie (divergencia #1). Se alimenta
  // del run activo mientras GENERA; misma fuente WS que el board (useLiveEvents).
  const isRunning = run?.status === "RUNNING";
  const liveEvents = useLiveEvents(run?.id ?? null, !!isRunning);
  const runningPhase = phases.find((p) => phaseState(p) === "running") ?? null;
  const runningPhaseLabel = runningPhase
    ? phaseLabel(t, runningPhase.stepId, runningPhase.name)
    : t("studio.activity.working");
  const lastMsg = liveEvents.length ? liveEvents[liveEvents.length - 1].message : "";

  // Estado del doc para su badge en el riel (mockup: ✓ generado / spinner generando):
  // deriva de la fase que lo produce. Sin fase (o no en curso) → ✓ (el archivo existe).
  function docGenerating(name: string, path: string): boolean {
    const step = stepForDoc(name, path);
    if (!step) return false;
    const ph = phases.find((p) => p.stepId === step);
    return ph ? phaseState(ph) === "running" : false;
  }

  // Al aparecer un run (o cambiar de run), aterrizás en su fase activa — una sola vez
  // por run.id, para no pisar la selección manual del usuario en cada refetch.
  const prevRun = useRef<string | null>(null);
  useEffect(() => {
    if (!run) return;
    if (prevRun.current === run.id) return;
    prevRun.current = run.id;
    if (phases.length && (run.status === "RUNNING" || run.status === "AWAITING" || run.status === "FAILED")) {
      setSelPhase(activePhaseIndex(phases));
      setSelected(null);
    }
  }, [run, phases]);

  // Refine (C3/D1): re-corre la fase del doc con el feedback inyectado, sobre el ÚLTIMO
  // run de este proyecto que CONTIENE esa fase (no el más nuevo a secas).
  const { data: designRuns } = useDesignRuns();
  const { data: log } = useDesignLog(projectId);
  const rerun = useRerunStep();
  const [refine, setRefine] = useState("");
  const activeFile = files.find((fl) => fl.path === active) ?? null;
  const activeStep = activeFile ? stepForDoc(activeFile.name, activeFile.path) : null;
  const refineRun = useMemo(() => {
    if (!activeStep) return null;
    return (
      (designRuns ?? [])
        .filter((r) => r.project_id === projectId && (r.phases ?? []).some((p) => p.stepId === activeStep))
        .sort((a, b) => b.created_at - a.created_at)[0] ?? null
    );
  }, [designRuns, projectId, activeStep]);
  function doRefine() {
    if (!refine.trim() || !refineRun || !activeStep || !activeFile) return;
    const fb = isHtml(activeFile.path)
      ? `El usuario está refinando la pantalla "${activeFile.name}" (${activeFile.path}). Concentrate en ESA superficie; no rehagas las otras salvo que sea imprescindible. Petición: ${refine.trim()}`
      : refine.trim();
    rerun.mutate([refineRun.id, activeStep, fb], { onSuccess: () => setRefine("") });
  }

  // Change Request (C4) — acción a nivel proyecto (puede tocar varios docs).
  const [crOpen, setCrOpen] = useState(false);
  const { data: history } = useDocHistory(projectId, active);
  const { data: content, isLoading: docLoading } = useProjectDoc(projectId, active, ver ?? "design");

  // Acciones del run (eliminar / relanzar) — antes en DesignPipeline, ahora en la topbar.
  const deleteRun = useDeleteRun();
  const createDesignRun = useCreateDesignRun();
  const [relaunching, setRelaunching] = useState(false);
  function doDelete() {
    if (!run) return;
    if (!window.confirm(t("studio.view.deleteRunConfirm"))) return;
    deleteRun.mutate([run.id], { onSuccess: () => setSelPhase(null) });
  }
  async function doRelaunch() {
    if (!run) return;
    setRelaunching(true);
    try {
      await createDesignRun.mutateAsync({
        project_id: run.project_id ?? "",
        repo: run.repo ?? "",
        instructions: run.idea ?? "",
      });
    } finally {
      setRelaunching(false);
    }
  }

  if (projLoading) {
    return (
      <div className="studio-shell">
        <div className="placeholder">
          <span className="spin" /> {t("studio.docs.loadingProject")}
        </div>
      </div>
    );
  }
  if (!project) {
    return (
      <div className="studio-shell">
        <div className="placeholder">{t("studio.docs.selectProject")}</div>
      </div>
    );
  }

  const viewPhase = selPhase != null ? phases[selPhase] : null;

  return (
    <div className={`studio-shell${fullscreen ? " doc-full" : ""}${!railOpen ? " rail-collapsed" : ""}`}>
      {/* ── Topbar ── */}
      <header className="studio-topbar">
        <h2 className="studio-proj">{project.name}</h2>
        {project.repo && (
          <a className="docs-repo" href={project.repo} target="_blank" rel="noreferrer">
            {t("studio.docs.repo")}
          </a>
        )}
        <span className="studio-branch">
          <span className="d" /> {t("studio.docs.onBranch")}
        </span>
        {run && !isTerminal && (
          <button
            className="studio-runchip"
            onClick={() => pickPhase(activePhaseIndex(phases))}
          >
            <span className="d" style={{ background: awaitingCount > 0 ? "var(--accent)" : "var(--navy)" }} />
            {awaitingCount > 0 ? t("studio.docs.awaitingN", { n: awaitingCount }) : t("studio.docs.runActive")}
          </button>
        )}
        <div className="sp" />
        {(phases.length > 0 || files.length > 0) && !fullscreen && (
          <button
            className={`btn ghost sm${!railOpen ? " on" : ""}`}
            onClick={() => setRailOpen((v) => !v)}
            title={t("studio.docs.railToggle")}
          >
            ⇤ {t("studio.docs.railToggle")}
          </button>
        )}
        {run && (
          <button
            className="btn ghost sm"
            style={{ color: "var(--danger)" }}
            onClick={doDelete}
            disabled={deleteRun.isPending}
            title={t("studio.view.deleteRunTitle")}
          >
            {deleteRun.isPending ? t("studio.view.deletingRun") : t("studio.view.deleteRun")}
          </button>
        )}
        {isTerminal && !publishedBacklog && (
          <button className="btn ghost sm" onClick={doRelaunch} disabled={relaunching}>
            {relaunching ? t("studio.view.relaunching") : t("studio.view.relaunchDesign")}
          </button>
        )}
        {log && log.length > 0 && (
          <button className={`btn ghost sm${showLog ? " on" : ""}`} onClick={() => { setShowLog((v) => !v); setSelPhase(null); }}>
            {t("studio.docs.changelog")}
          </button>
        )}
        <button className="btn ghost sm" onClick={() => router.push("/")}>
          {t("studio.view.newProject")}
        </button>
        {project.repo && (
          <button className="btn primary sm" onClick={() => setCrOpen(true)}>
            {t("studio.view.newIteration")}
          </button>
        )}
      </header>

      {/* ── Cuerpo: riel | main ── */}
      <div className="studio-body">
        <aside className="studio-rail">
          {phases.length > 0 && (
            <div className="rail-sec">
              <div className="rail-h">{t("studio.docs.phasesSec")}</div>
              {phases.map((p, i) => {
                const st = phaseState(p);
                return (
                  <button
                    key={p.stepId}
                    className={`v-phase v-${st}${selPhase === i ? " on" : ""}`}
                    onClick={() => pickPhase(i)}
                    title={phaseLabel(t, p.stepId, p.name)}
                  >
                    {/* pending = círculo vacío (mockup); done ✓ / current ● lo pone el glyph */}
                    <span className="v-g">{st === "pending" ? "" : PHASE_ICON[st]}</span>
                    <span className="v-lbl">{phaseLabel(t, p.stepId, p.name)}</span>
                    {i < phases.length - 1 && <span className="v-conn" />}
                  </button>
                );
              })}
            </div>
          )}

          <div className="rail-sec">
            <div className="rail-h">{t("studio.docs.pages")}</div>
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
                    (() => {
                      const gen = docGenerating(n.doc.name, n.doc.path);
                      const sel = !showLog && selPhase == null && active === n.doc.path;
                      return (
                        <button
                          key={n.doc.path}
                          className={`rail-doc${gen ? " gen" : ""}${sel ? " on" : ""}`}
                          onClick={() => pickDoc(n.doc!.path)}
                        >
                          <span className="pi">{meta(n.doc.name, n.doc.path).icon}</span>
                          <span className="lbl">{n.doc.name}</span>
                          <span className="badge">{gen ? <span className="spin" /> : "✓"}</span>
                        </button>
                      );
                    })()
                  ) : (
                    <div key="mockups-group">
                      <button className="rail-doc" onClick={() => setMockOpen((o) => !o)}>
                        <span className="pi">{mockOpen ? "▾" : "▸"}</span>
                        <span className="lbl">{t("studio.docs.title.mockups")}</span>
                        <span className="rail-doc-count">{n.mockups!.length}</span>
                      </button>
                      {mockOpen &&
                        n.mockups!.map((h) => {
                          const gen = docGenerating(h.name, h.path);
                          const sel = !showLog && selPhase == null && active === h.path;
                          return (
                            <button
                              key={h.path}
                              className={`rail-doc${gen ? " gen" : ""}${sel ? " on" : ""}`}
                              style={{ paddingLeft: 26 }}
                              onClick={() => pickDoc(h.path)}
                            >
                              <span className="pi">▨</span>
                              <span className="lbl">{h.name}</span>
                              <span className="badge">{gen ? <span className="spin" /> : "✓"}</span>
                            </button>
                          );
                        })}
                    </div>
                  ),
                )}
              </nav>
            )}
          </div>
        </aside>

        {/* ── Main ── */}
        <main className="studio-main">
          {showLog && log ? (
            <div className="studio-doc-scroll">
              <div className="studio-doc-inner">
                <div style={{ fontFamily: "var(--display)", fontWeight: 800, fontSize: 16, marginBottom: 16 }}>
                  {t("studio.docs.changelog")}
                </div>
                {log.map((c) => (
                  <div key={c.sha} style={{ position: "relative", padding: "0 6px 14px 22px" }}>
                    <span style={{ position: "absolute", left: 4, top: 4, width: 8, height: 8, borderRadius: "50%", background: "var(--accent)", boxShadow: "0 0 0 3px var(--accent-soft)" }} />
                    <span style={{ position: "absolute", left: 7, top: 14, bottom: 0, width: 1, background: "var(--stroke)" }} />
                    <div style={{ fontFamily: "var(--mono)", fontSize: 12.5, color: "var(--ink2)" }}>{changelogLabel(c.message)}</div>
                    <div style={{ fontFamily: "var(--mono)", fontSize: 11, color: "var(--ink4)" }}>{new Date(c.date).toLocaleString()}</div>
                  </div>
                ))}
              </div>
            </div>
          ) : viewPhase ? (
            // Vista de FASE: el PhasePanel intacto (artefacto + live-log colapsable + gate).
            <div className="studio-doc-scroll">
              <div className="studio-doc-inner">
                <PhasePanel runId={run!.id} phase={viewPhase} state={phaseState(viewPhase)} />
              </div>
            </div>
          ) : (
            // Vista de DOCUMENTO: header + reader + refine.
            <>
              <div className="studio-doc-head">
                <span className="eyebrow">{active ? metaTitle(activeFile?.name ?? active, active, t) : t("studio.docs.eyebrow")}</span>
                {(() => {
                  const hp = activeStep ? phases.find((p) => p.stepId === activeStep) : null;
                  return hp && phaseState(hp) === "approved" ? (
                    <span className="doc-okchip">{t("studio.docs.approved")}</span>
                  ) : null;
                })()}
                {history && history.length > 0 && (
                  <span className="doc-verchip">
                    v{ver === null ? history.length : history.length - history.findIndex((h) => h.sha === ver)}
                  </span>
                )}
                <div className="sp" />
                {active && !isHtml(active) && (
                  <button className="btn ghost sm" onClick={() => setFullscreen((f) => !f)}>
                    ⛶ {fullscreen ? t("studio.docs.exitFull") : t("studio.docs.fullscreen")}
                  </button>
                )}
              </div>
              <div className="studio-doc-scroll">
                <div className="studio-doc-inner">
                  {active && history && history.length > 1 && (
                    <div className="chips" style={{ marginBottom: 14 }}>
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
                </div>
              </div>
              {/* Refine unificado: mismo chat bajo CADA doc — sin excepción (incluye
                  Design System y los Mockups HTML; doRefine ya nombra la superficie
                  cuando es un .html para que el agente refine ESA pantalla). */}
              {active && activeStep && refineRun && ver === null && (
                <div className="studio-refine">
                  <form
                    className="studio-refine-in"
                    onSubmit={(e) => {
                      e.preventDefault();
                      doRefine();
                    }}
                  >
                    <input
                      value={refine}
                      onChange={(e) => setRefine(e.target.value)}
                      placeholder={t("studio.docs.refine.placeholder")}
                    />
                    <button type="submit" className="btn primary sm" disabled={!refine.trim() || rerun.isPending}>
                      {rerun.isPending ? t("studio.docs.refine.sending") : t("studio.docs.refine.send")}
                    </button>
                  </form>
                </div>
              )}
            </>
          )}
        </main>
      </div>

      {/* ── Actividad en vivo: drawer colapsable al pie del workspace ── */}
      {run && isRunning && (
        <section className={`studio-activity${actOpen ? " on" : ""}`}>
          <button className="studio-act-bar" onClick={() => setActOpen((v) => !v)} aria-expanded={actOpen}>
            <span className="studio-act-live" />
            <span className="studio-act-label">
              <b>{runningPhaseLabel}</b> · {t("studio.activity.generating")}
            </span>
            <span className="studio-act-sub">{lastMsg || t("studio.activity.starting")}</span>
            <span className="studio-act-chev">▾</span>
          </button>
          {actOpen && (
            <div className="studio-act-term">
              <LiveLog events={liveEvents} />
            </div>
          )}
        </section>
      )}

      <div className={`overlay ${crOpen ? "on" : ""}`} onClick={() => setCrOpen(false)} />
      {crOpen && projectId && project.repo && (
        <IterationModal
          projectId={projectId}
          repo={project.repo}
          onClose={() => setCrOpen(false)}
          onCreated={() => setCrOpen(false)}
        />
      )}
    </div>
  );
}
