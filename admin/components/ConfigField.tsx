"use client";

import { useState } from "react";
import { FieldMeta, humanize, inferType } from "@/lib/howenConfig";

// ConfigField recursively renders one config entry: a nested object becomes a
// collapsible group; a leaf string becomes a control (toggle / number / select /
// text) chosen from curated metadata or inferred from the value. Curated field
// metadata (segFields) is keyed by leaf name and applied at any depth, so a label
// like mainip→"Address" applies inside every server0..3 / chn0..15.

type Get = (path: string[]) => string | undefined;
type Set = (path: string[], val: string) => void;

export function ConfigField({
  name, value, path, segFields, readonly, get, set,
}: {
  name: string;
  value: any;
  path: string[];
  segFields?: Record<string, FieldMeta>;
  readonly?: boolean;
  get: Get;
  set: Set;
}) {
  if (value && typeof value === "object") {
    const entries = Object.entries(value);
    return (
      <Group label={humanize(name)} count={entries.length} defaultOpen={path.length <= 1 && entries.length <= 8}>
        {entries.map(([k, v]) => (
          <ConfigField key={k} name={k} value={v} path={[...path, k]} segFields={segFields} readonly={readonly} get={get} set={set} />
        ))}
      </Group>
    );
  }

  const meta = segFields?.[name];
  if (meta?.hidden) return null;

  const raw = value == null ? "" : String(value);
  const cur = get(path) ?? raw;
  const label = meta?.label ?? humanize(name);
  const ro = readonly || meta?.readonly;
  const type = meta?.type ?? inferType(raw);

  if (ro) return <Row label={label}><span className="font-mono text-slate-300">{cur || "—"}</span></Row>;

  if (type === "toggle") {
    const on = cur === "1";
    return (
      <Row label={label}>
        <button type="button" onClick={() => set(path, on ? "0" : "1")} className={`relative h-5 w-9 rounded-full transition ${on ? "bg-emerald-500" : "bg-edge"}`}>
          <span className={`absolute top-0.5 h-4 w-4 rounded-full bg-white transition ${on ? "left-4" : "left-0.5"}`} />
        </button>
      </Row>
    );
  }
  if (type === "select" && meta?.options) {
    return (
      <Row label={label}>
        <select className="input w-44" value={cur} onChange={(e) => set(path, e.target.value)}>
          {meta.options.map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}
          {!meta.options.some((o) => o.value === cur) && <option value={cur}>{cur || "—"}</option>}
        </select>
      </Row>
    );
  }
  return (
    <Row label={label}>
      <input
        className="input w-44 font-mono"
        inputMode={type === "number" ? "numeric" : undefined}
        value={cur}
        onChange={(e) => set(path, e.target.value)}
      />
    </Row>
  );
}

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label className="flex items-center justify-between gap-3 py-0.5 text-sm">
      <span className="shrink-0 text-slate-400">{label}</span>
      {children}
    </label>
  );
}

function Group({ label, count, defaultOpen, children }: { label: string; count: number; defaultOpen?: boolean; children: React.ReactNode }) {
  const [open, setOpen] = useState(!!defaultOpen);
  return (
    <div className="rounded-md border border-edge">
      <button type="button" onClick={() => setOpen((o) => !o)} className="flex w-full items-center justify-between px-2 py-1.5 text-left text-xs font-semibold text-slate-300 hover:bg-edge/40">
        <span>{label}</span>
        <span className="text-slate-500">{open ? "▾" : "▸"} {count}</span>
      </button>
      {open && <div className="space-y-0.5 border-t border-edge px-2 py-1.5">{children}</div>}
    </div>
  );
}
