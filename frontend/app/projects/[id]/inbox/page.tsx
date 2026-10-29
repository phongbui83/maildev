"use client";

import Link from "next/link";
import { useParams, useRouter, useSearchParams } from "next/navigation";
import { Suspense, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { CheckCheck, Inbox, Mail, MailOpen, Paperclip, RefreshCw, Search, Send, Trash2, X } from "lucide-react";
import Shell from "@/components/Shell";
import ProjectNav from "@/components/ProjectNav";
import EmailViewer from "@/components/EmailViewer";
import Avatar from "@/components/Avatar";
import { ApiError, api, del, post, type Email, type Paged, canWrite, canManage } from "@/lib/api";
import { useT } from "@/lib/i18n";
import { useProject } from "@/lib/project";
import { ErrorBox, Spinner, RelayBadge, SmimeBadge } from "@/components/ui";
import Pager from "@/components/Pager";

function dayGroup(iso: string, t: (k: string) => string): string {
  const d = new Date(iso);
  const now = new Date();
  const start = new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime();
  const diff = (start - new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime()) / 86400000;
  if (diff <= 0) return t("inbox.group.today");
  if (diff === 1) return t("inbox.group.yesterday");
  if (diff < 7) return t("inbox.group.week");
  if (diff < 31) return t("inbox.group.month");
  return t("inbox.group.older");
}

function timeLabel(iso: string): string {
  const d = new Date(iso);
  const now = new Date();
  const sameDay = d.toDateString() === now.toDateString();
  if (sameDay) return d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
  if (d.getFullYear() === now.getFullYear()) return d.toLocaleDateString(undefined, { day: "2-digit", month: "short" });
  return d.toLocaleDateString();
}

function InboxView() {
  const params = useParams<{ id: string }>();
  const router = useRouter();
  const search = useSearchParams();
  const selected = search.get("m");
  const { t } = useT();
  const { project, pendingRelay, reload: reloadProject } = useProject(params.id);

  const [emails, setEmails] = useState<Email[]>([]);
  const [total, setTotal] = useState(0);
  const [q, setQ] = useState("");
  const [folder, setFolder] = useState<"inbox" | "unread">("inbox");
  const [page, setPage] = useState(0);
  const [size, setSize] = useState(50);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [live, setLive] = useState(false);
  const qRef = useRef(q);
  qRef.current = q;
  const listRef = useRef<HTMLDivElement>(null);

  const load = useCallback(async () => {
    if (!project) return;
    try {
      const res = await api<Paged<Email>>(`/api/projects/${project.id}/emails?limit=${size}&offset=${page * size}&q=${encodeURIComponent(qRef.current)}${folder === "unread" ? "&unread=1" : ""}`);
      setEmails(res.items);
      setTotal(res.total);
      setError(null);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  }, [project, folder, page, size]);

  useEffect(() => {
    if (project) void load();
  }, [project?.id, folder, page, size, load]);
  useEffect(() => setPage(0), [folder, size]);

  useEffect(() => {
    if (!project) return;
    const es = new EventSource(`/api/projects/${project.id}/events`);
    es.onopen = () => setLive(true);
    es.onerror = () => setLive(false);
    const refresh = () => {
      void load();
      void reloadProject();
    };
    for (const ev of ["new", "deleted", "cleared", "updated", "relay", "project"]) es.addEventListener(ev, refresh);
    return () => es.close();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [project?.id]);

  const select = useCallback((id: string) => router.replace(`/projects/${params.id}/inbox?m=${id}`), [router, params.id]);

  // Keyboard navigation in the message list.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (!emails.length || (e.target as HTMLElement)?.tagName === "INPUT" || (e.target as HTMLElement)?.tagName === "TEXTAREA") return;
      const i = emails.findIndex((x) => x.id === selected);
      if (e.key === "ArrowDown" || e.key === "j") {
        e.preventDefault();
        select(emails[Math.min(i + 1, emails.length - 1)].id);
      } else if (e.key === "ArrowUp" || e.key === "k") {
        e.preventDefault();
        select(emails[Math.max(i - 1, 0)].id);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [emails, selected, select]);

  const groups = useMemo(() => {
    const out: { label: string; items: Email[] }[] = [];
    for (const e of emails) {
      const label = dayGroup(e.received_at, t);
      const g = out[out.length - 1];
      if (g && g.label === label) g.items.push(e);
      else out.push({ label, items: [e] });
    }
    return out;
  }, [emails, t]);

  async function markAllRead() {
    if (!project) return;
    await post(`/api/projects/${project.id}/emails/mark-all-read`);
    void load();
    void reloadProject();
  }
  async function clearInbox() {
    if (!project || !confirm(t("inbox.deleteAllConfirm", { n: total, name: project.name }))) return;
    await del(`/api/projects/${project.id}/emails`);
    router.replace(`/projects/${params.id}/inbox`);
    void load();
    void reloadProject();
  }
  async function toggleSeen(e: Email, ev: React.MouseEvent) {
    ev.stopPropagation();
    if (!project) return;
    await api(`/api/projects/${project.id}/emails/${e.id}`, { method: "PATCH", body: JSON.stringify({ seen: !e.seen }) });
    setEmails((prev) => prev.map((x) => (x.id === e.id ? { ...x, seen: !e.seen } : x)));
    void reloadProject();
  }

  const unread = project?.unread_count ?? 0;

  return (
    <Shell>
      <div className="flex h-full flex-col">
        {project && <ProjectNav project={project} pendingRelay={pendingRelay} />}

        {/* command bar */}
        <div className="cmdbar flex flex-wrap items-center gap-2 border-b px-3 py-2" style={{ borderColor: "var(--border)", background: "var(--panel)" }}>
          <button className="btn btn-sm" onClick={() => void load()} title={t("common.refresh")}><RefreshCw size={14} /> {t("common.refresh")}</button>
          {project && canWrite(project) && (
            <>
              <button className="btn btn-sm" onClick={markAllRead}><CheckCheck size={14} /> {t("inbox.markAllRead")}</button>
              <button className="btn btn-sm btn-danger" onClick={clearInbox}><Trash2 size={14} /> {t("inbox.deleteAll")}</button>
            </>
          )}
          <form
            className="relative mx-auto w-full max-w-xl"
            onSubmit={(e) => {
              e.preventDefault();
              setPage(0);
              void load();
            }}
          >
            <Search size={14} className="muted absolute left-2.5 top-2.5" />
            <input className="input pl-8 pr-8" placeholder={t("inbox.searchPlaceholder")} value={q} onChange={(e) => setQ(e.target.value)} />
            {q && (
              <button type="button" className="muted absolute right-2 top-2" onClick={() => { setQ(""); qRef.current = ""; setPage(0); void load(); }} aria-label={t("common.close")}><X size={14} /></button>
            )}
          </form>
          <span className="muted text-xs">{t("inbox.count", { n: total })}</span>
          {live ? <span className="badge badge-ok">{t("inbox.live")}</span> : <span className="badge">{t("inbox.offline")}</span>}
        </div>

        <div className="flex min-h-0 flex-1">
          {/* folder pane */}
          <nav className="hidden w-48 shrink-0 flex-col border-r py-2 md:flex" style={{ borderColor: "var(--border)" }}>
            <button className={`mx-2 flex items-center gap-2 rounded-lg px-3 py-2 text-left text-sm ${folder === "inbox" ? "row-active font-semibold" : ""}`} onClick={() => setFolder("inbox")}>
              <Inbox size={15} /> {t("project.inbox")}
              {unread > 0 && <span className="badge badge-accent ml-auto">{unread}</span>}
            </button>
            <button className={`mx-2 flex items-center gap-2 rounded-lg px-3 py-2 text-left text-sm ${folder === "unread" ? "row-active font-semibold" : ""}`} onClick={() => setFolder("unread")}>
              <Mail size={15} /> {t("inbox.unread")}
            </button>
            {project && canManage(project) && (
              <Link href={`/projects/${params.id}/relay`} className="mx-2 flex items-center gap-2 rounded-lg px-3 py-2 text-sm">
                <Send size={15} /> {t("relay.queue.title")}
                {pendingRelay > 0 && <span className="badge badge-accent ml-auto">{pendingRelay}</span>}
              </Link>
            )}
          </nav>

          {/* message list */}
          <div ref={listRef} className="flex w-full shrink-0 flex-col border-r md:w-[380px]" style={{ borderColor: "var(--border)" }}>
            <div className="min-h-0 flex-1 scroll">
              <ErrorBox error={error} />
              {loading ? (
                <Spinner />
              ) : emails.length === 0 ? (
                <div className="muted p-6 text-center text-sm">
                  {t("inbox.empty")} <Link className="underline" href={`/projects/${params.id}/dev`}>{t("project.dev")}</Link>.
                </div>
              ) : (
                groups.map((g) => (
                  <div key={g.label}>
                    <div className="muted sticky top-0 z-10 px-3 py-1 text-xs font-semibold uppercase tracking-wide" style={{ background: "var(--bg)" }}>{g.label}</div>
                    {g.items.map((e) => {
                      const from = e.from?.[0] || (e.envelope_from ? { name: "", address: e.envelope_from } : null);
                      const isSel = selected === e.id;
                      return (
                        <div
                          key={e.id}
                          onClick={() => select(e.id)}
                          className={`group flex cursor-pointer gap-3 border-b px-3 py-2 ${isSel ? "row-active" : ""}`}
                          style={{ borderColor: "var(--border)", borderLeft: `3px solid ${!e.seen ? "var(--accent)" : "transparent"}` }}
                        >
                          <Avatar addr={from} size={34} />
                          <div className="min-w-0 flex-1">
                            <div className="flex items-baseline justify-between gap-2">
                              <span className={`truncate text-sm ${!e.seen ? "font-semibold" : ""}`}>{from?.name || from?.address || t("inbox.unknownSender")}</span>
                              <span className="muted shrink-0 text-xs">{timeLabel(e.received_at)}</span>
                            </div>
                            <div className={`truncate text-sm ${!e.seen ? "font-semibold" : ""}`} style={!e.seen ? { color: "var(--accent)" } : undefined}>{e.subject || t("inbox.noSubject")}</div>
                            <div className="muted flex items-center gap-1 truncate text-xs">
                              <span className="truncate">{e.snippet || (e.to?.[0]?.address ? `${t("inbox.to")} ${e.to[0].address}` : "")}</span>
                              <span className="ml-auto flex shrink-0 items-center gap-1">
                                {e.attachment_count > 0 && <Paperclip size={12} />}
                                <SmimeBadge smime={e.smime} valid={e.smime_valid} signer={e.smime_signer} error={e.smime_error} />
                                <RelayBadge status={e.relay_status} />
                                {project && canWrite(project) && (
                                  <button className="btn btn-sm opacity-0 group-hover:opacity-100" onClick={(ev) => toggleSeen(e, ev)} title={e.seen ? t("inbox.markUnread") : t("inbox.markRead")}>
                                    {e.seen ? <Mail size={12} /> : <MailOpen size={12} />}
                                  </button>
                                )}
                              </span>
                            </div>
                          </div>
                        </div>
                      );
                    })}
                  </div>
                ))
              )}
            </div>
            <div className="border-t px-3 py-2" style={{ borderColor: "var(--border)" }}>
              <Pager page={page} size={size} total={total} onPage={setPage} onSize={setSize} />
            </div>
          </div>

          {/* reading pane */}
          <section className="hidden min-w-0 flex-1 md:block">
            {project && selected ? (
              <EmailViewer
                project={project}
                emailId={selected}
                onDeleted={() => {
                  router.replace(`/projects/${params.id}/inbox`);
                  void load();
                  void reloadProject();
                }}
                onLoaded={(e) => setEmails((prev) => prev.map((x) => (x.id === e.id ? { ...x, seen: true, relay_status: e.relay_status } : x)))}
              />
            ) : (
              <div className="muted flex h-full flex-col items-center justify-center gap-2 text-sm">
                <MailOpen size={40} className="opacity-40" />
                {t("inbox.select")}
              </div>
            )}
          </section>
        </div>
      </div>
    </Shell>
  );
}

// useSearchParams needs a Suspense boundary for static prerendering.
export default function InboxPage() {
  return (
    <Suspense>
      <InboxView />
    </Suspense>
  );
}
