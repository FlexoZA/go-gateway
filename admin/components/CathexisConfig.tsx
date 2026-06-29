"use client";

import { useCallback, useEffect, useState } from "react";
import { useConfirm } from "@/components/confirm";
import { api } from "@/lib/api";
import { Spinner } from "@/components/ui";
import * as CC from "@/lib/cathexisConfig";

// CathexisConfig is the device parameter-config editor for Cathexis MVR units. It
// reads the whole config in one shot (the device ignores a module filter) and edits
// it per segment. The shapes differ per segment, so each has its own editor and
// write rule:
//   network  — scalar fields; partial write (only changed fields).
//   general  — scalar fields; partial write but ALWAYS includes the mandatory account.
//   cameras  — array[2] of camera{profiles[2]}; the device requires BOTH cameras and
//              BOTH profiles (with index fields) on any write, so the whole array is sent.
//   events   — array of {event:[[key,value]]}; only the changed event objects are sent.
// Every successful write reboots the unit (it applies config on boot), so saves
// confirm first and do NOT auto-reload.

type Sc = Record<string, any>;

const clone = <T,>(v: T): T => JSON.parse(JSON.stringify(v));

export function CathexisConfig({ serial }: { serial: string; unit?: string; sleeping?: boolean }) {
  const [cat, setCat] = useState("network");
  const [sc, setSc] = useState<Sc>({});
  const [draft, setDraft] = useState<Sc>({});
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [waking, setWaking] = useState(false);
  const [sleeping, setSleeping] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const confirm = useConfirm();

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    setNotice(null);
    try {
      const res = await api<{ sc: Sc }>(`units/${encodeURIComponent(serial)}/config`);
      setSc(res.sc || {});
      setDraft(clone(res.sc || {}));
      setSleeping(false);
    } catch (e: any) {
      const msg = e.message || "Failed to read config";
      setSleeping(/standby/i.test(msg));
      setError(msg);
      setSc({});
      setDraft({});
    } finally {
      setLoading(false);
    }
  }, [serial]);

  useEffect(() => {
    load();
  }, [load]);

  const dirtySeg = (seg: string) => JSON.stringify(draft[seg]) !== JSON.stringify(sc[seg]);

  async function wake() {
    setWaking(true);
    setError(null);
    try {
      await api(`units/${encodeURIComponent(serial)}/commands`, { method: "POST", body: JSON.stringify({ type: "wake_device" }) });
      setNotice("Wake sent — give the unit a few seconds, then Reload.");
    } catch (e: any) {
      setError(e.message || "Failed to wake device");
    } finally {
      setWaking(false);
    }
  }

  // buildPayload assembles the {sc:{...}} body for one segment per its write rule.
  function buildPayload(seg: string): Sc | null {
    if (seg === "network") {
      const changed: Sc = {};
      for (const [k, v] of Object.entries(draft.network || {})) {
        if (CC.NETWORK_FIELDS[k]?.hidden || CC.NETWORK_FIELDS[k]?.readonly) continue;
        if (JSON.stringify(v) !== JSON.stringify(sc.network?.[k])) changed[k] = v;
      }
      return Object.keys(changed).length ? { network: changed } : null;
    }
    if (seg === "general") {
      const changed: Sc = {};
      for (const [k, v] of Object.entries(draft.general || {})) {
        if (CC.GENERAL_FIELDS[k]?.hidden || CC.GENERAL_FIELDS[k]?.readonly) continue;
        if (JSON.stringify(v) !== JSON.stringify(sc.general?.[k])) changed[k] = v;
      }
      if (!Object.keys(changed).length) return null;
      for (const req of CC.GENERAL_REQUIRED) {
        if (!(req in changed) && draft.general?.[req] !== undefined) changed[req] = draft.general[req];
      }
      return { general: changed };
    }
    if (seg === "cameras") {
      return { cameras: draft.cameras }; // whole array (device requires both cameras+profiles)
    }
    if (seg === "events") {
      const orig = sc.events || [];
      const changed = (draft.events || []).filter((ev: any, i: number) => JSON.stringify(ev) !== JSON.stringify(orig[i]));
      return changed.length ? { events: changed } : null;
    }
    return null;
  }

  async function save(seg: string, label: string) {
    if (!dirtySeg(seg)) return;
    const body = buildPayload(seg);
    if (!body) return;
    const warn = seg === "network" ? " Changing the server address or port can disconnect the unit from the gateway." : "";
    if (
      !(await confirm({
        title: `Save ${label}`,
        body: `Apply these ${label} changes to the device?${warn}`,
        confirmLabel: "Save",
      }))
    )
      return;
    setSaving(true);
    setError(null);
    setNotice(null);
    try {
      await api(`units/${encodeURIComponent(serial)}/config`, { method: "PUT", body: JSON.stringify({ sc: body }) });
      // Treat the draft as the new baseline (clears the dirty marker). Some changes
      // apply immediately; a few only take effect after the unit restarts (use the
      // Reboot button on the Status tab if a setting doesn't seem to take). Reload
      // to re-read the device's current values.
      setSc((p) => ({ ...p, ...clone(draft) }));
      setNotice(`${label} saved.`);
    } catch (e: any) {
      setError(e.message || "Save failed");
    } finally {
      setSaving(false);
    }
  }

  const setSeg = (seg: string, updater: (d: Sc) => void) =>
    setDraft((prev) => {
      const next = clone(prev);
      if (next[seg] === undefined) next[seg] = seg === "cameras" || seg === "events" ? [] : {};
      updater(next);
      return next;
    });

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-1">
        {CC.CATEGORIES.map((c) => (
          <button
            key={c.key}
            onClick={() => setCat(c.key)}
            className={`rounded-full px-3 py-1 text-sm ${cat === c.key ? "bg-indigo-600 text-white" : "bg-edge text-slate-300 hover:bg-edge/70"}`}
          >
            {c.label}
            {dirtySeg(c.key) && <span className="ml-1 text-amber-300">•</span>}
          </button>
        ))}
        <div className="grow" />
        <button className="btn-ghost" onClick={load} disabled={saving || loading}>
          Reload
        </button>
      </div>

      {error && (
        <div className="flex flex-wrap items-center gap-3 rounded-md border border-rose-500/40 bg-rose-500/10 px-3 py-2 text-sm text-rose-200">
          <span>{error}</span>
          {sleeping && (
            <button className="btn-primary" onClick={wake} disabled={waking}>
              {waking ? "Waking…" : "Wake device"}
            </button>
          )}
        </div>
      )}
      {notice && <div className="rounded-md border border-emerald-500/40 bg-emerald-500/10 px-3 py-2 text-sm text-emerald-200">{notice}</div>}

      {loading ? (
        <div className="flex items-center gap-3 text-sm text-slate-400">
          <Spinner /> Reading config from the device…
        </div>
      ) : (
        <>
          {cat === "network" && (
            <ScalarSegment
              title="Network"
              note="Changing the dAPI server address/port can disconnect the unit from the gateway."
              fields={CC.NETWORK_FIELDS}
              obj={draft.network || {}}
              dirty={dirtySeg("network")}
              saving={saving}
              onChange={(k, v) => setSeg("network", (d) => (d.network[k] = v))}
              onSave={() => save("network", "Network")}
            />
          )}
          {cat === "general" && (
            <ScalarSegment
              title="General"
              fields={CC.GENERAL_FIELDS}
              obj={draft.general || {}}
              dirty={dirtySeg("general")}
              saving={saving}
              onChange={(k, v) => setSeg("general", (d) => (d.general[k] = v))}
              onSave={() => save("general", "General")}
            />
          )}
          {cat === "cameras" && (
            <CamerasSegment
              cameras={draft.cameras || []}
              dirty={dirtySeg("cameras")}
              saving={saving}
              onChange={(ci, pi, k, v) => setSeg("cameras", (d) => (d.cameras[ci].profiles[pi][k] = v))}
              onCamChange={(ci, k, v) => setSeg("cameras", (d) => (d.cameras[ci][k] = v))}
              onSave={() => save("cameras", "Cameras")}
            />
          )}
          {cat === "events" && (
            <EventsSegment
              events={draft.events || []}
              dirty={dirtySeg("events")}
              saving={saving}
              onChange={(ei, key, v) =>
                setSeg("events", (d) => {
                  const pairs = d.events[ei].event as [string, string][];
                  const p = pairs.find((x) => x[0] === key);
                  if (p) p[1] = v;
                })
              }
              onSave={() => save("events", "Events")}
            />
          )}
          {cat === "description" && <DescriptionSegment obj={sc.description || {}} />}
        </>
      )}
    </div>
  );
}

