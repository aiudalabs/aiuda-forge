// STUDIO — plano de diseño conversacional, BMAD-like (doc 16 §2.4). SHELL fiel al mockup.
// TODO(endpoint): servicio Studio (doc 17 §3 T5) — GET/POST proyectos/fases/artifacts/handoff,
// fases corridas como turnos claude --resume con aprobación humana por fase.

export function StudioView() {
  return (
    <div className="wrap">
      <div className="shellnote">
        🔌 Shell — el servicio Studio aún no existe.{" "}
        <span className="mono">TODO: GET/POST /studio/projects · /phases · /artifacts · handoff→GitHub</span>
      </div>

      <div className="sectitle">
        <h2>Studio — diseño del producto</h2>
        <span className="c">enfoque BMAD · fase por fase</span>
        <span className="sp" />
        <button className="btn ghost sm">+ Nuevo proyecto</button>
      </div>

      <div className="studio">
        <div className="phases">
          <div className="phase done">
            <div className="num">✓</div>
            <div className="nm">
              Discovery <small>PRODUCT_BRIEF.md · OPINIONATED_DEFAULTS.md</small>
            </div>
            <span className="pill done">aprobada</span>
          </div>
          <div className="phase done">
            <div className="num">✓</div>
            <div className="nm">
              PRD <small>PRD.md · rev.2</small>
            </div>
            <span className="pill done">aprobada</span>
          </div>
          <div className="phase cur">
            <div className="num">3</div>
            <div className="nm">
              Arquitectura <small>ARCHITECTURE.md — esperando tu aprobación</small>
            </div>
            <span className="pill await">en revisión</span>
          </div>
          <div className="phase todo">
            <div className="num">4</div>
            <div className="nm">
              UI / Pantallas <small>UI_SCREENS.md</small>
            </div>
            <span className="pill queued">pendiente</span>
          </div>
          <div className="phase todo">
            <div className="num">5</div>
            <div className="nm">
              Backlog / Governance <small>ISSUES.md → handoff a GitHub</small>
            </div>
            <span className="pill queued">pendiente</span>
          </div>
        </div>

        <div className="chat">
          <div className="eyebrow acc">Conversación</div>
          <div className="msg b">
            <b>Studio:</b> Listé la arquitectura: 14 endpoints, 4 tablas nuevas. ¿La apruebo y sigo
            al backlog, o ajustamos?
          </div>
          <div className="msg h">Aprueba y genera el backlog</div>
          <div className="msg b">
            <b>Studio:</b> Hecho. 18 tickets en 3 olas; los publico como Issues en GitHub para que la
            fábrica los tome. <b>[Handoff → GitHub]</b>
          </div>
          <div style={{ display: "flex", gap: 8 }}>
            <button className="btn ghost sm" style={{ flex: 1 }}>
              Ver ARCHITECTURE.md
            </button>
            <button className="btn primary sm" style={{ flex: 1 }}>
              Aprobar fase 3
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
