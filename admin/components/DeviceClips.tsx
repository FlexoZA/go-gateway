"use client";

import { Fragment, useState } from "react";
import { api } from "@/lib/api";
import { useConfirm } from "@/components/confirm";
import { useFetch } from "@/lib/useFetch";
import { Badge, Empty, ErrorBanner, Spinner } from "@/components/ui";

// DeviceClips is the "Clips" tab of a device's detail page: the per-device saved-
// footage workflow (search recordings on the device → pull a clip to the server →
// download). Live streaming lives on the separate "Video" tab (DeviceVideo).
type Recording = {
  camera: number;
  profile: number;
  start_utc: number;
  end_utc: number;
  file_name: string;
  size: number;
  device_start: string;
  device_end: string;
  alarm: boolean;
  alarm_flags?: string;
};
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
  error?: string;
};
type Kind = "all" | "normal" | "alarm";

const MAX_CLIP_SECS = 300;
const MIN_CLIP_SECS = 5;

export function DeviceClips({ serial, sleeping }: { serial: string; sleeping: boolean }) {
  const range = defaultRange();
  // Stored clips for THIS device only (server-side filter), polled so status updates.
  const clips = useFetch<{ clips: Clip[] }>(`clips?serial=${encodeURIComponent(serial)}`, 4000);
  const confirm = useConfirm();

  const [camera, setCamera] = useState(0);
  const [profile, setProfile] = useState(0);
  const [kind, setKind] = useState<Kind>("all");
  const [start, setStart] = useState(range.start);
  const [end, setEnd] = useState(range.end);
  const [searching, setSearching] = useState(false);
  const [recordings, setRecordings] = useState<Recording[] | null>(null);
  const [requesting, setRequesting] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  // Trim state: which recording is open + the chosen sub-window (offset + length).
  const [trimKey, setTrimKey] = useState<string | null>(null);
  const [offsetSecs, setOffsetSecs] = useState(0);
  const [lengthSecs, setLengthSecs] = useState(30);

  const clipList = clips.data?.clips ?? [];

  function recKey(rec: Recording) {
    return `${rec.start_utc}-${rec.end_utc}`;
  }
  function openTrim(rec: Recording) {
    const len = rec.end_utc - rec.start_utc;
    setTrimKey(recKey(rec));
    setOffsetSecs(0);
    setLengthSecs(Math.min(30, len));
    setError(null);
    setNotice(null);
  }

  async function findRecordings() {
    setError(null);
    setNotice(null);
    setRecordings(null);
    const startUtc = Math.floor(new Date(start).getTime() / 1000);
    const endUtc = Math.floor(new Date(end).getTime() / 1000);
    if (!startUtc || !endUtc || endUtc <= startUtc) {
      setError("Search end must be after start.");
      return;
    }
    setSearching(true);
    try {
      const res = await api<{ recordings: Recording[]; count: number }>(
        `units/${encodeURIComponent(serial)}/recordings?camera=${camera}&profile=${profile}&kind=${kind}&start_utc=${startUtc}&end_utc=${endUtc}`,
      );
      setRecordings(res.recordings);
      if (res.count === 0) {
        setNotice(
          kind === "all"
            ? "No recordings found for that camera/quality/window. Try the other quality (main vs sub)."
            : `No ${kind} recordings in that window. Try “All”, the other quality, or a different window.`,
        );
      }
    } catch (e: any) {
      setError(e.message || "Search failed");
    } finally {
      setSearching(false);
    }
  }

  async function requestClip(rec: Recording, startUtc?: number, endUtc?: number) {
    setError(null);
    setNotice(null);
    const s = startUtc ?? rec.start_utc;
    const e = endUtc ?? rec.end_utc;
    setRequesting(recKey(rec));
    try {
      await api(`units/${encodeURIComponent(serial)}/clips`, {
        method: "POST",
        body: JSON.stringify({ camera: rec.camera, profile: rec.profile, start_utc: s, end_utc: e }),
      });
      setNotice("Clip requested — the device is uploading it. It will appear in Stored clips below.");
      setTrimKey(null);
      await clips.refresh();
    } catch (err: any) {
      setError(err.message || "Failed to request clip");
    } finally {
      setRequesting(null);
    }
  }

  async function remove(id: number) {
    if (!(await confirm({ title: "Delete clip?", body: "This deletes the clip and its file.", confirmLabel: "Delete" }))) return;
    try {
      await api(`clips/${id}`, { method: "DELETE" });
      await clips.refresh();
    } catch (e: any) {
      setError(e.message || "Failed to delete clip");
    }
  }

  return (
    <div>
      <ErrorBanner message={error || clips.error} />
      {notice && (
        <div className="mb-4 rounded-md border border-indigo-500/40 bg-indigo-500/10 px-3 py-2 text-sm text-indigo-200">
          {notice}
        </div>
      )}
      {/* Step 1: search this device for available recordings */}
      <div className="card mb-6 space-y-3">
        <div className="flex flex-wrap items-end gap-3">
          <div>
            <label className="text-xs text-slate-400">Camera</label>
            <select className="input mt-1 w-32" value={camera} onChange={(e) => setCamera(Number(e.target.value))} disabled={searching}>
              <option value={0}>Camera 1</option>
              <option value={1}>Camera 2</option>
            </select>
          </div>
          <div>
            <label className="text-xs text-slate-400">Quality</label>
            <select className="input mt-1 w-36" value={profile} onChange={(e) => setProfile(Number(e.target.value))} disabled={searching}>
              <option value={0}>High (main)</option>
              <option value={1}>Low (sub)</option>
            </select>
          </div>
          <div>
            <label className="text-xs text-slate-400">Type</label>
            <select className="input mt-1 w-36" value={kind} onChange={(e) => setKind(e.target.value as Kind)} disabled={searching}>
              <option value="all">All</option>
              <option value="normal">Normal</option>
              <option value="alarm">Alarm / event</option>
            </select>
          </div>
          <div>
            <label className="text-xs text-slate-400">From</label>
            <input type="datetime-local" className="input mt-1" value={start} onChange={(e) => setStart(e.target.value)} disabled={searching} />
          </div>
          <div>
            <label className="text-xs text-slate-400">To</label>
            <input type="datetime-local" className="input mt-1" value={end} onChange={(e) => setEnd(e.target.value)} disabled={searching} />
          </div>
          <button
            className="btn-primary"
            onClick={findRecordings}
            disabled={searching || sleeping}
            title={sleeping ? "Device is in standby — wake it to search recordings" : undefined}
          >
            {searching ? "Searching…" : "Find recordings"}
          </button>
        </div>
        <p className="text-xs text-slate-500">
          The device only has footage for cameras/qualities it was set to record. Pick a recording below to pull it to the server as an .mp4.
          <span className="text-slate-600"> Alarm/event clips are footage the device locked around a triggered alarm.</span>
        </p>
      </div>

      {/* Step 2: available recordings from the device */}
      {recordings && recordings.length > 0 && (
        <div className="card mb-8 overflow-x-auto p-0">
          <table className="min-w-full divide-y divide-edge">
            <thead>
              <tr>
                <th className="th">Start (device time)</th>
                <th className="th">End</th>
                <th className="th">Type</th>
                <th className="th">Length</th>
                <th className="th">Size</th>
                <th className="th text-right">Action</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-edge">
              {recordings.map((rec) => {
                const key = recKey(rec);
                const recLen = rec.end_utc - rec.start_utc;
                const open = trimKey === key;
                const maxOffset = Math.max(0, recLen - MIN_CLIP_SECS);
                const off = Math.min(Math.max(0, offsetSecs), maxOffset);
                const maxLen = Math.min(MAX_CLIP_SECS, recLen - off);
                const len = Math.min(Math.max(MIN_CLIP_SECS, lengthSecs), maxLen);
                const winStart = rec.start_utc + off;
                const winEnd = winStart + len;
                return (
                  <Fragment key={key}>
                    <tr className={open ? "bg-edge/40" : undefined}>
                      <td className="td font-mono">{rec.device_start || fmtUtc(rec.start_utc)}</td>
                      <td className="td font-mono text-slate-400">{rec.device_end || fmtUtc(rec.end_utc)}</td>
                      <td className="td">
                        {rec.alarm ? (
                          <span title={rec.alarm_flags ? `alarm flags 0x${rec.alarm_flags}` : undefined}>
                            <Badge tone="amber">alarm</Badge>
                          </span>
                        ) : (
                          <Badge tone="slate">normal</Badge>
                        )}
                      </td>
                      <td className="td text-slate-400">{recLen}s</td>
                      <td className="td text-slate-400">{fmtBytes(rec.size)}</td>
                      <td className="td">
                        <div className="flex justify-end gap-2">
                          <button className="btn-ghost" onClick={() => (open ? setTrimKey(null) : openTrim(rec))}>
                            {open ? "Close" : "Trim"}
                          </button>
                          <button className="btn-primary" disabled={requesting === key} onClick={() => requestClip(rec)}>
                            {requesting === key ? "Requesting…" : "Pull full"}
                          </button>
                        </div>
                      </td>
                    </tr>
                    {open && (
                      <tr className="bg-edge/40">
                        <td className="td" colSpan={6}>
                          <div className="flex flex-wrap items-end gap-4">
                            <div>
                              <label className="text-xs text-slate-400">Start at (seconds in)</label>
                              <input
                                type="number"
                                min={0}
                                max={maxOffset}
                                className="input mt-1 w-28"
                                value={off}
                                onChange={(e) => setOffsetSecs(Number(e.target.value))}
                              />
                            </div>
                            <div>
                              <label className="text-xs text-slate-400">Length (seconds, max {Math.min(MAX_CLIP_SECS, recLen)})</label>
                              <input
                                type="number"
                                min={MIN_CLIP_SECS}
                                max={maxLen}
                                className="input mt-1 w-28"
                                value={len}
                                onChange={(e) => setLengthSecs(Number(e.target.value))}
                              />
                            </div>
                            <div className="flex gap-1">
                              {[10, 30, 60, 120, 300].filter((s) => s <= Math.min(MAX_CLIP_SECS, recLen)).map((s) => (
                                <button key={s} className="btn-ghost px-2 py-1 text-xs" onClick={() => setLengthSecs(s)}>
                                  {s < 60 ? `${s}s` : `${s / 60}m`}
                                </button>
                              ))}
                            </div>
                            <div className="text-xs text-slate-400">
                              → {fmtUtc(winStart)} for {len}s
                            </div>
                            <button className="btn-primary" disabled={requesting === key} onClick={() => requestClip(rec, winStart, winEnd)}>
                              {requesting === key ? "Requesting…" : "Pull section"}
                            </button>
                          </div>
                        </td>
                      </tr>
                    )}
                  </Fragment>
                );
              })}
            </tbody>
          </table>
        </div>
      )}

      {/* Step 3: stored clips for this device */}
      <h4 className="mb-3 text-xs font-semibold uppercase tracking-wide text-slate-500">Stored clips</h4>
      {clips.loading && clipList.length === 0 ? (
        <Spinner />
      ) : clipList.length === 0 ? (
        <Empty>No clips yet for this device. Find a recording above and pull it.</Empty>
      ) : (
        <div className="card overflow-x-auto p-0">
          <table className="min-w-full divide-y divide-edge">
            <thead>
              <tr>
                <th className="th">Camera</th>
                <th className="th">Window (UTC)</th>
                <th className="th">Length</th>
                <th className="th">Status</th>
                <th className="th">Size</th>
                <th className="th text-right">Actions</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-edge">
              {clipList.map((c) => (
                <tr key={c.id}>
                  <td className="td">Cam {c.camera + 1} · {c.profile === 0 ? "high" : "low"}</td>
                  <td className="td text-slate-400">{fmtUtc(c.start_utc)} → {fmtUtc(c.end_utc)}</td>
                  <td className="td text-slate-400">{c.duration_secs}s</td>
                  <td className="td">
                    <Badge tone={clipTone(c.status)}>{c.status}</Badge>
                    {c.status === "receiving" && c.bytes_received > 0 && (
                      <span className="ml-2 text-xs text-slate-400">{fmtBytes(c.bytes_received)}</span>
                    )}
                    {c.status === "error" && c.error && <span className="ml-2 text-xs text-rose-300">{c.error}</span>}
                  </td>
                  <td className="td text-slate-400">{c.file_size > 0 ? fmtBytes(c.file_size) : "—"}</td>
                  <td className="td">
                    <div className="flex justify-end gap-2">
                      {c.status === "ready" && (
                        <a className="btn-ghost" href={`/api/gw/clips/${c.id}/download`}>Download</a>
                      )}
                      <button className="btn-danger" onClick={() => remove(c.id)}>Delete</button>
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

// datetime-local value (YYYY-MM-DDTHH:mm) in local wall-clock.
function toLocalInput(d: Date) {
  const off = d.getTimezoneOffset() * 60_000;
  return new Date(d.getTime() - off).toISOString().slice(0, 16);
}
function defaultRange() {
  const now = new Date();
  const weekAgo = new Date(now.getTime() - 7 * 86400_000);
  return { start: toLocalInput(weekAgo), end: toLocalInput(now) };
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
  if (!n) return "—";
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(0)} KB`;
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}
