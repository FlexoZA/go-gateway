"use client";

import { useEffect, useRef, useState } from "react";
import Hls from "hls.js";
import { api } from "@/lib/api";
import { Badge } from "@/components/ui";

type State = "idle" | "starting" | "live" | "error";

// LivePlayer starts a live HLS stream for a device camera and plays it with
// hls.js. The .m3u8 + .ts are fetched through the BFF (/api/gw/hls/...), so the
// player stays authenticated by the session cookie.
export function LivePlayer({ serial, disabled = false }: { serial: string; disabled?: boolean }) {
  const videoRef = useRef<HTMLVideoElement>(null);
  const hlsRef = useRef<Hls | null>(null);
  // The camera/profile actually streaming right now (null when idle). The unmount
  // cleanup and stop() use this rather than the live `camera`/`profile` state so we
  // always stop the stream that was started, not whatever the selects last showed.
  const activeRef = useRef<{ camera: number; profile: number } | null>(null);
  const [camera, setCamera] = useState(0);
  const [profile, setProfile] = useState(1); // default sub-stream (lower bandwidth/latency)
  const [state, setState] = useState<State>("idle");
  const [error, setError] = useState<string | null>(null);

  function teardown() {
    if (hlsRef.current) {
      hlsRef.current.destroy();
      hlsRef.current = null;
    }
    const v = videoRef.current;
    if (v) {
      v.removeAttribute("src");
      v.load();
    }
  }

  function attach(url: string) {
    const video = videoRef.current;
    if (!video) return;
    teardown();

    if (Hls.isSupported()) {
      const hls = new Hls({
        // Plain ffmpeg HLS (not LL-HLS); LL mode expects blocking playlist
        // reloads and misbehaves on the short startup playlist.
        lowLatencyMode: false,
        liveSyncDurationCount: 3,
        // ffmpeg may not have written the first segments yet — keep retrying the
        // manifest/segments for a while instead of failing immediately.
        manifestLoadingMaxRetry: 30,
        manifestLoadingRetryDelay: 1000,
        levelLoadingMaxRetry: 30,
        levelLoadingRetryDelay: 1000,
      });
      hlsRef.current = hls;
      hls.on(Hls.Events.MANIFEST_PARSED, () => {
        video.play().catch(() => {});
        setState("live");
      });
      hls.on(Hls.Events.ERROR, (_evt, data) => {
        if (data.fatal) {
          if (data.type === Hls.ErrorTypes.NETWORK_ERROR) {
            hls.startLoad(); // recover (e.g. segments not ready yet)
          } else if (data.type === Hls.ErrorTypes.MEDIA_ERROR) {
            hls.recoverMediaError();
          } else {
            setError("Playback error");
            setState("error");
          }
        }
      });
      hls.loadSource(url);
      hls.attachMedia(video);
    } else if (video.canPlayType("application/vnd.apple.mpegurl")) {
      video.src = url; // Safari native HLS
      video.play().catch(() => {});
      setState("live");
    } else {
      setError("HLS not supported in this browser");
      setState("error");
    }
  }

  async function start() {
    setState("starting");
    setError(null);
    try {
      const res = await api<{ hls_path: string; ready?: boolean }>(`units/${encodeURIComponent(serial)}/stream/start`, {
        method: "POST",
        body: JSON.stringify({ camera, profile }),
      });
      // The gateway waits for ffmpeg's first segment; ready=false means the
      // device accepted the command but sent no video in time (camera off /
      // wrong channel). Still attach — hls.js keeps retrying for late frames.
      if (res.ready === false) {
        setError("Device accepted the request but sent no video yet — check the camera/channel.");
      }
      activeRef.current = { camera, profile }; // record what we actually started
      attach(`/api/gw/hls/${res.hls_path}`);
    } catch (e: any) {
      setError(e.message || "Failed to start stream");
      setState("error");
    }
  }

  async function stop() {
    const active = activeRef.current ?? { camera, profile };
    activeRef.current = null;
    teardown();
    setState("idle");
    try {
      await api(`units/${encodeURIComponent(serial)}/stream/stop`, {
        method: "POST",
        body: JSON.stringify(active),
      });
    } catch {
      /* best-effort */
    }
  }

  // Stop the stream when leaving the page. Uses the ref (not the render-time
  // camera/profile) so it stops whatever is actually streaming.
  useEffect(() => {
    return () => {
      teardown();
      const active = activeRef.current;
      if (!active) return; // nothing was streaming
      // fire-and-forget stop so we don't leave the device streaming
      api(`units/${encodeURIComponent(serial)}/stream/stop`, {
        method: "POST",
        body: JSON.stringify(active),
      }).catch(() => {});
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const busy = state === "starting";
  const live = state === "live";

  return (
    <div className="max-w-3xl space-y-4">
      <div className="card space-y-3">
        <div className="flex flex-wrap items-end gap-3">
          <div>
            <label className="text-xs text-slate-400">Camera</label>
            <select className="input mt-1 w-40" value={camera} onChange={(e) => setCamera(Number(e.target.value))} disabled={live || busy}>
              <option value={0}>Camera 1 (Road)</option>
              <option value={1}>Camera 2 (Cab)</option>
            </select>
          </div>
          <div>
            <label className="text-xs text-slate-400">Quality</label>
            <select className="input mt-1 w-40" value={profile} onChange={(e) => setProfile(Number(e.target.value))} disabled={live || busy}>
              <option value={1}>Low (sub-stream)</option>
              <option value={0}>High (main)</option>
            </select>
          </div>
          <div className="grow" />
          {live || busy ? (
            <button className="btn-danger" onClick={stop}>
              Stop
            </button>
          ) : (
            <button
              className="btn-primary"
              onClick={start}
              disabled={disabled}
              title={disabled ? "Device is in standby — wake it to stream" : undefined}
            >
              Start stream
            </button>
          )}
        </div>

        <div className="flex items-center gap-2 text-xs">
          {state === "idle" && <Badge tone="slate">Idle</Badge>}
          {state === "starting" && <Badge tone="amber">Starting… (telling the device to stream)</Badge>}
          {state === "live" && <Badge tone="green">Live</Badge>}
          {state === "error" && <Badge tone="rose">Error</Badge>}
          {error && <span className="text-rose-300">{error}</span>}
        </div>
      </div>

      <div className="overflow-hidden rounded-lg border border-edge bg-black">
        <video ref={videoRef} className="aspect-video w-full" controls muted playsInline autoPlay />
      </div>

      <p className="text-xs text-slate-500">
        HLS has ~5–10s latency. If the picture takes a few seconds to appear after Start, that&apos;s the device negotiating the
        stream and ffmpeg producing the first segments.
      </p>
    </div>
  );
}
