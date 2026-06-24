// SETTINGS — conexiones · seguridad · política (doc 16 §2.7). SHELL fiel al mockup con fixtures.
// TODO(endpoint): store + GET/PUT /settings con secciones {mcp connections, agent auth mode,
// sandbox config, merge policy by risk} — secretos nunca devueltos en claro (doc 17 §1 T1).

const CARDS = [
  {
    h: "Tickets (MCP)",
    role: "El backlog vive en JIRA/GitHub; la fábrica los lee por MCP.",
    kv: [
      ["GitHub", "conectado ✓"],
      ["JIRA", "conectar…"],
    ],
  },
  {
    h: "Auth del agente",
    role: "Cómo corre Claude dentro del sandbox.",
    kv: [
      ["modo", "oauth_token (Max)"],
      ["egress", "allowlist · api.anthropic.com"],
    ],
  },
  {
    h: "Política de merge",
    role: "Por nivel de riesgo.",
    kv: [
      ["bajo riesgo", "automerge"],
      ["auth/pagos/migrac.", "human_gate"],
    ],
  },
  {
    h: "Sandbox",
    role: "Aislamiento de la ejecución.",
    kv: [
      ["runtime", "docker · gVisor (runsc)"],
      ["imagen agente", "vibeforge-agent"],
    ],
  },
  {
    h: "Miembros",
    role: "Roles y permisos.",
    kv: [
      ["nmlemus", "owner"],
      ["+ invitar…", ""],
    ],
  },
  {
    h: "Proyectos",
    role: "Multi-tenant.",
    kv: [
      ["3 activos", ""],
      ["+ nuevo proyecto", ""],
    ],
  },
];

export function SettingsView() {
  return (
    <div className="wrap">
      <div className="shellnote">
        🔌 Shell — el store de settings aún no existe.{" "}
        <span className="mono">TODO: GET/PUT /settings (mcp · auth · sandbox · merge-policy)</span>
      </div>

      <div className="sectitle">
        <h2>Settings</h2>
        <span className="c">conexiones · seguridad · política</span>
      </div>

      <div className="grid3">
        {CARDS.map((c) => (
          <div className="card" key={c.h}>
            <h3>{c.h}</h3>
            <div className="role">{c.role}</div>
            <div className="kv">
              {c.kv.map(([k, v], i) => (
                <span key={i}>
                  {k}
                  {v ? (
                    <>
                      : <b>{v}</b>
                    </>
                  ) : null}
                </span>
              ))}
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