// ---- typed field control ----

function Field({
  name,
  value,
  meta,
  stringMode,
  onChange,
}: {
  name: string;
  value: any;
  meta?: CC.CathexisFieldMeta;
  stringMode?: boolean; // events use string "0"/"1" values
  onChange: (v: any) => void;
}) {
  if (meta?.hidden) return null;
  const label = meta?.label ?? CC.humanize(name);
  const type = meta?.type ?? CC.inferType(value);

  if (meta?.readonly) {
    return (
      <Row label={label}>
        <span className="font-mono text-slate-300">{value === "" || value == null ? "—" : String(value)}</span>
      </Row>
    );
  }
  if (type === "toggle") {
    const on = stringMode ? value === "1" : !!value;
    return (
      <Row label={label}>
        <button
          type="button"
          onClick={() => onChange(stringMode ? (on ? "0" : "1") : !on)}
          className={`relative h-5 w-9 rounded-full transition ${on ? "bg-emerald-500" : "bg-edge"}`}
        >
          <span className={`absolute top-0.5 h-4 w-4 rounded-full bg-white transition ${on ? "left-4" : "left-0.5"}`} />
        </button>
      </Row>
    );
  }
  if (type === "select" && meta?.options) {
    const cur = String(value ?? "");
    return (
      <Row label={label}>
        <select className="input w-44" value={cur} onChange={(e) => onChange(e.target.value)}>
          {meta.options.map((o) => (
            <option key={o.value} value={o.value}>
              {o.label}
            </option>
          ))}
          {!meta.options.some((o) => o.value === cur) && <option value={cur}>{cur || "—"}</option>}
        </select>
      </Row>
    );
  }
  const numeric = type === "number";
  return (
    <Row label={label}>
      <input
        className="input w-44 font-mono"
        inputMode={numeric ? "numeric" : undefined}
        value={value == null ? "" : String(value)}
        onChange={(e) => {
          const raw = e.target.value;
          if (numeric && !stringMode) onChange(raw === "" ? "" : Number(raw));
          else onChange(raw);
        }}
      />
    </Row>
  );
}

