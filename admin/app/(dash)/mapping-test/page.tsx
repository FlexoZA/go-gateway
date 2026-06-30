"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { api } from "@/lib/api";
import { useFetch } from "@/lib/useFetch";
import { useGatewayInfo, capsForUnit } from "@/lib/useGatewayInfo";
import { Badge, Empty, ErrorBanner, PageHeader } from "@/components/ui";

// Mapping Test is a live tester for device event mappings: pick a connected
// device, trigger events on it (press panic, harsh-brake, etc.), and watch each
// raw device code resolve through the mapping tables in real time — e.g.
// "vibration_direction code 4 → COLLISION". Codes that no mapping row matches
// are flagged so you can add them on the Device Mapping page.
//
// It works by forcing the gateway's log capture to "debug" (so the per-alarm
// `alarm_forward` entries — which carry the decode trace the gateway just ran —
// are buffered), then polling /api/logs/live filtered to the chosen serial. The
// previous capture level is restored when the test stops.

type Unit = { serial: string; protocol: string; model: string; state?: string };

type TraceEntry = {
  ec: number;
  map_type?: string;
  code: number;
  event_code: string;
  source: "table" | "builtin" | "fallback";
};

type Entry = {
  seq: number;
  time: string;
  fields: Record<string, any>;
};

type LiveResp = { entries: Entry[]; cursor: number; capture_level: string };

type Fired = {
  seq: number;
  time: string;
  ec: number;
  model: string;
  mapped: string[];
  trace: TraceEntry[];
};

const POLL_MS = 1500;
const MAX_EVENTS = 300;

