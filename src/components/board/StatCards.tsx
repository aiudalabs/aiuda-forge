// Stat cards del board (doc 16 §2.1): en ejecución · esperan aprobación · PRs abiertos · costo.

import { useStats } from "@/lib/hooks";

export function StatCards() {
  const { data, isLoading } = useStats();
  const s = data ?? { running: 0, awaiting: 0, openPRs: 0, projectCost: 0 };

  return (
    <div className="stats">
      <div className="stat">
        <div className="eyebrow">En ejecución</div>
        <div className="n acc">{isLoading ? "—" : s.running}</div>
        <div className="sub">agentes trabajando</div>
      </div>
      <div className="stat">
        <div className="eyebrow">Esperan aprobación</div>
        <div className="n">{isLoading ? "—" : s.awaiting}</div>
        <div className="sub">human gate</div>
      </div>
      <div className="stat">
        <div className="eyebrow">PRs abiertos</div>
        <div className="n em">{isLoading ? "—" : s.openPRs}</div>
        <div className="sub">listos para revisar</div>
      </div>
      <div className="stat">
        <div className="eyebrow">Costo del proyecto</div>
        <div className="n serif">${s.projectCost.toFixed(2)}</div>
        <div className="sub">crédito Max</div>
      </div>
    </div>
  );
}
