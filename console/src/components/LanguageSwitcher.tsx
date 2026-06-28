"use client";

// Selector de idioma (i18n, v1.2). Dropdown compacto con banderas — va en la esquina
// superior derecha (topbar) y en las pantallas de auth. Persistido por el
// I18nProvider (localStorage). 🇪🇸 ES · 🇬🇧 EN · 🇧🇷 PT.
//
// Patrón accesible: una etiqueta visible (bandera + código) con un <select> nativo
// transparente encima que captura el clic y abre el menú del SO con las tres banderas.

import { LANGS, useI18n, type Lang } from "@/lib/i18n";

const FLAG: Record<Lang, string> = { es: "🇪🇸", en: "🇬🇧", pt: "🇧🇷" };

export function LanguageSwitcher() {
  const { lang, setLang, t } = useI18n();
  return (
    <div className="langsw" title={t("lang.label")}>
      <span className="face" aria-hidden>
        <span className="flag">{FLAG[lang]}</span>
        <span className="code">{lang.toUpperCase()}</span>
        <span className="caret">▾</span>
      </span>
      <select
        aria-label={t("lang.label")}
        value={lang}
        onChange={(e) => setLang(e.target.value as Lang)}
      >
        {LANGS.map((l) => (
          <option key={l} value={l}>
            {FLAG[l]} {t(`lang.${l}`)}
          </option>
        ))}
      </select>
      <style jsx>{`
        .langsw {
          position: relative;
          display: inline-flex;
          align-items: center;
          height: 32px;
        }
        .face {
          display: inline-flex;
          align-items: center;
          gap: 5px;
          height: 32px;
          padding: 0 8px;
          border: 1px solid var(--line, #e6e2da);
          border-radius: 9px;
          background: var(--card, #fff);
        }
        .flag {
          font-size: 15px;
          line-height: 1;
        }
        .code {
          font-size: 12px;
          font-weight: 700;
          letter-spacing: 0.03em;
          color: var(--ink, #222);
        }
        .caret {
          font-size: 10px;
          color: var(--ink4, #999);
        }
        select {
          position: absolute;
          inset: 0;
          width: 100%;
          height: 100%;
          opacity: 0;
          cursor: pointer;
          border: none;
        }
      `}</style>
    </div>
  );
}
