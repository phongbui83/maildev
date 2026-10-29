"use client";

import Link from "next/link";
import { useParams } from "next/navigation";
import { Inbox, KeyRound, Send, Settings2 } from "lucide-react";
import Shell from "@/components/Shell";
import ProjectNav from "@/components/ProjectNav";
import { canManage } from "@/lib/api";
import { useAuth } from "@/lib/auth";
import { useT } from "@/lib/i18n";
import { useProject } from "@/lib/project";
import { ErrorBox, Spinner } from "@/components/ui";

// Project hub: one click to development info, the inbox or project management.
export default function ProjectHubPage() {
  const params = useParams<{ id: string }>();
  const { t } = useT();
  const { user } = useAuth();
  const { project, pendingRelay, error } = useProject(params.id);

  if (error) return <Shell><div className="p-6"><ErrorBox error={error} /></div></Shell>;
  if (!project) return <Shell><Spinner /></Shell>;

  const base = `/projects/${project.slug}`;
  const manage = canManage(project) || user?.role === "auditor";

  const cards: { href: string; icon: React.ReactNode; title: string; text: string; badge?: number }[] = [
    { href: `${base}/dev`, icon: <KeyRound size={22} />, title: t("hub.dev.title"), text: t("hub.dev.text") },
    { href: `${base}/inbox`, icon: <Inbox size={22} />, title: t("hub.inbox.title"), text: t("hub.inbox.text", { n: project.email_count, unread: project.unread_count }), badge: project.unread_count },
  ];
  if (manage) cards.push({ href: `${base}/relay`, icon: <Send size={22} />, title: t("relay.queue.title"), text: project.relay_enabled ? t("hub.relay.text") : t("relay.disabled"), badge: pendingRelay });
  if (manage) cards.push({ href: `${base}/manage`, icon: <Settings2 size={22} />, title: t("hub.manage.title"), text: t("hub.manage.text") });

  return (
    <Shell>
      <div className="flex h-full flex-col">
        <ProjectNav project={project} pendingRelay={pendingRelay} />
        <div className="mx-auto w-full max-w-5xl p-6">
          {project.description && <p className="muted mb-5 text-sm">{project.description}</p>}
          <div className="grid gap-4 sm:grid-cols-2">
            {cards.map((c) => (
              <Link key={c.href} href={c.href} className="panel flex gap-4 p-5 transition hover:shadow-md" style={{ textDecoration: "none" }}>
                <div className="flex h-12 w-12 shrink-0 items-center justify-center rounded-xl" style={{ background: "var(--accent-soft)", color: "var(--accent)" }}>{c.icon}</div>
                <div className="min-w-0">
                  <div className="flex items-center gap-2 text-base font-semibold">
                    {c.title}
                    {c.badge ? <span className="badge badge-accent">{c.badge}</span> : null}
                  </div>
                  <p className="muted mt-1 text-sm">{c.text}</p>
                </div>
              </Link>
            ))}
          </div>
          <div className="muted mt-6 flex flex-wrap gap-4 text-xs">
            <span>{t("project.retention")}: {project.retention_days}d · {t("project.maxEmails")}: {project.max_emails}</span>
            <span>{project.relay_enabled ? t("relay.enabled") : t("relay.disabled")}</span>
            {project.auto_join && <span>{t("projects.autoJoin")}</span>}
          </div>
        </div>
      </div>
    </Shell>
  );
}
