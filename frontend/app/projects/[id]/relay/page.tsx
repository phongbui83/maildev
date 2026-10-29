"use client";

import Link from "next/link";
import { useParams } from "next/navigation";
import { useCallback, useEffect, useState } from "react";
import { ArrowLeft, Ban, RefreshCw, Send } from "lucide-react";
import Shell from "@/components/Shell";
import ProjectNav from "@/components/ProjectNav";
import { ApiError, api, post, type Email, type Paged, type Project, type RelayOutcome, canManage } from "@/lib/api";
import { fmtAddrs, fmtDate } from "@/lib/format";
import { useT } from "@/lib/i18n";
import { ErrorBox, Spinner, RelayBadge } from "@/components/ui";
import Pager from "@/components/Pager";

export default function RelayQueuePage() {
  const params = useParams<{ id: string }>();
  const { t } = useT();
  const [project, setProject] = useState<Project | null>(null);
  const [items, setItems] = useState<Email[]>([]);
  const [total, setTotal] = useState(0);
  const [status, setStatus] = useState("pending");
  const [page, setPage] = useState(0);
  const [size, setSize] = useState(50);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [note, setNote] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [summary, setSummary] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [loading, setLoading] = useState(true);

  const load = useCallback(async () => {
    try {
      const p = project ?? (await api<Project>(`/api/projects/${params.id}`));
      if (!project) setProject(p);
      const r = await api<Paged<Email>>(`/api/projects/${p.id}/relay/queue?status=${status}&limit=${size}&offset=${page * size}`);
      setItems(r.items);
      setTotal(r.total);
      setSelected(new Set());
      setError(null);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  }, [params.id, project, status, page, size]);

  useEffect(() => {
    void load();
  }, [load]);

  useEffect(() => {
    if (!project) return;
    const es = new EventSource(`/api/projects/${project.id}/events`);
    const refresh = () => void load();
    es.addEventListener("relay", refresh);
    es.addEventListener("new", refresh);
    return () => es.close();
  }, [project, load]);

  async function decide(action: "approve" | "reject", body: { ids?: string[]; all?: boolean }) {
    if (!project) return;
    const n = body.all ? total : body.ids?.length || 0;
    if (action === "approve" && !confirm(body.all ? t("relay.queue.approveAllConfirm", { n }) : t("relay.queue.approveConfirm", { n }))) return;
    setBusy(true);
    setError(null);
    setSummary(null);
    try {
      const r = await post<{ results: RelayOutcome[] }>(`/api/projects/${project.id}/relay/${action}`, { ...body, note });
      const sent = r.results.filter((x) => x.status === "sent").length;
      const failed = r.results.filter((x) => x.status === "failed").length;
      setSummary(t("relay.queue.results", { sent, failed, other: r.results.length - sent - failed }));
      const firstErr = r.results.find((x) => x.error);
      if (firstErr) setError(firstErr.error || null);
      await load();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  const manage = canManage(project);
  const allSelected = items.length > 0 && selected.size === items.length;

  return (
    <Shell>
      {project && <ProjectNav project={project} pendingRelay={status === "pending" ? total : undefined} />}
      <div className="mx-auto max-w-6xl p-6">
        <div className="mb-4 flex flex-wrap items-center gap-3">
          <Link href={`/projects/${params.id}/inbox`} className="btn btn-sm"><ArrowLeft size={14} /> {t("common.inbox")}</Link>
          <h1 className="text-xl font-semibold">{t("relay.queue.title")}</h1>
          <button className="btn btn-sm" onClick={() => void load()} title={t("common.refresh")}><RefreshCw size={14} /></button>
          <select className="input ml-auto w-auto" value={status} onChange={(e) => { setPage(0); setStatus(e.target.value); }}>
            <option value="pending">{t("relay.status.pending")}</option>
            <option value="sent">{t("relay.status.sent")}</option>
            <option value="failed">{t("relay.status.failed")}</option>
            <option value="rejected">{t("relay.status.rejected")}</option>
            <option value="">{t("common.all")}</option>
          </select>
        </div>
        {project && !project.relay_enabled && <p className="muted mb-3 text-sm">{t("relay.notEnabledHint")}</p>}
        <ErrorBox error={error} />
        {summary && <p className="mb-2 text-sm" style={{ color: "var(--ok)" }}>{summary}</p>}

        {manage && project?.relay_enabled && (
          <div className="panel mb-3 flex flex-wrap items-center gap-2 p-3">
            <input className="input w-72" placeholder={t("relay.queue.note")} value={note} onChange={(e) => setNote(e.target.value)} />
            <button className="btn btn-primary btn-sm" disabled={busy || selected.size === 0} onClick={() => decide("approve", { ids: [...selected] })}>
              <Send size={14} /> {t("relay.queue.approveSelected")} ({selected.size})
            </button>
            <button className="btn btn-danger btn-sm" disabled={busy || selected.size === 0} onClick={() => decide("reject", { ids: [...selected] })}>
              <Ban size={14} /> {t("relay.queue.rejectSelected")} ({selected.size})
            </button>
            {status === "pending" && total > 0 && (
              <button className="btn btn-sm" disabled={busy} onClick={() => decide("approve", { all: true })}>
                {t("relay.queue.approveAll")} ({total})
              </button>
            )}
          </div>
        )}

        {loading ? (
          <Spinner />
        ) : items.length === 0 ? (
          <div className="panel p-8 text-center muted">{t("relay.queue.empty")}</div>
        ) : (
          <div className="panel overflow-x-auto">
            <table className="tbl">
              <thead>
                <tr>
                  {manage && (
                    <th>
                      <input type="checkbox" checked={allSelected} onChange={(e) => setSelected(e.target.checked ? new Set(items.map((i) => i.id)) : new Set())} title={t("common.selectAll")} />
                    </th>
                  )}
                  <th>{t("relay.queue.subject")}</th>
                  <th>{t("viewer.from")}</th>
                  <th>{t("relay.queue.recipients")}</th>
                  <th>{t("relay.queue.requested")}</th>
                  <th>{t("relay.queue.result")}</th>
                  <th></th>
                </tr>
              </thead>
              <tbody>
                {items.map((e) => (
                  <tr key={e.id}>
                    {manage && (
                      <td>
                        <input
                          type="checkbox"
                          checked={selected.has(e.id)}
                          onChange={(ev) => {
                            const s = new Set(selected);
                            if (ev.target.checked) s.add(e.id);
                            else s.delete(e.id);
                            setSelected(s);
                          }}
                        />
                      </td>
                    )}
                    <td className="max-w-xs truncate">{e.subject || t("inbox.noSubject")}</td>
                    <td className="muted max-w-[12rem] truncate text-xs">{fmtAddrs(e.from) || e.envelope_from}</td>
                    <td className="max-w-xs break-all text-xs">{e.envelope_to.length ? e.envelope_to.join(", ") : fmtAddrs(e.to)}</td>
                    <td className="muted whitespace-nowrap text-xs">{fmtDate(e.relay_requested_at || e.received_at)}{e.relay_requested_by && <div>{e.relay_requested_by}</div>}</td>
                    <td className="text-xs">
                      <RelayBadge status={e.relay_status} />
                      {e.relay_decided_by && <div className="muted">{e.relay_decided_by} · {fmtDate(e.relay_decided_at)}</div>}
                      {e.relay_error && <div style={{ color: "var(--danger)" }}>{e.relay_error}</div>}
                      {e.relay_note && <div className="muted">“{e.relay_note}”</div>}
                    </td>
                    <td className="text-right">
                      <Link className="btn btn-sm" href={`/projects/${params.id}/inbox?m=${e.id}`}>{t("relay.queue.open")}</Link>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
            <div className="border-t px-3 py-2" style={{ borderColor: "var(--border)" }}>
              <Pager page={page} size={size} total={total} onPage={setPage} onSize={setSize} />
            </div>
          </div>
        )}
      </div>
    </Shell>
  );
}
