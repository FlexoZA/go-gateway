"use client";

import { useCallback, useEffect, useState } from "react";
import { useConfirm } from "@/components/confirm";
import { api } from "@/lib/api";
import { Spinner } from "@/components/ui";
import { ConfigField } from "@/components/ConfigField";
import { deviceConfigSchema } from "@/lib/deviceConfig";
import type { SegmentMeta } from "@/lib/howenConfig";

// DeviceConfig is the full device-configuration editor. It reads/writes the
// unit's parameter segments over the gateway, grouped into the device's own menu
// categories. Common segments are friendly (curated metadata); the rest render
// generically. Saving sends ONLY the changed leaves (firmware writes back garbage
// in untouched string fields, so a full read-modify-write would corrupt config).

type Sc = Record<string, any>;

function deepGet(o: any, path: string[]): any {
  return path.reduce((a, k) => (a == null ? undefined : a[k]), o);
}
function deepSet(o: any, path: string[], val: any): any {
  const [h, ...t] = path;
  if (t.length === 0) return { ...o, [h]: val };
  return { ...o, [h]: deepSet(o?.[h] ?? {}, t, val) };
}
// Order a segment's fields: curated ones first (in metadata order), then the rest.
function orderedEntries(segments: Record<string, SegmentMeta>, seg: string, obj: Sc): [string, any][] {
  const curated = Object.keys(segments[seg]?.fields || {});
  const keys = Object.keys(obj);
  const head = curated.filter((k) => k in obj);
  const tail = keys.filter((k) => !head.includes(k)).sort();
  return [...head, ...tail].map((k) => [k, obj[k]]);
}

export function DeviceConfig({ serial, unit }: { serial: string; unit: string }) {
  const schema = deviceConfigSchema(unit);
  const CATEGORIES = schema?.CATEGORIES ?? [];
  const SEGMENTS = schema?.SEGMENTS ?? {};
  const segmentMeta = schema?.segmentMeta ?? ((seg: string): SegmentMeta => ({ label: seg, fields: {} }));
  const [cat, setCat] = useState(CATEGORIES[0]?.key ?? "");
  const [sc, setSc] = useState<Sc>({});
  const [dirty, setDirty] = useState<Sc>({});
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState<string | null>(null);
  const [waking, setWaking] = useState(false);
  const [sleeping, setSleeping] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const confirm = useConfirm();

  const category = CATEGORIES.find((c) => c.key === cat)!;

  const load = useCallback(async (catKey: string) => {
    const c = CATEGORIES.find((x) => x.key === catKey)!;
    setLoading(true);
    setError(null);
    setNotice(null);
    setDirty({});
    try {
      const res = await api<{ sc: Sc }>(`units/${encodeURIComponent(serial)}/config?modules=${c.segments.join(",")}`);
      setSc(res.sc || {});
      setSleeping(false);
    } catch (e: any) {
      const msg = e.message || "Failed to read config";
      setSleeping(/standby/i.test(msg));
      setError(msg);
      setSc({});
    } finally {
      setLoading(false);
    }
  }, [serial]);

  useEffect(() => {
    load(cat);
  }, [cat, load]);

  if (!schema) {
    return (
      <div className="rounded-md border border-edge bg-panel px-4 py-3 text-sm text-slate-400">
        No editable device configuration for this unit type.
      </div>
    );
  }

  const get = (path: string[]) => deepGet(dirty, path);
  const set = (path: string[], val: string) => setDirty((d) => deepSet(d, path, val));
  const segDirty = (seg: string) => dirty[seg] && Object.keys(dirty[seg]).length > 0;

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

  async function save(seg: string) {
    if (!segDirty(seg)) return;
    const meta = segmentMeta(seg);
    if (
      meta.danger &&
      !(await confirm({
        title: meta.label,
        body: `${meta.note || "This can disrupt the unit."} Apply changes?`,
        confirmLabel: "Apply",
      }))
    )
      return;
    setSaving(seg);
    setError(null);
    setNotice(null);
    try {
      const res = await api<{ sc: Sc }>(`units/${encodeURIComponent(serial)}/config`, {
        method: "PUT",
        body: JSON.stringify({ sc: { [seg]: dirty[seg] } }),
      });
      setSc((p) => ({ ...p, ...(res.sc || {}) }));
      setDirty((p) => {
        const n = { ...p };
        delete n[seg];
        return n;
      });
      setNotice(`${meta.label} saved.`);
    } catch (e: any) {
      setError(e.message || "Save failed");
    } finally {
      setSaving(null);
    }
  }

  return (
    <div className="space-y-4">
      {/* category sub-tabs */}
      <div className="flex flex-wrap gap-1">
        {CATEGORIES.map((c) => (
          <button
            key={c.key}
            onClick={() => setCat(c.key)}
            className={`rounded-full px-3 py-1 text-sm ${cat === c.key ? "bg-indigo-600 text-white" : "bg-edge text-slate-300 hover:bg-edge/70"}`}
          >
            {c.label}
          </button>
        ))}
        <div className="grow" />
        <button className="btn-ghost" onClick={() => load(cat)} disabled={!!saving || loading}>Reload</button>
      </div>

      {error && (
        <div className="flex flex-wrap items-center gap-3 rounded-md border border-rose-500/40 bg-rose-500/10 px-3 py-2 text-sm text-rose-200">
          <span className="grow">{error}</span>
          {sleeping && <button className="btn-primary" onClick={wake} disabled={waking}>{waking ? "Waking…" : "Wake device"}</button>}
          <button onClick={() => setError(null)} aria-label="Dismiss" className="shrink-0 text-rose-300/80 hover:text-rose-100">✕</button>
        </div>
      )}
      {notice && <div className="rounded-md border border-emerald-500/40 bg-emerald-500/10 px-3 py-2 text-sm text-emerald-200">{notice}</div>}

      {loading ? (
        <div className="flex items-center gap-3 text-sm text-slate-400"><Spinner /> Reading {category.label} settings from the device…</div>
      ) : (
        <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
          {category.segments.filter((seg) => sc[seg]).map((seg) => {
            const meta = segmentMeta(seg);
            return (
              <div key={seg} className="card flex flex-col">
                <div className="mb-2 flex items-center justify-between">
                  <h3 className="text-sm font-semibold text-slate-200">{meta.label}</h3>
                  {meta.readonly ? (
                    <span className="text-xs text-slate-500">read-only</span>
                  ) : (
                    <button className={meta.danger ? "btn-danger" : "btn-primary"} disabled={!segDirty(seg) || saving === seg} onClick={() => save(seg)}>
                      {saving === seg ? "Saving…" : segDirty(seg) ? "Save" : "Saved"}
                    </button>
                  )}
                </div>
                {meta.note && <p className="mb-2 text-xs text-amber-300/80">⚠ {meta.note}</p>}
                <div className="grow space-y-1">
                  {orderedEntries(SEGMENTS, seg, sc[seg]).map(([k, v]) => (
                    <ConfigField key={k} name={k} value={v} path={[seg, k]} segFields={meta.fields} readonly={meta.readonly} get={get} set={set} />
                  ))}
                </div>
              </div>
            );
          })}
          {category.segments.every((seg) => !sc[seg]) && !error && (
            <div className="text-sm text-slate-400">This device reports no {category.label} settings.</div>
          )}
        </div>
      )}
    </div>
  );
}
