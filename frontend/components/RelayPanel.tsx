"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import { Send } from "lucide-react";
import { ApiError, api, post, type Project, type RelayState } from "@/lib/api";
import { useT } from "@/lib/i18n";
import { fmtDate } from "@/lib/format";
import { ErrorBox, Field, Spinner } from "./ui";

async function put<T>(path: string, body: unknown): Promise<T> {
  return api<T>(path, { method: "PUT", body: JSON.stringify(body) });
}

export default function RelayPanel({ project, isAdmin, onChange }: { project: Project; isAdmin: boolean; onChange: () => void }) {
  const { t } = useT();
  const [state, setState] = useState<RelayState | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [info, setInfo] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [form, setForm] = useState({
    kind: "smtp", host: "", port: 587, security: "starttls", tls_insecure: false, username: "", secret: "",
    region: "ap-southeast-1", access_key_id: "", endpoint: "", from_override: "", allowed_recipients: "",
  });

  async function load() {
    try {
      const s = await api<RelayState>(`/api/projects/${project.id}/relay`);
      setState(s);
      if (s.config) {
        setForm((f) => ({ ...f, ...s.config, secret: "", port: s.config!.port || 587, region: s.config!.region || f.region }));
      }
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err));
    }
  }
  useEffect(() => {
    void load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [project.id]);

  async function save() {
    setBusy(true);
    setError(null);
    setInfo(null);
    try {
      const s = await put<RelayState>(`/api/projects/${project.id}/relay`, form);
      setState(s);
      setForm((f) => ({ ...f, secret: "" }));
      setInfo(t("common.saved"));
      onChange();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }
  async function toggle(enabled: boolean) {
    setBusy(true);
    setError(null);
    try {
      const s = await put<RelayState>(`/api/projects/${project.id}/relay`, { enabled });
      setState(s);
      onChange();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }
  async function test() {
    setBusy(true);
    setError(null);
    setInfo(null);
    try {
      const r = await post<{ ok: boolean; message: string }>(`/api/projects/${project.id}/relay/test`);
      if (r.ok) setInfo(r.message);
      else setError(r.message);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  if (!state) return <Spinner />;
  const cfg = state.config;
  const set = (k: string, v: unknown) => setForm((f) => ({ ...f, [k]: v }));

  return (
    <div className="space-y-4">
      <div className="panel p-4">
        <div className="mb-2 flex flex-wrap items-center justify-between gap-2">
          <h2 className="flex items-center gap-2 font-semibold"><Send size={16} /> {t("relay.title")}</h2>
          <div className="flex items-center gap-2">
            <span className={`badge ${state.enabled ? "badge-ok" : ""}`}>{state.enabled ? t("relay.enabled") : t("relay.disabled")}</span>
            {isAdmin && (state.enabled ? (
              <button className="btn btn-sm btn-danger" disabled={busy} onClick={() => toggle(false)}>{t("relay.disable")}</button>
            ) : (
              <button className="btn btn-sm btn-primary" disabled={busy || !cfg} onClick={() => toggle(true)}>{t("relay.enable")}</button>
            ))}
            {state.enabled && <Link className="btn btn-sm" href={`/projects/${project.slug}/relay`}>{t("relay.queue.title")}</Link>}
          </div>
        </div>
        <p className="muted text-sm">{t("relay.intro")}</p>
      </div>

      <div className="panel p-4">
        <h3 className="mb-3 font-semibold">{t("relay.target")}</h3>
        <ErrorBox error={error} />
        {info && <p className="mb-2 text-sm" style={{ color: "var(--ok)" }}>{info}</p>}
        {!isAdmin ? (
          cfg ? (
            <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-sm">
              <dt className="muted">{t("relay.kind")}</dt><dd>{t(`relay.kind.${cfg.kind}`)}</dd>
              {cfg.kind === "smtp" && (<><dt className="muted">{t("relay.host")}</dt><dd>{cfg.host}:{cfg.port} ({t(`relay.security.${cfg.security}`)})</dd></>)}
              {cfg.kind === "ses" && (<><dt className="muted">{t("relay.region")}</dt><dd>{cfg.region} · {cfg.access_key_id}</dd></>)}
              {cfg.endpoint && (<><dt className="muted">{t("relay.endpoint")}</dt><dd>{cfg.endpoint}</dd></>)}
              {cfg.from_override && (<><dt className="muted">{t("relay.fromOverride")}</dt><dd>{cfg.from_override}</dd></>)}
              {cfg.allowed_recipients && (<><dt className="muted">{t("relay.allowed")}</dt><dd>{cfg.allowed_recipients}</dd></>)}
              <dt className="muted"></dt><dd className="muted text-xs">{t("relay.updatedBy", { who: cfg.updated_by || "", when: fmtDate(cfg.updated_at) })} · {t("relay.readOnly")}</dd>
            </dl>
          ) : (
            <p className="muted text-sm">{t("relay.noTarget")}</p>
          )
        ) : (
          <form
            className="space-y-3"
            onSubmit={(e) => {
              e.preventDefault();
              void save();
            }}
          >
            <Field label={t("relay.kind")}>
              <select className="input w-auto" value={form.kind} onChange={(e) => set("kind", e.target.value)}>
                <option value="smtp">{t("relay.kind.smtp")}</option>
                <option value="ses">{t("relay.kind.ses")}</option>
                <option value="sendgrid">{t("relay.kind.sendgrid")}</option>
              </select>
            </Field>
            {form.kind === "smtp" && (
              <>
                <div className="grid grid-cols-[1fr_8rem_12rem] gap-3">
                  <Field label={t("relay.host")}><input className="input" value={form.host} onChange={(e) => set("host", e.target.value)} required /></Field>
                  <Field label={t("relay.port")}><input className="input" type="number" min={1} max={65535} value={form.port} onChange={(e) => set("port", Number(e.target.value))} /></Field>
                  <Field label={t("relay.security")}>
                    <select className="input" value={form.security} onChange={(e) => set("security", e.target.value)}>
                      <option value="none">{t("relay.security.none")}</option>
                      <option value="starttls">{t("relay.security.starttls")}</option>
                      <option value="tls">{t("relay.security.tls")}</option>
                    </select>
                  </Field>
                </div>
                <label className="flex items-center gap-2 text-sm">
                  <input type="checkbox" checked={form.tls_insecure} onChange={(e) => set("tls_insecure", e.target.checked)} /> {t("relay.tlsInsecure")}
                </label>
                <div className="grid grid-cols-2 gap-3">
                  <Field label={t("relay.username")}><input className="input" value={form.username} onChange={(e) => set("username", e.target.value)} /></Field>
                  <Field label={t("relay.password")} hint={cfg?.has_secret ? `${t("relay.hasSecret")} · ${t("relay.secretKeep")}` : t("relay.noSecret")}>
                    <input className="input" type="password" autoComplete="new-password" value={form.secret} onChange={(e) => set("secret", e.target.value)} />
                  </Field>
                </div>
              </>
            )}
            {form.kind === "ses" && (
              <div className="grid grid-cols-3 gap-3">
                <Field label={t("relay.region")}><input className="input" value={form.region} onChange={(e) => set("region", e.target.value)} /></Field>
                <Field label={t("relay.accessKeyId")}><input className="input" value={form.access_key_id} onChange={(e) => set("access_key_id", e.target.value)} required /></Field>
                <Field label={t("relay.secretAccessKey")} hint={cfg?.has_secret ? t("relay.secretKeep") : t("relay.noSecret")}>
                  <input className="input" type="password" autoComplete="new-password" value={form.secret} onChange={(e) => set("secret", e.target.value)} />
                </Field>
              </div>
            )}
            {form.kind === "sendgrid" && (
              <Field label={t("relay.apiKey")} hint={cfg?.has_secret ? t("relay.secretKeep") : t("relay.noSecret")}>
                <input className="input" type="password" autoComplete="new-password" value={form.secret} onChange={(e) => set("secret", e.target.value)} />
              </Field>
            )}
            <Field label={t("relay.endpoint")} hint={t("relay.endpointHint")}><input className="input" value={form.endpoint} onChange={(e) => set("endpoint", e.target.value)} placeholder={form.kind === "ses" ? "https://email.ap-southeast-1.amazonaws.com" : form.kind === "sendgrid" ? "https://api.sendgrid.com" : ""} /></Field>
            <div className="grid grid-cols-2 gap-3">
              <Field label={t("relay.fromOverride")} hint={t("relay.fromOverrideHint")}><input className="input" value={form.from_override} onChange={(e) => set("from_override", e.target.value)} placeholder="MailDev <noreply@example.com>" /></Field>
              <Field label={t("relay.allowed")} hint={t("relay.allowedHint")}><input className="input" value={form.allowed_recipients} onChange={(e) => set("allowed_recipients", e.target.value)} placeholder="*@example.com" /></Field>
            </div>
            <div className="flex items-center gap-2">
              <button className="btn btn-primary" disabled={busy}>{t("relay.save")}</button>
              <button type="button" className="btn" disabled={busy || !cfg} onClick={test}>{t("relay.test")}</button>
              {cfg && <span className="muted text-xs">{t("relay.updatedBy", { who: cfg.updated_by || "", when: fmtDate(cfg.updated_at) })}</span>}
            </div>
          </form>
        )}
      </div>
    </div>
  );
}
