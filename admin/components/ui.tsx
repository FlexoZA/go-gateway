"use client";

import { type ReactNode } from "react";

export function PageHeader({ title, subtitle, action }: { title: string; subtitle?: string; action?: ReactNode }) {
  return (
    <div className="mb-6 flex items-end justify-between gap-4">
      <div>
        <h1 className="text-xl font-semibold text-white">{title}</h1>
        {subtitle && <p className="mt-1 text-sm text-slate-400">{subtitle}</p>}
      </div>
      {action}
    </div>
  );
}

export function Badge({ children, tone = "slate" }: { children: ReactNode; tone?: "green" | "amber" | "rose" | "slate" | "indigo" }) {
  const tones: Record<string, string> = {
    green: "bg-emerald-500/15 text-emerald-300 border-emerald-500/30",
    amber: "bg-amber-500/15 text-amber-300 border-amber-500/30",
    rose: "bg-rose-500/15 text-rose-300 border-rose-500/30",
    indigo: "bg-indigo-500/15 text-indigo-300 border-indigo-500/30",
    slate: "bg-slate-500/15 text-slate-300 border-slate-500/30",
  };
  return (
    <span className={`inline-flex items-center rounded-full border px-2 py-0.5 text-xs font-medium ${tones[tone]}`}>
      {children}
    </span>
  );
}

export function statusTone(status: string): "green" | "amber" | "rose" | "slate" {
  switch (status) {
    case "online":
      return "green";
    case "sleep":
      return "amber";
    case "offline":
      return "slate";
    default:
      return "slate";
  }
}

export function ErrorBanner({ message }: { message?: string | null }) {
  if (!message) return null;
  return <div className="mb-4 rounded-md border border-rose-500/40 bg-rose-500/10 px-3 py-2 text-sm text-rose-200">{message}</div>;
}

export function Empty({ children }: { children: ReactNode }) {
  return <div className="card text-center text-sm text-slate-400">{children}</div>;
}

export function Spinner() {
  return <div className="text-sm text-slate-400">Loading…</div>;
}
