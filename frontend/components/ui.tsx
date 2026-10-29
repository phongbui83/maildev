"use client";

import { X } from "lucide-react";
import { useEffect, type ReactNode } from "react";
import { useT } from "@/lib/i18n";

export function Spinner({ label }: { label?: string }) {
  const { t } = useT();
  return (
    <div className="flex items-center gap-2 muted text-sm p-4">
      <span className="inline-block h-4 w-4 animate-spin rounded-full border-2 border-current border-t-transparent" />
      {label || t("common.loading")}
    </div>
  );
}

export function ErrorBox({ error }: { error?: string | null }) {
  if (!error) return null;
  return (
    <div className="rounded-lg border px-3 py-2 text-sm" style={{ borderColor: "var(--danger)", color: "var(--danger)" }}>
      {error}
    </div>
  );
}

export function Modal({ title, onClose, children, wide }: { title: string; onClose: () => void; children: ReactNode; wide?: boolean }) {
  const { t } = useT();
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);
  return (
    <div className="fixed inset-0 z-50 flex items-start justify-center bg-black/40 p-4 pt-16" onClick={onClose}>
      <div className={`panel w-full ${wide ? "max-w-3xl" : "max-w-lg"} p-5`} onClick={(e) => e.stopPropagation()}>
        <div className="mb-4 flex items-center justify-between">
          <h2 className="text-lg font-semibold">{title}</h2>
          <button className="btn btn-sm" onClick={onClose} aria-label={t("common.close")}>
            <X size={14} />
          </button>
        </div>
        {children}
      </div>
    </div>
  );
}

export function Field({ label, children, hint }: { label: string; children: ReactNode; hint?: string }) {
  return (
    <label className="block text-sm">
      <span className="mb-1 block font-medium">{label}</span>
      {children}
      {hint && <span className="muted mt-1 block text-xs">{hint}</span>}
    </label>
  );
}

export function Confirm({ title, message, onConfirm, onClose, danger }: { title: string; message: string; onConfirm: () => void; onClose: () => void; danger?: boolean }) {
  const { t } = useT();
  return (
    <Modal title={title} onClose={onClose}>
      <p className="mb-4 text-sm">{message}</p>
      <div className="flex justify-end gap-2">
        <button className="btn" onClick={onClose}>{t("common.cancel")}</button>
        <button className={`btn ${danger ? "btn-danger" : "btn-primary"}`} onClick={onConfirm}>{t("common.confirm")}</button>
      </div>
    </Modal>
  );
}

export function CopyButton({ value }: { value: string }) {
  const { t } = useT();
  return (
    <button
      className="btn btn-sm"
      title={t("common.copy")}
      onClick={async () => {
        try {
          await navigator.clipboard.writeText(value);
        } catch {
          /* clipboard unavailable */
        }
      }}
    >
      {t("common.copy")}
    </button>
  );
}

export function RelayBadge({ status }: { status?: string }) {
  const { t } = useT();
  if (!status || status === "none") return null;
  const cls = status === "sent" ? "badge-ok" : status === "pending" ? "badge-accent" : "badge-danger";
  return <span className={`badge ${cls}`}>{t(`relay.status.${status}`)}</span>;
}

export function SmimeBadge({ smime, valid, signer, error }: { smime?: string; valid?: boolean; signer?: string; error?: string }) {
  const { t } = useT();
  if (!smime) return null;
  if (smime === "encrypted") return <span className="badge" title="S/MIME">{t("smime.encrypted")}</span>;
  const cls = valid ? "badge-ok" : error ? "badge-danger" : "";
  const label = valid ? t("smime.valid", { who: signer || "?" }) : error ? t("smime.invalid", { reason: error }) : t(`smime.${smime}`);
  return <span className={`badge ${cls}`} title={`${t(`smime.${smime}`)}${signer ? " · " + signer : ""}${error ? " · " + error : ""}`}>{label}</span>;
}
