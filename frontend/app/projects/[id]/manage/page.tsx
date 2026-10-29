"use client";

import { useParams, useRouter } from "next/navigation";
import { useState } from "react";
import { Trash2, UserPlus } from "lucide-react";
import Shell from "@/components/Shell";
import ProjectNav from "@/components/ProjectNav";
import AuditTable from "@/components/AuditTable";
import RelayPanel from "@/components/RelayPanel";
import UserPicker, { type DirectoryUser } from "@/components/UserPicker";
import Pager from "@/components/Pager";
import { ApiError, del, patch, post, type Member, type Project, canManage, api } from "@/lib/api";
import { useAuth } from "@/lib/auth";
import { useT } from "@/lib/i18n";
import { useLite } from "@/lib/config";
import { useProject } from "@/lib/project";
import { fmtDate } from "@/lib/format";
import { Confirm, ErrorBox, Field, Spinner } from "@/components/ui";
import { useEffect, useCallback } from "react";

type Tab = "members" | "settings" | "relay" | "audit";

export default function ManagePage() {
  const params = useParams<{ id: string }>();
  const router = useRouter();
  const { user } = useAuth();
  const { t } = useT();
  const { project, pendingRelay, error, reload } = useProject(params.id);
  const [tab, setTab] = useState<Tab>("members");
  const [confirmDelete, setConfirmDelete] = useState(false);

  if (error) return <Shell><div className="p-6"><ErrorBox error={error} /></div></Shell>;
  if (!project) return <Shell><Spinner /></Shell>;

  const manage = canManage(project);
  const isAdmin = user?.role === "admin";
  const isAuditor = user?.role === "auditor";
  if (!manage && !isAuditor) return <Shell><div className="p-6"><ErrorBox error={t("settings.audit.denied")} /></div></Shell>;

  return (
    <Shell>
      <div className="flex h-full flex-col">
        <ProjectNav project={project} pendingRelay={pendingRelay} />
        <div className="min-h-0 flex-1 scroll">
          <div className="mx-auto max-w-5xl p-6">
            <div className="mb-4 flex border-b" style={{ borderColor: "var(--border)" }}>
              {(["members", "settings", "relay", "audit"] as Tab[]).map((tb) => (
                <div key={tb} className={`tab ${tab === tb ? "tab-active" : ""}`} onClick={() => setTab(tb)}>{t(`settings.tab.${tb}`)}</div>
              ))}
            </div>
            {tab === "members" && <MembersPanel project={project} manage={manage} />}
            {tab === "settings" && <SettingsPanel project={project} manage={manage} isAdmin={!!isAdmin} onChange={reload} onDelete={() => setConfirmDelete(true)} />}
            {tab === "relay" && <RelayPanel project={project} isAdmin={!!isAdmin} onChange={reload} />}
            {tab === "audit" && <AuditTable endpoint={`/api/projects/${project.id}/audit`} />}
          </div>
        </div>
      </div>
      {confirmDelete && (
        <Confirm
          title={t("settings.delete.title")}
          message={t("settings.delete.message")}
          danger
          onClose={() => setConfirmDelete(false)}
          onConfirm={async () => {
            await del(`/api/projects/${project.id}`);
            router.replace("/");
          }}
        />
      )}
    </Shell>
  );
}

