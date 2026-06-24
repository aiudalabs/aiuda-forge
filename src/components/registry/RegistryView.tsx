"use client";

// REGISTRY — agentes / skills / workflows, no-code (doc 16 §2.5). SHELL fiel al mockup: las
// tarjetas abren los editores (modal de agente, compositor de workflow) con datos de ejemplo.
// TODO(endpoint): GET/POST/PUT/DELETE /registry/{agents,skills,workflows} + validación de schema
// + "probar agente" (doc 17 §1: LIST/create/delete/skills/validar aún faltan).

import { useState } from "react";

type Modal = { kind: "agent" | "wf"; name: string } | null;

export function RegistryView() {
  const [modal, setModal] = useState<Modal>(null);

  return (
    <div className="wrap">
      <div className="shellnote">
        🔌 Shell — edición/validación real pendiente.{" "}
        <span className="mono">
          TODO: GET/POST/DELETE /registry/{"{agents,skills,workflows}"} + validar schema + probar
        </span>
      </div>

      <div className="sectitle">
        <h2>Registry — agentes</h2>
        <span className="c">editable · sin código</span>
        <span className="sp" />
        <button className="btn ghost sm" onClick={() => setModal({ kind: "agent", name: "Nuevo agente" })}>
          + Nuevo
        </button>
        <button className="btn ghost sm">⤓ Importar (BMAD / aiuda-stack)</button>
      </div>

      <div className="grid3">
        <div className="card click" onClick={() => setModal({ kind: "agent", name: "dev" })}>
          <h3>
            dev <span className="badge opus">opus 4.8</span>
          </h3>
          <div className="role">Implementa el ticket dejando el árbol modificado, sin commit.</div>
          <div className="kv">
            <span>
              skills: <b>coding-conventions</b>
            </span>
            <span>
              tools: <b>read · edit · write · bash</b>
            </span>
          </div>
        </div>
        <div className="card click" onClick={() => setModal({ kind: "agent", name: "reviewer" })}>
          <h3>
            reviewer <span className="badge sonnet">sonnet 4.6</span>
          </h3>
          <div className="role">
            Revisa adversarialmente — busca el bug que el dev no vio. Modelo distinto = cross-model.
          </div>
          <div className="kv">
            <span>
              tools: <b>read · bash</b>
            </span>
            <span>
              prompt: <b>adversarial</b>
            </span>
          </div>
        </div>
        <div className="card click" onClick={() => setModal({ kind: "agent", name: "verifier" })}>
          <h3>
            verifier <span className="badge sonnet">sonnet 4.6</span>
          </h3>
          <div className="role">
            Verificador fresco: maneja la app/tests y juzga works|broken con evidencia.
          </div>
          <div className="kv">
            <span>
              tools: <b>read · bash</b>
            </span>
            <span>
              on_fail → <b>implement</b>
            </span>
          </div>
        </div>
      </div>

      <div className="sectitle">
        <h2>Workflows</h2>
        <span className="c">grafos en datos (YAML)</span>
        <span className="sp" />
        <button className="btn ghost sm" onClick={() => setModal({ kind: "wf", name: "Nuevo workflow" })}>
          + Nuevo
        </button>
      </div>

      <div className="grid3">
        <div className="card click" onClick={() => setModal({ kind: "wf", name: "factory" })}>
          <h3>factory</h3>
          <div className="role mono" style={{ fontSize: 11.5 }}>
            implement → gate → review → pr
          </div>
          <div className="kv">
            <span>
              <b>+ paso = editar YAML</b>, cero código
            </span>
          </div>
        </div>
        <div className="card click" onClick={() => setModal({ kind: "wf", name: "factory-plus" })}>
          <h3>factory-plus</h3>
          <div className="role mono" style={{ fontSize: 11.5 }}>
            implement → gate → review → verify → human_gate → pr
          </div>
          <div className="kv">
            <span>verify agéntica + gate humano</span>
          </div>
        </div>
        <div className="card click" onClick={() => setModal({ kind: "wf", name: "gated" })}>
          <h3>gated</h3>
          <div className="role mono" style={{ fontSize: 11.5 }}>
            implement → gate → human_gate → pr
          </div>
          <div className="kv">
            <span>gobernanza simple</span>
          </div>
        </div>
      </div>

      <div className={`overlay ${modal ? "on" : ""}`} onClick={() => setModal(null)} />
      {modal?.kind === "agent" && <AgentModal name={modal.name} onClose={() => setModal(null)} />}
      {modal?.kind === "wf" && <WorkflowModal name={modal.name} onClose={() => setModal(null)} />}
    </div>
  );
}

