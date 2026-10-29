"use client";

import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { messages, type Locale } from "./translations";
import { Flag } from "@/components/Flags";

export const LOCALES: { code: Locale; label: string; flag: string; short: string }[] = [
  { code: "vi", label: "Tiếng Việt", flag: "🇻🇳", short: "VI" },
  { code: "en", label: "English", flag: "🇬🇧", short: "EN" },
  { code: "ja", label: "日本語", flag: "🇯🇵", short: "JA" },
];

const STORAGE_KEY = "maildev.locale";

interface I18n {
  locale: Locale;
  setLocale: (l: Locale) => void;
  t: (key: string, vars?: Record<string, string | number>) => string;
}

const I18nContext = createContext<I18n>({ locale: "en", setLocale: () => {}, t: (k) => k });

function detect(): Locale {
  try {
    const saved = localStorage.getItem(STORAGE_KEY) as Locale | null;
    if (saved && messages[saved]) return saved;
  } catch {
    /* storage unavailable */
  }
  const lang = (navigator.language || "en").toLowerCase();
  if (lang.startsWith("vi")) return "vi";
  if (lang.startsWith("ja")) return "ja";
  return "en";
}

export function I18nProvider({ children }: { children: ReactNode }) {
  // Render English on the server and first client paint, then switch; avoids hydration mismatches.
  const [locale, setLocaleState] = useState<Locale>("en");
  useEffect(() => {
    setLocaleState(detect());
  }, []);
  const setLocale = useCallback((l: Locale) => {
    setLocaleState(l);
    try {
      localStorage.setItem(STORAGE_KEY, l);
    } catch {
      /* ignore */
    }
    document.documentElement.lang = l;
  }, []);
  const t = useCallback(
    (key: string, vars?: Record<string, string | number>) => {
      let s = messages[locale][key] ?? messages.en[key] ?? key;
      if (vars) {
        for (const [k, v] of Object.entries(vars)) s = s.replaceAll(`{${k}}`, String(v));
      }
      return s;
    },
    [locale],
  );
  const value = useMemo(() => ({ locale, setLocale, t }), [locale, setLocale, t]);
  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>;
}

export const useT = () => useContext(I18nContext);

export function LocaleSwitcher({ compact }: { compact?: boolean }) {
  const { locale, setLocale } = useT();
  return (
    <div className="seg" role="group" aria-label="Language">
      {LOCALES.map((l) => (
        <button key={l.code} type="button" className={`inline-flex items-center gap-1 ${locale === l.code ? "on" : ""}`} onClick={() => setLocale(l.code)} title={l.label}>
          <Flag code={l.code} size={compact ? 16 : 18} />{compact ? "" : l.short}
        </button>
      ))}
    </div>
  );
}
