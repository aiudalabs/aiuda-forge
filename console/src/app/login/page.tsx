"use client";

// Página de login (audit C1). Email + password → POST /auth/login → guarda el
// token y redirige al destino (?next) o al board. En modo mock (API caída) no se
// requiere login; el AuthGate ya deja pasar, pero si alguien llega aquí, el form
// sigue funcionando contra la API real cuando exista.

import { useState } from "react";
import { login } from "@/lib/auth";
import { useT } from "@/lib/i18n";
import { LanguageSwitcher } from "@/components/LanguageSwitcher";

export default function LoginPage() {
  const t = useT();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setBusy(true);
    try {
      await login(email, password);
      // Volver al destino original si venía en ?next, si no al board (/).
      const params = new URLSearchParams(window.location.search);
      const next = params.get("next");
      window.location.href = next ? decodeURIComponent(next) : "/";
    } catch (err) {
      setError(err instanceof Error ? err.message : t("auth.loginFailed"));
      setBusy(false);
    }
  }

  return (
    <div
      style={{
        minHeight: "100vh",
        display: "grid",
        placeItems: "center",
        background: "#FAF8F4",
      }}
    >
      <form
        onSubmit={onSubmit}
        style={{
          width: "100%",
          maxWidth: 360,
          padding: 32,
          borderRadius: 16,
          background: "#fff",
          boxShadow: "0 8px 32px rgba(0,0,0,0.08)",
          display: "flex",
          flexDirection: "column",
          gap: 16,
        }}
      >
        <h1 style={{ fontWeight: 900, fontSize: 24, margin: 0 }}>{t("auth.loginTitle")}</h1>
        <p style={{ margin: 0, color: "#666", fontSize: 14 }}>{t("auth.loginSubtitle")}</p>

        <label style={{ display: "flex", flexDirection: "column", gap: 6, fontSize: 13 }}>
          {t("auth.email")}
          <input
            type="email"
            autoComplete="username"
            required
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            style={inputStyle}
          />
        </label>

        <label style={{ display: "flex", flexDirection: "column", gap: 6, fontSize: 13 }}>
          {t("auth.password")}
          <input
            type="password"
            autoComplete="current-password"
            required
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            style={inputStyle}
          />
        </label>

        {error && (
          <div style={{ color: "#E8440A", fontSize: 13 }} role="alert">
            {error}
          </div>
        )}

        <button
          type="submit"
          disabled={busy}
          style={{
            padding: "10px 16px",
            borderRadius: 10,
            border: "none",
            background: "#E8440A",
            color: "#fff",
            fontWeight: 700,
            cursor: busy ? "not-allowed" : "pointer",
            opacity: busy ? 0.7 : 1,
          }}
        >
          {busy ? t("auth.entering") : t("auth.enter")}
        </button>

        <p style={{ margin: 0, fontSize: 13, color: "#666", textAlign: "center" }}>
          {t("auth.noAccount")}{" "}
          <a href="/register" style={{ color: "#E8440A", fontWeight: 600 }}>
            {t("auth.createOne")}
          </a>
        </p>

        <LanguageSwitcher />
      </form>
    </div>
  );
}

const inputStyle: React.CSSProperties = {
  padding: "10px 12px",
  borderRadius: 10,
  border: "1px solid #ddd",
  fontSize: 14,
  outline: "none",
};
