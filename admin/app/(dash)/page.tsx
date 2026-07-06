"use client";

import Link from "next/link";
import { useState } from "react";
import { api } from "@/lib/api";
import { useFetch } from "@/lib/useFetch";
import { useGatewayInfo, capsForUnit } from "@/lib/useGatewayInfo";
import { useTableSearch } from "@/lib/useTableSearch";
import { Badge, ConfirmDialog, Empty, ErrorBanner, PageHeader, Pagination, SearchInput, Spinner, statusTone } from "@/components/ui";

type Unit = {
  serial: string;
  protocol: string;
  model?: string;
  remote_addr: string;
  connected_at: string;
  state?: string;
  commands: string[];
};
type PortStatus = { unit: string; kind: string; port: number; listening: boolean };
type Metrics = {
  cpu_percent?: number;
  mem_percent?: number;
  mem_used_mb?: number;
  mem_total_mb?: number;
  process_rss_mb?: number;
  goroutines?: number;
};

export default function DashboardPage() {
  const info = useGatewayInfo();
  const units = useFetch<{ units: Unit[] }>("units", 5000);
  const devices = useFetch<{ devices: any[] }>("devices", 10000);
  const pending = useFetch<{ devices: any[] }>("devices/pending", 10000);
  const streams = useFetch<{ count: number; streams: any[] }>("streams", 5000);
  const ports = useFetch<{ ports: PortStatus[] }>("ports", 15000);
  const metrics = useFetch<Metrics>("metrics", 5000);

  const connected = units.data?.units ?? [];
  const search = useTableSearch(
    connected,
    (u, q) =>
      u.serial.toLowerCase().includes(q) ||
      (u.model?.toLowerCase().includes(q) ?? false) ||
      u.protocol.toLowerCase().includes(q),
  );
  const portList = ports.data?.ports ?? [];
  const standby = connected.filter((u) => u.state === "sleep").length;
  const streamCount = streams.data?.count ?? 0;
  const m = metrics.data;

  const [stopping, setStopping] = useState(false);
  const [stopErr, setStopErr] = useState<string | null>(null);
  const [confirmStop, setConfirmStop] = useState(false);
  async function doStopAll() {
    setStopping(true);
    setStopErr(null);
    try {
      await api("streams/stop-all", { method: "POST" });
      await streams.refresh();
      setConfirmStop(false);
    } catch (e: any) {
      setStopErr(e.message || "Failed to stop streams");
    } finally {
      setStopping(false);
    }
  }

  return (
    <div>
      <PageHeader
        title="Dashboard"
        subtitle="Live connectivity and registry overview"
        action={
          streamCount > 0 ? (
            <button
              className="btn-danger"
              onClick={() => {
                setStopErr(null);
                setConfirmStop(true);
              }}
            >
              {`Stop all streams (${streamCount})`}
            </button>
          ) : undefined
        }
      />
      <ErrorBanner message={units.error} />

      <div className="mb-6 grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <Stat label="Connected now" value={connected.length} tone="green" badge="live" />
        <Stat label="In standby" value={standby} tone="amber" badge="standby" />
        <Stat label="Active streams" value={streamCount} tone="green" badge="streaming" />
        <Stat label="Approved devices" value={devices.data?.devices?.length ?? "—"} tone="indigo" badge="registry" />
        <Stat label="Pending approval" value={pending.data?.devices?.length ?? "—"} tone="amber" badge="pending" />
      </div>

      <div className="mb-6 grid grid-cols-1 gap-4 sm:grid-cols-2">
        <Gauge
          label="CPU usage"
          badge="gateway host"
          percent={m?.cpu_percent}
          detail={m?.goroutines != null ? `${m.goroutines} goroutines` : undefined}
          error={metrics.error}
        />
        <Gauge
          label="Memory usage"
          badge="gateway host"
          percent={m?.mem_percent}
          detail={
            m?.mem_used_mb != null && m?.mem_total_mb != null
              ? `${fmtMB(m.mem_used_mb)} / ${fmtMB(m.mem_total_mb)}${m.process_rss_mb != null ? ` · ${fmtMB(m.process_rss_mb)} process` : ""}`
              : undefined
          }
          error={metrics.error}
        />
      </div>

      {portList.length > 0 && (
        <div className="card mb-6">
          <div className="mb-3 flex items-baseline justify-between">
            <h2 className="text-sm font-semibold text-slate-300">Device port listeners</h2>
            <span className="text-xs text-slate-500">gateway self-check (container-internal)</span>
          </div>
          <div className="flex flex-wrap gap-2">
            {portList.map((p) => (
              <div
                key={`${p.unit}-${p.kind}-${p.port}`}
                className="flex items-center gap-2 rounded-md border border-edge bg-panel/60 px-3 py-1.5 text-sm"
              >
                <span className="font-mono text-slate-200">{p.unit}</span>
                <span className="text-slate-400">{p.kind}</span>
                <span className="font-mono text-slate-300">:{p.port}</span>
                <Badge tone={p.listening ? "green" : "rose"}>{p.listening ? "listening" : "down"}</Badge>
              </div>
            ))}
          </div>
        </div>
      )}

      <div className="mb-3 flex flex-wrap items-center justify-between gap-3">
        <h2 className="text-sm font-semibold text-slate-300">Connected devices</h2>
        {connected.length > 0 && (
          <SearchInput
            value={search.query}
            onChange={search.setQuery}
            placeholder="Search serial, model, type…"
            className="w-72"
          />
        )}
      </div>
      {units.loading ? (
        <Spinner />
      ) : connected.length === 0 ? (
        <Empty>No devices are currently connected.</Empty>
      ) : search.total === 0 ? (
        <Empty>No connected devices match “{search.query}”.</Empty>
      ) : (
        <>
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
              {search.pageItems.map((u) => (
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
                    <VideoCell
                      unit={u}
                      hasVideo={capsForUnit(info, u.protocol)?.has_video !== false}
                      onWoke={units.refresh}
                    />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        <Pagination
          page={search.page}
          pageCount={search.pageCount}
          total={search.total}
          start={search.start}
          count={search.pageItems.length}
          onPage={search.setPage}
          noun="devices"
        />
        </>
      )}
      <ConfirmDialog
        open={confirmStop}
        title="Stop all live streams?"
        confirmLabel={`Stop ${streamCount} stream${streamCount === 1 ? "" : "s"}`}
        tone="danger"
        busy={stopping}
        onConfirm={doStopAll}
        onCancel={() => {
          setConfirmStop(false);
          setStopErr(null);
        }}
      >
        This stops every active live stream on the gateway. Anyone watching a live view
        will be disconnected. Recorded clips are not affected.
        {stopErr && (
          <div className="mt-3 rounded-md border border-rose-500/40 bg-rose-500/10 px-3 py-2 text-rose-200">
            {stopErr}
          </div>
        )}
      </ConfirmDialog>
    </div>
  );
}

// VideoCell renders the per-device action in the Video column. A connected device
// gets a "Live view" link; a device in standby that advertises the wake_device
// command gets a "Wake device" button instead (waking clears the standby state on
// the next units poll). A standby device that can't be woken shows the button
// disabled so it's clear no action is available.
function VideoCell({ unit, hasVideo, onWoke }: { unit: Unit; hasVideo: boolean; onWoke: () => void }) {
  const [waking, setWaking] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  if (!hasVideo) return <span className="text-slate-500">—</span>;

  if (unit.state !== "sleep") {
    return (
      <Link href={`/live/${encodeURIComponent(unit.serial)}`} className="btn-primary">
        Live view
      </Link>
    );
  }

  const canWake = !!unit.commands?.includes("wake_device");
  async function wake() {
    setWaking(true);
    setErr(null);
    try {
      await api(`units/${encodeURIComponent(unit.serial)}/commands`, {
        method: "POST",
        body: JSON.stringify({ type: "wake_device" }),
      });
      onWoke();
    } catch (e: any) {
      setErr(e.message || "Failed to wake device");
    } finally {
      setWaking(false);
    }
  }

  return (
    <div className="flex flex-col items-end gap-1">
      <button
        className="btn-primary"
        onClick={wake}
        disabled={!canWake || waking}
        title={canWake ? undefined : "This device can't be woken remotely"}
      >
        {waking ? "Waking…" : "Wake device"}
      </button>
      {err && <span className="text-xs text-rose-300">{err}</span>}
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

function Gauge({
  label,
  badge,
  percent,
  detail,
  error,
}: {
  label: string;
  badge: string;
  percent?: number;
  detail?: string;
  error?: string | null;
}) {
  const has = typeof percent === "number";
  const pct = has ? Math.max(0, Math.min(100, percent!)) : 0;
  const tone = pct >= 90 ? "rose" : pct >= 75 ? "amber" : "green";
  const barColor =
    tone === "rose" ? "bg-rose-500" : tone === "amber" ? "bg-amber-500" : "bg-green-500";
  return (
    <div className="card">
      <div className="flex items-baseline justify-between">
        <span className="text-xs uppercase tracking-wide text-slate-400">{label}</span>
        <Badge tone="slate">{badge}</Badge>
      </div>
      <div className="mt-2 flex items-baseline gap-2">
        <span className="text-2xl font-semibold text-white">{has ? `${pct.toFixed(1)}%` : "—"}</span>
        {detail && <span className="text-xs text-slate-400">{detail}</span>}
      </div>
      <div className="mt-3 h-2 w-full overflow-hidden rounded-full bg-panel/80 ring-1 ring-inset ring-edge">
        <div
          className={`h-full rounded-full ${barColor} transition-all duration-500`}
          style={{ width: `${pct}%` }}
        />
      </div>
      {error && !has && <div className="mt-2 text-xs text-slate-500">Metrics unavailable</div>}
    </div>
  );
}

function fmtMB(mb: number): string {
  if (mb >= 1024) return `${(mb / 1024).toFixed(1)} GB`;
  return `${Math.round(mb)} MB`;
}

function fmt(ts?: string): string {
  if (!ts) return "—";
  const d = new Date(ts);
  return isNaN(d.getTime()) ? ts : d.toLocaleString();
}
