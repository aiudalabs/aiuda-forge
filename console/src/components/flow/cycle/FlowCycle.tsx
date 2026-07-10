"use client";

// FlowCycle — la vista CICLO de /flow como TABLERO DE CONTROL del operador (no un
// diagrama decorativo): viewBox 940×560, terminología 100% Scrum, y cada estación es
// un CONTROL clicable con estado VIVO y SU acción. Es un SEGUNDO renderer del MISMO
// FlowModel que el grafo — no duplica derivación: consume cycleModel (puro, testeado).
// Lenguaje visual: sólido naranja = done/activo; punteado azul = pendiente/aún no
// corrido; en-curso anima el stroke (dash-offset, SIN transform en contenedores); los
// ◆ pulsan ámbar cuando esperan al humano. Cada estación de ceremonia implementa el
// ciclo de vida de 5 estados de cycleModel: off → Settings; waiting → informativa;
// running/awaiting/done → su run/gate. Ninguna caja punteada queda muda.

import { useEffect, useMemo, useRef, useState } from "react";
import { useI18n } from "@/lib/i18n";
import { statusToken } from "@/lib/statusToken";
import type { CeremonyModes, FlowModel } from "../flowGraph";
import {
  buildCycleView,
  type CeremonyStation,
  type StageKey,
  type StationState,
} from "./cycleModel";

// Sección de Settings a la que salta una estación "no activada" (deep-link ?focus=).
export type SettingsFocus = "autonomy" | "notifications";

export interface FlowCycleProps {
  model: FlowModel;
  modes: CeremonyModes;
  selected: string | null;
  onSelect: (nodeId: string) => void; // phase:/gate:/ceremony:<...>
  onOpenBoard: () => void; // PRODUCT BACKLOG
  onOpenSprint: (sprintId: string) => void; // círculo SPRINT N · SPRINT BACKLOG
  onOpenPreview: (runId: string) => void; // INCREMENTO
  onOpenSettings: (focus: SettingsFocus) => void; // estación "no activada" → configurar
  onAddBacklog: () => void; // ＋ añadir al Product Backlog (modal de solicitud de cambio)
  onOpenAbout: () => void; // ⓘ — qué es cada estación (página /flow/about)
  previewPending?: boolean;
}

// ── Estilo de una forma (rect/circle) según su estado vivo ──────────────────────
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

// Texto SVG que NUNCA se sale de su figura: mide el largo natural tras el montaje
// y, si excede maxWidth, fija textLength+lengthAdjust para comprimirlo al ancho
// disponible. A prueba de idiomas (es/en/pt tienen largos distintos) sin tocar el
// layout. Sin transform (scroll-safe). Los títulos cortos siguen como <text> normal.
function FitText({
  maxWidth,
  children,
  ...rest
}: { maxWidth: number; children: React.ReactNode } & React.SVGProps<SVGTextElement>) {
  const ref = useRef<SVGTextElement>(null);
  const [textLength, setTextLength] = useState<number | undefined>(undefined);
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    // Medir el largo NATURAL (sin la restricción previa) y clampar si hace falta.
    el.removeAttribute("textLength");
    el.removeAttribute("lengthAdjust");
    const natural = el.getComputedTextLength();
    setTextLength(natural > maxWidth ? maxWidth : undefined);
  }, [children, maxWidth]);
  return (
    <text
      ref={ref}
      textLength={textLength}
      lengthAdjust={textLength ? "spacingAndGlyphs" : undefined}
      {...rest}
    >
      {children}
    </text>
  );
}

