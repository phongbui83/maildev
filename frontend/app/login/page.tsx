"use client";

import { Suspense, useEffect, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import Logo from "@/components/Logo";
import { ApiError, post, api, type ServerConfig, type User } from "@/lib/api";
import { useAuth } from "@/lib/auth";
import { LocaleSwitcher, useT } from "@/lib/i18n";
import { ThemeToggle } from "@/lib/theme";
import { ErrorBox } from "@/components/ui";
import { BookOpen } from "lucide-react";

function LoginForm() {
  const { user, loading, refresh } = useAuth();
  const { t } = useT();
  const router = useRouter();
  const params = useSearchParams();
  const next = params.get("next") || "/";
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [cfg, setCfg] = useState<ServerConfig | null>(null);

  useEffect(() => {
    api<ServerConfig>("/api/config").then(setCfg).catch(() => {});
  }, []);
  useEffect(() => {
    if (!loading && user) router.replace(next);
  }, [loading, user, router, next]);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const u = await post<User>("/api/auth/login", { username, password });
      await refresh();
      router.replace(u.must_change_password ? "/password" : next);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : t("login.failed"));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="flex min-h-screen flex-col items-center justify-center gap-4 p-4">
      <form onSubmit={submit} className="panel w-full max-w-sm space-y-4 p-6">
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-2 text-lg font-semibold">
            <Logo size={28} /> {t("app.name")}
          </div>
          <div className="flex items-center gap-2"><LocaleSwitcher /><ThemeToggle /></div>
        </div>
        <p className="muted text-sm">{t(cfg?.edition === "lite" ? "login.subtitleLite" : "login.subtitle")}</p>
        <input className="input" placeholder={t("login.username")} autoComplete="username" value={username} onChange={(e) => setUsername(e.target.value)} autoFocus />
        <input className="input" placeholder={t("login.password")} type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} />
        <ErrorBox error={error} />
        <button className="btn btn-primary w-full justify-center" disabled={busy || !username || !password}>
          {busy ? t("login.submitting") : t("login.submit")}
        </button>
        {cfg?.auth_mode === "dev" && <p className="muted text-xs">{t("login.devHint")}</p>}
      </form>
      <a href={cfg?.edition === "lite" ? "/guide/lite.html" : "/guide/"} target="_blank" rel="noreferrer" className="panel flex w-full max-w-sm items-center gap-3 p-4 transition hover:shadow-md" style={{ textDecoration: "none" }}>
        <span className="flex h-11 w-11 shrink-0 items-center justify-center rounded-xl" style={{ background: "var(--accent-soft)", color: "var(--accent)" }}><BookOpen size={22} /></span>
        <span>
          <span className="block text-base font-semibold">{t("login.guide")}</span>
          <span className="muted block text-xs">{t("login.guideHint")}</span>
        </span>
      </a>
      <p className="muted text-center text-[11px]">{t("footer.credit")}</p>
    </div>
  );
}

export default function LoginPage() {
  return (
    <Suspense>
      <LoginForm />
    </Suspense>
  );
}
