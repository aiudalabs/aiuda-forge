"use client";

// i18n del console (v1.2) — minimalista, del lado del cliente, sin routing por URL.
// El idioma vive en localStorage ("vibeforge.lang", default "es") y se expone por
// contexto. useT() devuelve t(key, vars?) que busca la clave en el idioma activo
// con fallback a español y luego a la propia clave. Sólo traduce el CHROME de la UI;
// el contenido generado por agentes (specs, tickets, respuestas del Brain) queda en
// el idioma en que el modelo lo produce.

import { createContext, useCallback, useContext, useEffect, useMemo, useState } from "react";
import { MESSAGES } from "./catalog";

export type Lang = "es" | "en" | "pt";
export const LANGS: Lang[] = ["es", "en", "pt"];
const DEFAULT_LANG: Lang = "es";
const STORAGE_KEY = "vibeforge.lang";

function isLang(v: string | null): v is Lang {
  return v === "es" || v === "en" || v === "pt";
}

interface I18nValue {
  lang: Lang;
  setLang: (l: Lang) => void;
  t: (key: string, vars?: Record<string, string | number>) => string;
}

const I18nContext = createContext<I18nValue | null>(null);

export function I18nProvider({ children }: { children: React.ReactNode }) {
  const [lang, setLangState] = useState<Lang>(DEFAULT_LANG);

  // Hidratar desde localStorage tras el montaje (SSR-safe: el servidor renderiza es).
  useEffect(() => {
    const stored = typeof window !== "undefined" ? window.localStorage.getItem(STORAGE_KEY) : null;
    if (isLang(stored)) setLangState(stored);
  }, []);

  const setLang = useCallback((l: Lang) => {
    setLangState(l);
    if (typeof window !== "undefined") window.localStorage.setItem(STORAGE_KEY, l);
  }, []);

  const t = useCallback(
    (key: string, vars?: Record<string, string | number>) => {
      const dict = MESSAGES[lang] ?? MESSAGES[DEFAULT_LANG];
      let s = dict[key] ?? MESSAGES[DEFAULT_LANG][key] ?? key;
      if (vars) {
        for (const [k, v] of Object.entries(vars)) s = s.replace(`{${k}}`, String(v));
      }
      return s;
    },
    [lang],
  );

  const value = useMemo<I18nValue>(() => ({ lang, setLang, t }), [lang, setLang, t]);
  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>;
}

export function useI18n(): I18nValue {
  const ctx = useContext(I18nContext);
  if (!ctx) {
    // Fallback fuera del provider (p.ej. tests/edge): español sin estado.
    return {
      lang: DEFAULT_LANG,
      setLang: () => {},
      t: (key) => MESSAGES[DEFAULT_LANG][key] ?? key,
    };
  }
  return ctx;
}

/** Atajo: sólo la función de traducción. */
export function useT() {
  return useI18n().t;
}
