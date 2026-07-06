"use client";

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { usePathname } from "next/navigation";
import { api } from "@/lib/api";
import { useFetch } from "@/lib/useFetch";
import { useGatewayInfo, capsForUnit } from "@/lib/useGatewayInfo";

// MappingTest is a live tester for device event mappings: pick a connected
// device, trigger events on it (press panic, harsh-brake, etc.), and watch each
// raw device code resolve through the mapping tables in real time — e.g.
// "vibration_direction code 4 → COLLISION". Codes that no mapping row matches
// are flagged so you can add them on the Device Mapping page.
//
// It works by forcing the gateway's log capture to "debug" (so the per-alarm
// `alarm_forward` entries — which carry the decode trace the gateway just ran —
// are buffered), then polling /api/logs/live filtered to the chosen serial.
//
// The whole lifecycle lives in this provider (mounted once in the dash layout,
// above the router) rather than on the Mapping Test page, so a running test
// survives navigation: you can start it, jump to Device Mapping to fix a row,
// and keep watching the live feed in the drawer. The previous capture level is
// restored when the test stops (Stop, closing the drawer, or leaving the admin).

export const MAPPING_TEST_PATH = "/mapping-test";

const POLL_MS = 1500;
const UNITS_POLL_MS = 8000;
const MAX_EVENTS = 300;

export type TraceEntry = {
  ec?: number;
  name?: string; // raw device event name (Cathexis)
  map_type?: string;
  code: number;
  event_code: string;
  source: "table" | "builtin" | "fallback";
};

export type Fired = {
  seq: number;
  time: string;
  ec: number | null;
  model: string;
  mapped: string[];
  trace: TraceEntry[];
};

export type MappingUnit = { serial: string; protocol: string; model: string; state?: string; commands?: string[] };

type Entry = { seq: number; time: string; fields: Record<string, any> };
type LiveResp = { entries: Entry[]; cursor: number; capture_level: string };

type MappingTestState = {
  deviceList: MappingUnit[];
  serial: string; // the effective serial (selection or first device)
  setSerial: (s: string) => void;
  selected: MappingUnit | undefined;
  running: boolean;
  events: Fired[];
  note: string | null;
  error: string | null;
  setError: (e: string | null) => void;
  waking: boolean;
  canWake: boolean;
  sleeping: boolean;
  start: () => Promise<void>;
  stop: () => Promise<void>;
  wake: () => Promise<void>;
  clear: () => void;
};

const Ctx = createContext<MappingTestState | null>(null);

export function MappingTestProvider({ children }: { children: ReactNode }) {
  const pathname = usePathname();
  const onPage = pathname === MAPPING_TEST_PATH;

  const info = useGatewayInfo();
  const [running, setRunning] = useState(false);
  // Only poll the unit list when it's actually needed — while the test runs or
  // while the operator is on the Mapping Test page choosing a device. On other
  // pages the provider is dormant (single fetch, no interval).
  const units = useFetch<{ units: MappingUnit[] }>("units", running || onPage ? UNITS_POLL_MS : 0);

  const [serial, setSerial] = useState("");
  const [events, setEvents] = useState<Fired[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [note, setNote] = useState<string | null>(null);
  const [waking, setWaking] = useState(false);

  const cursor = useRef(0);
  const prevLevel = useRef<string | null>(null);

  // Only devices whose unit type drives output from editable mappings can be
  // meaningfully tested here.
  const deviceList = (units.data?.units ?? []).filter((u) => capsForUnit(info, u.protocol)?.has_mappings !== false);
  const effectiveSerial = serial || deviceList[0]?.serial || "";
  const selected = deviceList.find((u) => u.serial === effectiveSerial);
  const sleeping = selected?.state === "sleep";
  const canWake = !!selected?.commands?.includes("wake_device");

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

  const start = useCallback(async () => {
    if (!effectiveSerial) return;
    setError(null);
    setEvents([]);
    cursor.current = 0;
    // Freeze the current selection so a reordered unit list can't switch which
    // device we're listening to mid-test.
    setSerial(effectiveSerial);
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
  }, [effectiveSerial]);

  const wake = useCallback(async () => {
    if (!effectiveSerial) return;
    setWaking(true);
    setError(null);
    try {
      await api(`units/${encodeURIComponent(effectiveSerial)}/commands`, {
        method: "POST",
        body: JSON.stringify({ type: "wake_device" }),
      });
      setNote("Wake sent — give the device a few seconds to come out of standby.");
      await units.refresh();
    } catch (e: any) {
      setError(e.message || "Failed to wake device");
    } finally {
      setWaking(false);
    }
  }, [effectiveSerial, units]);

  const clear = useCallback(() => setEvents([]), []);

  // Restore the capture level if the operator leaves the admin entirely (this
  // provider only unmounts when the whole dashboard does — page-to-page nav keeps
  // it, and the test, alive).
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
          // Howen logs alarm_forward; Cathexis logs event_forward. Both carry
          // serial, mapped_events and the mapping_trace.
          const isEvent = f.event === "alarm_forward" || f.event === "event_forward";
          if (!isEvent || String(f.serial) !== effectiveSerial) continue;
          fired.push({
            seq: e.seq,
            time: e.time,
            ec: f.ec != null && f.ec !== "" ? Number(f.ec) : null,
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

  const value: MappingTestState = {
    deviceList,
    serial: effectiveSerial,
    setSerial,
    selected,
    running,
    events,
    note,
    error,
    setError,
    waking,
    canWake,
    sleeping,
    start,
    stop,
    wake,
    clear,
  };

  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export function useMappingTest(): MappingTestState {
  const ctx = useContext(Ctx);
  if (!ctx) throw new Error("useMappingTest must be used within MappingTestProvider");
  return ctx;
}