function MembersPanel({ project, manage }: { project: Project; manage: boolean }) {
  const { t } = useT();
  const [members, setMembers] = useState<Member[] | null>(null);
  const [username, setUsername] = useState("");
  const [picked, setPicked] = useState<DirectoryUser | undefined>();
  const [role, setRole] = useState<"owner" | "member">("member");
  const [error, setError] = useState<string | null>(null);
  const [info, setInfo] = useState<string | null>(null);
  const lite = useLite();
  const isLocalPick = picked?.source === "local" && !lite;
  const [page, setPage] = useState(0);
  const [size, setSize] = useState(25);
  const load = useCallback(async () => {
    try {
      setMembers(await api<Member[]>(`/api/projects/${project.id}/members`));
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err));
    }
  }, [project.id]);
  useEffect(() => {
    void load();
  }, [load]);

  async function add(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setInfo(null);
    try {
      const r = await post<{ pending: boolean; username: string }>(`/api/projects/${project.id}/members`, { username, role });
      setInfo(r.pending ? t("members.invited", { user: r.username }) : t("members.added", { user: r.username }));
      setUsername("");
      setPicked(undefined);
      void load();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err));
    }
  }
  async function remove(u: string) {
    if (!confirm(t("members.removeConfirm", { user: u, project: project.name }))) return;
    await del(`/api/projects/${project.id}/members/${encodeURIComponent(u)}`);
    void load();
  }
  if (!members) return <Spinner />;
  return (
    <div className="panel p-4">
      <h2 className="mb-3 font-semibold">{t("members.title")}</h2>
      {project.auto_join && <p className="muted mb-3 text-sm">{t("members.autoJoin")}</p>}
      {manage && (
        <form onSubmit={add} className="mb-4 flex flex-wrap items-end gap-2">
          <Field label={t(lite ? "login.username" : "members.username")}>
            <UserPicker value={username} onChange={(u, du) => { setUsername(u); setPicked(du); if (du?.source === "local" && !lite) setRole("member"); }} exclude={new Set(members.map((m) => m.username))} />
          </Field>
          <Field label={t("members.role")} hint={isLocalPick ? t("members.localMemberOnly") : undefined}>
            <select className="input w-32" value={role} onChange={(e) => setRole(e.target.value as "owner" | "member")} disabled={isLocalPick}>
              <option value="member">{t("role.member")}</option>
              <option value="owner">{t("role.owner")}</option>
            </select>
          </Field>
          <button className="btn btn-primary" disabled={!username}><UserPlus size={14} /> {t("members.add")}</button>
        </form>
      )}
      <ErrorBox error={error} />
      {info && <p className="mb-2 text-sm" style={{ color: "var(--ok)" }}>{info}</p>}
      <table className="tbl">
        <thead><tr><th>{t("members.user")}</th><th>{t("members.role")}</th><th>{t("members.since")}</th><th></th></tr></thead>
        <tbody>
          {members.slice(page * size, (page + 1) * size).map((m) => (
            <tr key={m.username}>
              <td>{m.display_name || m.username} <span className="muted text-xs">{m.username}{m.email ? ` · ${m.email}` : ""}</span>{m.pending && <span className="badge ml-2">{t("members.pending")}</span>}</td>
              <td><span className={`badge ${m.role === "owner" ? "badge-accent" : ""}`}>{t(`role.${m.role}`)}</span></td>
              <td className="muted text-xs">{fmtDate(m.added_at)}</td>
              <td className="text-right">{manage && <button className="btn btn-sm btn-danger" onClick={() => remove(m.username)}><Trash2 size={13} /></button>}</td>
            </tr>
          ))}
        </tbody>
      </table>
      <div className="mt-2"><Pager page={page} size={size} total={members.length} onPage={setPage} onSize={setSize} /></div>
    </div>
  );
}

function SettingsPanel({ project, manage, isAdmin, onChange, onDelete }: { project: Project; manage: boolean; isAdmin: boolean; onChange: () => void; onDelete: () => void }) {
  const { t } = useT();
  const [name, setName] = useState(project.name);
  const [description, setDescription] = useState(project.description);
  const [retention, setRetention] = useState(project.retention_days);
  const [maxEmails, setMaxEmails] = useState(project.max_emails);
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);
  async function save(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    try {
      await patch(`/api/projects/${project.id}`, isAdmin ? { name, description, retention_days: retention, max_emails: maxEmails } : { name, description });
      setSaved(true);
      onChange();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err));
    }
  }
  async function setStatus(status: "active" | "archived") {
    await patch(`/api/projects/${project.id}`, { status });
    onChange();
  }
  return (
    <div className="space-y-4">
      <form onSubmit={save} className="panel space-y-3 p-4">
        <h2 className="font-semibold">{t("project.settings")}</h2>
        <Field label={t("project.name")}><input className="input" value={name} onChange={(e) => setName(e.target.value)} disabled={!manage} /></Field>
        <Field label={t("project.description")}><input className="input" value={description} onChange={(e) => setDescription(e.target.value)} disabled={!manage} /></Field>
        <div className="grid grid-cols-2 gap-3">
          <Field label={t("project.retention")} hint={`${t("project.retentionHint")} ${t("project.retentionAdminHint")}`}><input type="number" min={1} max={180} className="input" value={retention} onChange={(e) => setRetention(Number(e.target.value))} disabled={!isAdmin} /></Field>
          <Field label={t("project.maxEmails")} hint={`${t("project.maxEmailsHint")} ${t("project.retentionAdminHint")}`}><input type="number" min={10} max={100000} className="input" value={maxEmails} onChange={(e) => setMaxEmails(Number(e.target.value))} disabled={!isAdmin} /></Field>
        </div>
        <ErrorBox error={error} />
        {manage && (
          <div className="flex items-center gap-3">
            <button className="btn btn-primary">{t("common.save")}</button>
            {saved && <span className="text-sm" style={{ color: "var(--ok)" }}>{t("common.saved")}</span>}
          </div>
        )}
      </form>
      {isAdmin && (
        <div className="panel p-4">
          <h2 className="mb-2 font-semibold" style={{ color: "var(--danger)" }}>{t("project.danger")}</h2>
          <div className="flex flex-wrap gap-2">
            {project.status === "active" ? (
              <button className="btn btn-danger" onClick={() => setStatus("archived")}>{t("project.archive")}</button>
            ) : (
              <button className="btn" onClick={() => setStatus("active")}>{t("project.reactivate")}</button>
            )}
            {!project.auto_join && <button className="btn btn-danger" onClick={onDelete}><Trash2 size={14} /> {t("project.deleteAll")}</button>}
          </div>
        </div>
      )}
    </div>
  );
}