function AgentModal({ name, onClose }: { name: string; onClose: () => void }) {
  const isNew = name === "Nuevo agente";
  return (
    <div className="modal on">
      <div className="mh">
        <h3>{isNew ? "Nuevo agente" : `Editar agente · ${name}`}</h3>
        <button className="x" onClick={onClose}>
          ✕
        </button>
      </div>
      <div className="mb">
        <div className="row2">
          <div className="field">
            <label>ID</label>
            <input className="inp mono" defaultValue={isNew ? "" : "reviewer"} />
          </div>
          <div className="field">
            <label>Versión</label>
            <input className="inp mono" defaultValue="1.0.0" />
          </div>
        </div>
        <div className="row2">
          <div className="field">
            <label>
              Modelo <span style={{ color: "var(--accent)" }}>(cross-model)</span>
            </label>
            <input className="inp" defaultValue="claude-sonnet-4-6" />
          </div>
          <div className="field">
            <label>Effort</label>
            <input className="inp" defaultValue="high" />
          </div>
        </div>
        <div className="field">
          <label>Rol / persona</label>
          <input
            className="inp"
            defaultValue="Revisa adversarialmente el árbol — encuentra el bug que el implementer no vio."
          />
        </div>
        <div className="field">
          <label>Tools (allowlist)</label>
          <div className="chips">
            <span className="chip on">read</span>
            <span className="chip on">bash</span>
            <span className="chip">edit</span>
            <span className="chip">write</span>
          </div>
        </div>
        <div className="field">
          <label>Skills</label>
          <div className="chips">
            <span className="chip on">security-checklist</span>
            <span className="chip">coding-conventions</span>
            <span className="chip">+ añadir</span>
          </div>
        </div>
        <div className="row2">
          <div className="field">
            <label>Entradas (tipadas)</label>
            <input className="inp mono" defaultValue="ticket, diff_hint" />
          </div>
          <div className="field">
            <label>Salidas (tipadas)</label>
            <input className="inp mono" defaultValue="verdict, notes[]" />
          </div>
        </div>
        <div className="row2">
          <div className="field">
            <label>Review</label>
            <input className="inp" defaultValue="cross-model (adversarial)" />
          </div>
          <div className="field">
            <label>Approval</label>
            <input className="inp" defaultValue="risk-policy" />
          </div>
        </div>
        <div style={{ display: "flex", gap: 10, marginTop: 8 }}>
          <button className="btn ghost" style={{ flex: 1 }} onClick={onClose}>
            Cancelar
          </button>
          <button className="btn ghost" style={{ flex: 1 }}>
            ▶ Probar agente
          </button>
          <button className="btn primary" style={{ flex: 1 }} onClick={onClose}>
            Guardar (valida schema)
          </button>
        </div>
        <p style={{ fontSize: 12, color: "var(--ink4)", marginTop: 12 }}>
          El I/O tipado deja que la UI conecte este agente solo a pasos compatibles. &quot;Probar&quot; lo
          corre sobre un input de ejemplo antes de cablearlo. Se guarda como manifest YAML; cero
          código.
        </p>
      </div>
    </div>
  );
}

function WorkflowModal({ name, onClose }: { name: string; onClose: () => void }) {
  const nodes = [
    { pt: "agent", pn: "implement", pm: "dev · opus", conn: "fail" },
    { pt: "gate", pn: "gate", pm: "on_fail → implement (×2)", conn: "" },
    { pt: "agent", pn: "review", pm: "reviewer · sonnet (cross-model)", conn: "fail" },
    { pt: "agentic_verify", pn: "verify", pm: "verifier · on_fail → implement", conn: "" },
    { pt: "human_gate", pn: "approve", pm: "pausa + notifica", conn: "", accent: true },
    { pt: "pr", pn: "pr", pm: "approval: risk-policy", conn: "" },
  ];
  return (
    <div className="modal on">
      <div className="mh">
        <h3>Workflow · {name}</h3>
        <div style={{ display: "flex", gap: 8 }}>
          <button className="btn ghost sm">YAML ⇄</button>
          <button className="x" onClick={onClose}>
            ✕
          </button>
        </div>
      </div>
      <div className="mb">
        <div className="eyebrow acc" style={{ marginBottom: 12 }}>
          Pasos (arrastra para reordenar · cada uno es dato)
        </div>
        <div className="pipe">
          {nodes.map((n, i) => (
            <div key={n.pn}>
              <div
                className="pnode"
                style={n.accent ? { borderColor: "var(--accent)", background: "var(--accent-soft)" } : undefined}
              >
                <span
                  className="pt"
                  style={n.accent ? { background: "var(--accent)", color: "#fff" } : undefined}
                >
                  {n.pt}
                </span>
                <span className="pn">{n.pn}</span>
                <span className="pm">{n.pm}</span>
              </div>
              {i < nodes.length - 1 && <div className={`pconn ${n.conn}`} />}
            </div>
          ))}
        </div>
        <div className="pconn" />
        <div className="addstep">+ Agregar paso (agent · gate · verify · human_gate · pr)</div>
        <div style={{ display: "flex", gap: 10, marginTop: 18 }}>
          <button className="btn ghost" style={{ flex: 1 }} onClick={onClose}>
            Cancelar
          </button>
          <button className="btn primary" style={{ flex: 1 }} onClick={onClose}>
            Validar y guardar
          </button>
        </div>
        <p style={{ fontSize: 12, color: "var(--ink4)", marginTop: 12 }}>
          Valida ciclos, tipos de I/O y pasos huérfanos antes de guardar. El próximo run usa el grafo
          nuevo — sin recompilar.
        </p>
      </div>
    </div>
  );
}
