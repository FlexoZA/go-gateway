"use client";

import { useState } from "react";

// Collapsible JSON tree viewer. Objects/arrays are expandable; primitives are
// colored by type. Kept dependency-free.

function Primitive({ value }: { value: unknown }) {
  if (value === null) return <span className="text-slate-500">null</span>;
  switch (typeof value) {
    case "string":
      return <span className="text-emerald-300">&quot;{value}&quot;</span>;
    case "number":
      return <span className="text-sky-300">{String(value)}</span>;
    case "boolean":
      return <span className="text-violet-300">{String(value)}</span>;
    default:
      return <span className="text-slate-300">{String(value)}</span>;
  }
}

function Node({ keyName, value, depth }: { keyName?: string; value: unknown; depth: number }) {
  const isObject = value !== null && typeof value === "object";
  const [open, setOpen] = useState(depth < 2);

  if (!isObject) {
    return (
      <div className="leading-6">
        {keyName !== undefined && <span className="text-slate-400">{keyName}: </span>}
        <Primitive value={value} />
      </div>
    );
  }

  const entries = Array.isArray(value)
    ? value.map((v, i) => [String(i), v] as const)
    : Object.entries(value as Record<string, unknown>);
  const open0 = Array.isArray(value) ? "[" : "{";
  const close0 = Array.isArray(value) ? "]" : "}";

  return (
    <div className="leading-6">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        className="text-left text-slate-400 hover:text-slate-200"
      >
        <span className="inline-block w-3 select-none text-slate-500">{open ? "▾" : "▸"}</span>
        {keyName !== undefined && <span className="text-slate-400">{keyName}: </span>}
        <span className="text-slate-500">
          {open0}
          {!open && <span className="text-slate-600"> {entries.length} {Array.isArray(value) ? "items" : "keys"} </span>}
          {!open && close0}
        </span>
      </button>
      {open && (
        <div className="border-l border-edge pl-4">
          {entries.map(([k, v]) => (
            <Node key={k} keyName={k} value={v} depth={depth + 1} />
          ))}
          <div className="text-slate-500">{close0}</div>
        </div>
      )}
    </div>
  );
}

export function JsonTree({ data }: { data: unknown }) {
  return (
    <div className="font-mono text-xs">
      <Node value={data} depth={0} />
    </div>
  );
}
