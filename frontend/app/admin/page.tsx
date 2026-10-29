"use client";

import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { Suspense, useCallback, useEffect, useMemo, useState } from "react";
import { Archive, ArchiveRestore, ArrowDown, ArrowUp, FolderKanban, Plus, ScrollText, Search, Trash2, Users } from "lucide-react";
import Shell from "@/components/Shell";
import AuditTable from "@/components/AuditTable";
import { ApiError, api, del, patch, post, type Project, type User } from "@/lib/api";
import { useAuth } from "@/lib/auth";
import { useT } from "@/lib/i18n";
import { useLite } from "@/lib/config";
import { fmtDate } from "@/lib/format";
import { ErrorBox, Field, Modal, Spinner } from "@/components/ui";
import Pager from "@/components/Pager";

type Tab = "projects" | "users" | "logs";

function useSort<T>(items: T[], initial: string, getters: Record<string, (x: T) => string | number | boolean | undefined | null>) {
  const [key, setKey] = useState(initial);
  const [dir, setDir] = useState<1 | -1>(1);
  const sorted = useMemo(() => {
    const g = getters[key];
    if (!g) return items;
    return [...items].sort((a, b) => {
      const va = g(a) ?? "";
      const vb = g(b) ?? "";
      if (va < vb) return -dir;
      if (va > vb) return dir;
      return 0;
    });
  }, [items, key, dir, getters]);
  const toggle = (k: string) => {
    if (k === key) setDir((d) => (d === 1 ? -1 : 1));
    else {
      setKey(k);
      setDir(1);
    }
  };
  const Th = ({ k, children }: { k: string; children: React.ReactNode }) => (
    <th className="cursor-pointer select-none" onClick={() => toggle(k)}>
      <span className="inline-flex items-center gap-1">{children}{key === k && (dir === 1 ? <ArrowUp size={12} /> : <ArrowDown size={12} />)}</span>
    </th>
  );
  return { sorted, Th };
}

function AdminInner() {
  const { user } = useAuth();
  const { t } = useT();
  const router = useRouter();
  const params = useSearchParams();
  const tab = (params.get("tab") as Tab) || "projects";
  const setTab = (tb: Tab) => router.replace(`/admin?tab=${tb}`);
  const isAdmin = user?.role === "admin";

  const [users, setUsers] = useState<User[] | null>(null);
  const [projects, setProjects] = useState<Project[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  const load = useCallback(async () => {
    try {
      const [u, p] = await Promise.all([api<User[]>("/api/admin/users"), api<Project[]>("/api/projects")]);
      setUsers(u);
      setProjects(p);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err));
    }
  }, []);
  useEffect(() => {
    if (user) void load();
  }, [user, load]);

  const tabs: { k: Tab; icon: React.ReactNode }[] = [
    { k: "projects", icon: <FolderKanban size={14} /> },
    { k: "users", icon: <Users size={14} /> },
    { k: "logs", icon: <ScrollText size={14} /> },
  ];

  return (
    <Shell>
      <div className="mx-auto max-w-7xl p-6">
        <div className="mb-4 flex border-b" style={{ borderColor: "var(--border)" }}>
          {tabs.map((tb) => (
            <div key={tb.k} className={`tab inline-flex items-center gap-1 ${tab === tb.k ? "tab-active" : ""}`} onClick={() => setTab(tb.k)}>{tb.icon} {t(`admin.tab.${tb.k}`)}</div>
          ))}
        </div>
        <ErrorBox error={error} />
        {notice && <p className="mb-2 break-all font-mono text-sm" style={{ color: "var(--ok)" }}>{notice}</p>}
        {tab === "projects" && (projects ? <ProjectsTab projects={projects} isAdmin={!!isAdmin} reload={load} setError={setError} /> : <Spinner />)}
        {tab === "users" && (users ? <UsersTab users={users} me={user!} isAdmin={!!isAdmin} reload={load} setError={setError} setNotice={setNotice} /> : <Spinner />)}
        {tab === "logs" && <div className="panel p-4"><AuditTable endpoint="/api/admin/audit" projects={projects || []} /></div>}
      </div>
    </Shell>
  );
}

