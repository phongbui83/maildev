"use client";

import { ChevronLeft, ChevronRight } from "lucide-react";
import { useT } from "@/lib/i18n";

// Uniform pagination control: page index (0-based), page size, total count.
export default function Pager({ page, size, total, onPage, onSize, sizes = [25, 50, 100] }: {
  page: number; size: number; total: number; onPage: (p: number) => void; onSize?: (s: number) => void; sizes?: number[];
}) {
  const { t } = useT();
  const pages = Math.max(1, Math.ceil(total / size));
  const from = total === 0 ? 0 : page * size + 1;
  const to = Math.min(total, (page + 1) * size);
  return (
    <div className="flex flex-wrap items-center gap-2 text-xs">
      <span className="muted">{t("pager.range", { from, to, total })}</span>
      {onSize && (
        <select className="input w-auto py-1 text-xs" value={size} onChange={(e) => onSize(Number(e.target.value))}>
          {sizes.map((s) => <option key={s} value={s}>{t("pager.perPage", { n: s })}</option>)}
        </select>
      )}
      <div className="ml-auto flex items-center gap-1">
        <button className="btn btn-sm" disabled={page <= 0} onClick={() => onPage(page - 1)} aria-label={t("common.prev")}><ChevronLeft size={14} /></button>
        <span className="muted">{page + 1} / {pages}</span>
        <button className="btn btn-sm" disabled={page + 1 >= pages} onClick={() => onPage(page + 1)} aria-label={t("common.next")}><ChevronRight size={14} /></button>
      </div>
    </div>
  );
}