// ScalarSegment renders an object segment (network/general) as curated scalar
// fields, in metadata order then the rest, skipping hidden/nested entries.
function ScalarSegment({
  title,
  note,
  fields,
  obj,
  dirty,
  saving,
  onChange,
  onSave,
}: {
  title: string;
  note?: string;
  fields: Record<string, CC.CathexisFieldMeta>;
  obj: Record<string, any>;
  dirty: boolean;
  saving: boolean;
  onChange: (k: string, v: any) => void;
  onSave: () => void;
}) {
  const curated = Object.keys(fields).filter((k) => k in obj && !fields[k]?.hidden);
  const rest = Object.keys(obj)
    .filter((k) => !curated.includes(k) && !fields[k]?.hidden && (typeof obj[k] !== "object" || obj[k] == null))
    .sort();
  const keys = [...curated, ...rest];
  return (
    <div className="card">
      <div className="mb-2 flex items-center justify-between">
        <h3 className="text-sm font-semibold text-slate-200">{title}</h3>
        <SaveButton dirty={dirty} saving={saving} onSave={onSave} />
      </div>
      {note && <p className="mb-2 text-xs text-amber-300/80">⚠ {note}</p>}
      <div className="grid grid-cols-1 gap-x-6 md:grid-cols-2">
        {keys.map((k) => (
          <Field key={k} name={k} value={obj[k]} meta={fields[k]} onChange={(v) => onChange(k, v)} />
        ))}
      </div>
    </div>
  );
}

