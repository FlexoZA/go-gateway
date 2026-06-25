"use client";

import { useMemo, useRef, useState } from "react";
import { useConsole } from "@/contexts/console-context";
import { EnvSwitcher } from "./env-switcher";
import { MethodBadge } from "./method-badge";
import { StatusPill } from "./status-pill";
import { formatRelative } from "@/lib/console/time";
import { usePersistedState } from "@/lib/usePersistedState";
import { STORAGE_KEYS } from "@/lib/console/storage";
import type { Collection } from "@/lib/console/types";

type SidebarTab = "collections" | "history";

export function Sidebar() {
  const {
    collections,
    setCollections,
    createCollection,
    deleteCollection,
    deleteRequest,
    renameCollection,
    restoreBuiltInCollections,
    replaceDraft,
    history,
    clearHistory,
  } = useConsole();

  const [tab, setTab] = usePersistedState<SidebarTab>(STORAGE_KEYS.sidebarTab, "collections");
  const [filter, setFilter] = useState("");
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const fileRef = useRef<HTMLInputElement>(null);

  function toggle(id: string) {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }

  const filtered = useMemo(() => {
    if (!filter.trim()) return collections;
    const q = filter.toLowerCase();
    return collections
      .map((c) => ({
        ...c,
        requests: c.requests.filter(
          (r) =>
            r.name.toLowerCase().includes(q) ||
            r.path.toLowerCase().includes(q) ||
            r.method.toLowerCase().includes(q),
        ),
      }))
      .filter((c) => c.name.toLowerCase().includes(q) || c.requests.length > 0);
  }, [collections, filter]);

  function exportJson() {
    const blob = new Blob([JSON.stringify(collections, null, 2)], { type: "application/json" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = "api-console-collections.json";
    a.click();
    URL.revokeObjectURL(url);
  }

  function importJson(file: File) {
    file.text().then((text) => {
      try {
        const parsed = JSON.parse(text) as Collection[];
        if (Array.isArray(parsed)) setCollections(parsed);
      } catch {
        alert("Invalid collections JSON");
      }
    });
  }

  return (
    <div className="flex h-full flex-col">
      <div className="border-b border-edge p-3">
        <EnvSwitcher />
      </div>

      <div className="flex border-b border-edge">
        {(["collections", "history"] as const).map((t) => (
          <button
            key={t}
            type="button"
            onClick={() => setTab(t)}
            className={`flex-1 px-3 py-2 text-xs font-medium capitalize ${
              tab === t ? "border-b-2 border-indigo-500 text-white" : "text-slate-400 hover:text-slate-200"
            }`}
          >
            {t}
          </button>
        ))}
      </div>

      {tab === "collections" && (
        <div className="flex min-h-0 flex-1 flex-col">
          <div className="space-y-2 p-2">
            <input
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
              placeholder="Filter requests…"
              className="input text-xs"
            />
            <div className="flex flex-wrap gap-1">
              <button type="button" onClick={createCollection} className="btn-ghost text-xs">
                + New
              </button>
              <button
                type="button"
                onClick={() => {
                  const { addedCollections, addedRequests } = restoreBuiltInCollections();
                  alert(`Restored ${addedCollections} collections, ${addedRequests} requests.`);
                }}
                className="btn-ghost text-xs"
              >
                Restore defaults
              </button>
              <button type="button" onClick={exportJson} className="btn-ghost text-xs">
                Export
              </button>
              <button type="button" onClick={() => fileRef.current?.click()} className="btn-ghost text-xs">
                Import
              </button>
              <input
                ref={fileRef}
                type="file"
                accept="application/json"
                className="hidden"
                onChange={(e) => e.target.files?.[0] && importJson(e.target.files[0])}
              />
            </div>
          </div>

          <div className="min-h-0 flex-1 overflow-auto px-2 pb-2">
            {filtered.map((c) => {
              const open = expanded.has(c.id) || filter.trim().length > 0;
              return (
                <div key={c.id} className="mb-1">
                  <div className="group flex items-center gap-1 rounded px-1 py-1 hover:bg-edge/60">
                    <button
                      type="button"
                      onClick={() => toggle(c.id)}
                      className="flex flex-1 items-center gap-1 text-left text-sm text-slate-200"
                    >
                      <span className="w-3 select-none text-slate-500">{open ? "▾" : "▸"}</span>
                      <span className="truncate">{c.name}</span>
                      <span className="text-xs text-slate-500">({c.requests.length})</span>
                    </button>
                    <button
                      type="button"
                      onClick={() => {
                        const name = prompt("Rename collection", c.name);
                        if (name) renameCollection(c.id, name);
                      }}
                      className="hidden text-xs text-slate-500 hover:text-slate-200 group-hover:inline"
                      title="Rename"
                    >
                      ✎
                    </button>
                    <button
                      type="button"
                      onClick={() => {
                        if (confirm(`Delete collection "${c.name}"?`)) deleteCollection(c.id);
                      }}
                      className="hidden text-xs text-slate-500 hover:text-rose-300 group-hover:inline"
                      title="Delete"
                    >
                      ✕
                    </button>
                  </div>
                  {open && (
                    <div className="ml-4 border-l border-edge pl-1">
                      {c.requests.map((r) => (
                        <div
                          key={r.id}
                          className="group flex items-center gap-2 rounded px-2 py-1 hover:bg-edge/60"
                        >
                          <button
                            type="button"
                            onClick={() => replaceDraft(r)}
                            className="flex flex-1 items-center gap-2 overflow-hidden text-left"
                            title={r.path}
                          >
                            <MethodBadge method={r.method} className="w-12 shrink-0" />
                            <span className="truncate text-xs text-slate-300">{r.name}</span>
                          </button>
                          <button
                            type="button"
                            onClick={() => deleteRequest(c.id, r.id)}
                            className="hidden text-xs text-slate-500 hover:text-rose-300 group-hover:inline"
                            title="Delete request"
                          >
                            ✕
                          </button>
                        </div>
                      ))}
                    </div>
                  )}
                </div>
              );
            })}
          </div>
        </div>
      )}

      {tab === "history" && (
        <div className="flex min-h-0 flex-1 flex-col">
          <div className="p-2">
            <button type="button" onClick={clearHistory} className="btn-ghost w-full text-xs">
              Clear history
            </button>
          </div>
          <div className="min-h-0 flex-1 overflow-auto px-2 pb-2">
            {history.length === 0 && <p className="px-1 py-2 text-xs text-slate-500">No history yet.</p>}
            {history.map((h) => (
              <button
                key={h.id}
                type="button"
                onClick={() => replaceDraft(h.request)}
                className="mb-1 flex w-full items-center gap-2 rounded px-2 py-1.5 text-left hover:bg-edge/60"
              >
                <MethodBadge method={h.request.method} className="w-12 shrink-0" />
                <span className="flex-1 truncate font-mono text-xs text-slate-300" title={h.resolvedPath}>
                  {h.resolvedPath}
                </span>
                <StatusPill status={h.status} />
                <span className="shrink-0 text-[10px] text-slate-500">{formatRelative(h.timestamp)}</span>
              </button>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}
