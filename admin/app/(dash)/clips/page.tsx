"use client";

import { useState } from "react";
import { api } from "@/lib/api";
import { useFetch } from "@/lib/useFetch";
import { Badge, Empty, ErrorBanner, PageHeader, Spinner } from "@/components/ui";

type Unit = { serial: string; model: string };
type Clip = {
  id: number;
  serial: string;
  camera: number;
  profile: number;
  start_utc: number;
  end_utc: number;
  duration_secs: number;
  status: string;
  file_size: number;
  bytes_received: number;
  created_at: string;
};

// Default the request window to the last 1 minute (local time).
function defaultRange() {
  const now = new Date();
  const start = new Date(now.getTime() - 60_000);
  return { start: toLocalInput(start), end: toLocalInput(now) };
}
function toLocalInput(d: Date) {
  // datetime-local wants YYYY-MM-DDTHH:mm in local time.
  const off = d.getTimezoneOffset() * 60_000;
  return new Date(d.getTime() - off).toISOString().slice(0, 16);
}

export default function ClipsPage() {
  const units = useFetch<{ units: Unit[] }>("units", 8000);
  const clips = useFetch<{ clips: Clip[] }>("clips", 4000);
  const range = defaultRange();

  const [serial, setSerial] = useState("");
  const [camera, setCamera] = useState(0);
  const [profile, setProfile] = useState(1);
  const [start, setStart] = useState(range.start);
  const [end, setEnd] = useState(range.end);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  const unitList = units.data?.units ?? [];
  const clipList = clips.data?.clips ?? [];
  const effectiveSerial = serial || unitList[0]?.serial || "";

  async function request() {
    setError(null);
    setNotice(null);
    if (!effectiveSerial) {
      setError("No connected device to request from.");
      return;
    }
    const startUtc = Math.floor(new Date(start).getTime() / 1000);
    const endUtc = Math.floor(new Date(end).getTime() / 1000);
    if (!startUtc || !endUtc || endUtc <= startUtc) {
      setError("End time must be after start time.");
      return;
    }
    setBusy(true);
    try {
      await api(`units/${encodeURIComponent(effectiveSerial)}/clips`, {
        method: "POST",
        body: JSON.stringify({ camera, profile, start_utc: startUtc, end_utc: endUtc }),
      });
      setNotice("Clip requested — the device is uploading it now. It will appear below.");
      await clips.refresh();
    } catch (e: any) {
      setError(e.message || "Failed to request clip");
    } finally {
      setBusy(false);
    }
  }

  async function remove(id: number) {
    if (!confirm("Delete this clip and its file?")) return;
    try {
      await api(`clips/${id}`, { method: "DELETE" });
      await clips.refresh();
    } catch (e: any) {
      setError(e.message || "Failed to delete clip");
    }
  }

  return (
    <div>
      <PageHeader title="Clips" subtitle="Pull recorded video off a device's SD card and store it on the server" />
      <ErrorBanner message={error || units.error || clips.error} />
      {notice && <div className="mb-4 rounded-md border border-indigo-500/40 bg-indigo-500/10 px-3 py-2 text-sm text-indigo-200">{notice}</div>}

      <div className="card mb-8 space-y-3">
        <div className="flex flex-wrap items-end gap-3">
          <div>
            <label className="text-xs text-slate-400">Device</label>
            <select className="input mt-1 w-56" value={effectiveSerial} onChange={(e) => setSerial(e.target.value)} disabled={busy}>
              {unitList.length === 0 && <option value="">No connected devices</option>}
              {unitList.map((u) => (
                <option key={u.serial} value={u.serial}>
                  {u.serial} ({u.model})
                </option>
              ))}
            </select>
          </div>
          <div>
            <label className="text-xs text-slate-400">Camera</label>
            <select className="input mt-1 w-36" value={camera} onChange={(e) => setCamera(Number(e.target.value))} disabled={busy}>
              <option value={0}>Camera 1 (Road)</option>
              <option value={1}>Camera 2 (Cab)</option>
            </select>
          </div>
          <div>
            <label className="text-xs text-slate-400">Quality</label>
            <select className="input mt-1 w-36" value={profile} onChange={(e) => setProfile(Number(e.target.value))} disabled={busy}>
              <option value={1}>Low (sub)</option>
              <option value={0}>High (main)</option>
            </select>
          </div>
          <div>
            <label className="text-xs text-slate-400">Start</label>
            <input type="datetime-local" className="input mt-1" value={start} onChange={(e) => setStart(e.target.value)} disabled={busy} />
          </div>
          <div>
            <label className="text-xs text-slate-400">End</label>
            <input type="datetime-local" className="input mt-1" value={end} onChange={(e) => setEnd(e.target.value)} disabled={busy} />
          </div>
          <button className="btn-primary" onClick={request} disabled={busy || !effectiveSerial}>
            {busy ? "Requesting…" : "Request clip"}
          </button>
        </div>
        <p className="text-xs text-slate-500">
          The device reads the footage from its SD card and uploads it to the gateway, which stores it as an .mp4. Larger
          windows take longer; the clip appears below with live status.
        </p>
      </div>

      <h2 className="mb-3 text-sm font-semibold text-slate-300">Stored clips</h2>
      {clips.loading && clipList.length === 0 ? (
        <Spinner />
      ) : clipList.length === 0 ? (
        <Empty>No clips yet. Request one above.</Empty>
      ) : (
        <div className="card overflow-x-auto p-0">
          <table className="min-w-full divide-y divide-edge">
            <thead>
              <tr>
                <th className="th">Device</th>
                <th className="th">Camera</th>
                <th className="th">Window</th>
                <th className="th">Duration</th>
                <th className="th">Status</th>
                <th className="th">Size</th>
                <th className="th text-right">Actions</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-edge">
              {clipList.map((c) => (
                <tr key={c.id}>
                  <td className="td font-mono">{c.serial}</td>
                  <td className="td">Cam {c.camera + 1} · {c.profile === 0 ? "high" : "low"}</td>
                  <td className="td text-slate-400">
                    {fmtUtc(c.start_utc)} → {fmtUtc(c.end_utc)}
                  </td>
                  <td className="td text-slate-400">{c.duration_secs}s</td>
                  <td className="td">
                    <Badge tone={clipTone(c.status)}>{c.status}</Badge>
                    {c.status === "receiving" && c.bytes_received > 0 && (
                      <span className="ml-2 text-xs text-slate-400">{fmtBytes(c.bytes_received)}</span>
                    )}
                    {c.status === "error" && <span className="ml-2 text-xs text-rose-300">{(c as any).error}</span>}
                  </td>
                  <td className="td text-slate-400">{c.file_size > 0 ? fmtBytes(c.file_size) : "—"}</td>
                  <td className="td">
                    <div className="flex justify-end gap-2">
                      {c.status === "ready" && (
                        <a className="btn-ghost" href={`/api/gw/clips/${c.id}/download`}>
                          Download
                        </a>
                      )}
                      <button className="btn-danger" onClick={() => remove(c.id)}>
                        Delete
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

function clipTone(status: string): "green" | "amber" | "rose" | "slate" {
  if (status === "ready") return "green";
  if (status === "error") return "rose";
  if (status === "receiving" || status === "requested") return "amber";
  return "slate";
}
function fmtUtc(unix: number): string {
  if (!unix) return "—";
  return new Date(unix * 1000).toLocaleString();
}
function fmtBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(0)} KB`;
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}
