"use client";

import { useEffect, type ReactNode } from "react";

export function PageHeader({ title, subtitle, action }: { title: ReactNode; subtitle?: ReactNode; action?: ReactNode }) {
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

export function ErrorBanner({ message, onDismiss }: { message?: string | null; onDismiss?: () => void }) {
  if (!message) return null;
  return (
    <div className="mb-4 flex items-start gap-3 rounded-md border border-rose-500/40 bg-rose-500/10 px-3 py-2 text-sm text-rose-200">
      <span className="grow">{message}</span>
      {onDismiss && (
        <button onClick={onDismiss} aria-label="Dismiss" className="shrink-0 text-rose-300/80 hover:text-rose-100">
          ✕
        </button>
      )}
    </div>
  );
}

export function Empty({ children }: { children: ReactNode }) {
  return <div className="card text-center text-sm text-slate-400">{children}</div>;
}

export function Spinner() {
  return <div className="text-sm text-slate-400">Loading…</div>;
}

// ConfirmDialog is an in-app modal confirmation, replacing the browser's native
// window.confirm. Controlled via `open`; the caller runs the action in onConfirm
// and closes by flipping `open`. Clicking the backdrop or pressing Escape cancels
// (disabled while `busy`).
export function ConfirmDialog({
  open,
  title,
  children,
  confirmLabel = "Confirm",
  cancelLabel = "Cancel",
  tone = "danger",
  busy = false,
  onConfirm,
  onCancel,
}: {
  open: boolean;
  title: string;
  children?: ReactNode;
  confirmLabel?: string;
  cancelLabel?: string;
  tone?: "danger" | "primary";
  busy?: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}) {
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape" && !busy) onCancel();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, busy, onCancel]);

  if (!open) return null;
  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4"
      role="dialog"
      aria-modal="true"
      onClick={() => !busy && onCancel()}
    >
      <div
        className="w-full max-w-md rounded-lg border border-edge bg-panel p-5 shadow-xl"
        onClick={(e) => e.stopPropagation()}
      >
        <h2 className="text-base font-semibold text-white">{title}</h2>
        {children && <div className="mt-2 text-sm text-slate-300">{children}</div>}
        <div className="mt-5 flex justify-end gap-2">
          <button className="btn-ghost" onClick={onCancel} disabled={busy}>
            {cancelLabel}
          </button>
          <button
            className={tone === "danger" ? "btn-danger" : "btn-primary"}
            onClick={onConfirm}
            disabled={busy}
          >
            {busy ? "Working…" : confirmLabel}
          </button>
        </div>
      </div>
    </div>
  );
}
