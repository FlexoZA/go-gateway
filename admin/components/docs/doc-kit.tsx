"use client";

import { useState, type ReactNode } from "react";

// Small, dependency-free building blocks for documentation pages. Used inside a
// <div className="doc-prose"> (see globals.css) which styles the surrounding
// headings/paragraphs/tables.

type Method = "GET" | "POST" | "PUT" | "PATCH" | "DELETE";

const METHOD_TONES: Record<Method, string> = {
  GET: "text-emerald-300",
  POST: "text-amber-300",
  PUT: "text-sky-300",
  PATCH: "text-violet-300",
  DELETE: "text-rose-300",
};

/** An HTTP endpoint header: method + path, with an optional one-line description. */
export function Endpoint({
  method,
  path,
  children,
}: {
  method: Method;
  path: string;
  children?: ReactNode;
}) {
  return (
    <div className="mt-5 rounded-md border border-edge bg-panel/60 px-3 py-2">
      <div className="flex items-baseline gap-2">
        <span className={`font-mono text-xs font-bold ${METHOD_TONES[method]}`}>{method}</span>
        <span className="break-all font-mono text-sm text-slate-100">{path}</span>
      </div>
      {children && <p className="mt-1 text-xs text-slate-400">{children}</p>}
    </div>
  );
}

/** A copyable code block. `label` shows above the code (e.g. "Request", "curl"). */
export function CodeBlock({ children, label }: { children: string; label?: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <div className="my-3">
      <div className="flex items-center justify-between rounded-t-md border border-b-0 border-edge bg-ink/80 px-3 py-1.5">
        <span className="text-xs font-medium text-slate-400">{label ?? "Example"}</span>
        <button
          type="button"
          onClick={() => {
            navigator.clipboard?.writeText(children);
            setCopied(true);
            setTimeout(() => setCopied(false), 1200);
          }}
          className="text-xs text-slate-400 hover:text-slate-200"
        >
          {copied ? "Copied" : "Copy"}
        </button>
      </div>
      <pre className="overflow-auto rounded-b-md border border-edge bg-ink p-3 text-xs leading-relaxed text-slate-200">
        <code>{children}</code>
      </pre>
    </div>
  );
}

/** A callout box for tips/warnings. */
export function Callout({
  tone = "info",
  title,
  children,
}: {
  tone?: "info" | "warn";
  title?: string;
  children: ReactNode;
}) {
  const tones = {
    info: "border-indigo-500/40 bg-indigo-500/10 text-indigo-100",
    warn: "border-amber-500/40 bg-amber-500/10 text-amber-100",
  };
  return (
    <div className={`my-4 rounded-md border px-3 py-2 text-sm ${tones[tone]}`}>
      {title && <div className="mb-1 font-semibold">{title}</div>}
      <div className="text-slate-300">{children}</div>
    </div>
  );
}
