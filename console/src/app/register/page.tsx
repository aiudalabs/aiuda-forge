"use client";

// Página de registro (v1.2 roles). Email + password → POST /auth/register → guarda
// el token y aterriza logueado en el destino (?next) o el board. Alta self-service:
// cualquiera puede crear una cuenta y luego ser invitado a proyectos por su email.

import { useState } from "react";
import { register } from "@/lib/auth";
import { useT } from "@/lib/i18n";
import { LanguageSwitcher } from "@/components/LanguageSwitcher";

export default function RegisterPage() {
  const t = useT();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    if (password.length < 8) {
      setError(t("auth.passwordTooShort"));
      return;
    }
    setBusy(true);
    try {
      await register(email, password);
      const params = new URLSearchParams(window.location.search);
      const next = params.get("next");
      window.location.href = next ? decodeURIComponent(next) : "/";
    } catch (err) {
      setError(err instanceof Error ? err.message : t("auth.registerFailed"));
      setBusy(false);
    }
  }

  return (
    <div style={{ position: "relative", minHeight: "100vh", display: "grid", placeItems: "center", background: "#FAF8F4" }}>
      <div style={{ position: "absolute", top: 16, right: 16 }}>
        <LanguageSwitcher />
      </div>
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
        <h1 style={{ fontWeight: 900, fontSize: 24, margin: 0 }}>{t("auth.registerTitle")}</h1>
        <p style={{ margin: 0, color: "#666", fontSize: 14 }}>{t("auth.registerSubtitle")}</p>

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
            autoComplete="new-password"
            required
            minLength={8}
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            style={inputStyle}
          />
          <span style={{ color: "#999", fontSize: 11 }}>{t("auth.passwordHint")}</span>
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
          {busy ? t("auth.creating") : t("auth.create")}
        </button>

        <p style={{ margin: 0, fontSize: 13, color: "#666", textAlign: "center" }}>
          {t("auth.haveAccount")}{" "}
          <a href="/login" style={{ color: "#E8440A", fontWeight: 600 }}>
            {t("auth.signIn")}
          </a>
        </p>
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
