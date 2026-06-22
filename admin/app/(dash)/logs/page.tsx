"use client";

import { useState } from "react";
import { useFetch } from "@/lib/useFetch";
import { Empty, ErrorBanner, PageHeader, Spinner } from "@/components/ui";

type GatewayError = {
  id: number;
  unit: string;
  namespace: string;
  event: string;
  message: string;
  fields: any;
  created_at: string;
};
type DeviceError = {
  id: number;
  serial: string;
  error_category: string;
  error_message: string;
  remote_address: string;
  created_at: string;
};

export default function LogsPage() {
  const [tab, setTab] = useState<"gateway" | "device">("gateway");

  return (
    <div>
      <PageHeader
        title="Logs"
        subtitle="System errors and device-reported problems"
        action={
          <button className="btn-ghost" onClick={() => location.reload()}>
            Refresh
          </button>
        }
      />

      <div className="mb-4 flex gap-2">
        <TabButton active={tab === "gateway"} onClick={() => setTab("gateway")}>
          Gateway errors
        </TabButton>
        <TabButton active={tab === "device"} onClick={() => setTab("device")}>
          Device errors
        </TabButton>
      </div>

      {tab === "gateway" ? <GatewayLogs /> : <DeviceLogs />}
    </div>
  );
}

function GatewayLogs() {
  const { data, error, loading } = useFetch<{ logs: GatewayError[] }>("logs?limit=200", 10000);
  const logs = data?.logs ?? [];
  return (
    <>
      <ErrorBanner message={error} />
      {loading ? (
        <Spinner />
      ) : logs.length === 0 ? (
        <Empty>No gateway errors recorded.</Empty>
      ) : (
        <div className="card overflow-x-auto p-0">
          <table className="min-w-full divide-y divide-edge">
            <thead>
              <tr>
                <th className="th">Time</th>
                <th className="th">Event</th>
                <th className="th">Namespace</th>
                <th className="th">Details</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-edge">
              {logs.map((l) => (
                <tr key={l.id}>
                  <td className="td whitespace-nowrap text-slate-400">{fmt(l.created_at)}</td>
                  <td className="td font-mono text-rose-300">{l.event || "—"}</td>
                  <td className="td text-slate-400">{l.namespace || "—"}</td>
                  <td className="td">
                    <div>{l.message || messageFrom(l.fields)}</div>
                    {l.fields && <Fields fields={l.fields} />}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </>
  );
}

function DeviceLogs() {
  const { data, error, loading } = useFetch<{ errors: DeviceError[] }>("device-errors?limit=200", 10000);
  const errs = data?.errors ?? [];
  return (
    <>
      <ErrorBanner message={error} />
      {loading ? (
        <Spinner />
      ) : errs.length === 0 ? (
        <Empty>No device-reported errors.</Empty>
      ) : (
        <div className="card overflow-x-auto p-0">
          <table className="min-w-full divide-y divide-edge">
            <thead>
              <tr>
                <th className="th">Time</th>
                <th className="th">Serial</th>
                <th className="th">Category</th>
                <th className="th">Message</th>
                <th className="th">Remote</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-edge">
              {errs.map((e) => (
                <tr key={e.id}>
                  <td className="td whitespace-nowrap text-slate-400">{fmt(e.created_at)}</td>
                  <td className="td font-mono">{e.serial}</td>
                  <td className="td text-amber-300">{e.error_category || "—"}</td>
                  <td className="td">{e.error_message}</td>
                  <td className="td font-mono text-slate-400">{e.remote_address || "—"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </>
  );
}

function TabButton({ active, onClick, children }: { active: boolean; onClick: () => void; children: React.ReactNode }) {
  return (
    <button
      onClick={onClick}
      className={`rounded-md px-3 py-1.5 text-sm font-medium ${active ? "bg-indigo-600 text-white" : "border border-edge text-slate-300 hover:bg-edge"}`}
    >
      {children}
    </button>
  );
}

function Fields({ fields }: { fields: any }) {
  const entries = Object.entries(fields || {}).filter(([k]) => k !== "event" && k !== "message");
  if (entries.length === 0) return null;
  return (
    <div className="mt-1 flex flex-wrap gap-2">
      {entries.map(([k, v]) => (
        <span key={k} className="rounded bg-ink px-1.5 py-0.5 font-mono text-xs text-slate-400">
          {k}={typeof v === "object" ? JSON.stringify(v) : String(v)}
        </span>
      ))}
    </div>
  );
}

function messageFrom(fields: any): string {
  if (fields && typeof fields.message === "string") return fields.message;
  return "—";
}

function fmt(ts?: string): string {
  if (!ts) return "—";
  const d = new Date(ts);
  return isNaN(d.getTime()) ? ts : d.toLocaleString();
}