function CamerasSegment({
  cameras,
  dirty,
  saving,
  onChange,
  onCamChange,
  onSave,
}: {
  cameras: any[];
  dirty: boolean;
  saving: boolean;
  onChange: (ci: number, pi: number, k: string, v: any) => void;
  onCamChange: (ci: number, k: string, v: any) => void;
  onSave: () => void;
}) {
  const camName = (i: number) => (i === 0 ? "Road camera" : i === 1 ? "Cab camera" : `Camera ${i + 1}`);
  const profName = (i: number) => (i === 0 ? "High res" : "Low res");
  return (
    <div className="space-y-4">
      <div className="flex items-center justify-end">
        <SaveButton dirty={dirty} saving={saving} onSave={onSave} />
      </div>
      <p className="text-xs text-slate-500">Both cameras and both profiles are sent on save (device requirement).</p>
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        {cameras.map((cam, ci) => (
          <div key={ci} className="card">
            <div className="mb-2 flex items-center justify-between">
              <h3 className="text-sm font-semibold text-slate-200">{camName(ci)}</h3>
              <Field name="enabled" value={cam.enabled} meta={{ label: "Enabled", type: "toggle" }} onChange={(v) => onCamChange(ci, "enabled", v)} />
            </div>
            <div className="space-y-2">
              {(cam.profiles || []).map((prof: any, pi: number) => (
                <div key={pi} className="rounded-md border border-edge p-2">
                  <div className="mb-1 text-xs font-semibold text-slate-300">{profName(pi)}</div>
                  {Object.keys(CC.CAMERA_PROFILE_FIELDS)
                    .filter((k) => k in prof)
                    .map((k) => (
                      <Field key={k} name={k} value={prof[k]} meta={CC.CAMERA_PROFILE_FIELDS[k]} onChange={(v) => onChange(ci, pi, k, v)} />
                    ))}
                </div>
              ))}
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}

function EventsSegment({
  events,
  dirty,
  saving,
  onChange,
  onSave,
}: {
  events: any[];
  dirty: boolean;
  saving: boolean;
  onChange: (ei: number, key: string, v: string) => void;
  onSave: () => void;
}) {
  const nameOf = (ev: any): string => {
    const p = (ev.event as [string, string][]).find((x) => x[0] === "name");
    return p ? p[1] : "event";
  };
  return (
    <div className="space-y-4">
      <div className="flex items-center justify-end">
        <SaveButton dirty={dirty} saving={saving} onSave={onSave} />
      </div>
      <p className="text-xs text-slate-500">Driver-behaviour thresholds are g-force (harsh) or m/s (speeding). Only changed events are sent.</p>
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        {events.map((ev, ei) => {
          const pairs = ev.event as [string, string][];
          return (
            <div key={ei} className="card">
              <h3 className="mb-2 text-sm font-semibold text-slate-200">{CC.eventLabel(nameOf(ev))}</h3>
              <div className="grid grid-cols-1 gap-x-6 md:grid-cols-2">
                {pairs.map(([k, v]) => (
                  <Field key={k} name={k} value={v} meta={CC.EVENT_FIELDS[k]} stringMode onChange={(nv) => onChange(ei, k, nv)} />
                ))}
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
}

function DescriptionSegment({ obj }: { obj: Record<string, any> }) {
  const order = ["cathexis_serial", "firmware_version", "orgname", "sitename", "dealer_name", "registration", "description"];
  const keys = [...order.filter((k) => k in obj), ...Object.keys(obj).filter((k) => !order.includes(k))];
  return (
    <div className="card">
      <div className="mb-2 flex items-center justify-between">
        <h3 className="text-sm font-semibold text-slate-200">Device info</h3>
        <span className="text-xs text-slate-500">read-only</span>
      </div>
      {keys.map((k) => (
        <Row key={k} label={CC.humanize(k)}>
          <span className="font-mono text-slate-300">{obj[k] === "" || obj[k] == null ? "—" : String(obj[k])}</span>
        </Row>
      ))}
    </div>
  );
}

function SaveButton({ dirty, saving, onSave }: { dirty: boolean; saving: boolean; onSave: () => void }) {
  return (
    <button className="btn-primary" disabled={!dirty || saving} onClick={onSave}>
      {saving ? "Saving…" : dirty ? "Save" : "Saved"}
    </button>
  );
}

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label className="flex items-center justify-between gap-3 py-0.5 text-sm">
      <span className="shrink-0 text-slate-400">{label}</span>
      {children}
    </label>
  );
}
