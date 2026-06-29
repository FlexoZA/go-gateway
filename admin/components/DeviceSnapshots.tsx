"use client";

import { useEffect, useRef, useState } from "react";
import { api, apiBinary } from "@/lib/api";
import { useFetch } from "@/lib/useFetch";
import { useConfirm } from "@/components/confirm";
import { Empty, ErrorBanner, Spinner } from "@/components/ui";

// Resolution codes per the Howen H-Protocol snapshot request (0x4020 `res`).
const RESOLUTIONS: { value: number; label: string }[] = [
  { value: 0, label: "Follow video" },
  { value: 1, label: "1080p" },
  { value: 2, label: "720p" },
  { value: 3, label: "VGA" },
  { value: 4, label: "D1" },
];

type SavedSnapshot = {
  channel: number;
  device_path: string;
  size: number;
  utc: number;
  device_time: string;
  kind: string;
};

type GatewaySnapshot = {
  id: number;
  serial: string;
  camera: number;
  kind: string;
  source: string;
  captured_utc: number;
  device_path: string;
  file_size: number;
  created_at: string;
};

type Preview = { src: string; meta: string; downloadHref: string; downloadName: string; blob: boolean };

// DeviceSnapshots is the "Snapshots" tab: capture a new still (preview or save),
// search/view stills stored on the device's SD card, and manage snapshots saved
// to the gateway (file under CLIPS_ROOT/snapshots + a DB row).
//
// hasCapture gates the on-demand capture + device-search panels (Howen). A unit
// that only pushes event-preview snapshots (Cathexis) sets hasCapture=false and
// shows just the gateway-saved list, which fills automatically as events fire.
export function DeviceSnapshots({ serial, sleeping, hasCapture = true }: { serial: string; sleeping: boolean; hasCapture?: boolean }) {
  const [preview, setPreview] = useState<Preview | null>(null);
  const blobRef = useRef<string | null>(null);
  const gatewaySaved = useFetch<{ snapshots: GatewaySnapshot[] }>(`snapshots?serial=${encodeURIComponent(serial)}`);

  useEffect(
    () => () => {
      if (blobRef.current) URL.revokeObjectURL(blobRef.current);
    },
    [],
  );

  function showPreview(p: Preview) {
    if (blobRef.current) {
      URL.revokeObjectURL(blobRef.current);
      blobRef.current = null;
    }
    if (p.blob) blobRef.current = p.src;
    setPreview(p);
  }
  function closePreview() {
    if (blobRef.current) {
      URL.revokeObjectURL(blobRef.current);
      blobRef.current = null;
    }
    setPreview(null);
  }

  return (
    <div className="space-y-8">
      {hasCapture ? (
        <>
          <section>
            <h3 className="mb-3 text-sm font-semibold text-slate-300">Capture a snapshot</h3>
            <CapturePanel serial={serial} sleeping={sleeping} onPreview={showPreview} onSaved={gatewaySaved.refresh} />
          </section>

          <section>
            <h3 className="mb-3 text-sm font-semibold text-slate-300">Saved on the device</h3>
            <DevicePanel serial={serial} sleeping={sleeping} onView={showPreview} onSaved={gatewaySaved.refresh} />
          </section>
        </>
      ) : (
        <div className="rounded-md border border-indigo-500/40 bg-indigo-500/10 px-3 py-2 text-sm text-indigo-200">
          This unit pushes a snapshot automatically whenever an event triggers (for cameras enabled in the device’s
          event-preview config). They appear below as they arrive — there is no on-demand capture.
        </div>
      )}

      <section>
        <h3 className="mb-3 text-sm font-semibold text-slate-300">Saved on the gateway</h3>
        <GatewayPanel serial={serial} saved={gatewaySaved} onView={showPreview} />
      </section>

      {preview && <SnapshotModal preview={preview} onClose={closePreview} />}
    </div>
  );
}

