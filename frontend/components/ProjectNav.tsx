"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { useEffect } from "react";
import { Inbox, KeyRound, Send, Settings2, LayoutGrid } from "lucide-react";
import type { Project } from "@/lib/api";
import { canManage } from "@/lib/api";
import { useAuth } from "@/lib/auth";
import { useT } from "@/lib/i18n";

const PALETTE = ["#2563eb", "#16a34a", "#d97706", "#dc2626", "#7c3aed", "#0891b2", "#db2777", "#65a30d", "#ea580c", "#4f46e5"];
export function colorFor(slug: string) {
  let h = 0;
  for (const ch of slug) h = (h * 31 + ch.charCodeAt(0)) >>> 0;
  return PALETTE[h % PALETTE.length];
}

// Sub-navigation shared by every project page: hub, dev info, inbox, manage.
export default function ProjectNav({ project, pendingRelay }: { project: Project; pendingRelay?: number }) {
  const { t } = useT();
  const { user } = useAuth();
  const pathname = usePathname();
  const base = `/projects/${project.slug}`;
  useEffect(() => {
    document.title = `${project.name} · MailDev`;
    return () => { document.title = "MailDev"; };
  }, [project.name]);
  const manage = canManage(project) || user?.role === "auditor";
  const items: { href: string; label: string; icon: React.ReactNode; badge?: number; exact?: boolean }[] = [
    { href: base, label: t("project.hub"), icon: <LayoutGrid size={14} />, exact: true },
    { href: `${base}/dev`, label: t("project.dev"), icon: <KeyRound size={14} /> },
    { href: `${base}/inbox`, label: t("project.inbox"), icon: <Inbox size={14} />, badge: project.unread_count || undefined },
  ];
  if (manage) items.push({ href: `${base}/relay`, label: t("relay.queue.title"), icon: <Send size={14} />, badge: pendingRelay || undefined });
  if (manage) items.push({ href: `${base}/manage`, label: t("project.manage"), icon: <Settings2 size={14} /> });

  return (
    <div className="flex items-center gap-1 border-b px-4" style={{ borderColor: "var(--border)", background: "var(--panel)" }}>
      <div className="mr-3 flex items-center gap-2 py-2">
        <span className="inline-block h-3 w-3 rounded-full" style={{ background: colorFor(project.slug) }} />
        <span className="font-semibold">{project.name}</span>
        <code className="muted text-xs">{project.slug}</code>
        <span className="badge">{project.my_role ? t(`role.${project.my_role}`) : user?.role === "auditor" ? t("role.auditor") : ""}</span>
        {project.status === "archived" && <span className="badge badge-danger">{t("projects.archived")}</span>}
      </div>
      {items.map((it) => {
        const active = it.exact ? pathname === it.href : pathname.startsWith(it.href);
        return (
          <Link key={it.href} href={it.href} className={`tab inline-flex items-center gap-1 ${active ? "tab-active" : ""}`}>
            {it.icon} {it.label}
            {it.badge ? <span className="badge badge-accent ml-1">{it.badge}</span> : null}
          </Link>
        );
      })}
    </div>
  );
}
