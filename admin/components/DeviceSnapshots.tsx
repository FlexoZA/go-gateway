"use client";

import { useEffect, useRef, useState } from "react";
import { apiBinary } from "@/lib/api";
import { ErrorBanner, Spinner } from "@/components/ui";

// Resolution codes per the Howen H-Protocol snapshot request (0x4020 `res`).
const RESOLUTIONS: { value: number; label: string }[] = [
  { value: 0, label: "Follow video" },
  { value: 1, label: "1080p" },
  { value: 2, label: "720p" },
  { value: 3, label: "VGA" },
  { value: 4, label: "D1" },
];

// DeviceSnapshots is the "Snapshots" tab of a device's detail page: capture a
// still on a chosen camera and show it inline. The gateway triggers the capture
// (0x4020) and pulls the JPEG back over the device's media link (file-transfer
// 0x4090), responding with image/jpeg — so we fetch it as a Blob and preview it.
export function DeviceSnapshots({ serial, sleeping }: { serial: string; sleeping: boolean }) {
  const [camera, setCamera] = useState(0);
  const [resolution, setResolution] = useState(0);
  const [capturing, setCapturing] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [shot, setShot] = useState<{ url: string; bytes: number; at: Date; camera: number } | null>(null);
  const urlRef = useRef<string | null>(null);

  // Revoke the last object URL on unmount so we don't leak blobs.
  useEffect(
    () => () => {
      if (urlRef.current) URL.revokeObjectURL(urlRef.current);
    },
    [],
  );

  async function capture() {
    setError(null);
    setCapturing(true);
    try {
      const blob = await apiBinary(
        `units/${encodeURIComponent(serial)}/snapshot/image?camera=${camera}&resolution=${resolution}`,
        { method: "POST" },
      );
      const url = URL.createObjectURL(blob);
      if (urlRef.current) URL.revokeObjectURL(urlRef.current);
      urlRef.current = url;
      setShot({ url, bytes: blob.size, at: new Date(), camera });
    } catch (e: any) {
      setError(e.message || "Capture failed");
    } finally {
      setCapturing(false);
    }
  }

  return (
    <div>
      <ErrorBanner message={error} />

      <div className="card mb-6 space-y-3">
        <div className="flex flex-wrap items-end gap-3">
          <div>
            <label className="text-xs text-slate-400">Camera</label>
            <select
              className="input mt-1 w-32"
              value={camera}
              onChange={(e) => setCamera(Number(e.target.value))}
              disabled={capturing}
            >
              {[0, 1, 2, 3].map((c) => (
                <option key={c} value={c}>
                  Camera {c + 1}
                </option>
              ))}
            </select>
          </div>
          <div>
            <label className="text-xs text-slate-400">Resolution</label>
            <select
              className="input mt-1 w-40"
              value={resolution}
              onChange={(e) => setResolution(Number(e.target.value))}
              disabled={capturing}
            >
              {RESOLUTIONS.map((r) => (
                <option key={r.value} value={r.value}>
                  {r.label}
                </option>
              ))}
            </select>
          </div>
          <button
            className="btn-primary"
            onClick={capture}
            disabled={capturing || sleeping}
            title={sleeping ? "Device is in standby — wake it to capture a snapshot" : undefined}
          >
            {capturing ? "Capturing…" : "Capture snapshot"}
          </button>
        </div>
        <p className="text-xs text-slate-500">
          The device takes the photo and uploads it over its media link, so a capture can take a few
          seconds.
        </p>
      </div>

      {capturing && !shot && <Spinner />}

      {shot && (
        <div className="card p-3">
          <div className="mb-2 flex flex-wrap items-center justify-between gap-2 text-xs text-slate-400">
            <span>
              Camera {shot.camera + 1} · {fmtBytes(shot.bytes)} · {shot.at.toLocaleString()}
            </span>
            <a
              className="btn-ghost"
              href={shot.url}
              download={`snapshot_${serial}_cam${shot.camera + 1}_${Math.floor(shot.at.getTime() / 1000)}.jpg`}
            >
              Download
            </a>
          </div>
          {/* eslint-disable-next-line @next/next/no-img-element */}
          <img src={shot.url} alt="Device snapshot" className="w-full rounded-md border border-edge" />
        </div>
      )}
    </div>
  );
}

function fmtBytes(n: number): string {
  if (!n) return "—";
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(0)} KB`;
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}
