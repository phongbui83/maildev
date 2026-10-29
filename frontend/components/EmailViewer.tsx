"use client";

import { useEffect, useState } from "react";
import { Download, Paperclip, Trash2, Image as ImageIcon, Smartphone, Tablet, Monitor, Send, Ban, FileText, File as FileIcon } from "lucide-react";
import { ApiError, api, del, post, type Email, type Project, type RelayOutcome, canWrite, canManage } from "@/lib/api";
import { fmtAddrs, fmtBytes, fmtDate } from "@/lib/format";
import { useT } from "@/lib/i18n";
import Avatar from "./Avatar";
import { Spinner, ErrorBox, RelayBadge, SmimeBadge } from "./ui";

type Tab = "html" | "text" | "headers" | "raw";
const widths: Record<string, string> = { phone: "375px", tablet: "768px", desktop: "100%" };

export default function EmailViewer({ project, emailId, onDeleted, onLoaded }: { project: Project; emailId: string; onDeleted: () => void; onLoaded?: (e: Email) => void }) {
  const { t } = useT();
  const [email, setEmail] = useState<Email | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [tab, setTab] = useState<Tab>("html");
  const [remote, setRemote] = useState(false);
  const [width, setWidth] = useState<"phone" | "tablet" | "desktop">("desktop");
  const [raw, setRaw] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function load(peek = false) {
    try {
      const e = await api<Email>(`/api/projects/${project.id}/emails/${emailId}${peek ? "?peek=1" : ""}`);
      setEmail(e);
      onLoaded?.(e);
      return e;
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err));
      return null;
    }
  }

  useEffect(() => {
    setEmail(null);
    setError(null);
    setRaw(null);
    setRemote(false);
    load().then((e) => e && setTab(e.html_body ? "html" : "text"));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [project.id, emailId]);

  useEffect(() => {
    if (tab === "raw" && raw === null) {
      fetch(`/api/projects/${project.id}/emails/${emailId}/raw`, { credentials: "same-origin" })
        .then((r) => r.text())
        .then(setRaw)
        .catch(() => setRaw("(failed to load)"));
    }
  }, [tab, raw, project.id, emailId]);

  if (error) return <div className="p-4"><ErrorBox error={error} /></div>;
  if (!email) return <Spinner label={t("viewer.loading")} />;

  const base = `/api/projects/${project.id}/emails/${email.id}`;
  const from = email.from?.[0] || (email.envelope_from ? { name: "", address: email.envelope_from } : null);

  async function remove() {
    if (!confirm(t("viewer.deleteConfirm"))) return;
    await del(`${base}`);
    onDeleted();
  }

  async function relay(action: "request" | "approve" | "reject") {
    setBusy(true);
    setError(null);
    try {
      const r = await post<{ results?: RelayOutcome[] } | undefined>(`${base}/relay/${action}`, action === "request" ? undefined : {});
      const out = r?.results?.[0];
      if (out && out.status === "failed") setError(out.error || out.status);
      await load(true);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  const canRequest = project.relay_enabled && canWrite(project) && (email.relay_status === "none" || email.relay_status === "failed" || email.relay_status === "rejected");
  const canDecide = project.relay_enabled && canManage(project) && email.relay_status === "pending";
  const canResend = project.relay_enabled && canManage(project) && email.relay_status === "failed";
  const realAttachments = (email.attachments || []).filter((a) => !a.inline || !a.content_id);

  return (
    <div className="flex h-full flex-col">
      {/* toolbar */}
      <div className="flex flex-wrap items-center gap-1 border-b px-3 py-1.5" style={{ borderColor: "var(--border)", background: "var(--panel)" }}>
        {canWrite(project) && (
          <button className="btn btn-sm" onClick={remove}><Trash2 size={14} /> {t("common.delete")}</button>
        )}
        <a className="btn btn-sm" href={`${base}/raw?download=1`}><Download size={14} /> .eml</a>
        {canRequest && <button className="btn btn-sm" disabled={busy} onClick={() => relay("request")}><Send size={14} /> {t("viewer.relay.request")}</button>}
        {(canDecide || canResend) && (
          <button className="btn btn-sm btn-primary" disabled={busy} onClick={() => confirm(t("relay.queue.approveConfirm", { n: 1 })) && relay("approve")}><Send size={14} /> {t("viewer.relay.approve")}</button>
        )}
        {canDecide && <button className="btn btn-sm btn-danger" disabled={busy} onClick={() => relay("reject")}><Ban size={14} /> {t("viewer.relay.reject")}</button>}
        <div className="ml-auto flex items-center gap-1">
          <div className="seg">
            {(["html", "text", "headers", "raw"] as Tab[]).map((tb) => (
              <button key={tb} type="button" className={tab === tb ? "on" : ""} onClick={() => setTab(tb)}>{t(`viewer.tab.${tb}`)}</button>
            ))}
          </div>
          {tab === "html" && (
            <>
              <button className={`btn btn-sm ${remote ? "btn-primary" : ""}`} onClick={() => setRemote((v) => !v)} title={t("viewer.remoteTitle")}><ImageIcon size={14} /></button>
              <div className="seg">
                <button type="button" className={width === "phone" ? "on" : ""} onClick={() => setWidth("phone")} title="375px"><Smartphone size={13} /></button>
                <button type="button" className={width === "tablet" ? "on" : ""} onClick={() => setWidth("tablet")} title="768px"><Tablet size={13} /></button>
                <button type="button" className={width === "desktop" ? "on" : ""} onClick={() => setWidth("desktop")} title="100%"><Monitor size={13} /></button>
              </div>
            </>
          )}
        </div>
      </div>

      {/* header */}
      <div className="border-b px-5 pb-3 pt-4" style={{ borderColor: "var(--border)" }}>
        <h1 className="mb-3 text-lg font-semibold break-words">
          {email.subject || t("inbox.noSubject")}{" "}
          <SmimeBadge smime={email.smime} valid={email.smime_valid} signer={email.smime_signer} error={email.smime_error} />{" "}
          <RelayBadge status={email.relay_status} />
        </h1>
        <div className="flex items-start gap-3">
          <Avatar addr={from} size={40} />
          <div className="min-w-0 flex-1 text-sm">
            <div className="flex flex-wrap items-baseline justify-between gap-x-3">
              <span>
                <span className="font-semibold">{from?.name || from?.address || t("inbox.unknownSender")}</span>
                {from?.name && <span className="muted"> &lt;{from.address}&gt;</span>}
              </span>
              <span className="muted shrink-0 text-xs">{fmtDate(email.sent_at || email.received_at)}</span>
            </div>
            <div className="muted break-all"><span className="font-medium">{t("viewer.to")}:</span> {fmtAddrs(email.to) || email.envelope_to.join(", ")}</div>
            {email.cc?.length > 0 && <div className="muted break-all"><span className="font-medium">{t("viewer.cc")}:</span> {fmtAddrs(email.cc)}</div>}
            {email.bcc?.length > 0 && <div className="muted break-all"><span className="font-medium">{t("viewer.bcc")}:</span> {fmtAddrs(email.bcc)}</div>}
            <div className="muted mt-1 text-xs">
              {t("viewer.received")} {fmtDate(email.received_at)} · {t("viewer.via", { source: email.source, ip: email.client_ip })} · {fmtBytes(email.size)}
              {email.envelope_to?.length > 0 && <> · {t("viewer.envelope")}: {email.envelope_from} → {email.envelope_to.join(", ")}</>}
            </div>
            {email.relay_status !== "none" && (
              <div className="muted mt-1 text-xs">
                {email.relay_status === "pending" && email.relay_requested_by && t("viewer.relay.requestedBy", { who: email.relay_requested_by })}
                {email.relay_status !== "pending" && email.relay_decided_by && <>{t("viewer.relay.decidedBy", { status: t(`relay.status.${email.relay_status}`), who: email.relay_decided_by })} {fmtDate(email.relay_decided_at)}</>}
                {email.relay_error && <span style={{ color: "var(--danger)" }}> · {email.relay_error}</span>}
                {email.relay_note && <span> · “{email.relay_note}”</span>}
              </div>
            )}
          </div>
        </div>
        {realAttachments.length > 0 && (
          <div className="mt-3 flex flex-wrap gap-2">
            {realAttachments.map((a) => (
              <a key={a.id} href={`${base}/attachments/${a.id}?download=1`} className="btn btn-sm" title={`${a.content_type} · ${fmtBytes(a.size)}`}>
                {a.content_type.startsWith("text/") ? <FileText size={14} /> : <FileIcon size={14} />}
                <span className="max-w-[14rem] truncate">{a.filename}</span>
                <span className="muted text-xs">{fmtBytes(a.size)}</span>
              </a>
            ))}
            <span className="muted self-center text-xs"><Paperclip size={12} className="inline" /> {realAttachments.length}</span>
          </div>
        )}
      </div>

      {/* body */}
      <div className="min-h-0 flex-1 scroll" style={{ background: tab === "html" ? "#fff" : undefined }}>
        {tab === "html" && (
          <div className="flex h-full justify-center" style={{ background: width === "desktop" ? "#fff" : "var(--bg)" }}>
            <iframe key={`${email.id}-${remote}`} title="HTML preview" sandbox="" src={`${base}/html${remote ? "?remote=1" : ""}`} className="h-full border-0 bg-white" style={{ width: widths[width] }} />
          </div>
        )}
        {tab === "text" && <pre className="whitespace-pre-wrap break-words p-5 text-sm">{email.text_body || t("viewer.noText")}</pre>}
        {tab === "headers" && (
          <table className="tbl">
            <tbody>
              {(email.headers || []).map((h, i) => (
                <tr key={i}>
                  <td className="whitespace-nowrap font-medium">{h.key}</td>
                  <td className="break-all font-mono text-xs">{h.value}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
        {tab === "raw" && <pre className="whitespace-pre-wrap break-all p-5 font-mono text-xs">{raw ?? t("common.loading")}</pre>}
      </div>
    </div>
  );
}
