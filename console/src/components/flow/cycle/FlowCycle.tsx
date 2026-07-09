"use client";

// FlowCycle — la vista CICLO de /flow: el diagrama "00 · El ciclo Scrum de Fluxo"
// portado EXACTO (viewBox 940×560, mismas posiciones/formas/textos/paleta Aiuda del
// documento SDLC-map). Es un SEGUNDO renderer del MISMO FlowModel que el grafo — no
// duplica derivación: consume cycleModel (puro, testeado) para mapear cada estación
// a su estado VIVO. La semántica original naranja=existe / azul-punteado=propuesto se
// re-mapea conservando el lenguaje visual: sólido naranja = done/activo; punteado azul
// = pendiente/aún no corrido; en-curso anima el stroke (dash-offset, SIN transform en
// contenedores); los ◆ de gates pulsan ámbar cuando esperan al humano. Los modos auto
// atenúan (opacity) las ceremonias — la FORMA nunca cambia. Click por estación navega
// a actuar (drawer de fase/gate, RunDrawer de ceremonia, Sprints, Board, preview).

import { useMemo } from "react";
import { useT } from "@/lib/i18n";
import { statusToken } from "@/lib/statusToken";
import type { CeremonyModes, FlowModel } from "../flowGraph";
import {
  buildCycleView,
  type CeremonyStation,
  type StageKey,
  type StationState,
} from "./cycleModel";

export interface FlowCycleProps {
  model: FlowModel;
  modes: CeremonyModes;
  selected: string | null;
  onSelect: (nodeId: string) => void; // phase:/gate:/ceremony:<...>
  onOpenBoard: () => void; // PRODUCT BACKLOG
  onOpenSprint: (sprintId: string) => void; // círculo SPRINT N · SPRINT BACKLOG
  onOpenPreview: (runId: string) => void; // INCREMENTO
  previewPending?: boolean;
}

// ── Estilo de una forma (rect/circle) según su estado vivo ──────────────────────
// Colores por CSS vars locales (paleta Aiuda del doc), aplicadas inline: el sólido
// naranja del "existe", el azul-punteado del "propuesto", ámbar del gate, rojo de la
// falla. La animación de marcha (running) vive en la clase CSS cyc-run.
function shapeProps(
  state: StationState,
  opts: { soft?: boolean; width?: number; dim?: boolean; selected?: boolean } = {},
): { style: React.CSSProperties; className: string } {
  const width = opts.width ?? 2;
  const softFill = opts.soft ? "var(--cyc-orange-soft)" : "var(--cyc-white)";
  let stroke = "var(--cyc-orange)";
  let fill = softFill;
  let dash: string | undefined;
  let anim = "";
  switch (state) {
    case "done":
      stroke = "var(--cyc-orange)";
      break;
    case "running":
      stroke = "var(--cyc-orange)";
      dash = "6 4";
      anim = " cyc-run";
      break;
    case "awaiting":
      stroke = "var(--cyc-amber)";
      anim = " cyc-await";
      break;
    case "failed":
      stroke = "var(--cyc-fail)";
      break;
    case "pending":
    default:
      stroke = "var(--cyc-blue)";
      fill = opts.soft ? "var(--cyc-blue-soft)" : "var(--cyc-white)";
      dash = "6 4";
      break;
  }
  return {
    style: {
      stroke,
      fill,
      strokeWidth: width,
      strokeDasharray: dash,
      opacity: opts.dim ? 0.4 : undefined,
    },
    className: `cyc-shape${anim}${opts.selected ? " cyc-sel" : ""}`,
  };
}

// Color de una LÍNEA de fase de la banda de diseño. done = tinta oscura (idéntico al
// original, "existe"); el resto surface el estado. NO usa statusToken (es estado de
// FASE, no de story): paleta local.
function lineColor(state: StationState): string {
  switch (state) {
    case "done":
      return "var(--cyc-ink)";
    case "running":
      return "var(--cyc-orange)";
    case "awaiting":
      return "var(--cyc-amber)";
    case "failed":
      return "var(--cyc-fail)";
    default:
      return "var(--cyc-ink2)";
  }
}

