"use client";

import { useCallback, useEffect, useState } from "react";
import { ApiError, api, type AuditEntry, type Paged } from "@/lib/api";
import { fmtDate } from "@/lib/format";
import { useT } from "@/lib/i18n";
import { ErrorBox, Spinner } from "./ui";

const ACTIONS = [
  "", "login", "login_failed", "logout", "project_create", "project_update", "project_delete", "member_add", "member_invite", "member_remove",
  "credentials_init", "credentials_reinit", "credentials_view", "email_view", "email_download", "attachment_download", "email_delete", "inbox_clear",
  "relay_enable", "relay_disable", "relay_config_update", "relay_test", "relay_request", "relay_approve", "relay_sent", "relay_failed", "relay_reject",
  "mail_captured", "smtp_login", "smtp_login_failed", "imap_login", "imap_login_failed", "pop3_login", "pop3_login_failed", "api_auth_failed",
  "user_role_change", "user_disabled_change",
];
const PROTOCOLS = ["", "web", "smtp", "imap", "pop3", "api", "sendgrid", "ses", "azure", "graph", "gmail", "system"];

export default function AuditTable({ endpoint, projects }: { endpoint: string; projects?: { id: string; name: string; slug: string }[] }) {
  const { t } = useT();
  const [items, setItems] = useState<AuditEntry[]>([]);
  const [total, setTotal] = useState(0);
  const [offset, setOffset] = useState(0);
  const [action, setAction] = useState("");
  const [actor, setActor] = useState("");
  const [protocol, setProtocol] = useState("");
  const [projectId, setProjectId] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const limit = 100;

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const sep = endpoint.includes("?") ? "&" : "?";
      const res = await api<Paged<AuditEntry>>(`${endpoint}${sep}limit=${limit}&offset=${offset}&action=${encodeURIComponent(action)}&actor=${encodeURIComponent(actor)}&protocol=${encodeURIComponent(protocol)}${projectId ? `&project_id=${projectId}` : ""}`);
      setItems(res.items);
      setTotal(res.total);
      setError(null);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  }, [endpoint, offset, action, actor, protocol, projectId]);

  useEffect(() => {
    void load();
  }, [load]);

  return (
    <div>
      <div className="mb-3 flex flex-wrap items-center gap-2">
        <select className="input w-auto" value={action} onChange={(e) => { setOffset(0); setAction(e.target.value); }}>
          {ACTIONS.map((a) => <option key={a} value={a}>{a || t("audit.allActions")}</option>)}
        </select>
        <select className="input w-auto" value={protocol} onChange={(e) => { setOffset(0); setProtocol(e.target.value); }}>
          {PROTOCOLS.map((p) => <option key={p} value={p}>{p || t("audit.allProtocols")}</option>)}
        </select>
        {projects && (
          <select className="input w-auto" value={projectId} onChange={(e) => { setOffset(0); setProjectId(e.target.value); }}>
            <option value="">{t("admin.projectFilter")}: {t("admin.filter.all")}</option>
            {projects.map((p) => <option key={p.id} value={p.id}>{p.name} ({p.slug})</option>)}
          </select>
        )}
        <input className="input w-56" placeholder={t("audit.actorFilter")} value={actor} onChange={(e) => { setOffset(0); setActor(e.target.value); }} />
        <span className="muted text-sm">{t("common.entries", { n: total })}</span>
        <div className="ml-auto flex gap-1">
          <button className="btn btn-sm" disabled={offset === 0} onClick={() => setOffset(Math.max(0, offset - limit))}>{t("common.prev")}</button>
          <button className="btn btn-sm" disabled={offset + limit >= total} onClick={() => setOffset(offset + limit)}>{t("common.next")}</button>
        </div>
      </div>
      <ErrorBox error={error} />
      {loading ? (
        <Spinner />
      ) : (
        <table className="tbl">
          <thead>
            <tr><th>{t("audit.time")}</th><th>{t("audit.actor")}</th><th>{t("audit.protocol")}</th><th>{t("audit.action")}</th><th>{t("audit.project")}</th><th>{t("audit.target")}</th><th>{t("audit.detail")}</th><th>{t("audit.ip")}</th></tr>
          </thead>
          <tbody>
            {items.map((e) => (
              <tr key={e.id}>
                <td className="whitespace-nowrap">{fmtDate(e.at)}</td>
                <td>{e.actor}</td>
                <td><span className="badge">{e.protocol}</span></td>
                <td><span className={`badge ${e.action.includes("fail") || e.action.includes("delete") || e.action.includes("clear") || e.action.includes("reject") ? "badge-danger" : e.action === "relay_sent" ? "badge-ok" : ""}`}>{e.action}</span></td>
                <td>{e.project_slug}</td>
                <td className="break-all font-mono text-xs">{e.target}</td>
                <td className="break-all font-mono text-xs">{e.detail && Object.keys(e.detail).length ? JSON.stringify(e.detail) : ""}</td>
                <td className="muted text-xs">{e.ip}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}
