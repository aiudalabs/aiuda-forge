// TICKETS — espejo de JIRA/GitHub por MCP (doc 16 §2.3). SHELL fiel al mockup con fixtures.
// TODO(endpoint): GET /tickets (lo expone el Orquestador / T1, doc 17 §3) — id, título, estado,
// owner/lane, ola, depends_on, run asociado + PR. Aquí no se editan tickets (viven en JIRA/GitHub).

const TICKETS = [
  { id: "ENG-1", title: "is_valid_email", owner: "python-dev", dep: "—", status: "done", label: "done" },
  { id: "ENG-2", title: "to_roman", owner: "python-dev", dep: "—", status: "done", label: "done" },
  { id: "ENG-3", title: "fib", owner: "python-dev", dep: "ENG-1", status: "done", label: "done" },
  { id: "ENG-12", title: "reverse_words", owner: "python-dev", dep: "—", status: "await", label: "awaiting" },
  { id: "ENG-14", title: "validador cédula", owner: "python-dev", dep: "ENG-1", status: "run_", label: "running" },
  { id: "ENG-15", title: "formato fecha PA", owner: "python-dev", dep: "ENG-14", status: "queued", label: "bloqueado" },
];

export function TicketsView() {
  return (
    <div className="wrap">
      <div className="shellnote">
        🔌 Shell — cableado pendiente. <span className="mono">TODO: GET /tickets</span> (espejo
        JIRA/GitHub por MCP, lo expone el orquestador).
      </div>

      <div className="sectitle">
        <h2>Tickets</h2>
        <span className="c">espejo de GitHub · MCP</span>
        <span className="sp" />
        <span className="tag">conectado ✓ nmlemus/manitaspty</span>
      </div>

      <div className="ttable">
        <div className="trow">
          <span>ID</span>
          <span>Título</span>
          <span>Owner / lane</span>
          <span>Depende de</span>
          <span>Estado</span>
        </div>
        {TICKETS.map((t) => (
          <div className="trow" key={t.id}>
            <span className="id">{t.id}</span>
            <span>{t.title}</span>
            <span>{t.owner}</span>
            <span className="dep">{t.dep}</span>
            <span>
              <span className={`pill ${t.status}`}>{t.label}</span>
            </span>
          </div>
        ))}
      </div>

      <div className="sectitle">
        <h2>Grafo de dependencias</h2>
        <span className="c">qué está READY vs bloqueado</span>
      </div>
      <div className="dag">
        <div className="dagrow">
          <span className="node done">ENG-1 ✓</span>
          <span className="arr">──▶</span>
          <span className="node done">ENG-3 ✓</span>
        </div>
        <div className="dagrow">
          <span className="node done" style={{ visibility: "hidden" }}>
            ENG-1
          </span>
          <span className="arr" style={{ visibility: "hidden" }}>
            ──▶
          </span>
          <span className="node ready">ENG-14 ⟳ running</span>
          <span className="arr">──▶</span>
          <span className="node blocked">ENG-15 ⏳ bloqueado por ENG-14</span>
        </div>
        <div className="dagrow">
          <span className="node await">ENG-12 ⏸ awaiting</span>
        </div>
      </div>
    </div>
  );
}
