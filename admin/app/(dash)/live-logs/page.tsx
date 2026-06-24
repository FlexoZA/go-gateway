"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { api } from "@/lib/api";
import { useUnits } from "@/lib/useGatewayInfo";
import { Badge, ErrorBanner, PageHeader } from "@/components/ui";

// Live Logs tails the gateway's in-memory log stream (connects, approvals, GPS
// forwards, ACKs, errors) by polling /api/logs/live with a cursor. The level
// selector also sets the gateway's capture verbosity at runtime (PUT
// /api/logs/level) — picking "debug" makes per-frame device activity flow without
// restarting the gateway. It does NOT change container-log verbosity.

type Level = "error" | "info" | "debug";

type Entry = {
  seq: number;
  time: string;
  level: Level;
  ns: string;
  fields: Record<string, any>;
};

type LiveResp = { entries: Entry[]; cursor: number; capture_level: Level };

const MAX_LINES = 2000;
const POLL_MS = 1500;

export default function LiveLogsPage() {
  const units = useUnits();
  const [level, setLevel] = useState<Level>("info");
  const [unit, setUnit] = useState("");
  const [q, setQ] = useState("");
  const [paused, setPaused] = useState(false);
  const [follow, setFollow] = useState(true);
  const [entries, setEntries] = useState<Entry[]>([]);
  const [error, setError] = useState<string | null>(null);

  const cursor = useRef(0);
  const bottom = useRef<HTMLDivElement | null>(null);

  // Apply the chosen capture level to the gateway (so debug is actually buffered),
  // then reset the tail and re-pull from the buffer with the current filters.
  const reset = useCallback(async () => {
    cursor.current = 0;
    setEntries([]);
    try {
      await api("logs/level", { method: "PUT", body: JSON.stringify({ level }) });
      setError(null);
    } catch (e: any) {
      setError(e.message || "Failed to set capture level");
    }
  }, [level]);

  useEffect(() => {
    reset();
  }, [reset, unit, q]);

  useEffect(() => {
    if (paused) return;
    let live = true;
    const poll = async () => {
      try {
        const qs = new URLSearchParams({ after: String(cursor.current), level, limit: "500" });
        if (unit) qs.set("unit", unit);
        if (q.trim()) qs.set("q", q.trim());
        const res = await api<LiveResp>(`logs/live?${qs.toString()}`);
        if (!live) return;
        cursor.current = res.cursor;
        if (res.entries.length) {
          setEntries((prev) => {
            const next = [...prev, ...res.entries];
            return next.length > MAX_LINES ? next.slice(next.length - MAX_LINES) : next;
          });
        }
        setError(null);
      } catch (e: any) {
        if (live) setError(e.message || "Poll failed");
      }
    };
    poll();
    const id = setInterval(poll, POLL_MS);
    return () => {
      live = false;
      clearInterval(id);
    };
  }, [paused, level, unit, q]);

  useEffect(() => {
    if (follow && !paused) bottom.current?.scrollIntoView({ block: "end" });
  }, [entries, follow, paused]);

  return (
    <div>
      <PageHeader
        title="Live Logs"
        subtitle="A live tail of gateway activity. Set the level to Debug to watch per-frame device traffic (GPS, ACKs). This does not affect container logs."
      />

      <div className="mb-3 flex flex-wrap items-end gap-3">
        <Field label="Level">
          <select className="input" value={level} onChange={(e) => setLevel(e.target.value as Level)}>
            <option value="error">Error</option>
            <option value="info">Info</option>
            <option value="debug">Debug (verbose)</option>
          </select>
        </Field>
        {units.length > 1 && (
          <Field label="Unit">
            <select className="input" value={unit} onChange={(e) => setUnit(e.target.value)}>
              <option value="">All</option>
              {units.map((u) => (
                <option key={u.unit} value={u.unit}>
                  {u.unit}
                </option>
              ))}
            </select>
          </Field>
        )}
        <Field label="Search">
          <input className="input" value={q} onChange={(e) => setQ(e.target.value)} placeholder="serial, event, …" />
        </Field>
        <div className="grow" />
        <label className="flex items-center gap-2 text-sm text-slate-300">
          <input type="checkbox" checked={follow} onChange={(e) => setFollow(e.target.checked)} /> Follow
        </label>
        <button className={paused ? "btn-primary" : "btn-ghost"} onClick={() => setPaused((p) => !p)}>
          {paused ? "Resume" : "Pause"}
        </button>
        <button className="btn-ghost" onClick={() => setEntries([])}>
          Clear
        </button>
      </div>

      <ErrorBanner message={error} />

      <div className="card h-[70vh] overflow-auto p-0 font-mono text-xs">
        {entries.length === 0 ? (
          <div className="p-4 text-slate-400">
            {paused ? "Paused." : "Waiting for activity…"} {level === "debug" ? "" : "Tip: switch to Debug to see GPS/ACK traffic."}
          </div>
        ) : (
          <div className="divide-y divide-edge/50">
            {entries.map((e) => (
              <Line key={e.seq} e={e} />
            ))}
            <div ref={bottom} />
          </div>
        )}
      </div>
      <p className="mt-2 text-xs text-slate-500">
        Showing the last {entries.length} of up to {MAX_LINES} buffered lines · polling every {POLL_MS / 1000}s.
      </p>
    </div>
  );
}

function Line({ e }: { e: Entry }) {
  const tone = e.level === "error" ? "rose" : e.level === "debug" ? "amber" : "slate";
  const { event, ...rest } = e.fields || {};
  const detail = Object.entries(rest)
    .map(([k, v]) => `${k}=${typeof v === "object" ? JSON.stringify(v) : v}`)
    .join("  ");
  return (
    <div className="flex items-start gap-3 px-3 py-1 hover:bg-edge/30">
      <span className="shrink-0 text-slate-500">{e.time.slice(11, 23)}</span>
      <Badge tone={tone as any}>{e.level}</Badge>
      <span className="shrink-0 text-indigo-300">{e.ns}</span>
      <span className="shrink-0 text-slate-200">{event != null ? String(event) : ""}</span>
      <span className="break-all text-slate-400">{detail}</span>
    </div>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <label className="mb-1 block text-xs text-slate-400">{label}</label>
      {children}
    </div>
  );
}
