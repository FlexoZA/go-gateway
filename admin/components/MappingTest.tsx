"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import type { ReactNode } from "react";
import { Badge, Empty, ErrorBanner } from "@/components/ui";
import { MAPPING_TEST_PATH, useMappingTest, type Fired, type TraceEntry } from "@/contexts/MappingTest";

// MappingControls is the device picker + start/stop/clear row (with the standby
// wake affordance). Used on the full Mapping Test page; the drawer shows a
// compact header instead since the device is frozen while running.
export function MappingControls() {
  const { deviceList, serial, setSerial, running, sleeping, canWake, waking, wake, start, stop, clear, events } =
    useMappingTest();
  return (
    <div className="mb-4 flex flex-wrap items-end gap-3">
      <div>
        <label className="mb-1 block text-xs text-slate-400">Device</label>
        <select className="input w-72" value={serial} disabled={running} onChange={(e) => setSerial(e.target.value)}>
          {deviceList.length === 0 && <option value="">No connected devices</option>}
          {deviceList.map((u) => (
            <option key={u.serial} value={u.serial}>
              {u.serial} · {u.protocol}
              {u.model ? ` (${u.model})` : ""}
            </option>
          ))}
        </select>
      </div>
      {sleeping && (
        <div className="flex items-center gap-2 pb-1">
          <span className="text-xs text-amber-300">⚠ Device is in standby — wake it to receive events.</span>
          <button
            className="btn-primary"
            onClick={wake}
            disabled={!canWake || waking}
            title={canWake ? undefined : "This device can't be woken remotely"}
          >
            {waking ? "Waking…" : "Wake device"}
          </button>
        </div>
      )}
      <div className="grow" />
      {running ? (
        <button className="btn-danger" onClick={stop}>
          Stop test
        </button>
      ) : (
        <button className="btn-primary" disabled={!serial} onClick={start}>
          Start test
        </button>
      )}
      <button className="btn-ghost" onClick={clear} disabled={events.length === 0}>
        Clear
      </button>
    </div>
  );
}

// MappingEvents renders the live feed: an error banner, the "listening" banner,
// then the fired-event cards (or an empty prompt). `compact` trims the wide-only
// hint for the drawer.
export function MappingEvents({ compact = false }: { compact?: boolean }) {
  const { running, events, note, error, setError } = useMappingTest();
  return (
    <div>
      <ErrorBanner message={error} onDismiss={error ? () => setError(null) : undefined} />
      {running && (
        <div className="mb-4 rounded-md border border-indigo-500/40 bg-indigo-500/10 px-3 py-2 text-sm text-indigo-200">
          <span className="inline-flex items-center gap-2">
            <span className="h-2 w-2 animate-pulse rounded-full bg-indigo-400" />
            {note || `Listening…`}
          </span>
          {!compact && (
            <span className="ml-2 text-xs text-indigo-300/70">Capture level forced to debug while testing; restored on stop.</span>
          )}
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

// DashMain wraps the routed page content. It shifts aside (on wide screens) to
// make room for the mapping-test drawer, and renders the drawer itself.
export function DashMain({ children }: { children: ReactNode }) {
  const { running } = useMappingTest();
  const pathname = usePathname();
  const drawerOpen = running && pathname !== MAPPING_TEST_PATH;
  return (
    <>
      <main className={`flex-1 overflow-auto p-6 md:p-8 ${drawerOpen ? "lg:mr-[28rem]" : ""}`}>{children}</main>
      <MappingTestDrawer open={drawerOpen} />
    </>
  );
}

// MappingTestDrawer is the persistent right-side panel showing the live feed off
// the Mapping Test page. It has no backdrop so the page underneath stays usable
// (the whole point — edit a mapping while watching events). Closing it stops the
// test, which restores the gateway's capture level.
function MappingTestDrawer({ open }: { open: boolean }) {
  const { serial, stop } = useMappingTest();
  if (!open) return null;
  return (
    <aside className="fixed right-0 top-0 z-40 flex h-screen w-full max-w-md flex-col border-l border-edge bg-panel shadow-2xl">
      <header className="flex items-center justify-between gap-2 border-b border-edge px-4 py-3">
        <div className="flex min-w-0 items-center gap-2">
          <span className="h-2 w-2 shrink-0 animate-pulse rounded-full bg-indigo-400" />
          <h2 className="text-sm font-semibold text-white">Mapping Test</h2>
          <Badge tone="slate">
            <span className="font-mono">{serial}</span>
          </Badge>
        </div>
        <div className="flex shrink-0 items-center gap-2">
          <Link href={MAPPING_TEST_PATH} className="btn-ghost">
            Full view
          </Link>
          <button className="btn-ghost" onClick={stop} title="Close and stop the test" aria-label="Close and stop the test">
            ✕
          </button>
        </div>
      </header>
      <div className="min-h-0 flex-1 overflow-auto p-3">
        <MappingEvents compact />
      </div>
    </aside>
  );
}

function FiredCard({ ev }: { ev: Fired }) {
  return (
    <div className="card">
      <div className="mb-2 flex items-center gap-3 text-xs text-slate-400">
        <span className="font-mono text-slate-300">{ev.time.slice(11, 23)}</span>
        {ev.ec != null && <span className="font-mono">EC {ev.ec}</span>}
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
      {t.name ? (
        // Cathexis: the device's own event name is the natural identity.
        <span className="font-mono text-slate-400">
          <span className="text-indigo-300">{t.name}</span> <span className="text-slate-500">(code {t.code})</span>
        </span>
      ) : t.map_type ? (
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
