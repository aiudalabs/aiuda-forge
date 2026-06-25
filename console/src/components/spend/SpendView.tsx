"use client";

// GASTO / Analytics (doc 16 §2.6).
// Cableado contra GET /metrics del control-plane.
// Muestra total_cost_usd, cost_by_workflow, cost_by_step, acceptance_rate.

import { useMetrics } from "@/lib/hooks";

// ─────────────────────────────────────────────────────────────────────────────
// Helpers de formato
// ─────────────────────────────────────────────────────────────────────────────

function fmt$(n: number) {
  return `$${n.toFixed(2)}`;
}

function fmtPct(r: number) {
  return `${Math.round(r * 100)}%`;
}

// ─────────────────────────────────────────────────────────────────────────────
// Componente raíz
// ─────────────────────────────────────────────────────────────────────────────

export function SpendView() {
  const { data, isLoading, isError } = useMetrics();

  if (isLoading) {
    return (
      <div className="wrap">
        <div className="placeholder">
          <div className="ph-ic"><span className="spin" /></div>
          Cargando métricas…
        </div>
      </div>
    );
  }

  if (isError || !data) {
    return (
      <div className="wrap">
        <div className="placeholder err">
          <div className="ph-ic">⚠</div>
          No se pudo cargar las métricas.
        </div>
      </div>
    );
  }

  // Derivar el costo por flujo más alto para la barra de referencia (100%).
  // Guardamos ?? {} para tolerar campos ausentes en respuestas sparse del backend.
  const wfEntries = Object.entries(data.cost_by_workflow ?? {}).sort((a, b) => b[1] - a[1]);
  // Mínimo 0.001 para evitar división por cero cuando el único entry tiene coste 0.
  const maxWf = Math.max(wfEntries[0]?.[1] ?? 0, 0.001);

  const stepEntries = Object.entries(data.cost_by_step ?? {}).sort((a, b) => b[1] - a[1]);
  const maxStep = Math.max(stepEntries[0]?.[1] ?? 0, 0.001);

  // cost-per-accepted-change: total / runs aceptados (done).
  const doneCount = data.by_status?.DONE ?? 0;
  const costPerAccepted = doneCount > 0 ? data.total_cost_usd / doneCount : 0;

  return (
    <div className="wrap">
      <div className="sectitle">
        <h2>Gasto</h2>
        <span className="c">tokens y $ · métricas en vivo</span>
      </div>

      {/* KPIs principales */}
      <div className="stats">
        <div className="stat">
          <div className="eyebrow">Total acumulado</div>
          <div className="n serif">{fmt$(data.total_cost_usd)}</div>
          <div className="sub">todos los runs</div>
        </div>
        <div className="stat">
          <div className="eyebrow">Costo / PR aceptado</div>
          <div className="n acc serif">{doneCount > 0 ? fmt$(costPerAccepted) : "—"}</div>
          <div className="sub">cost-per-accepted-change</div>
        </div>
        <div className="stat">
          <div className="eyebrow">Aceptación</div>
          <div className="n em serif">{fmtPct(data.acceptance_rate ?? 0)}</div>
          <div className="sub">runs → PR mergeable</div>
        </div>
        <div className="stat">
          <div className="eyebrow">Runs DONE</div>
          <div className="n serif">{doneCount}</div>
          <div className="sub">de {Object.values(data.by_status ?? {}).reduce((a, b) => a + b, 0)} totales</div>
        </div>
      </div>

      {/* Por workflow */}
      {wfEntries.length > 0 && (
        <>
          <div className="sectitle">
            <h2>Por workflow</h2>
          </div>
          <div className="bars">
            {wfEntries.map(([name, cost]) => (
              <div className="bar" key={name}>
                <span>{name}</span>
                <div className="track">
                  <div className="fill" style={{ width: `${(cost / maxWf) * 100}%` }} />
                </div>
                <span className="v">{fmt$(cost)}</span>
              </div>
            ))}
          </div>
        </>
      )}

      {/* Por paso */}
      {stepEntries.length > 0 && (
        <>
          <div className="sectitle">
            <h2>Por paso</h2>
          </div>
          <div className="bars">
            {stepEntries.map(([name, cost]) => (
              <div className="bar" key={name}>
                <span>{name}</span>
                <div className="track">
                  <div className="fill" style={{ width: `${(cost / maxStep) * 100}%` }} />
                </div>
                <span className="v">{fmt$(cost)}</span>
              </div>
            ))}
          </div>
        </>
      )}

      {/* Por estado */}
      {Object.keys(data.by_status ?? {}).length > 0 && (
        <>
          <div className="sectitle">
            <h2>Runs por estado</h2>
          </div>
          <div className="kv" style={{ display: "flex", gap: 16, flexWrap: "wrap", padding: "8px 0" }}>
            {Object.entries(data.by_status ?? {}).map(([status, count]) => (
              <span key={status}>
                {status}: <b>{count}</b>
              </span>
            ))}
          </div>
        </>
      )}
    </div>
  );
}
