"use client";

import Link from "next/link";
import { useCallback, useEffect, useMemo, useState } from "react";
import { ArrowDown, ArrowUp, Check, GripVertical, Inbox, KeyRound, Pencil, Plus, Send, Settings2, X } from "lucide-react";
import Shell from "@/components/Shell";
import { ApiError, api, patch, post, type Project, canManage } from "@/lib/api";
import { useAuth } from "@/lib/auth";
import { useT } from "@/lib/i18n";
import { useLite } from "@/lib/config";
import { ErrorBox, Field, Modal, Spinner } from "@/components/ui";
import Pager from "@/components/Pager";

const ORDER_KEY = "maildev.projectOrder";
// The project list endpoint does not carry the pending relay count; the badge appears on the project pages.
const pendingRelayOf = (_p: Project): number | undefined => undefined;
import { colorFor } from "@/components/ProjectNav";

function loadOrder(): string[] {
  try {
    return JSON.parse(localStorage.getItem(ORDER_KEY) || "[]");
  } catch {
    return [];
  }
}
function saveOrder(slugs: string[]) {
  try {
    localStorage.setItem(ORDER_KEY, JSON.stringify(slugs));
  } catch {
    /* ignore */
  }
}

function NewProjectModal({ onClose, onCreated }: { onClose: () => void; onCreated: (p: Project) => void }) {
  const { t } = useT();
  const [slug, setSlug] = useState("");
  const [name, setName] = useState("");
  const [owner, setOwner] = useState("");
  const [description, setDescription] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      onCreated(await post<Project>("/api/projects", { slug, name, owner_username: owner, description, init: true }));
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }
  return (
    <Modal title={t("projects.create.title")} onClose={onClose}>
      <form onSubmit={submit} className="space-y-3">
        <Field label={t("projects.create.slug")} hint={t("projects.create.slugHint")}>
          <input className="input" value={slug} onChange={(e) => setSlug(e.target.value.toLowerCase())} pattern="[a-z0-9][a-z0-9-]{1,62}" required autoFocus />
        </Field>
        <Field label={t("projects.create.name")}><input className="input" value={name} onChange={(e) => setName(e.target.value)} /></Field>
        <Field label={t("projects.create.owner")} hint={t("projects.create.ownerHint")}><input className="input" value={owner} onChange={(e) => setOwner(e.target.value)} /></Field>
        <Field label={t("projects.create.description")}><input className="input" value={description} onChange={(e) => setDescription(e.target.value)} /></Field>
        <ErrorBox error={error} />
        <div className="flex justify-end gap-2">
          <button type="button" className="btn" onClick={onClose}>{t("common.cancel")}</button>
          <button className="btn btn-primary" disabled={busy || !slug}>{t("projects.create.submit")}</button>
        </div>
      </form>
    </Modal>
  );
}