export default function MappingTestPage() {
  const info = useGatewayInfo();
  const units = useFetch<{ units: Unit[] }>("units", 8000);
  const [serial, setSerial] = useState("");
  const [running, setRunning] = useState(false);
  const [events, setEvents] = useState<Fired[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [note, setNote] = useState<string | null>(null);

  const cursor = useRef(0);
  const prevLevel = useRef<string | null>(null);

  // Only devices whose unit type drives output from editable mappings can be
  // meaningfully tested here.
  const deviceList = (units.data?.units ?? []).filter((u) => capsForUnit(info, u.protocol)?.has_mappings !== false);
  const effectiveSerial = serial || deviceList[0]?.serial || "";
  const selected = deviceList.find((u) => u.serial === effectiveSerial);

  const stop = useCallback(async () => {
    setRunning(false);
    if (prevLevel.current) {
      try {
        await api("logs/level", { method: "PUT", body: JSON.stringify({ level: prevLevel.current }) });
      } catch {
        /* best-effort restore */
      }
      prevLevel.current = null;
    }
  }, []);

  async function start() {
    if (!effectiveSerial) return;
    setError(null);
    setEvents([]);
    cursor.current = 0;
    try {
      // Remember the operator's level so we can put it back, then force debug so
      // alarm_forward entries (which carry the decode trace) are captured.
      const cur = await api<{ level: string }>("logs/level");
      prevLevel.current = cur.level || "info";
      await api("logs/level", { method: "PUT", body: JSON.stringify({ level: "debug" }) });
      setNote(`Listening to ${effectiveSerial}. Trigger an event on the device — mapped events appear below.`);
      setRunning(true);
    } catch (e: any) {
      setError(e.message || "Failed to start test");
    }
  }

  // Restore the capture level if the operator navigates away mid-test.
  useEffect(() => {
    return () => {
      if (prevLevel.current) {
        api("logs/level", { method: "PUT", body: JSON.stringify({ level: prevLevel.current }) }).catch(() => {});
      }
    };
  }, []);

  useEffect(() => {
    if (!running || !effectiveSerial) return;
    let live = true;
    const poll = async () => {
      try {
        const qs = new URLSearchParams({ after: String(cursor.current), level: "debug", limit: "500", q: effectiveSerial });
        const res = await api<LiveResp>(`logs/live?${qs.toString()}`);
        if (!live) return;
        cursor.current = res.cursor;
        const fired: Fired[] = [];
        for (const e of res.entries) {
          const f = e.fields || {};
          if (f.event !== "alarm_forward" || String(f.serial) !== effectiveSerial) continue;
          fired.push({
            seq: e.seq,
            time: e.time,
            ec: Number(f.ec),
            model: String(f.model || ""),
            mapped: Array.isArray(f.mapped_events) ? f.mapped_events.map(String) : [],
            trace: Array.isArray(f.mapping_trace) ? (f.mapping_trace as TraceEntry[]) : [],
          });
        }
        if (fired.length) {
          // Newest first so the latest trigger is always in view.
          setEvents((prev) => [...fired.reverse(), ...prev].slice(0, MAX_EVENTS));
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
  }, [running, effectiveSerial]);

  return (
    <div>
      <PageHeader
        title="Mapping Test"
        subtitle="Trigger events on a live device and watch each raw code resolve through your mappings in real time. Unmapped codes are flagged so you can add them."
      />

      <div className="mb-4 flex flex-wrap items-end gap-3">
        <div>
          <label className="mb-1 block text-xs text-slate-400">Device</label>
          <select
            className="input w-72"
            value={effectiveSerial}
            disabled={running}
            onChange={(e) => setSerial(e.target.value)}
          >
            {deviceList.length === 0 && <option value="">No connected devices</option>}
            {deviceList.map((u) => (
              <option key={u.serial} value={u.serial}>
                {u.serial} · {u.protocol}
                {u.model ? ` (${u.model})` : ""}
              </option>
            ))}
          </select>
        </div>
        {selected?.state === "sleep" && (
          <span className="pb-2 text-xs text-amber-300">⚠ Device is in standby — wake it (Devices page) to receive events.</span>
        )}
        <div className="grow" />
        {running ? (
          <button className="btn-danger" onClick={stop}>
            Stop test
          </button>
        ) : (
          <button className="btn-primary" disabled={!effectiveSerial} onClick={start}>
            Start test
          </button>
        )}
        <button className="btn-ghost" onClick={() => setEvents([])} disabled={events.length === 0}>
          Clear
        </button>
      </div>

      <ErrorBanner message={error} onDismiss={error ? () => setError(null) : undefined} />

      {running && (
        <div className="mb-4 rounded-md border border-indigo-500/40 bg-indigo-500/10 px-3 py-2 text-sm text-indigo-200">
          <span className="inline-flex items-center gap-2">
            <span className="h-2 w-2 animate-pulse rounded-full bg-indigo-400" />
            {note || `Listening to ${effectiveSerial}…`}
          </span>
          <span className="ml-2 text-xs text-indigo-300/70">Capture level forced to debug while testing; restored on stop.</span>
        </div>
      )}

      {events.length === 0 ? (
        <Empty>
          {running
            ? "Waiting for events… trigger one on the device (e.g. press the panic button or harsh-brake)."
            : "Pick a device and press Start test, then trigger events on it."}
        </Empty>
      ) : (
        <div className="space-y-2">
          {events.map((ev) => (
            <FiredCard key={ev.seq} ev={ev} />
          ))}
        </div>
      )}
    </div>
  );
}

function FiredCard({ ev }: { ev: Fired }) {
  return (
    <div className="card">
      <div className="mb-2 flex items-center gap-3 text-xs text-slate-400">
        <span className="font-mono text-slate-300">{ev.time.slice(11, 23)}</span>
        <span className="font-mono">EC {ev.ec}</span>
        {ev.model && <span>· {ev.model}</span>}
      </div>
      <div className="space-y-1">
        {ev.trace.length === 0 ? (
          <span className="text-sm text-slate-400">No decode trace.</span>
        ) : (
          ev.trace.map((t, i) => <TraceLine key={i} t={t} />)
        )}
      </div>
    </div>
  );
}

function TraceLine({ t }: { t: TraceEntry }) {
  const unmapped = t.source === "fallback";
  return (
    <div className="flex flex-wrap items-center gap-2 text-sm">
      {t.map_type ? (
        <span className="font-mono text-slate-400">
          <span className="text-indigo-300">{t.map_type}</span> code <span className="text-slate-100">{t.code}</span>
        </span>
      ) : (
        <span className="font-mono text-slate-400">
          EC <span className="text-slate-100">{t.code}</span>
        </span>
      )}
      <span className="text-slate-500">→</span>
      {unmapped ? (
        <span className="font-semibold text-amber-300">{t.event_code || "(no mapping)"}</span>
      ) : (
        <span className="font-semibold text-emerald-300">{t.event_code}</span>
      )}
      {t.source === "table" && <Badge tone="green">mapped</Badge>}
      {t.source === "builtin" && <Badge tone="slate">built-in</Badge>}
      {unmapped && <Badge tone="amber">unmapped</Badge>}
    </div>
  );
}
