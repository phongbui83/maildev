"use client";

import { useEffect, useRef, useState } from "react";
import { Search, UserRound } from "lucide-react";
import { api } from "@/lib/api";
import { useT } from "@/lib/i18n";
import { useLite } from "@/lib/config";

export interface DirectoryUser {
  username: string;
  fullname: string;
  email: string;
  department: string;
  source: "ldap" | "local";
}

// Type-to-search member picker backed by /api/directory/users.
export default function UserPicker({ value, onChange, exclude }: { value: string; onChange: (username: string, user?: DirectoryUser) => void; exclude?: Set<string> }) {
  const { t } = useT();
  const lite = useLite();
  const [q, setQ] = useState(value);
  const [items, setItems] = useState<DirectoryUser[]>([]);
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(0);
  const box = useRef<HTMLDivElement>(null);

  useEffect(() => {
    setQ(value);
  }, [value]);

  useEffect(() => {
    const h = setTimeout(() => {
      api<{ items: DirectoryUser[] }>(`/api/directory/users?q=${encodeURIComponent(q)}`)
        .then((r) => {
          setItems(r.items.filter((u) => !exclude?.has(u.username)));
          setActive(0);
        })
        .catch(() => setItems([]));
    }, 150);
    return () => clearTimeout(h);
  }, [q, exclude]);

  useEffect(() => {
    const onDoc = (e: MouseEvent) => {
      if (box.current && !box.current.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("mousedown", onDoc);
    return () => document.removeEventListener("mousedown", onDoc);
  }, []);

  function pick(u: DirectoryUser) {
    setQ(u.username);
    onChange(u.username, u);
    setOpen(false);
  }

  return (
    <div ref={box} className="relative w-80">
      <Search size={14} className="muted absolute left-2.5 top-2.5" />
      <input
        className="input pl-8"
        placeholder={t(lite ? "members.pickerPlaceholderLite" : "members.pickerPlaceholder")}
        value={q}
        onChange={(e) => {
          setQ(e.target.value);
          onChange(e.target.value.toLowerCase().trim());
          setOpen(true);
        }}
        onFocus={() => setOpen(true)}
        onKeyDown={(e) => {
          if (!open || items.length === 0) return;
          if (e.key === "ArrowDown") {
            e.preventDefault();
            setActive((a) => Math.min(a + 1, items.length - 1));
          } else if (e.key === "ArrowUp") {
            e.preventDefault();
            setActive((a) => Math.max(a - 1, 0));
          } else if (e.key === "Enter") {
            e.preventDefault();
            pick(items[active]);
          } else if (e.key === "Escape") {
            setOpen(false);
          }
        }}
      />
      {open && items.length > 0 && (
        <ul className="panel absolute z-30 mt-1 max-h-72 w-[28rem] overflow-auto py-1 shadow-lg" role="listbox">
          {items.map((u, i) => (
            <li
              key={u.username}
              role="option"
              aria-selected={i === active}
              className={`flex cursor-pointer items-center gap-2 px-3 py-1.5 text-sm ${i === active ? "row-active" : ""}`}
              onMouseEnter={() => setActive(i)}
              onMouseDown={(e) => {
                e.preventDefault();
                pick(u);
              }}
            >
              <UserRound size={14} className="muted shrink-0" />
              <span className="font-medium">{u.fullname || u.username}</span>
              <code className="muted text-xs">{u.username}</code>
              {u.department && <span className="badge">{u.department}</span>}
              {u.source === "local" && !lite && <span className="badge">{t("auth.external")}</span>}
              <span className="muted ml-auto truncate text-xs">{u.email}</span>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
