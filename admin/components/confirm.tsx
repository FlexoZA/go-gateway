"use client";

import { createContext, useCallback, useContext, useState, type ReactNode } from "react";
import { ConfirmDialog } from "@/components/ui";

export type ConfirmOptions = {
  title: string;
  body?: ReactNode;
  confirmLabel?: string;
  cancelLabel?: string;
  tone?: "danger" | "primary";
};

type ConfirmFn = (opts: ConfirmOptions) => Promise<boolean>;
type Pending = { opts: ConfirmOptions; resolve: (v: boolean) => void };

const ConfirmContext = createContext<ConfirmFn | null>(null);

// ConfirmProvider renders one shared ConfirmDialog and exposes an imperative
// confirm() — a drop-in for window.confirm that returns a Promise<boolean> —
// to every descendant via useConfirm(). Mounted once in the dashboard layout.
export function ConfirmProvider({ children }: { children: ReactNode }) {
  const [pending, setPending] = useState<Pending | null>(null);

  const confirm = useCallback<ConfirmFn>(
    (opts) => new Promise<boolean>((resolve) => setPending({ opts, resolve })),
    [],
  );

  const settle = (v: boolean) => {
    pending?.resolve(v);
    setPending(null);
  };

  return (
    <ConfirmContext.Provider value={confirm}>
      {children}
      <ConfirmDialog
        open={!!pending}
        title={pending?.opts.title ?? ""}
        confirmLabel={pending?.opts.confirmLabel}
        cancelLabel={pending?.opts.cancelLabel}
        tone={pending?.opts.tone}
        onConfirm={() => settle(true)}
        onCancel={() => settle(false)}
      >
        {pending?.opts.body}
      </ConfirmDialog>
    </ConfirmContext.Provider>
  );
}

// useConfirm returns the imperative confirm(): `if (await confirm({...})) { … }`.
export function useConfirm(): ConfirmFn {
  const ctx = useContext(ConfirmContext);
  if (!ctx) throw new Error("useConfirm must be used within ConfirmProvider");
  return ctx;
}
