"use client";

// GASTO / Analytics (doc 16 §2.6).
// Cableado contra GET /metrics del control-plane.
// Muestra total_cost_usd, cost_by_workflow, cost_by_step, acceptance_rate.

import { useMetrics, useGitHubSpend } from "@/lib/hooks";
import { useActiveProjectId } from "@/lib/activeProject";
import { useT } from "@/lib/i18n";
import type { CSSProperties } from "react";

// ─────────────────────────────────────────────────────────────────────────────
// Helpers de formato
// ─────────────────────────────────────────────────────────────────────────────

function fmt$(n: number) {
  return `$${n.toFixed(2)}`;
}

function fmtPct(r: number) {
  return `${Math.round(r * 100)}%`;
}

// Compact integer format: 845 · 12.3K · 4.1M.
function fmtNum(n: number) {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}K`;
  return `${n}`;
}

// Cantidad medida (minutos, requests, GB-h): puede ser fraccionaria.
function fmtQty(n: number) {
  return n.toLocaleString(undefined, { maximumFractionDigits: 1 });
}

// ─────────────────────────────────────────────────────────────────────────────
// Sección "GitHub" — gasto medido del ciclo actual (F4). Degrada a placeholder
// con la razón si GitHub no expone la facturación del owner del repo.
// ─────────────────────────────────────────────────────────────────────────────

const thCell: CSSProperties = { padding: "8px 12px", fontWeight: 700 };
const tdCell: CSSProperties = { padding: "8px 12px", color: "var(--ink2)" };
const numCell: CSSProperties = { textAlign: "right", fontVariantNumeric: "tabular-nums" };

function GitHubSpendSection({ projectId }: { projectId: string | null }) {
  const t = useT();
  const { data, isLoading } = useGitHubSpend(projectId);

  if (!projectId) return null;

  const items = data?.items ?? [];

  return (
    <>
      <div className="sectitle">
        <h2>{t("spend.github.title")}</h2>
        {data?.available && data.cycle && (
          <span className="c">{t("spend.github.cycle", { cycle: data.cycle })}</span>
        )}
      </div>

      {isLoading ? (
        <div className="placeholder">
          <div className="ph-ic"><span className="spin" /></div>
          {t("spend.github.loading")}
        </div>
      ) : !data || !data.available ? (
        <div className="placeholder">
          <div className="ph-ic">ⓘ</div>
          {data?.reason ?? t("spend.github.unavailable")}
        </div>
      ) : (
        <>
          <div className="stats">
            <div className="stat">
              <div className="eyebrow">{t("spend.github.copilot")}</div>
              <div className="n serif">{fmt$(data.totals?.copilot ?? 0)}</div>
              <div className="sub">{t("spend.github.copilot.sub")}</div>
            </div>
            <div className="stat">
              <div className="eyebrow">{t("spend.github.actions")}</div>
              <div className="n serif">{fmt$(data.totals?.actions ?? 0)}</div>
              <div className="sub">{t("spend.github.actions.sub")}</div>
            </div>
          </div>

          {items.length > 0 ? (
            <div className="ttable" style={{ overflowX: "auto", marginTop: 4 }}>
              <table style={{ width: "100%", borderCollapse: "collapse", fontSize: 13 }}>
                <thead>
                  <tr
                    style={{
                      textAlign: "left",
                      background: "var(--bg2)",
                      color: "var(--ink4)",
                      fontSize: 11,
                      letterSpacing: "0.06em",
                      textTransform: "uppercase",
                    }}
                  >
                    <th style={thCell}>{t("spend.github.col.product")}</th>
                    <th style={thCell}>{t("spend.github.col.sku")}</th>
                    <th style={{ ...thCell, ...numCell }}>{t("spend.github.col.qty")}</th>
                    <th style={{ ...thCell, ...numCell }}>{t("spend.github.col.gross")}</th>
                    <th style={{ ...thCell, ...numCell }}>{t("spend.github.col.net")}</th>
                  </tr>
                </thead>
                <tbody>
                  {items.map((it) => (
                    <tr key={it.product + "·" + it.sku} style={{ borderTop: "1px solid var(--stroke)" }}>
                      <td style={tdCell}>{it.product}</td>
                      <td style={tdCell}>{it.sku}</td>
                      <td style={{ ...tdCell, ...numCell }}>
                        {fmtQty(it.quantity)} <span style={{ color: "var(--ink4)" }}>{it.unit_type}</span>
                      </td>
                      <td style={{ ...tdCell, ...numCell }}>{fmt$(it.gross_amount)}</td>
                      <td style={{ ...tdCell, ...numCell }}>{fmt$(it.net_amount)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          ) : (
            <div className="placeholder">
              <div className="ph-ic">ⓘ</div>
              {t("spend.github.noUsage")}
            </div>
          )}
        </>
      )}
    </>
  );
}

// ─────────────────────────────────────────────────────────────────────────────
// Componente raíz
// ─────────────────────────────────────────────────────────────────────────────

export function SpendView() {
  const t = useT();
  const projectId = useActiveProjectId();
  const { data, isLoading, isError } = useMetrics(projectId);

  if (isLoading) {
    return (
      <div className="wrap">
        <div className="placeholder">
          <div className="ph-ic"><span className="spin" /></div>
          {t("spend.loading")}
        </div>
      </div>
    );
  }

  if (isError || !data) {
    return (
      <div className="wrap">
        <div className="placeholder err">
          <div className="ph-ic">⚠</div>
          {t("spend.error")}
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
  const tokensIn = data.total_tokens_in ?? 0;
  const tokensOut = data.total_tokens_out ?? 0;

  return (
    <div className="wrap">
      <div className="sectitle">
        <h2>{t("spend.title")}</h2>
        <span className="c">{t("spend.subtitle")}</span>
      </div>

      {/* Gasto medido en GitHub (F4) — al inicio, antes de las métricas del kernel. */}
      <GitHubSpendSection projectId={projectId} />

      {/* KPIs principales */}
      <div className="stats">
        <div className="stat">
          <div className="eyebrow">{t("spend.kpi.total")}</div>
          <div className="n serif">{fmt$(data.total_cost_usd)}</div>
          <div className="sub">{t("spend.kpi.total.sub")}</div>
        </div>
        <div className="stat">
          <div className="eyebrow">{t("spend.kpi.costPerPr")}</div>
          <div className="n acc serif">{doneCount > 0 ? fmt$(costPerAccepted) : "—"}</div>
          <div className="sub">{t("spend.kpi.costPerPr.sub")}</div>
        </div>
        <div className="stat">
          <div className="eyebrow">{t("spend.kpi.acceptance")}</div>
          <div className="n em serif">{fmtPct(data.acceptance_rate ?? 0)}</div>
          <div className="sub">{t("spend.kpi.acceptance.sub")}</div>
        </div>
        <div className="stat">
          <div className="eyebrow">{t("spend.kpi.runsDone")}</div>
          <div className="n serif">{doneCount}</div>
          <div className="sub">{t("spend.kpi.runsDone.sub", { n: Object.values(data.by_status ?? {}).reduce((a, b) => a + b, 0) })}</div>
        </div>
      </div>

      {/* Uso del agente — significativo incluso en suscripción (donde el $ puede ser 0). */}
      <div className="stats" style={{ marginTop: 0 }}>
        <div className="stat">
          <div className="eyebrow">{t("spend.kpi.agentCalls")}</div>
          <div className="n serif">{fmtNum(data.agent_calls ?? 0)}</div>
          <div className="sub">{t("spend.kpi.agentCalls.sub")}</div>
        </div>
        <div className="stat">
          <div className="eyebrow">{t("spend.kpi.turns")}</div>
          <div className="n serif">{fmtNum(data.total_turns ?? 0)}</div>
          <div className="sub">{t("spend.kpi.turns.sub")}</div>
        </div>
        <div className="stat">
          <div className="eyebrow">{t("spend.kpi.tokensIn")}</div>
          <div className="n serif">{tokensIn > 0 ? fmtNum(tokensIn) : "—"}</div>
          <div className="sub">{tokensIn > 0 ? t("spend.kpi.tokensIn.sub") : t("spend.kpi.tokens.fromNext")}</div>
        </div>
        <div className="stat">
          <div className="eyebrow">{t("spend.kpi.tokensOut")}</div>
          <div className="n serif">{tokensOut > 0 ? fmtNum(tokensOut) : "—"}</div>
          <div className="sub">{tokensOut > 0 ? t("spend.kpi.tokensOut.sub") : t("spend.kpi.tokens.fromNext")}</div>
        </div>
      </div>

      {/* Por workflow */}
      {wfEntries.length > 0 && (
        <>
          <div className="sectitle">
            <h2>{t("spend.byWorkflow")}</h2>
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
            <h2>{t("spend.byStep")}</h2>
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
            <h2>{t("spend.byStatus")}</h2>
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