function ProjectCard({ p, active, onHover, onMove, onSaved, first, last }: {
  p: Project; active: boolean; onHover: () => void; onMove: (dir: -1 | 1) => void; onSaved: (p: Project) => void; first: boolean; last: boolean;
}) {
  const { t } = useT();
  const [editing, setEditing] = useState(false);
  const [desc, setDesc] = useState(p.description);
  const [busy, setBusy] = useState(false);
  const color = colorFor(p.slug);
  const editable = canManage(p);

  async function save() {
    setBusy(true);
    try {
      onSaved(await patch<Project>(`/api/projects/${p.id}`, { description: desc }));
      setEditing(false);
    } finally {
      setBusy(false);
    }
  }

  return (
    <div
      className={`panel pcard p-3 ${active ? "pcard-active" : ""}`}
      style={{ ["--card-color" as string]: color, opacity: p.status === "archived" ? 0.6 : 1 }}
      onMouseEnter={onHover}
      onFocus={onHover}
      onClick={onHover}
      tabIndex={0}
    >
      <div className="flex items-start gap-2">
        <GripVertical size={14} className="muted mt-1 shrink-0 cursor-grab" />
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <span className="truncate font-semibold">{p.name}</span>
            <code className="muted text-xs">{p.slug}</code>
            {p.unread_count > 0 && <span className="badge badge-accent">{p.unread_count}</span>}
          </div>
          {editing ? (
            <div className="mt-1 flex items-start gap-1" onClick={(e) => e.stopPropagation()}>
              <textarea className="input text-sm" rows={2} value={desc} placeholder={t("home.descriptionPlaceholder")} onChange={(e) => setDesc(e.target.value)} autoFocus />
              <button className="btn btn-sm btn-primary" disabled={busy} onClick={save} title={t("common.save")}><Check size={13} /></button>
              <button className="btn btn-sm" onClick={() => { setDesc(p.description); setEditing(false); }} title={t("common.cancel")}><X size={13} /></button>
            </div>
          ) : (
            <p className="muted mt-1 line-clamp-2 text-sm">
              {p.description || <span className="italic">{t("home.descriptionPlaceholder")}</span>}
              {editable && (
                <button className="btn btn-sm ml-1 align-middle" onClick={(e) => { e.stopPropagation(); setEditing(true); }} title={t("home.editDescription")}><Pencil size={11} /></button>
              )}
            </p>
          )}
          <div className="mt-1 flex items-center gap-1 text-xs">
            <span className="badge">{p.my_role ? t(`role.${p.my_role}`) : t("common.noAccess")}</span>
            {p.relay_enabled && <span className="badge badge-accent"><Send size={10} className="mr-1 inline" />{t("projects.relay")}</span>}
            {p.auto_join && <span className="badge">{t("projects.autoJoin")}</span>}
            {p.status === "archived" && <span className="badge badge-danger">{t("projects.archived")}</span>}
          </div>
        </div>
        <div className="flex shrink-0 flex-col gap-1" onClick={(e) => e.stopPropagation()}>
          <button className="btn btn-sm" disabled={first} onClick={() => onMove(-1)} title={t("home.moveUp")}><ArrowUp size={12} /></button>
          <button className="btn btn-sm" disabled={last} onClick={() => onMove(1)} title={t("home.moveDown")}><ArrowDown size={12} /></button>
        </div>
      </div>
    </div>
  );
}

