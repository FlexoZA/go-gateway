"use client";

import { Fragment, useState } from "react";
import { api } from "@/lib/api";
import { useConfirm } from "@/components/confirm";
import { useFetch } from "@/lib/useFetch";
import { useGatewayInfo, capsForUnit } from "@/lib/useGatewayInfo";
import { Badge, Empty, ErrorBanner, PageHeader, Spinner } from "@/components/ui";

type Unit = { serial: string; protocol: string; model: string; state?: string };
type Recording = {
  camera: number;
  profile: number;
  start_utc: number;
  end_utc: number;
  file_name: string;
  size: number;
  device_start: string;
  device_end: string;
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

export default function ClipsPage() {
  const info = useGatewayInfo();
  const caps = info?.capabilities;
  const units = useFetch<{ units: Unit[] }>("units", 8000);
  const clips = useFetch<{ clips: Clip[] }>("clips", 4000);
  const range = defaultRange();
  const confirm = useConfirm();

  const [serial, setSerial] = useState("");
  const [camera, setCamera] = useState(0);
  const [profile, setProfile] = useState(0); // most devices record the main stream
  const [start, setStart] = useState(range.start);
  const [end, setEnd] = useState(range.end);
  const [searching, setSearching] = useState(false);
  const [recordings, setRecordings] = useState<Recording[] | null>(null);
  const [requesting, setRequesting] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  // Trim state: which recording is open for trimming, and the chosen sub-window
  // (offset seconds into the recording + length seconds). Howen serves arbitrary
  // sub-windows, so you can pull just a section instead of the whole file.
  const [trimKey, setTrimKey] = useState<string | null>(null);
  const [offsetSecs, setOffsetSecs] = useState(0);
  const [lengthSecs, setLengthSecs] = useState(30);
  const MAX_CLIP_SECS = 300; // device cap (matches the old admin)
  const MIN_CLIP_SECS = 5;

  // Switching device must discard the previous device's recordings/trim state —
  // otherwise a "Pull" would post the old device's time window to the new device.
  function changeSerial(next: string) {
    setSerial(next);
    setRecordings(null);
    setTrimKey(null);
    setError(null);
    setNotice(null);
  }

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

  // Only devices whose unit type supports clips/video can be searched here — a
  // GPS-only unit (e.g. GT06) has no footage, so keep it out of the picker.
  const unitList = (units.data?.units ?? []).filter((u) => capsForUnit(info, u.protocol)?.has_clips !== false);
  const clipList = clips.data?.clips ?? [];
  const effectiveSerial = serial || unitList[0]?.serial || "";
  const selectedUnit = unitList.find((u) => u.serial === effectiveSerial);
  const sleeping = selectedUnit?.state === "sleep";
  const [waking, setWaking] = useState(false);

  async function wakeDevice() {
    if (!effectiveSerial) return;
    setWaking(true);
    setError(null);
    setNotice(null);
    try {
      await api(`units/${encodeURIComponent(effectiveSerial)}/commands`, {
        method: "POST",
        body: JSON.stringify({ type: "wake_device" }),
      });
      setNotice("Wake sent. Give the device a few seconds to come out of standby, then search again.");
      await units.refresh();
    } catch (e: any) {
      setError(e.message || "Failed to wake device");
    } finally {
      setWaking(false);
    }
  }

  async function findRecordings() {
    setError(null);
    setNotice(null);
    setRecordings(null);
    if (!effectiveSerial) {
      setError("No connected device to search.");
      return;
    }
    const startUtc = Math.floor(new Date(start).getTime() / 1000);
    const endUtc = Math.floor(new Date(end).getTime() / 1000);
    if (!startUtc || !endUtc || endUtc <= startUtc) {
      setError("Search end must be after start.");
      return;
    }
    setSearching(true);
    try {
      const res = await api<{ recordings: Recording[]; count: number }>(
        `units/${encodeURIComponent(effectiveSerial)}/recordings?camera=${camera}&profile=${profile}&start_utc=${startUtc}&end_utc=${endUtc}`,
      );
      setRecordings(res.recordings);
      if (res.count === 0) {
        setNotice("No recordings found for that camera/quality/window. Try the other quality (main vs sub) or a wider date range.");
      }
    } catch (e: any) {
      setError(e.message || "Search failed");
    } finally {
      setSearching(false);
    }
  }

  // requestClip pulls a window from a recording. Without a window it pulls the
  // whole file; with one it pulls just that trimmed section.
  async function requestClip(rec: Recording, startUtc?: number, endUtc?: number) {
    setError(null);
    setNotice(null);
    const start = startUtc ?? rec.start_utc;
    const end = endUtc ?? rec.end_utc;
    setRequesting(recKey(rec));
    try {
      await api(`units/${encodeURIComponent(effectiveSerial)}/clips`, {
        method: "POST",
        body: JSON.stringify({ camera: rec.camera, profile: rec.profile, start_utc: start, end_utc: end }),
      });
      setNotice("Clip requested — the device is uploading it. It will appear in Stored clips below.");
      setTrimKey(null);
      await clips.refresh();
    } catch (e: any) {
      setError(e.message || "Failed to request clip");
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

  // Defense-in-depth behind the hidden nav link: a user could still reach /clips
  // by URL on a gateway whose unit/config has no video.
  if (caps && !caps.has_clips) {
    return (
      <div>
        <PageHeader title="Clips" subtitle="Find footage on a device's SD card and store it on the server" />
        <div className="rounded-md border border-edge bg-panel px-4 py-3 text-sm text-slate-400">
          Video is not enabled for this gateway, so recorded clips are unavailable.
        </div>
      </div>
    );
  }

  return (
    <div>
      <PageHeader title="Clips" subtitle="Find footage on a device's SD card and store it on the server" />
      <ErrorBanner message={error || units.error || clips.error} onDismiss={error ? () => setError(null) : undefined} />
      {notice && <div className="mb-4 rounded-md border border-indigo-500/40 bg-indigo-500/10 px-3 py-2 text-sm text-indigo-200">{notice}</div>}
      {sleeping && (
        <div className="mb-4 flex flex-wrap items-center gap-3 rounded-md border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-sm text-amber-200">
          <span>⚠ {effectiveSerial} is in <strong>standby</strong> — it won&apos;t return recordings or clips until woken.</span>
          <button className="btn-primary" onClick={wakeDevice} disabled={waking}>
            {waking ? "Waking…" : "Wake device"}
          </button>
        </div>
      )}

      {/* Step 1: search the device for available recordings */}
      <div className="card mb-6 space-y-3">
        <div className="flex flex-wrap items-end gap-3">
          <div>
            <label className="text-xs text-slate-400">Device</label>
            <select className="input mt-1 w-52" value={effectiveSerial} onChange={(e) => changeSerial(e.target.value)} disabled={searching}>
              {unitList.length === 0 && <option value="">No connected devices</option>}
              {unitList.map((u) => (
                <option key={u.serial} value={u.serial}>{u.serial} ({u.model})</option>
              ))}
            </select>
          </div>
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
            <label className="text-xs text-slate-400">From</label>
            <input type="datetime-local" className="input mt-1" value={start} onChange={(e) => setStart(e.target.value)} disabled={searching} />
          </div>
          <div>
            <label className="text-xs text-slate-400">To</label>
            <input type="datetime-local" className="input mt-1" value={end} onChange={(e) => setEnd(e.target.value)} disabled={searching} />
          </div>
          <button className="btn-primary" onClick={findRecordings} disabled={searching || !effectiveSerial}>
            {searching ? "Searching…" : "Find recordings"}
          </button>
        </div>
        <p className="text-xs text-slate-500">
          The device only has footage for cameras/qualities it was set to record. Pick a recording below to pull it to the server as an .mp4.
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
                // Clamp the chosen sub-window to the recording bounds.
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
                        <td className="td" colSpan={5}>
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
                            <button
                              className="btn-primary"
                              disabled={requesting === key}
                              onClick={() => requestClip(rec, winStart, winEnd)}
                            >
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

      {/* Step 3: stored clips */}
      <h2 className="mb-3 text-sm font-semibold text-slate-300">Stored clips</h2>
      {clips.loading && clipList.length === 0 ? (
        <Spinner />
      ) : clipList.length === 0 ? (
        <Empty>No clips yet. Find a recording above and pull it.</Empty>
      ) : (
        <div className="card overflow-x-auto p-0">
          <table className="min-w-full divide-y divide-edge">
            <thead>
              <tr>
                <th className="th">Device</th>
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
                  <td className="td font-mono">{c.serial}</td>
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