export default function AdminPage() {
  return (
    <Suspense>
      <AdminInner />
    </Suspense>
  );
}

// ---------------- Projects ----------------

function ProjectsTab({ projects, isAdmin, reload, setError }: { projects: Project[]; isAdmin: boolean; reload: () => void; setError: (s: string | null) => void }) {
  const { t } = useT();
  const lite = useLite();
  const [q, setQ] = useState("");
  const [status, setStatus] = useState<"" | "active" | "archived">("");
  const [showNew, setShowNew] = useState(false);
  const [page, setPage] = useState(0);
  const [size, setSize] = useState(25);
  useEffect(() => setPage(0), [q, status, size]);
  const filtered = useMemo(() => {
    const s = q.toLowerCase();
    return projects.filter((p) => (!status || p.status === status) && (!s || p.name.toLowerCase().includes(s) || p.slug.includes(s) || p.description.toLowerCase().includes(s)));
  }, [projects, q, status]);
  const getters = useMemo(() => ({
    name: (p: Project) => p.name.toLowerCase(),
    status: (p: Project) => p.status,
    emails: (p: Project) => p.email_count,
    unread: (p: Project) => p.unread_count,
    retention: (p: Project) => p.retention_days,
    created: (p: Project) => p.created_at,
  }), []);
  const { sorted, Th } = useSort(filtered, "name", getters);

  async function setStatusOf(p: Project, st: "active" | "archived") {
    if (st === "archived" && !confirm(t("admin.archiveConfirm", { name: p.name }))) return;
    try {
      await patch(`/api/projects/${p.id}`, { status: st });
      reload();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err));
    }
  }
  async function remove(p: Project) {
    if (!confirm(t("admin.deleteProjectConfirm", { name: p.name }))) return;
    try {
      await del(`/api/projects/${p.id}`);
      reload();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err));
    }
  }

  return (
    <div className="panel p-4">
      <div className="mb-3 flex flex-wrap items-center gap-2">
        <div className="relative">
          <Search size={14} className="muted absolute left-2.5 top-2.5" />
          <input className="input w-64 pl-8" placeholder={t("admin.search")} value={q} onChange={(e) => setQ(e.target.value)} />
        </div>
        <div className="seg">
          {(["", "active", "archived"] as const).map((s) => (
            <button key={s} type="button" className={status === s ? "on" : ""} onClick={() => setStatus(s)}>{t(`admin.filter.${s || "all"}`)}</button>
          ))}
        </div>
        <span className="muted text-xs">{t("common.entries", { n: sorted.length })} · {t("admin.sortHint")}</span>
        {isAdmin && !lite && <button className="btn btn-primary btn-sm ml-auto" onClick={() => setShowNew(true)}><Plus size={14} /> {t("projects.new")}</button>}
      </div>
      <div className="overflow-x-auto">
        <table className="tbl">
          <thead>
            <tr>
              <Th k="name">{t("admin.project")}</Th>
              <Th k="status">{t("admin.status")}</Th>
              <Th k="emails">{t("admin.emails")}</Th>
              <Th k="retention">{t("admin.retention")}</Th>
              <Th k="created">{t("admin.created")}</Th>
              <th>{t("admin.actions")}</th>
            </tr>
          </thead>
          <tbody>
            {sorted.slice(page * size, (page + 1) * size).map((p) => (
              <tr key={p.id} style={{ opacity: p.status === "archived" ? 0.7 : 1 }}>
                <td>
                  <Link className="font-medium hover:underline" href={`/projects/${p.slug}`}>{p.name}</Link> <code className="muted text-xs">{p.slug}</code>
                  {p.auto_join && <span className="badge ml-2">{t("projects.autoJoin")}</span>}
                  {p.relay_enabled && <span className="badge badge-accent ml-2">{t("projects.relay")}</span>}
                  {p.description && <div className="muted max-w-md truncate text-xs">{p.description}</div>}
                </td>
                <td><span className={`badge ${p.status === "archived" ? "badge-danger" : "badge-ok"}`}>{p.status === "archived" ? t("projects.archived") : t("admin.active")}</span></td>
                <td>{p.email_count} <span className="muted text-xs">({t("admin.unread", { n: p.unread_count })})</span></td>
                <td className="muted text-xs">{p.retention_days}d / {p.max_emails}</td>
                <td className="muted text-xs">{fmtDate(p.created_at)}</td>
                <td>
                  <div className="flex flex-wrap gap-1">
                    <Link className="btn btn-sm" href={`/projects/${p.slug}/inbox`}>{t("project.inbox")}</Link>
                    <Link className="btn btn-sm" href={`/projects/${p.slug}/manage`}>{t("common.manage")}</Link>
                    {isAdmin && (p.status === "active" ? (
                      <button className="btn btn-sm" onClick={() => setStatusOf(p, "archived")}><Archive size={13} /> {t("admin.archive")}</button>
                    ) : (
                      <button className="btn btn-sm" onClick={() => setStatusOf(p, "active")}><ArchiveRestore size={13} /> {t("admin.reactivate")}</button>
                    ))}
                    {isAdmin && !p.auto_join && <button className="btn btn-sm btn-danger" onClick={() => remove(p)}><Trash2 size={13} /></button>}
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <div className="mt-2"><Pager page={page} size={size} total={sorted.length} onPage={setPage} onSize={setSize} /></div>
      {showNew && <NewProjectModal onClose={() => setShowNew(false)} onCreated={() => { setShowNew(false); reload(); }} />}
    </div>
  );
}

function NewProjectModal({ onClose, onCreated }: { onClose: () => void; onCreated: () => void }) {
  const { t } = useT();
  const [form, setForm] = useState({ slug: "", name: "", owner_username: "", description: "" });
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await post<Project>("/api/projects", { ...form, init: true });
      onCreated();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }
  return (
    <Modal title={t("projects.create.title")} onClose={onClose}>
      <form onSubmit={submit} className="space-y-3">
        <Field label={t("projects.create.slug")} hint={t("projects.create.slugHint")}><input className="input" value={form.slug} onChange={(e) => setForm({ ...form, slug: e.target.value.toLowerCase() })} pattern="[a-z0-9][a-z0-9-]{1,62}" required autoFocus /></Field>
        <Field label={t("projects.create.name")}><input className="input" value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} /></Field>
        <Field label={t("projects.create.owner")} hint={t("projects.create.ownerHint")}><input className="input" value={form.owner_username} onChange={(e) => setForm({ ...form, owner_username: e.target.value })} /></Field>
        <Field label={t("projects.create.description")}><input className="input" value={form.description} onChange={(e) => setForm({ ...form, description: e.target.value })} /></Field>
        <ErrorBox error={error} />
        <div className="flex justify-end gap-2">
          <button type="button" className="btn" onClick={onClose}>{t("common.cancel")}</button>
          <button className="btn btn-primary" disabled={busy || !form.slug}>{t("projects.create.submit")}</button>
        </div>
      </form>
    </Modal>
  );
}

