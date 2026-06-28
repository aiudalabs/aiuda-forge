"use client";

// Sección Cuenta y seguridad: cambiar la propia contraseña (v1.2). Verifica la
// actual, exige ≥8 y confirmación; en éxito el backend invalida las sesiones, así
// que tras un instante mandamos a /login para re-autenticar.

import { useState } from "react";
import { changePassword, logout } from "@/lib/auth";
import { useT } from "@/lib/i18n";

export function AccountSecurity() {
  const t = useT();
  const [cur, setCur] = useState("");
  const [next, setNext] = useState("");
  const [confirm, setConfirm] = useState("");
  const [busy, setBusy] = useState(false);
  const [ok, setOk] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    if (next.length < 8) {
      setError(t("account.tooShort"));
      return;
    }
    if (next !== confirm) {
      setError(t("account.mismatch"));
      return;
    }
    setBusy(true);
    try {
      await changePassword(cur, next);
      setOk(true);
      // El backend cerró todas las sesiones → re-login tras mostrar el aviso.
      setTimeout(async () => {
        await logout();
        window.location.href = "/login";
      }, 1500);
    } catch (err) {
      setError(err instanceof Error ? err.message : t("account.failed"));
      setBusy(false);
    }
  }

  return (
    <form className="acc" onSubmit={onSubmit}>
      <h3>{t("account.title")}</h3>
      <label>
        {t("account.current")}
        <input type="password" autoComplete="current-password" required value={cur} onChange={(e) => setCur(e.target.value)} />
      </label>
      <label>
        {t("account.new")}
        <input type="password" autoComplete="new-password" required minLength={8} value={next} onChange={(e) => setNext(e.target.value)} />
      </label>
      <label>
        {t("account.confirm")}
        <input type="password" autoComplete="new-password" required value={confirm} onChange={(e) => setConfirm(e.target.value)} />
      </label>

      {error && <div className="msg err">{error}</div>}
      {ok && <div className="msg ok">{t("account.changed")}</div>}

      <button type="submit" disabled={busy || ok}>
        {busy ? t("account.updating") : t("account.update")}
      </button>

      <style jsx>{`
        .acc { width: 100%; padding: 18px; background: #fff; border: 1px solid #eee; border-radius: 14px; display: flex; flex-direction: column; gap: 12px; }
        h3 { margin: 0; font-size: 14px; font-weight: 800; }
        label { display: flex; flex-direction: column; gap: 6px; font-size: 13px; color: #555; max-width: 360px; }
        input { box-sizing: border-box; padding: 9px 11px; border: 1px solid #ddd; border-radius: 9px; font-size: 14px; outline: none; }
        .msg { font-size: 13px; padding: 8px 12px; border-radius: 9px; max-width: 360px; }
        .msg.err { background: #fdf0ec; color: #E8440A; border: 1px solid #f5d5c8; }
        .msg.ok { background: #f0f8f0; color: #2a6; border: 1px solid #cce8cc; }
        button { align-self: flex-start; padding: 9px 16px; border: none; border-radius: 9px; background: #E8440A; color: #fff; font-weight: 700; cursor: pointer; }
        button:disabled { opacity: 0.5; cursor: not-allowed; }
      `}</style>
    </form>
  );
}
