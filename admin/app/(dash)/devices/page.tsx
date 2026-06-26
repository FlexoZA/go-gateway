"use client";

import Link from "next/link";
import { useState } from "react";
import { useConfirm } from "@/components/confirm";
import { api } from "@/lib/api";
import { useFetch } from "@/lib/useFetch";
import { Badge, Empty, ErrorBanner, PageHeader, Spinner, statusTone } from "@/components/ui";

type Device = { serial: string; protocol: string; status: string; first_seen_at: string; last_seen_at: string };
type Pending = { serial: string; protocol_guess: string; remote_ip: string; last_seen_at: string };

export default function DevicesPage() {
  const approved = useFetch<{ devices: Device[] }>("devices", 8000);
  const pending = useFetch<{ devices: Pending[] }>("devices/pending", 8000);
  const [busy, setBusy] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const confirm = useConfirm();

  async function act(method: string, path: string, key: string) {
    setBusy(key);
    setActionError(null);
    try {
      await api(path, { method });
      await Promise.all([approved.refresh(), pending.refresh()]);
    } catch (e: any) {
      setActionError(e.message || "Action failed");
    } finally {
      setBusy(null);
    }
  }

  const pendingList = pending.data?.devices ?? [];
  const approvedList = approved.data?.devices ?? [];

  return (
    <div>
      <PageHeader title="Devices" subtitle="Approve connecting units and manage the registry" />
      <ErrorBanner message={actionError || approved.error || pending.error} />

      <h2 className="mb-3 text-sm font-semibold text-slate-300">
        Pending approval {pendingList.length > 0 && <Badge tone="amber">{pendingList.length}</Badge>}
      </h2>
      {pending.loading ? (
        <Spinner />
      ) : pendingList.length === 0 ? (
        <Empty>No devices awaiting approval. (Quarantine is only used when DEVICE_REJECT_UNKNOWN is on.)</Empty>
      ) : (
        <div className="card mb-8 overflow-x-auto p-0">
          <table className="min-w-full divide-y divide-edge">
            <thead>
              <tr>
                <th className="th">Serial</th>
                <th className="th">Protocol guess</th>
                <th className="th">Remote IP</th>
                <th className="th">Last seen</th>
                <th className="th text-right">Actions</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-edge">
              {pendingList.map((d) => (
                <tr key={d.serial}>
                  <td className="td font-mono">
                    <Link href={`/devices/${encodeURIComponent(d.serial)}`} className="text-indigo-300 hover:underline">
                      {d.serial}
                    </Link>
                  </td>
                  <td className="td">{d.protocol_guess || "—"}</td>
                  <td className="td font-mono text-slate-400">{d.remote_ip || "—"}</td>
                  <td className="td text-slate-400">{fmt(d.last_seen_at)}</td>
                  <td className="td">
                    <div className="flex justify-end gap-2">
                      <button
                        className="btn-primary"
                        disabled={busy === d.serial}
                        onClick={() => act("POST", `devices/${encodeURIComponent(d.serial)}/approve`, d.serial)}
                      >
                        Approve
                      </button>
                      <button
                        className="btn-ghost"
                        disabled={busy === d.serial}
                        onClick={() => act("POST", `devices/${encodeURIComponent(d.serial)}/reject`, d.serial)}
                      >
                        Reject
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <h2 className="mb-3 text-sm font-semibold text-slate-300">Approved devices</h2>
      {approved.loading ? (
        <Spinner />
      ) : approvedList.length === 0 ? (
        <Empty>No devices in the registry yet.</Empty>
      ) : (
        <div className="card overflow-x-auto p-0">
          <table className="min-w-full divide-y divide-edge">
            <thead>
              <tr>
                <th className="th">Serial</th>
                <th className="th">Protocol</th>
                <th className="th">Status</th>
                <th className="th">First seen</th>
                <th className="th">Last seen</th>
                <th className="th text-right">Actions</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-edge">
              {approvedList.map((d) => (
                <tr key={d.serial}>
                  <td className="td font-mono">
                    <Link href={`/devices/${encodeURIComponent(d.serial)}`} className="text-indigo-300 hover:underline">
                      {d.serial}
                    </Link>
                  </td>
                  <td className="td">{d.protocol || "—"}</td>
                  <td className="td">
                    <Badge tone={statusTone(d.status)}>{d.status || "unknown"}</Badge>
                  </td>
                  <td className="td text-slate-400">{fmt(d.first_seen_at)}</td>
                  <td className="td text-slate-400">{fmt(d.last_seen_at)}</td>
                  <td className="td">
                    <div className="flex justify-end">
                      <button
                        className="btn-danger"
                        disabled={busy === d.serial}
                        onClick={async () => {
                          if (
                            await confirm({
                              title: "Remove device?",
                              body: `${d.serial} will be removed from the registry.`,
                              confirmLabel: "Remove",
                            })
                          ) {
                            act("DELETE", `devices/${encodeURIComponent(d.serial)}`, d.serial);
                          }
                        }}
                      >
                        Remove
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

function fmt(ts?: string): string {
  if (!ts) return "—";
  const d = new Date(ts);
  return isNaN(d.getTime()) ? ts : d.toLocaleString();
}
