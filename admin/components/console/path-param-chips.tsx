"use client";

import { useEffect, useRef, useState } from "react";
import type { PathParamBinding } from "@/lib/console/types";
import { fetchViaProxy } from "@/lib/console/client";
import { pathPlaceholdersIn } from "@/lib/console/variables";

interface Props {
  path: string;
  pathParams: PathParamBinding[] | undefined;
  onChange: (bindings: PathParamBinding[]) => void;
}

function extractArray(json: unknown, field?: string): Array<Record<string, unknown>> {
  if (Array.isArray(json)) return json as Array<Record<string, unknown>>;
  if (json && typeof json === "object") {
    const obj = json as Record<string, unknown>;
    if (field && Array.isArray(obj[field])) return obj[field] as Array<Record<string, unknown>>;
    // Fall back to the first array-valued property.
    for (const v of Object.values(obj)) {
      if (Array.isArray(v)) return v as Array<Record<string, unknown>>;
    }
  }
  return [];
}

function ListPicker({
  binding,
  onPick,
}: {
  binding: PathParamBinding;
  onPick: (value: string, label: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [items, setItems] = useState<Array<Record<string, unknown>>>([]);
  const [filter, setFilter] = useState("");
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const ctrl = new AbortController();
    setLoading(true);
    setError(null);
    fetchViaProxy(binding.listEndpoint!, ctrl.signal)
      .then((json) => setItems(extractArray(json, binding.listArrayField)))
      .catch((e) => setError(e instanceof Error ? e.message : String(e)))
      .finally(() => setLoading(false));
    return () => ctrl.abort();
  }, [open, binding.listEndpoint, binding.listArrayField]);

  useEffect(() => {
    function onDoc(e: MouseEvent) {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    }
    if (open) document.addEventListener("mousedown", onDoc);
    return () => document.removeEventListener("mousedown", onDoc);
  }, [open]);

  const valueField = binding.listValueField || "id";
  const labelField = binding.listLabelField;
  const subField = binding.listSubLabelField;

  const rows = items.map((it) => {
    const value = it[valueField] != null ? String(it[valueField]) : "";
    const label = labelField && it[labelField] != null ? String(it[labelField]) : value;
    const sub = subField && it[subField] != null ? String(it[subField]) : "";
    return { value, label, sub };
  });
  const filtered = filter
    ? rows.filter((r) => `${r.label} ${r.value} ${r.sub}`.toLowerCase().includes(filter.toLowerCase()))
    : rows;

  return (
    <div className="relative inline-block" ref={ref}>
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        className="rounded-md border border-edge bg-ink px-2 py-1 text-xs text-slate-300 hover:bg-edge"
      >
        {binding.displayLabel || binding.value || "pick…"} ▾
      </button>
      {open && (
        <div className="absolute z-20 mt-1 w-64 rounded-md border border-edge bg-panel p-2 shadow-lg">
          <input
            autoFocus
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            placeholder="Filter…"
            className="input mb-2 text-xs"
          />
          <div className="max-h-56 overflow-auto">
            {loading && <p className="px-1 py-2 text-xs text-slate-500">Loading…</p>}
            {error && <p className="px-1 py-2 text-xs text-rose-300">{error}</p>}
            {!loading && !error && filtered.length === 0 && (
              <p className="px-1 py-2 text-xs text-slate-500">No options.</p>
            )}
            {filtered.map((r) => (
              <button
                key={r.value}
                type="button"
                onClick={() => {
                  onPick(r.value, r.label);
                  setOpen(false);
                }}
                className="block w-full truncate rounded px-2 py-1 text-left text-xs text-slate-200 hover:bg-edge"
              >
                <span className="font-mono">{r.label}</span>
                {r.sub && <span className="ml-1 text-slate-500">{r.sub}</span>}
              </button>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}

export function PathParamChips({ path, pathParams, onChange }: Props) {
  const names = pathPlaceholdersIn(path);
  if (names.length === 0) return null;

  const byName = new Map((pathParams ?? []).map((b) => [b.name, b]));

  function setBinding(name: string, patch: Partial<PathParamBinding>) {
    const existing = byName.get(name) ?? { name, source: "free" as const };
    const next: PathParamBinding = { ...existing, ...patch };
    const others = (pathParams ?? []).filter((b) => b.name !== name);
    onChange([...others, next]);
  }

  return (
    <div className="flex flex-wrap items-center gap-2">
      <span className="text-xs text-slate-500">Path params:</span>
      {names.map((name) => {
        const b = byName.get(name) ?? { name, source: "free" as const };
        const filled = b.value !== undefined && b.value !== "";
        return (
          <div
            key={name}
            className={`flex items-center gap-1.5 rounded-md border px-2 py-1 ${
              filled ? "border-emerald-500/40 bg-emerald-500/5" : "border-edge"
            }`}
          >
            <span className="font-mono text-xs text-slate-400">{`{${name}}`}</span>
            {b.source === "list" && (
              <ListPicker
                binding={b}
                onPick={(value, label) => setBinding(name, { value, displayLabel: label })}
              />
            )}
            {b.source === "enum" && (
              <select
                value={b.value ?? ""}
                onChange={(e) => setBinding(name, { value: e.target.value, displayLabel: e.target.value })}
                className="input py-1 text-xs"
              >
                <option value="">pick…</option>
                {(b.enumValues ?? []).map((v) => (
                  <option key={v} value={v}>
                    {v}
                  </option>
                ))}
              </select>
            )}
            {b.source === "free" && (
              <input
                value={b.value ?? ""}
                onChange={(e) => setBinding(name, { value: e.target.value, displayLabel: e.target.value })}
                placeholder={name}
                className="input w-32 py-1 font-mono text-xs"
              />
            )}
            {filled && (
              <button
                type="button"
                onClick={() => setBinding(name, { value: "", displayLabel: "" })}
                className="text-xs text-slate-500 hover:text-rose-300"
                title="Clear"
              >
                ✕
              </button>
            )}
          </div>
        );
      })}
    </div>
  );
}