function CapturePanel({
  serial,
  sleeping,
  onPreview,
  onSaved,
}: {
  serial: string;
  sleeping: boolean;
  onPreview: (p: Preview) => void;
  onSaved: () => void;
}) {
  const [camera, setCamera] = useState(0);
  const [resolution, setResolution] = useState(0);
  const [busy, setBusy] = useState<"capture" | "save" | null>(null);
  const [error, setError] = useState<string | null>(null);

  async function capture() {
    setError(null);
    setBusy("capture");
    try {
      const blob = await apiBinary(
        `units/${encodeURIComponent(serial)}/snapshot/image?camera=${camera}&resolution=${resolution}`,
        { method: "POST" },
      );
      const url = URL.createObjectURL(blob);
      const ts = Math.floor(Date.now() / 1000);
      onPreview({
        src: url,
        blob: true,
        meta: `Camera ${camera + 1} · ${fmtBytes(blob.size)} · ${new Date().toLocaleString()}`,
        downloadHref: url,
        downloadName: `snapshot_${serial}_cam${camera + 1}_${ts}.jpg`,
      });
    } catch (e: any) {
      setError(e.message || "Capture failed");
    } finally {
      setBusy(null);
    }
  }

  async function captureAndSave() {
    setError(null);
    setBusy("save");
    try {
      const res = await api<{ id: number }>(`units/${encodeURIComponent(serial)}/snapshots/save`, {
        method: "POST",
        body: JSON.stringify({ source: "capture", camera, resolution }),
      });
      onSaved();
      const href = `/api/gw/snapshots/${res.id}/download`;
      onPreview({
        src: href,
        blob: false,
        meta: `Saved to gateway · Camera ${camera + 1} · ${new Date().toLocaleString()}`,
        downloadHref: href,
        downloadName: `snapshot_${res.id}.jpg`,
      });
    } catch (e: any) {
      setError(e.message || "Save failed");
    } finally {
      setBusy(null);
    }
  }

  return (
    <div>
      <ErrorBanner message={error} />
      <div className="card space-y-3">
        <div className="flex flex-wrap items-end gap-3">
          <div>
            <label className="text-xs text-slate-400">Camera</label>
            <select className="input mt-1 w-32" value={camera} onChange={(e) => setCamera(Number(e.target.value))} disabled={!!busy}>
              {[0, 1, 2, 3].map((c) => (
                <option key={c} value={c}>
                  Camera {c + 1}
                </option>
              ))}
            </select>
          </div>
          <div>
            <label className="text-xs text-slate-400">Resolution</label>
            <select className="input mt-1 w-40" value={resolution} onChange={(e) => setResolution(Number(e.target.value))} disabled={!!busy}>
              {RESOLUTIONS.map((r) => (
                <option key={r.value} value={r.value}>
                  {r.label}
                </option>
              ))}
            </select>
          </div>
          <button
            className="btn-ghost"
            onClick={capture}
            disabled={!!busy || sleeping}
            title={sleeping ? "Device is in standby — wake it to capture a snapshot" : undefined}
          >
            {busy === "capture" ? "Capturing…" : "Capture"}
          </button>
          <button
            className="btn-primary"
            onClick={captureAndSave}
            disabled={!!busy || sleeping}
            title={sleeping ? "Device is in standby — wake it to capture a snapshot" : undefined}
          >
            {busy === "save" ? "Saving…" : "Capture & save"}
          </button>
        </div>
        <p className="text-xs text-slate-500">
          <strong>Capture</strong> previews the image only. <strong>Capture &amp; save</strong> stores it on the gateway
          (it appears under “Saved on the gateway” below). A capture can take a few seconds.
        </p>
      </div>
    </div>
  );
}