export default function HomePage() {
  const { user } = useAuth();
  const { t } = useT();
  const lite = useLite();
  const [projects, setProjects] = useState<Project[] | null>(null);
  const [order, setOrder] = useState<string[]>([]);
  const [activeSlug, setActiveSlug] = useState<string | null>(null);
  const [dragSlug, setDragSlug] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [showNew, setShowNew] = useState(false);
  const [page, setPage] = useState(0);
  const [size, setSize] = useState(25);

  const load = useCallback(async () => {
    try {
      setProjects(await api<Project[]>("/api/projects"));
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err));
    }
  }, []);
  useEffect(() => {
    setOrder(loadOrder());
    if (user) void load();
  }, [user, load]);

  // Saved order first, unknown projects appended in server order.
  const sorted = useMemo(() => {
    if (!projects) return [];
    const idx = new Map(order.map((s, i) => [s, i]));
    return [...projects].sort((a, b) => (idx.get(a.slug) ?? 1e9) - (idx.get(b.slug) ?? 1e9));
  }, [projects, order]);

  useEffect(() => {
    if (!activeSlug && sorted.length) setActiveSlug(sorted[0].slug);
  }, [sorted, activeSlug]);

  function persist(slugs: string[]) {
    setOrder(slugs);
    saveOrder(slugs);
  }
  function move(slug: string, dir: -1 | 1) {
    const slugs = sorted.map((p) => p.slug);
    const i = slugs.indexOf(slug);
    const j = i + dir;
    if (j < 0 || j >= slugs.length) return;
    [slugs[i], slugs[j]] = [slugs[j], slugs[i]];
    persist(slugs);
  }
  function dropOn(target: string) {
    if (!dragSlug || dragSlug === target) return;
    const slugs = sorted.map((p) => p.slug).filter((s) => s !== dragSlug);
    slugs.splice(slugs.indexOf(target), 0, dragSlug);
    persist(slugs);
    setDragSlug(null);
  }

  const active = sorted.find((p) => p.slug === activeSlug) || null;
  const manage = active ? canManage(active) || user?.role === "auditor" : false;
  const color = active ? colorFor(active.slug) : "var(--accent)";
  const actions = active
    ? [
        { href: `/projects/${active.slug}/dev`, icon: <KeyRound size={22} />, title: t("hub.dev.title"), text: t("hub.dev.text") },
        { href: `/projects/${active.slug}/inbox`, icon: <Inbox size={22} />, title: t("hub.inbox.title"), text: t("hub.inbox.text", { n: active.email_count, unread: active.unread_count }), badge: active.unread_count },
        ...(manage ? [{ href: `/projects/${active.slug}/relay`, icon: <Send size={22} />, title: t("relay.queue.title"), text: active.relay_enabled ? t("hub.relay.text") : t("relay.disabled"), badge: active.relay_enabled ? pendingRelayOf(active) : undefined }] : []),
        ...(manage ? [{ href: `/projects/${active.slug}/manage`, icon: <Settings2 size={22} />, title: t("hub.manage.title"), text: t("hub.manage.text") }] : []),
      ]
    : [];

  return (
    <Shell>
      <div className="flex h-full flex-col md:flex-row">
        <aside className="flex w-full shrink-0 flex-col border-r md:w-[420px]" style={{ borderColor: "var(--border)" }}>
          <div className="flex items-center justify-between border-b px-4 py-2" style={{ borderColor: "var(--border)" }}>
            <h1 className="font-semibold">{t("projects.title")} <span className="muted text-xs">({sorted.length})</span></h1>
            {user?.role === "admin" && !lite && (
              <button className="btn btn-sm btn-primary" onClick={() => setShowNew(true)}><Plus size={14} /> {t("projects.new")}</button>
            )}
          </div>
          <div className="min-h-0 flex-1 scroll space-y-2 p-3">
            <ErrorBox error={error} />
            {!projects ? (
              <Spinner />
            ) : sorted.length === 0 ? (
              <div className="panel p-6 text-center muted text-sm">{t("projects.empty")}</div>
            ) : (
              sorted.slice(page * size, (page + 1) * size).map((p, i) => (
                <div
                  key={p.id}
                  draggable
                  onDragStart={() => setDragSlug(p.slug)}
                  onDragOver={(e) => e.preventDefault()}
                  onDrop={() => dropOn(p.slug)}
                  onDragEnd={() => setDragSlug(null)}
                  className={dragSlug === p.slug ? "pcard-dragging" : ""}
                >
                  <ProjectCard
                    p={p}
                    active={p.slug === activeSlug}
                    first={page * size + i === 0}
                    last={page * size + i === sorted.length - 1}
                    onHover={() => setActiveSlug(p.slug)}
                    onMove={(dir) => move(p.slug, dir)}
                    onSaved={(np) => setProjects((prev) => (prev || []).map((x) => (x.id === np.id ? { ...x, ...np, my_role: x.my_role, email_count: x.email_count, unread_count: x.unread_count } : x)))}
                  />
                </div>
              ))
            )}
          </div>
          <div className="border-t px-3 py-2" style={{ borderColor: "var(--border)" }}>
            <Pager page={page} size={size} total={sorted.length} onPage={setPage} onSize={setSize} sizes={[10, 25, 50]} />
          </div>
        </aside>

        <section className="min-h-0 flex-1 scroll p-6">
          {!active ? (
            <div className="muted flex h-full items-center justify-center text-sm">{t("home.pick")}</div>
          ) : (
            <div className="mx-auto max-w-3xl">
              <div className="mb-4 flex flex-wrap items-center gap-3">
                <span className="inline-block h-3 w-3 rounded-full" style={{ background: color }} />
                <h2 className="text-xl font-semibold">{active.name}</h2>
                <code className="muted text-sm">{active.slug}</code>
                <span className="badge">{active.my_role ? t(`role.${active.my_role}`) : user?.role === "auditor" ? t("role.auditor") : ""}</span>
                <Link href={`/projects/${active.slug}`} className="btn btn-sm ml-auto">{t("home.open")}</Link>
              </div>
              {active.description && <p className="muted mb-5 text-sm">{active.description}</p>}
              <div className="grid gap-4 sm:grid-cols-2">
                {actions.map((c) => (
                  <Link key={c.href} href={c.href} className="panel acard flex gap-4 p-5 transition hover:shadow-md" style={{ ["--card-color" as string]: color, textDecoration: "none" }}>
                    <div className="flex h-12 w-12 shrink-0 items-center justify-center rounded-xl" style={{ background: "var(--accent-soft)", color }}>{c.icon}</div>
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
                <span>{t("project.retention")}: {active.retention_days}d · {t("project.maxEmails")}: {active.max_emails}</span>
                <span>{active.relay_enabled ? t("relay.enabled") : t("relay.disabled")}</span>
              </div>
            </div>
          )}
        </section>
      </div>
      {showNew && (
        <NewProjectModal
          onClose={() => setShowNew(false)}
          onCreated={() => {
            setShowNew(false);
            void load();
          }}
        />
      )}
    </Shell>
  );
}