export function FlowCycle(props: FlowCycleProps) {
  const { model, modes, selected, onSelect, onOpenBoard, onOpenSprint, onOpenPreview } = props;
  const t = useT();
  const v = useMemo(() => buildCycleView(model, modes), [model, modes]);

  const stageByKey = useMemo(() => new Map(v.stages.map((s) => [s.key, s])), [v.stages]);
  const stage = (k: StageKey) => stageByKey.get(k)!;

  const sprintName = v.sprint ? v.sprint.name : t("flow.cycle.sprintN");
  const sprintId = v.sprint?.id ?? null;

  // Ids de nodo de ceremonia (== los que el drawer compartido resuelve en el modelo).
  const ceremonyId = (c: CeremonyStation) => (sprintId ? `ceremony:${c.kind}:${sprintId}` : "");

  // Handlers de estación (sólo se enganchan si hay algo que abrir).
  const openStage = (k: StageKey) => {
    const s = stage(k);
    if (s.target) onSelect(s.target);
  };
  const openGate = () => {
    if (v.awaitingGate) onSelect(v.awaitingGate);
  };
  const openCeremony = (c: CeremonyStation) => {
    if (c.runId && sprintId) onSelect(ceremonyId(c));
  };
  const openSprint = () => {
    if (sprintId) onOpenSprint(sprintId);
  };
  const openPreview = () => {
    if (v.increment.previewRunId) onOpenPreview(v.increment.previewRunId);
  };

  const tok = (s: "done" | "running" | "failed") => statusToken(s).color;

  return (
    <div className="cyc-scroll">
      <div className="cyc-card cyc">
        <svg viewBox="0 0 940 560" className="cyc-svg" role="img" aria-label={t("flow.cycle.aria")}>
          <defs>
            <marker id="cycAO" markerWidth="9" markerHeight="9" refX="7" refY="4.5" orient="auto">
              <path d="M0,0 L9,4.5 L0,9 Z" className="mk-o" />
            </marker>
            <marker id="cycAB" markerWidth="9" markerHeight="9" refX="7" refY="4.5" orient="auto">
              <path d="M0,0 L9,4.5 L0,9 Z" className="mk-b" />
            </marker>
          </defs>

          {/* ============ IZQUIERDA: pipeline de diseño ============ */}
          <rect x="18" y="118" width="150" height="300" rx="14" {...shapeProps(v.bandState, { soft: true })} />
          <text x="93" y="143" textAnchor="middle" fontSize="13" fontWeight="800" className="f-orange">
            {t("flow.cycle.studioTitle")}
          </text>
          <text x="93" y="158" textAnchor="middle" fontSize="9" className="f-ink2">
            {t("flow.cycle.studioSub")}
          </text>
          <g fontSize="10.5" fontWeight="600">
            {(
              [
                ["brief", 182],
                ["constitution", 200],
                ["prd", 218],
                ["architecture", 236],
                ["ui", 254],
                ["backlog", 272],
              ] as [StageKey, number][]
            ).map(([k, y]) => {
              const s = stage(k);
              const sel = !!s.target && s.target === selected;
              return (
                <text
                  key={k}
                  x="93"
                  y={y}
                  textAnchor="middle"
                  style={{ fill: lineColor(s.state), cursor: s.target ? "pointer" : "default" }}
                  className={`cyc-line${s.state === "running" ? " cyc-pulse" : ""}${sel ? " cyc-line-sel" : ""}`}
                  onClick={() => openStage(k)}
                >
                  {t(`flow.cycle.stage.${k}`)}
                </text>
              );
            })}
          </g>
          <text
            x="93"
            y="294"
            textAnchor="middle"
            fontSize="9"
            fontWeight="700"
            className={`f-amber${v.awaitingGate ? " cyc-pulse" : ""}`}
            style={{ cursor: v.awaitingGate ? "pointer" : "default" }}
            onClick={openGate}
          >
            {t("flow.cycle.gates")}
          </text>
          <text x="93" y="308" textAnchor="middle" fontSize="9" fontWeight="700" className="f-blue">
            {t("flow.cycle.gatesVerbs")}
          </text>

          {/* Product backlog */}
          <g className="cyc-station" onClick={onOpenBoard}>
            <rect x="33" y="330" width="120" height="72" rx="10" {...shapeProps(v.productBacklog)} />
            <text x="93" y="352" textAnchor="middle" fontSize="11" fontWeight="800" className="f-ink">
              {t("flow.cycle.backlogT1")}
            </text>
            <text x="93" y="366" textAnchor="middle" fontSize="11" fontWeight="800" className="f-ink">
              {t("flow.cycle.backlogT2")}
            </text>
            <text x="93" y="382" textAnchor="middle" fontSize="8.5" className="f-ink2">
              {t("flow.cycle.backlogS1")}
            </text>
            <text x="93" y="394" textAnchor="middle" fontSize="8.5" className="f-ink2">
              {t("flow.cycle.backlogS2")}
            </text>
          </g>

          {/* flecha backlog -> planning */}
          <path d="M168 366 H 216" className="ar-o" markerEnd="url(#cycAO)" />

          {/* ============ SPRINT PLANNING (ceremonia) ============ */}
          <g
            className={v.planning.runId ? "cyc-station" : undefined}
            onClick={() => openCeremony(v.planning)}
          >
            <rect
              x="222"
              y="330"
              width="118"
              height="72"
              rx="10"
              {...shapeProps(v.planning.state, {
                soft: true,
                dim: v.planning.dim,
                selected: selected === ceremonyId(v.planning),
              })}
            />
            <text x="281" y="354" textAnchor="middle" fontSize="11" fontWeight="800" className="f-blue" opacity={v.planning.dim ? 0.55 : 1}>
              {t("flow.cycle.planningT1")}
            </text>
            <text x="281" y="368" textAnchor="middle" fontSize="11" fontWeight="800" className="f-blue" opacity={v.planning.dim ? 0.55 : 1}>
              {t("flow.cycle.planningT2")}
            </text>
            <text x="281" y="384" textAnchor="middle" fontSize="8.5" className="f-blue" opacity={v.planning.dim ? 0.55 : 1}>
              {t("flow.cycle.planningS1")}
            </text>
            <text x="281" y="395" textAnchor="middle" fontSize="8.5" className="f-blue" opacity={v.planning.dim ? 0.55 : 1}>
              {t("flow.cycle.planningS2")}
            </text>
          </g>

          {/* flecha planning -> sprint backlog */}
          <path d="M340 366 H 388" className="ar-b" markerEnd="url(#cycAB)" />

          {/* ============ SPRINT BACKLOG ============ */}
          <g className={sprintId ? "cyc-station" : undefined} onClick={openSprint}>
            <rect x="394" y="330" width="112" height="72" rx="10" {...shapeProps(v.productBacklog)} />
            <text x="450" y="354" textAnchor="middle" fontSize="11" fontWeight="800" className="f-ink">
              {t("flow.cycle.sprintBacklogT1")}
            </text>
            <text x="450" y="368" textAnchor="middle" fontSize="11" fontWeight="800" className="f-ink">
              {t("flow.cycle.sprintBacklogT2")}
            </text>
            <text x="450" y="384" textAnchor="middle" fontSize="8.5" className="f-ink2">
              {t("flow.cycle.sprintBacklogS1")}
            </text>
            <text x="450" y="395" textAnchor="middle" fontSize="8.5" className="f-ink2">
              {t("flow.cycle.sprintBacklogS2")}
            </text>
          </g>

          {/* flecha sprint backlog -> ciclo sprint */}
          <path d="M506 366 C 550 366, 560 330, 590 300" className="ar-o" markerEnd="url(#cycAO)" />

          {/* ============ CÍRCULO DAILY DIGEST (estático informativo) ============ */}
          <circle cx="660" cy="118" r="46" fill="none" className="cyc-daily" />
          <path d="M660 72 A 46 46 0 0 1 706 118" className="ar-b" markerEnd="url(#cycAB)" />
          <text x="660" y="110" textAnchor="middle" fontSize="10.5" fontWeight="800" className="f-blue">
            {t("flow.cycle.dailyT1")}
          </text>
          <text x="660" y="123" textAnchor="middle" fontSize="10.5" fontWeight="800" className="f-blue">
            {t("flow.cycle.dailyT2")}
          </text>
          <text x="660" y="137" textAnchor="middle" fontSize="8" className="f-blue">
            {t("flow.cycle.dailyS")}
          </text>
          <path d="M660 168 V 196" className="ar-b-plain" />

          {/* ============ CÍRCULO SPRINT N ============ */}
          <g className={sprintId ? "cyc-station" : undefined} onClick={openSprint}>
            <circle
              cx="660"
              cy="300"
              r="100"
              {...shapeProps(
                v.counts.running > 0 ? "running" : v.counts.done > 0 ? "done" : "pending",
                { soft: true, width: 3, selected: false },
              )}
            />
            {/* contadores inyectados (statusToken = única fuente del color de estado) */}
            <text x="628" y="248" textAnchor="middle" fontSize="11" fontWeight="800" style={{ fill: tok("done") }}>
              ✓{v.counts.done}
            </text>
            <text x="660" y="248" textAnchor="middle" fontSize="11" fontWeight="800" style={{ fill: tok("running") }}>
              ●{v.counts.running}
            </text>
            <text x="692" y="248" textAnchor="middle" fontSize="11" fontWeight="800" style={{ fill: tok("failed") }}>
              ✕{v.counts.failed}
            </text>
            {/* nombre real del sprint (o "SPRINT N") en el lugar del título */}
            <text x="660" y="268" textAnchor="middle" fontSize="14" fontWeight="900" className="f-orange">
              {sprintName}
            </text>
            <g fontSize="9.5" fontWeight="600" className="f-ink">
              <text x="660" y="288" textAnchor="middle" className="f-ink">
                {t("flow.cycle.sprintL1")}
              </text>
              <text x="660" y="303" textAnchor="middle" className="f-ink">
                {t("flow.cycle.sprintL2")}
              </text>
              <text x="660" y="318" textAnchor="middle" className="f-ink">
                {t("flow.cycle.sprintL3")}
              </text>
            </g>
            <text x="660" y="340" textAnchor="middle" fontSize="8.5" fontWeight="700" className="f-blue">
              {t("flow.cycle.sprintB1")}
            </text>
            <text x="660" y="351" textAnchor="middle" fontSize="8.5" fontWeight="700" className="f-blue">
              {t("flow.cycle.sprintB2")}
            </text>
          </g>
          <path d="M660 200 A 100 100 0 0 1 760 300" className="ar-o-thick" markerEnd="url(#cycAO)" />
          <path d="M660 400 A 100 100 0 0 1 560 300" className="ar-o-thick" markerEnd="url(#cycAO)" />

          {/* flecha sprint -> incremento */}
          <path d="M746 364 C 790 400, 800 420, 812 440" className="ar-o" markerEnd="url(#cycAO)" />

          {/* ============ INCREMENTO ============ */}
          <g className={v.increment.previewRunId ? "cyc-station" : undefined} onClick={openPreview}>
            <rect x="768" y="446" width="120" height="62" rx="10" {...shapeProps(v.increment.state)} />
            <text x="828" y="468" textAnchor="middle" fontSize="11" fontWeight="800" className="f-ink">
              {t("flow.cycle.incrementT")}
            </text>
            <text x="828" y="483" textAnchor="middle" fontSize="8.5" className="f-ink2">
              {t("flow.cycle.incrementS1")}
            </text>
            <text x="828" y="495" textAnchor="middle" fontSize="8.5" fontWeight="700" className="f-blue">
              {v.increment.previewRunId ? `▶ ${t("flow.cycle.incrementPreview")}` : t("flow.cycle.incrementS2")}
            </text>
          </g>

          {/* flecha incremento -> review */}
          <path d="M768 477 H 700" className="ar-b" markerEnd="url(#cycAB)" />

          {/* ============ SPRINT REVIEW (ceremonia) ============ */}
          <g className={v.review.runId ? "cyc-station" : undefined} onClick={() => openCeremony(v.review)}>
            <rect
              x="576"
              y="446"
              width="118"
              height="62"
              rx="10"
              {...shapeProps(v.review.state, {
                soft: true,
                dim: v.review.dim,
                selected: selected === ceremonyId(v.review),
              })}
            />
            <text x="635" y="466" textAnchor="middle" fontSize="11" fontWeight="800" className="f-blue" opacity={v.review.dim ? 0.55 : 1}>
              {t("flow.cycle.reviewT")}
            </text>
            <text x="635" y="481" textAnchor="middle" fontSize="8.5" className="f-blue" opacity={v.review.dim ? 0.55 : 1}>
              {t("flow.cycle.reviewS1")}
            </text>
            <text x="635" y="493" textAnchor="middle" fontSize="8.5" className="f-blue" opacity={v.review.dim ? 0.55 : 1}>
              {t("flow.cycle.reviewS2")}
            </text>
          </g>

          {/* flecha review -> retro */}
          <path d="M576 477 H 508" className="ar-b" markerEnd="url(#cycAB)" />

          {/* ============ RETRO (ceremonia) ============ */}
          <g className={v.retro.runId ? "cyc-station" : undefined} onClick={() => openCeremony(v.retro)}>
            <rect
              x="384"
              y="446"
              width="118"
              height="62"
              rx="10"
              {...shapeProps(v.retro.state, {
                soft: true,
                dim: v.retro.dim,
                selected: selected === ceremonyId(v.retro),
              })}
            />
            <text x="443" y="466" textAnchor="middle" fontSize="11" fontWeight="800" className="f-blue" opacity={v.retro.dim ? 0.55 : 1}>
              {t("flow.cycle.retroT")}
            </text>
            <text x="443" y="481" textAnchor="middle" fontSize="8.5" className="f-blue" opacity={v.retro.dim ? 0.55 : 1}>
              {t("flow.cycle.retroS1")}
            </text>
            <text x="443" y="493" textAnchor="middle" fontSize="8.5" className="f-blue" opacity={v.retro.dim ? 0.55 : 1}>
              {t("flow.cycle.retroS2")}
            </text>
          </g>

          {/* flecha retro -> planning (cierra el círculo) */}
          <path d="M384 477 C 300 477, 281 460, 281 408" className="ar-b" markerEnd="url(#cycAB)" />
          <text x="322" y="452" textAnchor="middle" fontSize="9" fontWeight="700" className="f-blue">
            {t("flow.cycle.loopLabel")}
          </text>

          {/* ============ EVOLUCIÓN: flecha de vuelta al product backlog ============ */}
          <path
            d="M828 446 C 828 60, 400 40, 110 40 C 60 40, 40 60, 55 112"
            className="ar-o-thin"
            markerEnd="url(#cycAO)"
          />
          <text x="440" y="32" textAnchor="middle" fontSize="10" fontWeight="700" className="f-orange">
            {t("flow.cycle.evolutionLabel")}
          </text>

          {/* leyenda — re-etiquetada a estado VIVO conservando el lenguaje visual */}
          <g transform="translate(22,470)">
            <line x1="0" y1="0" x2="26" y2="0" className="leg-o" />
            <text x="32" y="4" fontSize="10" fontWeight="600" className="f-ink">
              {t("flow.cycle.legendDone")}
            </text>
            <line x1="0" y1="20" x2="26" y2="20" className="leg-b" />
            <text x="32" y="24" fontSize="10" fontWeight="600" className="f-ink">
              {t("flow.cycle.legendPending")}
            </text>
            <text x="0" y="46" fontSize="10" fontWeight="700" className="f-amber">
              {t("flow.cycle.legendGate")}
            </text>
          </g>
        </svg>
      </div>
    </div>
  );
}
