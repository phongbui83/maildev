"use client";

import type { Address } from "@/lib/api";

const COLORS = ["#2563eb", "#16a34a", "#d97706", "#dc2626", "#7c3aed", "#0891b2", "#db2777", "#65a30d", "#ea580c", "#4f46e5", "#0d9488", "#9333ea"];

export function initialsOf(a?: Address | null, fallback = "?"): string {
  const name = a?.name?.trim() || a?.address?.split("@")[0] || fallback;
  const parts = name.replace(/[._-]+/g, " ").split(/\s+/).filter(Boolean);
  if (parts.length >= 2) return (parts[0][0] + parts[parts.length - 1][0]).toUpperCase();
  return name.slice(0, 2).toUpperCase();
}

export function colorOf(key: string): string {
  let h = 0;
  for (const ch of key) h = (h * 31 + ch.charCodeAt(0)) >>> 0;
  return COLORS[h % COLORS.length];
}

export default function Avatar({ addr, size = 36 }: { addr?: Address | null; size?: number }) {
  const key = addr?.address || addr?.name || "?";
  return (
    <span
      className="inline-flex shrink-0 items-center justify-center rounded-full font-semibold text-white"
      style={{ width: size, height: size, background: colorOf(key.toLowerCase()), fontSize: size * 0.38 }}
      aria-hidden="true"
    >
      {initialsOf(addr)}
    </span>
  );
}
