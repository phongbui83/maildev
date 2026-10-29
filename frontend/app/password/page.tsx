"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import Shell from "@/components/Shell";
import { ApiError, post } from "@/lib/api";
import { useAuth } from "@/lib/auth";
import { useT } from "@/lib/i18n";
import { ErrorBox, Field } from "@/components/ui";

export default function PasswordPage() {
  const { user, refresh } = useAuth();
  const { t } = useT();
  const router = useRouter();
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [confirm, setConfirm] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (next !== confirm) {
      setError(t("password.mismatch"));
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await post("/api/auth/password", { current_password: current, new_password: next });
      await refresh();
      router.replace("/");
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Shell>
      <div className="mx-auto max-w-md p-6">
        <form onSubmit={submit} className="panel space-y-3 p-5">
          <h1 className="text-lg font-semibold">{t("password.title")}</h1>
          {user?.must_change_password && <p className="text-sm" style={{ color: "var(--danger)" }}>{t("password.mustChange")}</p>}
          <Field label={t("password.current")}><input className="input" type="password" autoComplete="current-password" value={current} onChange={(e) => setCurrent(e.target.value)} required /></Field>
          <Field label={t("password.new")}><input className="input" type="password" autoComplete="new-password" value={next} onChange={(e) => setNext(e.target.value)} required minLength={10} /></Field>
          <Field label={t("password.confirm")}><input className="input" type="password" autoComplete="new-password" value={confirm} onChange={(e) => setConfirm(e.target.value)} required /></Field>
          <ErrorBox error={error} />
          <button className="btn btn-primary" disabled={busy}>{t("password.submit")}</button>
        </form>
      </div>
    </Shell>
  );
}
