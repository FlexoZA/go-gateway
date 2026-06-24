"use client";

import Link from "next/link";
import { useFetch } from "@/lib/useFetch";
import { Badge, Empty, ErrorBanner, PageHeader, Spinner, statusTone } from "@/components/ui";

type Unit = {
  serial: string;
  protocol: string;
  model?: string;
  remote_addr: string;
  connected_at: string;
  state?: string;
  commands: string[];
};

export default function DashboardPage() {
  const units = useFetch<{ units: Unit[] }>("units", 5000);
  const devices = useFetch<{ devices: any[] }>("devices", 10000);
  const pending = useFetch<{ devices: any[] }>("devices/pending", 10000);

  const connected = units.data?.units ?? [];
  const standby = connected.filter((u) => u.state === "sleep").length;

  return (
    <div>
      <PageHeader title="Dashboard" subtitle="Live connectivity and registry overview" />
      <ErrorBanner message={units.error} />

      <div className="mb-6 grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <Stat label="Connected now" value={connected.length} tone="green" badge="live" />
        <Stat label="In standby" value={standby} tone="amber" badge="standby" />
        <Stat label="Approved devices" value={devices.data?.devices?.length ?? "—"} tone="indigo" badge="registry" />
        <Stat label="Pending approval" value={pending.data?.devices?.length ?? "—"} tone="amber" badge="pending" />
      </div>

      <h2 className="mb-3 text-sm font-semibold text-slate-300">Connected devices</h2>
      {units.loading ? (
        <Spinner />
      ) : connected.length === 0 ? (
        <Empty>No devices are currently connected.</Empty>
      ) : (
        <div className="card overflow-x-auto p-0">
          <table className="min-w-full divide-y divide-edge">
            <thead>
              <tr>
                <th className="th">Serial</th>
                <th className="th">Type</th>
                <th className="th">Model</th>
                <th className="th">State</th>
                <th className="th">Remote</th>
                <th className="th">Connected</th>
                <th className="th">Commands</th>
                <th className="th text-right">Video</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-edge">
              {connected.map((u) => (
                <tr key={u.serial}>
                  <td className="td font-mono">
                    <Link href={`/devices/${encodeURIComponent(u.serial)}`} className="text-indigo-300 hover:underline">
                      {u.serial}
                    </Link>
                  </td>
                  <td className="td"><Badge tone="indigo">{u.protocol}</Badge></td>
                  <td className="td">{u.model || "—"}</td>
                  <td className="td">
                    {u.state === "sleep" ? <Badge tone="amber">standby</Badge> : <Badge tone="green">online</Badge>}
                  </td>
                  <td className="td font-mono text-slate-400">{u.remote_addr}</td>
                  <td className="td text-slate-400">{fmt(u.connected_at)}</td>
                  <td className="td">
                    <Badge tone="slate">{u.commands?.length ?? 0} cmds</Badge>
                  </td>
                  <td className="td text-right">
                    <Link href={`/live/${encodeURIComponent(u.serial)}`} className="btn-primary">
                      Live view
                    </Link>
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

function Stat({ label, value, tone, badge }: { label: string; value: number | string; tone: "green" | "amber" | "indigo"; badge: string }) {
  return (
    <div className="card">
      <div className="text-xs uppercase tracking-wide text-slate-400">{label}</div>
      <div className="mt-2 flex items-baseline gap-2">
        <span className="text-2xl font-semibold text-white">{value}</span>
        <Badge tone={tone}>{badge}</Badge>
      </div>
    </div>
  );
}

function fmt(ts?: string): string {
  if (!ts) return "—";
  const d = new Date(ts);
  return isNaN(d.getTime()) ? ts : d.toLocaleString();
}