// ---------------- Users ----------------

function UsersTab({ users, me, isAdmin, reload, setError, setNotice }: { users: User[]; me: User; isAdmin: boolean; reload: () => void; setError: (s: string | null) => void; setNotice: (s: string | null) => void }) {
  const { t } = useT();
  const lite = useLite();
  const [q, setQ] = useState("");
  const [source, setSource] = useState<"" | "ldap" | "local">("");
  const [role, setRole] = useState("");
  const [dept, setDept] = useState("");
  const [page, setPage] = useState(0);
  const [PAGE, setPAGE] = useState(50);
  const [showNew, setShowNew] = useState(false);
  const [nu, setNu] = useState({ username: "", display_name: "", email: "", password: "" });

  const filtered = useMemo(() => {
    const s = q.toLowerCase();
    return users.filter((u) => (!source || u.auth_source === source) && (!role || u.role === role) && (!dept || u.department === dept) && (!s || u.username.includes(s) || u.display_name.toLowerCase().includes(s) || u.email.toLowerCase().includes(s) || u.department.toLowerCase().includes(s)));
  }, [users, q, source, role, dept]);
  const departments = useMemo(() => Array.from(new Set(users.map((u) => u.department).filter(Boolean))).sort(), [users]);
  useEffect(() => setPage(0), [q, source, role, dept]);
  const getters = useMemo(() => ({
    user: (u: User) => (u.display_name || u.username).toLowerCase(),
    email: (u: User) => u.email,
    role: (u: User) => u.role,
    source: (u: User) => u.auth_source,
    dept: (u: User) => u.department,
    login: (u: User) => u.last_login_at || "",
    status: (u: User) => (u.disabled ? 1 : 0),
  }), []);
  const { sorted, Th } = useSort(filtered, "user", getters);

  const run = async (fn: () => Promise<unknown>) => {
    setError(null);
    try {
      await fn();
      reload();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err));
    }
  };
  async function createLocal(e: React.FormEvent) {
    e.preventDefault();
    await run(async () => {
      const r = await post<{ user: User; password?: string }>("/api/admin/local-users", nu);
      setNotice(r.password ? t("admin.oneTimePassword", { pw: r.password }) : `${r.user.username} ✓`);
      setNu({ username: "", display_name: "", email: "", password: "" });
      setShowNew(false);
    });
  }
  const locked = (u: User) => !!u.locked_until && new Date(u.locked_until) > new Date();

  return (
    <div className="panel p-4">
      <div className="mb-3 flex flex-wrap items-center gap-2">
        <div className="relative">
          <Search size={14} className="muted absolute left-2.5 top-2.5" />
          <input className="input w-64 pl-8" placeholder={t("admin.search")} value={q} onChange={(e) => setQ(e.target.value)} />
        </div>
        {!lite && (
          <div className="seg">
            {(["", "ldap", "local"] as const).map((s) => (
              <button key={s} type="button" className={source === s ? "on" : ""} onClick={() => setSource(s)}>{s ? t(`admin.source.${s}`) : t("admin.filter.all")}</button>
            ))}
          </div>
        )}
        <select className="input w-auto" value={role} onChange={(e) => setRole(e.target.value)}>
          <option value="">{t("admin.role")}: {t("admin.filter.all")}</option>
          {["admin", "auditor", "user"].map((r) => <option key={r} value={r}>{t(`role.${r}`)}</option>)}
        </select>
        {!lite && (
          <select className="input w-auto" value={dept} onChange={(e) => setDept(e.target.value)}>
            <option value="">{t("admin.department")}: {t("admin.filter.all")}</option>
            {departments.map((d) => <option key={d} value={d}>{d}</option>)}
          </select>
        )}
        {isAdmin && <button className="btn btn-primary btn-sm ml-auto" onClick={() => setShowNew((v) => !v)}><Plus size={14} /> {t(lite ? "admin.newUser" : "admin.newExternal")}</button>}
      </div>
      {showNew && isAdmin && (
        <form onSubmit={createLocal} className="mb-4 flex flex-wrap items-end gap-2 rounded-lg border p-3" style={{ borderColor: "var(--border)" }}>
          <Field label={t("login.username")}><input className="input w-44" value={nu.username} onChange={(e) => setNu({ ...nu, username: e.target.value.toLowerCase() })} required /></Field>
          <Field label={t("admin.displayName")}><input className="input w-44" value={nu.display_name} onChange={(e) => setNu({ ...nu, display_name: e.target.value })} /></Field>
          <Field label={t("admin.email")}><input className="input w-52" type="email" value={nu.email} onChange={(e) => setNu({ ...nu, email: e.target.value })} /></Field>
          <Field label={t("admin.passwordOptional")}><input className="input w-52" type="text" autoComplete="off" value={nu.password} onChange={(e) => setNu({ ...nu, password: e.target.value })} /></Field>
          <button className="btn btn-primary">{t("admin.create")}</button>
          <span className="muted w-full text-xs">{t(lite ? "admin.newUserHint" : "admin.localUsersHint")}</span>
        </form>
      )}
      <div className="overflow-x-auto">
        <table className="tbl">
          <thead>
            <tr>
              <Th k="user">{t("admin.user")}</Th>
              <Th k="email">{t("admin.email")}</Th>
              {!lite && <Th k="dept">{t("admin.department")}</Th>}
              {!lite && <Th k="source">{t("admin.source")}</Th>}
              <Th k="role">{t("admin.role")}</Th>
              <Th k="login">{t("admin.lastLogin")}</Th>
              <Th k="status">{t("admin.status")}</Th>
              <th>{t("admin.actions")}</th>
            </tr>
          </thead>
          <tbody>
            {sorted.slice(page * PAGE, (page + 1) * PAGE).map((u) => (
              <tr key={u.id} style={{ opacity: u.disabled ? 0.5 : 1 }}>
                <td>{u.display_name || u.username} <span className="muted text-xs">{u.username}</span>{u.ldap_uuid && <div className="muted font-mono text-[10px]">{u.ldap_uuid}</div>}</td>
                <td className="muted text-sm">{u.email}</td>
                {!lite && <td className="muted text-sm">{u.department}</td>}
                {!lite && <td><span className="badge">{t(`admin.source.${u.auth_source}`)}</span></td>}
                <td>
                  {isAdmin && u.id !== me.id && (u.auth_source !== "local" || lite) ? (
                    <select className="input w-28" value={u.role} onChange={(e) => run(() => patch(`/api/admin/users/${u.id}`, { role: e.target.value }))}>
                      {["user", "auditor", "admin"].map((r) => <option key={r} value={r}>{t(`role.${r}`)}</option>)}
                    </select>
                  ) : (
                    <span className="badge">{t(`role.${u.role}`)}</span>
                  )}
                </td>
                <td className="muted text-xs">{u.last_login_at ? fmtDate(u.last_login_at) : t("common.never")}</td>
                <td>
                  <span className={`badge ${u.disabled ? "badge-danger" : "badge-ok"}`}>{u.disabled ? t("admin.disabledBadge") : t("admin.active")}</span>
                  {locked(u) && <span className="badge badge-danger ml-1">{t("admin.locked")}</span>}
                  {u.must_change_password && <span className="badge ml-1" title={t("password.mustChange")}>pw</span>}
                </td>
                <td>
                  {isAdmin && u.id !== me.id && (
                    <div className="flex flex-wrap gap-1">
                      <button className={`btn btn-sm ${u.disabled ? "" : "btn-danger"}`} onClick={() => { if (u.disabled || confirm(t("admin.disableConfirm", { user: u.username }))) void run(() => patch(`/api/admin/users/${u.id}`, { disabled: !u.disabled })); }}>{u.disabled ? t("admin.enable") : t("admin.disable")}</button>
                      {u.auth_source === "local" && (
                        <button className="btn btn-sm" onClick={() => { if (confirm(`${t("admin.resetPassword")}: ${u.username}?`)) void run(async () => { const r = await post<{ password?: string }>(`/api/admin/users/${u.id}/reset-password`, {}); setNotice(r.password ? t("admin.oneTimePassword", { pw: r.password }) : "ok"); }); }}>{t("admin.resetPassword")}</button>
                      )}
                      {locked(u) && <button className="btn btn-sm" onClick={() => run(() => patch(`/api/admin/users/${u.id}`, { unlock: true }))}>{t("admin.unlock")}</button>}
                      <button className="btn btn-sm btn-danger" onClick={() => { if (confirm(t("admin.deleteUserConfirm", { user: u.username }))) void run(() => del(`/api/admin/users/${u.id}`)); }}><Trash2 size={13} /></button>
                    </div>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <div className="mt-2"><Pager page={page} size={PAGE} total={sorted.length} onPage={setPage} onSize={setPAGE} /></div>
    </div>
  );
}
