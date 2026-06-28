"use client";

// Selector de idioma (i18n, v1.2). ES · EN · PT. Persistido por el I18nProvider
// (localStorage). Compacto para el pie del sidebar y reutilizable en /login.

import { LANGS, useI18n, type Lang } from "@/lib/i18n";

export function LanguageSwitcher({ compact = false }: { compact?: boolean }) {
  const { lang, setLang, t } = useI18n();
  return (
    <div className={`langsw${compact ? " compact" : ""}`} role="group" aria-label={t("lang.label")}>
      {LANGS.map((l) => (
        <button
          key={l}
          className={`langopt${l === lang ? " on" : ""}`}
          aria-pressed={l === lang}
          onClick={() => setLang(l as Lang)}
          title={t(`lang.${l}`)}
        >
          {l.toUpperCase()}
        </button>
      ))}
      <style jsx>{`
        .langsw {
          display: flex;
          gap: 4px;
          padding: ${compact ? "8px 0 0" : "0"};
        }
        .langopt {
          flex: 1;
          padding: 4px 8px;
          font-size: 11px;
          font-weight: 700;
          letter-spacing: 0.03em;
          border: 1px solid var(--line, #e6e2da);
          border-radius: 7px;
          background: transparent;
          color: var(--ink4, #999);
          cursor: pointer;
        }
        .langopt.on {
          background: var(--accent, #e8440a);
          border-color: var(--accent, #e8440a);
          color: #fff;
        }
      `}</style>
    </div>
  );
}