// Color de una LÍNEA de fase de la banda de diseño.
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
  const {
    model,
    modes,
    selected,
    onSelect,
    onOpenBoard,
    onOpenSprint,
    onOpenPreview,
    onOpenSettings,
    onAddBacklog,
    onOpenAbout,
  } = props;
  const { t, lang } = useI18n();
  const v = useMemo(() => buildCycleView(model, modes), [model, modes]);

  const stageByKey = useMemo(() => new Map(v.stages.map((s) => [s.key, s])), [v.stages]);
  const stage = (k: StageKey) => stageByKey.get(k)!;

  const sprintName = v.sprint ? v.sprint.name : t("flow.cycle.sprintN");
  // Nombre largo → se trunca con "…" (cabe en el círculo); el nombre completo va en
  // el tooltip (<title>) y sigue entero al abrir Sprints. FitText comprime el resto.
  const sprintNameShort =
    sprintName.length > 24 ? `${sprintName.slice(0, 23).trimEnd()}…` : sprintName;
  const sprintId = v.sprint?.id ?? null;

  // Ids de nodo de ceremonia (== los que el drawer compartido resuelve en el modelo).
  const ceremonyId = (c: CeremonyStation) => (sprintId ? `ceremony:${c.kind}:${sprintId}` : "");

  // ── El sublabel dinámico de una ceremonia: su estado VIVO como texto de control ──
  const dateFmt = useMemo(
    () => new Intl.DateTimeFormat(lang, { day: "2-digit", month: "short" }),
    [lang],
  );
  const controlLabel = (c: CeremonyStation): string => {
    switch (c.control) {
      case "off":
        return t("flow.cycle.ctrl.off");
      case "waiting":
        return t(`flow.cycle.wait.${c.kind}`);
      case "running":
        return t("flow.cycle.ctrl.running");
      case "awaiting":
        return t("flow.cycle.ctrl.awaiting");
      case "done":
        return c.at ? `${t("flow.cycle.ctrl.done")} · ${dateFmt.format(new Date(c.at))}` : t("flow.cycle.ctrl.done");
    }
  };
  // Color del sublabel de control según su estado (amber cuando espera decisión).
  const controlColor = (c: CeremonyStation): string =>
    c.control === "awaiting" ? "var(--cyc-amber)" : "var(--cyc-blue)";

  // Handlers de estación.
  const openStage = (k: StageKey) => {
    const s = stage(k);
    if (s.target) onSelect(s.target);
  };
  const openGate = () => {
    if (v.awaitingGate) onSelect(v.awaitingGate);
  };
  // Click de una ceremonia = su acción según el ciclo de vida:
  //   off → Settings (activarla) · running/awaiting/done → su run · waiting → informativa.
  const clickCeremony = (c: CeremonyStation) => {
    if (c.control === "off") {
      onOpenSettings("autonomy");
      return;
    }
    if (c.runId && sprintId) onSelect(ceremonyId(c));
  };
  const ceremonyClickable = (c: CeremonyStation) => c.control === "off" || !!c.runId;
  const openSprint = () => {
    if (sprintId) onOpenSprint(sprintId);
  };
  const openPreview = () => {
    if (v.increment.previewRunId) onOpenPreview(v.increment.previewRunId);
  };

  const tok = (s: "done" | "running" | "failed") => statusToken(s).color;

  // Render de una estación de ceremonia (planning/review/retro) como control.
  const ceremonyBox = (
    c: CeremonyStation,
    geom: { x: number; y: number; w: number; h: number; cx: number },
    titleLines: string[],
    descLine: string,
  ) => {
    const clickable = ceremonyClickable(c);
    const titleY = geom.y + 24;
    return (
      <g
        className={clickable ? "cyc-station" : undefined}
        onClick={() => clickCeremony(c)}
        role={clickable ? "button" : undefined}
        aria-label={`${titleLines.join(" ")} — ${controlLabel(c)}`}
      >
        <rect
          x={geom.x}
          y={geom.y}
          width={geom.w}
          height={geom.h}
          rx="10"
          {...shapeProps(c.state, { soft: true, dim: c.dim, selected: selected === ceremonyId(c) })}
        />
        {titleLines.map((ln, i) => (
          <text
            key={i}
            x={geom.cx}
            y={titleY + i * 14}
            textAnchor="middle"
            fontSize="11"
            fontWeight="800"
            className="f-blue"
            opacity={c.dim ? 0.55 : 1}
          >
            {ln}
          </text>
        ))}
        <FitText
          x={geom.cx}
          y={titleY + titleLines.length * 14 + 2}
          maxWidth={geom.w - 10}
          textAnchor="middle"
          fontSize="8.5"
          className="f-blue"
          opacity={c.dim ? 0.55 : 1}
        >
          {descLine}
        </FitText>
        {/* sublabel dinámico: el estado VIVO como control */}
        <FitText
          x={geom.cx}
          y={titleY + titleLines.length * 14 + 14}
          maxWidth={geom.w - 10}
          textAnchor="middle"
          fontSize="8.5"
          fontWeight="700"
          className={c.control === "awaiting" ? "cyc-pulse" : undefined}
          style={{ fill: controlColor(c) }}
          opacity={c.dim ? 0.7 : 1}
        >
          {controlLabel(c)}
        </FitText>
      </g>
    );
  };

  return (
    <div className="cyc-scroll">
      <div className="cyc-card cyc">
        {/* ⓘ — qué es cada estación (Scrum ↔ Fluxo). Sobre el SVG, esquina superior. */}
        <button className="cyc-about" onClick={onOpenAbout} title={t("flow.cycle.about")} aria-label={t("flow.cycle.about")}>
          ⓘ
        </button>
        <svg viewBox="0 0 940 560" className="cyc-svg" role="img" aria-label={t("flow.cycle.aria")}>
          <defs>
            <marker id="cycAO" markerWidth="9" markerHeight="9" refX="7" refY="4.5" orient="auto">
              <path d="M0,0 L9,4.5 L0,9 Z" className="mk-o" />
            </marker>
            <marker id="cycAB" markerWidth="9" markerHeight="9" refX="7" refY="4.5" orient="auto">
              <path d="M0,0 L9,4.5 L0,9 Z" className="mk-b" />
            </marker>
          </defs>

          {/* ============ IZQUIERDA: pipeline de diseño (STUDIO) ============ */}
          <rect x="18" y="118" width="150" height="300" rx="14" {...shapeProps(v.bandState, { soft: true })} />
          <text x="93" y="143" textAnchor="middle" fontSize="13" fontWeight="800" className="f-orange">
            {t("flow.cycle.studioTitle")}
          </text>
          <FitText x="93" y="158" maxWidth={140} textAnchor="middle" fontSize="9" className="f-ink2">
            {t("flow.cycle.studioSub")}
          </FitText>
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
          <FitText
            x="93"
            y="294"
            maxWidth={144}
            textAnchor="middle"
            fontSize="9"
            fontWeight="700"
            className={`f-amber${v.awaitingGate ? " cyc-pulse" : ""}`}
            style={{ cursor: v.awaitingGate ? "pointer" : "default" }}
            onClick={openGate}
          >
            {t("flow.cycle.gates")}
          </FitText>
          <FitText x="93" y="308" maxWidth={144} textAnchor="middle" fontSize="9" fontWeight="700" className="f-blue">
            {t("flow.cycle.gatesVerbs")}
          </FitText>

          {/* Product backlog */}
          <g className="cyc-station" onClick={onOpenBoard}>
            <rect x="33" y="330" width="120" height="72" rx="10" {...shapeProps(v.productBacklog)} />
            <text x="93" y="352" textAnchor="middle" fontSize="11" fontWeight="800" className="f-ink">
              {t("flow.cycle.backlogT1")}
            </text>
            <text x="93" y="366" textAnchor="middle" fontSize="11" fontWeight="800" className="f-ink">
              {t("flow.cycle.backlogT2")}
            </text>
            <FitText x="93" y="382" maxWidth={112} textAnchor="middle" fontSize="8.5" className="f-ink2">
              {t("flow.cycle.backlogS1")}
            </FitText>
            <FitText x="93" y="394" maxWidth={112} textAnchor="middle" fontSize="8.5" className="f-ink2">
              {t("flow.cycle.backlogS2")}
            </FitText>
          </g>
          {/* ＋ añadir al Product Backlog — abre el modal de solicitud de cambio.
              foreignObject: botón HTML real (accesible) anclado bajo la estación. */}
          <foreignObject x="8" y="422" width="168" height="24">
            <button
              className="cyc-add"
              onClick={(e) => {
                e.stopPropagation();
                onAddBacklog();
              }}
            >
              {t("flow.cycle.backlogAdd")}
            </button>
          </foreignObject>

          {/* flecha backlog -> planning */}
          <path d="M168 366 H 216" className="ar-o" markerEnd="url(#cycAO)" />

          {/* ============ SPRINT PLANNING (ceremonia · control) ============ */}
          {ceremonyBox(
            v.planning,
            { x: 222, y: 330, w: 118, h: 72, cx: 281 },
            [t("flow.cycle.planningT1"), t("flow.cycle.planningT2")],
            t("flow.cycle.planningS1"),
          )}

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
            <FitText x="450" y="384" maxWidth={104} textAnchor="middle" fontSize="8.5" className="f-ink2">
              {t("flow.cycle.sprintBacklogS1")}
            </FitText>
            <FitText x="450" y="395" maxWidth={104} textAnchor="middle" fontSize="8.5" className="f-ink2">
              {t("flow.cycle.sprintBacklogS2")}
            </FitText>
          </g>

          {/* flecha sprint backlog -> ciclo sprint */}
          <path d="M506 366 C 550 366, 560 330, 590 300" className="ar-o" markerEnd="url(#cycAO)" />

          {/* ============ CÍRCULO DAILY (control · configurar canal) ============ */}
          <g className="cyc-station" onClick={() => onOpenSettings("notifications")} role="button" aria-label={t("flow.cycle.dailyS")}>
            <circle cx="660" cy="118" r="46" fill="none" className="cyc-daily" />
            <text x="660" y="110" textAnchor="middle" fontSize="10.5" fontWeight="800" className="f-blue">
              {t("flow.cycle.dailyT1")}
            </text>
            <text x="660" y="123" textAnchor="middle" fontSize="10.5" fontWeight="800" className="f-blue">
              {t("flow.cycle.dailyT2")}
            </text>
            <FitText x="660" y="137" maxWidth={80} textAnchor="middle" fontSize="8" className="f-blue">
              {t("flow.cycle.dailyS")}
            </FitText>
          </g>
          <path d="M660 72 A 46 46 0 0 1 706 118" className="ar-b" markerEnd="url(#cycAB)" />
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
            {/* nombre real del sprint (o "SPRINT N"): truncado + tooltip con el completo */}
            <FitText x="660" y="268" maxWidth={186} textAnchor="middle" fontSize="14" fontWeight="900" className="f-orange">
              <title>{sprintName}</title>
              {sprintNameShort}
            </FitText>
            <g fontSize="9.5" fontWeight="600" className="f-ink">
              <FitText x="660" y="288" maxWidth={188} textAnchor="middle" className="f-ink">
                {t("flow.cycle.sprintL1")}
              </FitText>
              <FitText x="660" y="303" maxWidth={190} textAnchor="middle" className="f-ink">
                {t("flow.cycle.sprintL2")}
              </FitText>
              <FitText x="660" y="318" maxWidth={186} textAnchor="middle" className="f-ink">
                {t("flow.cycle.sprintL3")}
              </FitText>
            </g>
            <FitText x="660" y="340" maxWidth={178} textAnchor="middle" fontSize="8.5" fontWeight="700" className="f-blue">
              {t("flow.cycle.sprintB1")}
            </FitText>
            <FitText x="660" y="351" maxWidth={178} textAnchor="middle" fontSize="8.5" fontWeight="700" className="f-blue">
              {t("flow.cycle.sprintB2")}
            </FitText>
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
            <FitText x="828" y="483" maxWidth={112} textAnchor="middle" fontSize="8.5" className="f-ink2">
              {t("flow.cycle.incrementS1")}
            </FitText>
            <FitText x="828" y="495" maxWidth={112} textAnchor="middle" fontSize="8.5" fontWeight="700" className="f-blue">
              {v.increment.previewRunId ? `▶ ${t("flow.cycle.incrementPreview")}` : t("flow.cycle.incrementS2")}
            </FitText>
          </g>

          {/* flecha incremento -> review */}
          <path d="M768 477 H 700" className="ar-b" markerEnd="url(#cycAB)" />

          {/* ============ SPRINT REVIEW (ceremonia · control) ============ */}
          {ceremonyBox(
            v.review,
            { x: 576, y: 446, w: 118, h: 62, cx: 635 },
            [t("flow.cycle.reviewT")],
            t("flow.cycle.reviewS1"),
          )}

          {/* flecha review -> retro */}
          <path d="M576 477 H 508" className="ar-b" markerEnd="url(#cycAB)" />

          {/* ============ RETROSPECTIVA (ceremonia · control) ============ */}
          {ceremonyBox(
            v.retro,
            { x: 384, y: 446, w: 118, h: 62, cx: 443 },
            [t("flow.cycle.retroT")],
            t("flow.cycle.retroS1"),
          )}

          {/* flecha retro -> planning (cierra el círculo) */}
          <path d="M384 477 C 300 477, 281 460, 281 408" className="ar-b" markerEnd="url(#cycAB)" />
          <text x="322" y="452" textAnchor="middle" fontSize="9" fontWeight="700" className="f-blue">
            {t("flow.cycle.loopLabel")}
          </text>

          {/* ============ EVOLUCIÓN: polilínea ORTOGONAL por los márgenes ============
              sube por la derecha desde INCREMENT, corre por el borde superior, baja al
              Product Backlog — sin cruzar ninguna caja ni texto del viewBox. */}
          <path d="M888 446 V 24 H 93 V 110" className="ar-o-thin" markerEnd="url(#cycAO)" />
          <text x="440" y="15" textAnchor="middle" fontSize="10" fontWeight="700" className="f-orange">
            {t("flow.cycle.evolutionLabel")}
          </text>

          {/* leyenda — re-etiquetada a estado VIVO conservando el lenguaje visual */}
          <g transform="translate(22,522)">
            <line x1="0" y1="0" x2="26" y2="0" className="leg-o" />
            <text x="32" y="4" fontSize="10" fontWeight="600" className="f-ink">
              {t("flow.cycle.legendDone")}
            </text>
            <line x1="180" y1="0" x2="206" y2="0" className="leg-b" />
            <text x="212" y="4" fontSize="10" fontWeight="600" className="f-ink">
              {t("flow.cycle.legendPending")}
            </text>
            <text x="420" y="4" fontSize="10" fontWeight="700" className="f-amber">
              {t("flow.cycle.legendGate")}
            </text>
          </g>
        </svg>
      </div>
    </div>
  );
}
