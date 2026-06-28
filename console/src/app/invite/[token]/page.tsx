"use client";

// Aceptar invitación (v1.2 roles). El owner comparte /invite/<token>. Flujo:
//  · Sin sesión → mandamos a /register?next=/invite/<token> (el invitado crea su
//    cuenta con el MISMO email; tras registrarse vuelve aquí y se acepta).
//  · Con sesión → POST /invites/<token>/accept. El backend exige que el email de la
//    sesión coincida con el del invite; si no, lo dice.

import { use, useEffect, useState } from "react";
import Link from "next/link";
import { acceptInvite } from "@/lib/api";
import { getToken } from "@/lib/auth";

type State =
  | { kind: "working" }
  | { kind: "ok"; projectId: string; role: string }
  | { kind: "error"; message: string };

export default function InvitePage({ params }: { params: Promise<{ token: string }> }) {
  const { token } = use(params);
  const [state, setState] = useState<State>({ kind: "working" });

  useEffect(() => {
    // Sin sesión → a registro, preservando el destino para volver y aceptar.
    if (!getToken()) {
      const next = encodeURIComponent(`/invite/${token}`);
      window.location.href = `/register?next=${next}`;
      return;
    }
    let cancelled = false;
    (async () => {
      try {
        const res = await acceptInvite(token);
        if (!cancelled) setState({ kind: "ok", projectId: res.project_id, role: res.role });
      } catch (err) {
        if (!cancelled) {
          const message = err instanceof Error ? err.message : "No se pudo aceptar la invitación.";
          setState({ kind: "error", message });
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [token]);

  return (
    <div style={{ minHeight: "100vh", display: "grid", placeItems: "center", background: "#FAF8F4" }}>
      <div style={card}>
        {state.kind === "working" && <p style={{ margin: 0, color: "#666" }}>Aceptando invitación…</p>}

        {state.kind === "ok" && (
          <>
            <h1 style={{ fontWeight: 900, fontSize: 22, margin: 0 }}>¡Listo! 🎉</h1>
            <p style={{ margin: 0, color: "#666", fontSize: 14 }}>
              Te uniste al proyecto <strong>{state.projectId}</strong> como{" "}
              <strong>{state.role}</strong>.
            </p>
            <Link href="/" style={btn}>
              Ir a la consola
            </Link>
          </>
        )}

        {state.kind === "error" && (
          <>
            <h1 style={{ fontWeight: 900, fontSize: 20, margin: 0, color: "#E8440A" }}>
              No se pudo aceptar
            </h1>
            <p style={{ margin: 0, color: "#666", fontSize: 14 }}>{state.message}</p>
            <Link href="/" style={btn}>
              Ir a la consola
            </Link>
          </>
        )}
      </div>
    </div>
  );
}

const card: React.CSSProperties = {
  width: "100%",
  maxWidth: 380,
  padding: 32,
  borderRadius: 16,
  background: "#fff",
  boxShadow: "0 8px 32px rgba(0,0,0,0.08)",
  display: "flex",
  flexDirection: "column",
  gap: 14,
  textAlign: "center",
};

const btn: React.CSSProperties = {
  padding: "10px 16px",
  borderRadius: 10,
  background: "#E8440A",
  color: "#fff",
  fontWeight: 700,
  textDecoration: "none",
};
