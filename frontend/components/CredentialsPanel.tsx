"use client";

import { useCallback, useEffect, useState } from "react";
import { Cloud, KeyRound, Mail, RefreshCw, Check, Copy } from "lucide-react";
import { ApiError, api, post, type Credential, type Project, type ServerConfig, canManage } from "@/lib/api";
import { useT } from "@/lib/i18n";
import { fmtDate } from "@/lib/format";
import { Confirm, ErrorBox, Spinner } from "./ui";
import SetupGuide from "./SetupGuide";

function CopyIcon({ value }: { value: string }) {
  const [ok, setOk] = useState(false);
  return (
    <button
      className="btn btn-sm"
      style={{ padding: "3px 6px" }}
      title="Copy"
      onClick={async () => {
        try {
          await navigator.clipboard.writeText(value);
          setOk(true);
          setTimeout(() => setOk(false), 1200);
        } catch {
          /* ignore */
        }
      }}
    >
      {ok ? <Check size={13} style={{ color: "var(--ok)" }} /> : <Copy size={13} />}
    </button>
  );
}

function Row({ label, value, mono = true }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="flex items-center gap-2 py-1">
      <span className="muted w-24 shrink-0 text-xs">{label}</span>
      <code className={`min-w-0 flex-1 truncate ${mono ? "" : "font-sans"} text-xs`} title={value}>{value}</code>
      <CopyIcon value={value} />
    </div>
  );
}

// Development information: the project's credentials and how to connect.
export default function CredentialsPanel({ project }: { project: Project }) {
  const { t } = useT();
  const [cfg, setCfg] = useState<ServerConfig | null>(null);
  const [creds, setCreds] = useState<Credential[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [confirmReinit, setConfirmReinit] = useState(false);
  const manage = canManage(project);

  const load = useCallback(async () => {
    try {
      const [c, conf] = await Promise.all([api<Credential[]>(`/api/projects/${project.id}/credentials`), api<ServerConfig>("/api/config")]);
      setCreds(c);
      setCfg(conf);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err));
    }
  }, [project.id]);
  useEffect(() => {
    void load();
  }, [load]);

  async function init(reinit: boolean) {
    setConfirmReinit(false);
    try {
      setCreds(await post<Credential[]>(`/api/projects/${project.id}/init${reinit ? "?reinit=1" : ""}`));
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err));
    }
  }

  if (!cfg || !creds) return <Spinner />;
  const smtp = creds.find((c) => c.kind === "smtp");
  const apiKey = creds.find((c) => c.kind === "api");
  const ses = creds.find((c) => c.kind === "ses");
  const host = cfg.mail_host;
  const lastUsed = (c?: Credential) => (c?.last_used_at ? fmtDate(c.last_used_at) : t("common.never"));

  return (
    <div className="space-y-5">
      <ErrorBox error={error} />
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 className="flex items-center gap-2 font-semibold"><KeyRound size={16} /> {t("settings.credentials")}</h2>
        <div className="flex gap-2">
          {manage && creds.length === 0 && <button className="btn btn-sm btn-primary" onClick={() => init(false)}>{t("settings.initialize")}</button>}
          {manage && creds.length > 0 && <button className="btn btn-sm btn-danger" onClick={() => setConfirmReinit(true)}><RefreshCw size={14} /> {t("settings.reinit")}</button>}
        </div>
      </div>
      {creds.length === 0 ? (
        <p className="muted text-sm">{manage ? t("settings.noCreds.owner") : t("settings.noCreds.member")}</p>
      ) : (
        <div className="grid gap-3 md:grid-cols-3">
          <div className="panel acard p-4" style={{ ["--card-color" as string]: "#0f6cbd" }}>
            <div className="mb-2 flex items-center gap-2 font-semibold"><Mail size={16} /> SMTP · IMAP · POP3</div>
            <Row label="Host" value={host} />
            <Row label="SMTP" value={[...cfg.smtp_ports, ...cfg.smtps_ports.map((p) => `${p} (TLS)`)].join(", ")} />
            <Row label="IMAP / POP3" value={`${[...cfg.imap_ports, ...cfg.imaps_ports.map((p) => `${p} (TLS)`)].join(", ")} / ${[...(cfg.pop3_ports || []), ...(cfg.pop3s_ports || []).map((p) => `${p} (TLS)`)].join(", ")}`} />
            <Row label={t("login.username")} value={smtp?.username || ""} />
            <Row label={t("login.password")} value={smtp?.secret || ""} />
            <div className="muted mt-1 text-xs">{t("settings.lastUsed")}: {lastUsed(smtp)}</div>
          </div>
          <div className="panel acard p-4" style={{ ["--card-color" as string]: "#107c41" }}>
            <div className="mb-2 flex items-center gap-2 font-semibold"><KeyRound size={16} /> API key</div>
            <Row label="Key ID" value={apiKey?.username || ""} />
            <Row label="Secret" value={apiKey?.secret || ""} />
            <Row label={t("settings.secret")} value={apiKey ? `${apiKey.username}.${apiKey.secret}` : ""} />
            <div className="muted mt-1 text-xs">REST · SendGrid · Azure · Graph · Gmail — {t("settings.lastUsed")}: {lastUsed(apiKey)}</div>
          </div>
          <div className="panel acard p-4" style={{ ["--card-color" as string]: "#d97706" }}>
            <div className="mb-2 flex items-center gap-2 font-semibold"><Cloud size={16} /> AWS SES</div>
            <Row label="Access key" value={ses?.username || ""} />
            <Row label="Secret key" value={ses?.secret || ""} />
            <Row label="Endpoint" value={`${cfg.public_url}/ses`} />
            <div className="muted mt-1 text-xs">{t("settings.lastUsed")}: {lastUsed(ses)}</div>
          </div>
        </div>
      )}
      {creds.length > 0 && (
        <div>
          <h2 className="mb-3 font-semibold">{t("settings.howToConnect")}</h2>
          <SetupGuide cfg={cfg} creds={creds} />
        </div>
      )}
      {confirmReinit && (
        <Confirm title={t("settings.reinit.title")} message={t("settings.reinit.message")} danger onClose={() => setConfirmReinit(false)} onConfirm={() => init(true)} />
      )}
    </div>
  );
}
