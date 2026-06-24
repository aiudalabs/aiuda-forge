// GASTO / Analytics (doc 16 §2.6). SHELL fiel al mockup con fixtures.
// TODO(endpoint): GET /metrics + /analytics con desglose por proyecto/agente/modelo/workflow,
// cost-per-accepted-change, techo Max y % consumido, alertas que disparan /control/pause.

const PROJECTS = [
  { name: "manitaspty", pct: 72, value: "$2.71" },
  { name: "comandaya", pct: 41, value: "$1.55" },
  { name: "recepia", pct: 23, value: "$0.86" },
];

export function SpendView() {
  return (
    <div className="wrap">
      <div className="shellnote">
        🔌 Shell — KPIs con fixtures. <span className="mono">TODO: GET /metrics · /analytics</span>{" "}
        (desglose por proyecto/agente/modelo).
      </div>

      <div className="sectitle">
        <h2>Gasto</h2>
        <span className="c">tokens y $ · crédito Max</span>
      </div>

      <div className="stats">
        <div className="stat">
          <div className="eyebrow">Hoy</div>
          <div className="n serif">$3.42</div>
          <div className="sub">312k tokens</div>
        </div>
        <div className="stat">
          <div className="eyebrow">Costo / PR aceptado</div>
          <div className="n acc serif">$0.31</div>
          <div className="sub">cost-per-accepted-change</div>
        </div>
        <div className="stat">
          <div className="eyebrow">Mes</div>
          <div className="n serif">$41.80</div>
          <div className="sub">de techo $100 (Max 5×)</div>
        </div>
        <div className="stat">
          <div className="eyebrow">Aceptación</div>
          <div className="n em serif">86%</div>
          <div className="sub">runs → PR mergeable</div>
        </div>
      </div>

      <div className="sectitle">
        <h2>Por proyecto</h2>
      </div>
      <div className="bars">
        {PROJECTS.map((p) => (
          <div className="bar" key={p.name}>
            <span>{p.name}</span>
            <div className="track">
              <div className="fill" style={{ width: `${p.pct}%` }} />
            </div>
            <span className="v">{p.value}</span>
          </div>
        ))}
      </div>
    </div>
  );
}
