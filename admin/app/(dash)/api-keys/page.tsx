"use client";

import { useState } from "react";
import { useConfirm } from "@/components/confirm";
import { api } from "@/lib/api";
import { copyText } from "@/lib/clipboard";
import { useFetch } from "@/lib/useFetch";
import { Badge, Empty, ErrorBanner, PageHeader, Spinner } from "@/components/ui";

type APIKey = {
  name: string;
  prefix: string;
  is_active: boolean;
  created_at: string;
  last_used_at: string | null;
  expires_at: string | null;
};

export default function APIKeysPage() {
  const { data, error, loading, refresh } = useFetch<{ api_keys: APIKey[] }>("api-keys");
  const [actionError, setActionError] = useState<string | null>(null);
  const [newKey, setNewKey] = useState<string | null>(null);
  const confirm = useConfirm();

  const keys = data?.api_keys ?? [];

  async function create(name: string) {
    setActionError(null);
    setNewKey(null);
    try {
      const res = await api<{ key: string }>("api-keys", { method: "POST", body: JSON.stringify({ name }) });
      setNewKey(res.key);
      await refresh();
    } catch (e: any) {
      setActionError(e.message || "Create failed");
    }
  }

  async function revoke(prefix: string) {
    setActionError(null);
    try {
      await api(`api-keys/${encodeURIComponent(prefix)}`, { method: "DELETE" });
      await refresh();
    } catch (e: any) {
      setActionError(e.message || "Revoke failed");
    }
  }

  async function del(prefix: string) {
    setActionError(null);
    try {
      await api(`api-keys/${encodeURIComponent(prefix)}?hard=true`, { method: "DELETE" });
      await refresh();
    } catch (e: any) {
      setActionError(e.message || "Delete failed");
    }
  }

  return (
    <div>
      <PageHeader title="API Keys" subtitle="Bearer keys that grant external systems access to the gateway HTTP API." />
      <ErrorBanner message={actionError || error} />

      <div className="max-w-3xl space-y-6">
        {newKey && <NewKeyBanner value={newKey} onDismiss={() => setNewKey(null)} />}

        <CreateKey onCreate={create} />

        {loading ? (
          <Spinner />
        ) : keys.length === 0 ? (
          <Empty>No API keys yet.</Empty>
        ) : (
          <div className="card overflow-x-auto p-0">
            <table className="min-w-full divide-y divide-edge">
              <thead>
                <tr>
                  <th className="th">Name</th>
                  <th className="th">Prefix</th>
                  <th className="th">Status</th>
                  <th className="th">Last used</th>
                  <th className="th text-right">Actions</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-edge">
                {keys.map((k) => (
                  <tr key={k.prefix}>
                    <td className="td">{k.name || "—"}</td>
                    <td className="td font-mono text-slate-400">{k.prefix}…</td>
                    <td className="td">
                      <Badge tone={k.is_active ? "green" : "slate"}>{k.is_active ? "Active" : "Revoked"}</Badge>
                    </td>
                    <td className="td text-slate-400">{k.last_used_at ? new Date(k.last_used_at).toLocaleString() : "never"}</td>
                    <td className="td">
                      <div className="flex justify-end">
                        {k.is_active ? (
                          <button
                            className="btn-danger"
                            onClick={async () => {
                              if (
                                await confirm({
                                  title: "Revoke API key?",
                                  body: `“${k.name || k.prefix}” — any system using it will stop working immediately.`,
                                  confirmLabel: "Revoke",
                                })
                              )
                                revoke(k.prefix);
                            }}
                          >
                            Revoke
                          </button>
                        ) : (
                          <button
                            className="btn-ghost"
                            onClick={async () => {
                              if (
                                await confirm({
                                  title: "Delete API key?",
                                  body: `“${k.name || k.prefix}” is already revoked. Deleting removes it from the list permanently. This cannot be undone.`,
                                  confirmLabel: "Delete",
                                })
                              )
                                del(k.prefix);
                            }}
                          >
                            Delete
                          </button>
                        )}
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  );
}

function NewKeyBanner({ value, onDismiss }: { value: string; onDismiss: () => void }) {
  const [copied, setCopied] = useState(false);
  const [copyFailed, setCopyFailed] = useState(false);
  return (
    <div className="card border-emerald-500/40 bg-emerald-500/10">
      <div className="flex items-start justify-between gap-4">
        <div className="min-w-0">
          <h3 className="text-sm font-semibold text-emerald-200">New API key — copy it now</h3>
          <p className="mt-1 text-xs text-emerald-200/80">
            This is the only time the key is shown. It is stored only as a hash and cannot be retrieved again.
          </p>
          <code className="mt-3 block overflow-x-auto rounded-md border border-edge bg-ink px-3 py-2 font-mono text-sm text-slate-100">
            {value}
          </code>
        </div>
        <div className="flex shrink-0 flex-col gap-2">
          <button
            className="btn-primary"
            onClick={async () => {
              const ok = await copyText(value);
              if (ok) {
                setCopied(true);
                setTimeout(() => setCopied(false), 1500);
              } else {
                setCopyFailed(true);
                setTimeout(() => setCopyFailed(false), 2500);
              }
            }}
          >
            {copied ? "Copied" : copyFailed ? "Press Ctrl+C" : "Copy"}
          </button>
          <button className="btn-ghost" onClick={onDismiss}>
            Dismiss
          </button>
        </div>
      </div>
    </div>
  );
}

function CreateKey({ onCreate }: { onCreate: (name: string) => Promise<void> }) {
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);

  return (
    <div className="card space-y-3">
      <div>
        <h2 className="text-sm font-semibold text-slate-300">Generate API key</h2>
        <p className="mt-1 text-xs text-slate-400">
          A key grants <span className="text-slate-200">full access</span> to the gateway HTTP API. Name it after the system that
          will use it so you can revoke it later.
        </p>
      </div>
      <div className="flex items-end gap-3">
        <div className="grow">
          <label className="text-xs text-slate-400">Name / label</label>
          <input className="input mt-1" value={name} onChange={(e) => setName(e.target.value)} placeholder="e.g. fleet-dashboard" />
        </div>
        <button
          className="btn-primary"
          disabled={busy || !name.trim()}
          onClick={async () => {
            setBusy(true);
            await onCreate(name.trim());
            setBusy(false);
            setName("");
          }}
        >
          {busy ? "Generating…" : "Generate key"}
        </button>
      </div>
    </div>
  );
}