function DevicePanel({
  serial,
  sleeping,
  onView,
  onSaved,
}: {
  serial: string;
  sleeping: boolean;
  onView: (p: Preview) => void;
  onSaved: () => void;
}) {
  const range = defaultRange();
  const [camera, setCamera] = useState(-1);
  const [kind, setKind] = useState<"general" | "alarm">("general");
  const [start, setStart] = useState(range.start);
  const [end, setEnd] = useState(range.end);
  const [searching, setSearching] = useState(false);
  const [savingPath, setSavingPath] = useState<string | null>(null);
  const [results, setResults] = useState<SavedSnapshot[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  async function search() {
    setError(null);
    setNotice(null);
    setResults(null);
    const startUtc = Math.floor(new Date(start).getTime() / 1000);
    const endUtc = Math.floor(new Date(end).getTime() / 1000);
    if (!startUtc || !endUtc || endUtc <= startUtc) {
      setError("Search end must be after start.");
      return;
    }
    setSearching(true);
    try {
      const res = await api<{ snapshots: SavedSnapshot[]; count: number }>(
        `units/${encodeURIComponent(serial)}/snapshots/search?camera=${camera}&start_utc=${startUtc}&end_utc=${endUtc}&kind=${kind}`,
      );
      setResults(res.snapshots);
      if (res.count === 0) setNotice("No saved snapshots for that camera/kind/window.");
    } catch (e: any) {
      setError(e.message || "Search failed");
    } finally {
      setSearching(false);
    }
  }

  function view(s: SavedSnapshot) {
    const href = `/api/gw/units/${encodeURIComponent(serial)}/snapshots/file?path=${encodeURIComponent(s.device_path)}`;
    onView({
      src: href,
      blob: false,
      meta: `Camera ${s.channel + 1} · ${fmtBytes(s.size)} · ${s.device_time || fmtUtc(s.utc)}`,
      downloadHref: href,
      downloadName: baseName(s.device_path) || `snapshot_cam${s.channel + 1}.jpg`,
    });
  }

  async function saveToGateway(s: SavedSnapshot) {
    setError(null);
    setNotice(null);
    setSavingPath(s.device_path);
    try {
      await api(`units/${encodeURIComponent(serial)}/snapshots/save`, {
        method: "POST",
        body: JSON.stringify({ source: "device", device_path: s.device_path, camera: s.channel, kind: s.kind, captured_utc: s.utc }),
      });
      onSaved();
      setNotice("Saved to gateway — see “Saved on the gateway” below.");
    } catch (e: any) {
      setError(e.message || "Save failed");
    } finally {
      setSavingPath(null);
    }
  }

  return (
    <div>
      <ErrorBanner message={error} />
      {notice && (
        <div className="mb-4 rounded-md border border-indigo-500/40 bg-indigo-500/10 px-3 py-2 text-sm text-indigo-200">{notice}</div>
      )}
      <div className="card mb-6 space-y-3">
        <div className="flex flex-wrap items-end gap-3">
          <div>
            <label className="text-xs text-slate-400">Camera</label>
            <select className="input mt-1 w-32" value={camera} onChange={(e) => setCamera(Number(e.target.value))} disabled={searching}>
              <option value={-1}>All</option>
              {[0, 1, 2, 3].map((c) => (
                <option key={c} value={c}>
                  Camera {c + 1}
                </option>
              ))}
            </select>
          </div>
          <div>
            <label className="text-xs text-slate-400">Kind</label>
            <select className="input mt-1 w-36" value={kind} onChange={(e) => setKind(e.target.value as "general" | "alarm")} disabled={searching}>
              <option value="general">General</option>
              <option value="alarm">Alarm</option>
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
            onClick={search}
            disabled={searching || sleeping}
            title={sleeping ? "Device is in standby — wake it to search snapshots" : undefined}
          >
            {searching ? "Searching…" : "Find snapshots"}
          </button>
        </div>
        <p className="text-xs text-slate-500">
          Snapshots are indexed by the device clock; the gateway localizes the window. Times shown are the device-reported time.
        </p>
      </div>

      {searching ? (
        <Spinner />
      ) : results && results.length > 0 ? (
        <div className="card overflow-x-auto p-0">
          <table className="min-w-full divide-y divide-edge">
            <thead>
              <tr>
                <th className="th">Time (device)</th>
                <th className="th">Camera</th>
                <th className="th">Kind</th>
                <th className="th">Size</th>
                <th className="th text-right">Action</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-edge">
              {results.map((s) => (
                <tr key={s.device_path}>
                  <td className="td font-mono">{s.device_time || fmtUtc(s.utc)}</td>
                  <td className="td text-slate-400">Cam {s.channel + 1}</td>
                  <td className="td text-slate-400 capitalize">{s.kind}</td>
                  <td className="td text-slate-400">{fmtBytes(s.size)}</td>
                  <td className="td">
                    <div className="flex justify-end gap-2">
                      <button className="btn-ghost" onClick={() => view(s)}>
                        View
                      </button>
                      <button className="btn-primary" onClick={() => saveToGateway(s)} disabled={savingPath === s.device_path}>
                        {savingPath === s.device_path ? "Saving…" : "Save to gateway"}
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : results ? (
        <Empty>No saved snapshots for that window.</Empty>
      ) : null}
    </div>
  );
}

function GatewayPanel({
  serial,
  saved,
  onView,
}: {
  serial: string;
  saved: ReturnType<typeof useFetch<{ snapshots: GatewaySnapshot[] }>>;
  onView: (p: Preview) => void;
}) {
  const confirm = useConfirm();
  const [error, setError] = useState<string | null>(null);
  const list = saved.data?.snapshots ?? [];

  function view(s: GatewaySnapshot) {
    const href = `/api/gw/snapshots/${s.id}/download`;
    onView({
      src: href,
      blob: false,
      meta: `Camera ${s.camera + 1} · ${s.source} · ${fmtBytes(s.file_size)} · ${fmtUtc(s.captured_utc)}`,
      downloadHref: href,
      downloadName: `snapshot_${s.id}.jpg`,
    });
  }

  async function remove(id: number) {
    if (!(await confirm({ title: "Delete snapshot?", body: "This removes it from the gateway storage and database.", confirmLabel: "Delete" }))) return;
    setError(null);
    try {
      await api(`snapshots/${id}`, { method: "DELETE" });
      await saved.refresh();
    } catch (e: any) {
      setError(e.message || "Delete failed");
    }
  }

  return (
    <div>
      <ErrorBanner message={error || saved.error} />
      {saved.loading && list.length === 0 ? (
        <Spinner />
      ) : list.length === 0 ? (
        <Empty>No snapshots saved on the gateway yet. Use “Capture &amp; save” or “Save to gateway” above.</Empty>
      ) : (
        <div className="card overflow-x-auto p-0">
          <table className="min-w-full divide-y divide-edge">
            <thead>
              <tr>
                <th className="th">Captured</th>
                <th className="th">Camera</th>
                <th className="th">Event / kind</th>
                <th className="th">Source</th>
                <th className="th">Size</th>
                <th className="th text-right">Actions</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-edge">
              {list.map((s) => (
                <tr key={s.id}>
                  <td className="td font-mono">{fmtUtc(s.captured_utc)}</td>
                  <td className="td text-slate-400">Cam {s.camera + 1}</td>
                  <td className="td text-slate-400">{s.kind || "—"}</td>
                  <td className="td text-slate-400 capitalize">{s.source}</td>
                  <td className="td text-slate-400">{fmtBytes(s.file_size)}</td>
                  <td className="td">
                    <div className="flex justify-end gap-2">
                      <button className="btn-ghost" onClick={() => view(s)}>
                        View
                      </button>
                      <a className="btn-ghost" href={`/api/gw/snapshots/${s.id}/download`} download={`snapshot_${s.id}.jpg`}>
                        Download
                      </a>
                      <button className="btn-danger" onClick={() => remove(s.id)}>
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

// SnapshotModal shows a captured/saved still in a centered lightbox — click the
// backdrop, press Escape, or hit Close to dismiss.
function SnapshotModal({ preview, onClose }: { preview: Preview; onClose: () => void }) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 p-4"
      role="dialog"
      aria-modal="true"
      onClick={onClose}
    >
      <div
        className="flex max-h-[90vh] w-full max-w-5xl flex-col rounded-lg border border-edge bg-panel p-3 shadow-xl"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="mb-2 flex flex-wrap items-center justify-between gap-2 text-xs text-slate-400">
          <span>{preview.meta}</span>
          <div className="flex gap-2">
            <a className="btn-ghost" href={preview.downloadHref} download={preview.downloadName}>
              Download
            </a>
            <button className="btn-ghost" onClick={onClose}>
              Close
            </button>
          </div>
        </div>
        {/* eslint-disable-next-line @next/next/no-img-element */}
        <img
          src={preview.src}
          alt="Device snapshot"
          className="mx-auto max-h-[80vh] w-auto rounded-md border border-edge object-contain"
        />
      </div>
    </div>
  );
}

function fmtBytes(n: number): string {
  if (!n) return "—";
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(0)} KB`;
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}
function fmtUtc(unix: number): string {
  if (!unix) return "—";
  return new Date(unix * 1000).toLocaleString();
}
function baseName(path: string): string {
  const parts = path.split("/").filter(Boolean);
  return parts.length ? parts[parts.length - 1] : path;
}
function toLocalInput(d: Date) {
  const off = d.getTimezoneOffset() * 60_000;
  return new Date(d.getTime() - off).toISOString().slice(0, 16);
}
function defaultRange() {
  const now = new Date();
  const weekAgo = new Date(now.getTime() - 7 * 86400_000);
  return { start: toLocalInput(weekAgo), end: toLocalInput(now) };
}
