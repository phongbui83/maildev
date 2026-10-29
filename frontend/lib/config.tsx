"use client";

import { useEffect, useState } from "react";
import { api, type ServerConfig } from "./api";

// /api/config is public and static for the lifetime of the server: fetch it
// once per page load and share the result between components.
let cached: ServerConfig | null = null;
let inflight: Promise<ServerConfig> | null = null;

export function loadServerConfig(): Promise<ServerConfig> {
  if (cached) return Promise.resolve(cached);
  if (!inflight) {
    inflight = api<ServerConfig>("/api/config").then((c) => {
      cached = c;
      return c;
    }).finally(() => {
      inflight = null;
    });
  }
  return inflight;
}

export function useServerConfig(): ServerConfig | null {
  const [cfg, setCfg] = useState<ServerConfig | null>(cached);
  useEffect(() => {
    if (!cached) loadServerConfig().then(setCfg).catch(() => {});
  }, []);
  return cfg;
}

// Lite edition: one project, every account is local and may own it.
export function useLite(): boolean {
  return useServerConfig()?.edition === "lite";
}
