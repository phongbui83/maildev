"use client";

import { useCallback, useEffect, useState } from "react";
import { ApiError, api, type Email, type Paged, type Project, canManage } from "./api";

// Loads a project by slug (or id) plus the pending relay count for managers.
export function useProject(ref: string) {
  const [project, setProject] = useState<Project | null>(null);
  const [pendingRelay, setPendingRelay] = useState(0);
  const [error, setError] = useState<string | null>(null);

  const reload = useCallback(async () => {
    try {
      const p = await api<Project>(`/api/projects/${ref}`);
      setProject(p);
      setError(null);
      if (p.relay_enabled && canManage(p)) {
        api<Paged<Email>>(`/api/projects/${p.id}/relay/queue?status=pending&limit=1`).then((r) => setPendingRelay(r.total)).catch(() => {});
      } else {
        setPendingRelay(0);
      }
      return p;
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err));
      return null;
    }
  }, [ref]);

  useEffect(() => {
    void reload();
  }, [reload]);

  return { project, pendingRelay, error, reload };
}
