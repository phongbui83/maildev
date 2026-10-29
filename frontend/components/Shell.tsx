"use client";

import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import { useEffect, type ReactNode } from "react";
import { KeyRound, LogOut, Shield } from "lucide-react";
import Logo from "./Logo";
import { useAuth } from "@/lib/auth";
import { useLite } from "@/lib/config";
import { LocaleSwitcher, useT } from "@/lib/i18n";
import { ThemeToggle } from "@/lib/theme";
import { Spinner } from "./ui";

export default function Shell({ children }: { children: ReactNode }) {
  const { user, loading, logout } = useAuth();
  const { t } = useT();
  const lite = useLite();
  const router = useRouter();
  const pathname = usePathname();

  useEffect(() => {
    if (!loading && !user) router.replace(`/login?next=${encodeURIComponent(pathname)}`);
    if (!loading && user?.must_change_password && pathname !== "/password") router.replace("/password");
  }, [loading, user, router, pathname]);

  if (loading || !user) return <Spinner label={t("nav.checkingSession")} />;

  const isAdminish = user.role === "admin" || user.role === "auditor";

  return (
    <div className="flex h-screen flex-col">
      <header className="flex items-center justify-between border-b px-4 py-2" style={{ borderColor: "var(--border)", background: "var(--panel)" }}>
        <div className="flex items-center gap-5">
          <Link href="/" className="flex items-center gap-2 font-semibold">
            <Logo size={24} /> <span>{t("app.name")}</span>
          </Link>
          <nav className="flex items-center gap-3 text-sm">
            <Link href="/" className={pathname === "/" ? "font-medium" : "muted"}>{t("nav.projects")}</Link>
            {isAdminish && (
              <Link href="/admin" className={pathname.startsWith("/admin") ? "font-medium" : "muted"}>
                <span className="inline-flex items-center gap-1"><Shield size={14} /> {t("nav.admin")}</span>
              </Link>
            )}
          </nav>
        </div>
        <div className="flex items-center gap-3 text-sm">
          <LocaleSwitcher compact />
          <ThemeToggle />
          <span>
            {user.display_name || user.username} <span className="badge ml-1">{t(`role.${user.role}`)}</span>
            {user.auth_source === "local" && !lite && <span className="badge ml-1">{t("auth.external")}</span>}
          </span>
          {user.auth_source === "local" && (
            <Link href="/password" className="btn btn-sm" title={t("password.title")}><KeyRound size={14} /></Link>
          )}
          <button className="btn btn-sm" onClick={logout} title={t("common.signOut")}>
            <LogOut size={14} />
          </button>
        </div>
      </header>
      <main className="min-h-0 flex-1">{children}</main>
      <footer className="muted border-t px-4 py-1 text-center text-[11px]" style={{ borderColor: "var(--border)", background: "var(--panel)" }}>
        {t("footer.credit")} · <a className="underline" href={lite ? "/guide/lite.html" : "/guide/"} target="_blank" rel="noreferrer">{t("login.guide")}</a>
      </footer>
    </div>
  );
}
