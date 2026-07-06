"use client";

import { useCallback, useEffect, useState } from "react";
import { useConfirm } from "@/components/confirm";
import { api } from "@/lib/api";
import { Spinner } from "@/components/ui";
import * as N from "@/lib/n62Config";

// N62Config is the device parameter-config editor for JT808 / N62 dashcam units.
// The gateway tunnels the device's own ULV Get/Set JSON over the CMS link, so a
// segment read is the device's live truth and a save is applied on the device.
//
// Segments are read on demand per category (each round-trip is an over-the-air ULV
// exchange). Three segment shapes, each with its own write rule:
//   scalar  — object of fields; N62 merges partial top-level Sets, so send only
//             changed fields (GenDateTime Zone/NtpSync are compound and rebuilt).
//   nested  — homogeneous indexed subs (NetCms.Server_xx, RecStream/RecCamAttr
//             .Chn_xx); firmware does NOT merge partial subs, so the whole segment
//             is sent (every sub, curated fields), mirroring the device UI.
//   list    — alarm / ADAS / DMS: top scalars + named/indexed alarm subs, each with
//             editable fields plus a linkage string (LnkParam) PRESERVED VERBATIM;
//             ADAS/DMS pack tuning knobs into a CSV Param we split/rejoin. Sent whole.

type Obj = Record<string, any>;
type Sc = Record<string, Obj>;

const clone = <T,>(v: T): T => JSON.parse(JSON.stringify(v));
const eq = (a: any, b: any) => JSON.stringify(a) === JSON.stringify(b);

// coerce turns an editor value into the JSON type the device expects: numeric for
// select/number/toggle controls (N62 enums are integers), string otherwise.
function coerce(meta: N.N62FieldMeta | undefined, v: any): any {
  const t = meta?.type;
  if (t === "text" || t === "password") return v == null ? "" : String(v);
  if (t === "number" || t === "select" || t === "toggle") {
    if (v === "" || v == null) return v;
    const n = Number(v);
    // Never let a non-finite value serialize to JSON null and hit the firmware;
    // clamp numbers to the field's declared range so out-of-range edits can't
    // either (min/max were previously dead metadata).
    if (!Number.isFinite(n)) return "";
    let c = n;
    if (typeof meta?.min === "number" && c < meta.min) c = meta.min;
    if (typeof meta?.max === "number" && c > meta.max) c = meta.max;
    return c;
  }
  return v;
}

// coerceLike matches the original device type: some alarm sub-objects use a boolean
// "En" (AlmSpd/AlmGsn/AlmSys) while others use 0/1 (AlmDriving/AlmIoIn). We echo the
// original shape so a Set doesn't change the field's JSON type under the firmware.
function coerceLike(orig: any, meta: N.N62FieldMeta | undefined, v: any): any {
  if (typeof orig === "boolean") {
    if (typeof v === "boolean") return v;
    return !(v === 0 || v === "0" || v === "" || v === false || v == null);
  }
  return coerce(meta, v);
}

export function N62Config({ serial }: { serial: string; unit?: string }) {
  const [cat, setCat] = useState(N.CATEGORIES[0].key);
  const [sc, setSc] = useState<Sc>({}); // device baseline, keyed by segment
  const [draft, setDraft] = useState<Sc>({}); // edited copy
  const [loaded, setLoaded] = useState<Record<string, boolean>>({});
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState<string | null>(null); // segment being saved
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const confirm = useConfirm();

  // ingest normalises freshly-read segments (compound split + CSV split) into sc+draft.
  // The N62's config handler intermittently answers a Get with an empty/garbled body,
  // which the gateway surfaces as {raw_text} / {error}; skip those so a flaky read
  // leaves the previous values (or a "Reload" prompt) instead of a blank card.
  const ingest = useCallback((fresh: Sc) => {
    const norm: Sc = {};
    for (const [seg, obj] of Object.entries(fresh)) {
      if (!obj || "raw_text" in obj || "error" in obj) continue;
      norm[seg] = N.splitListCsv(seg, N.splitCompound(seg, obj));
    }
    if (!Object.keys(norm).length) return;
    setSc((p) => ({ ...p, ...clone(norm) }));
    setDraft((p) => ({ ...p, ...clone(norm) }));
  }, []);

  const loadCategory = useCallback(
    async (key: string) => {
      const c = N.CATEGORIES.find((x) => x.key === key);
      if (!c) return;
      setLoading(true);
      setError(null);
      setNotice(null);
      try {
        const res = await api<{ sc: Sc }>(`units/${encodeURIComponent(serial)}/config?modules=${c.segments.join(",")}`);
        ingest(res.sc || {});
        setLoaded((p) => ({ ...p, [key]: true }));
      } catch (e: any) {
        setError(e.message || "Failed to read config from the device");
      } finally {
        setLoading(false);
      }
    },
    [serial, ingest],
  );

  useEffect(() => {
    if (!loaded[cat]) loadCategory(cat);
  }, [cat, loaded, loadCategory]);

  const setField = (seg: string, field: string, v: any) =>
    setDraft((prev) => {
      const next = clone(prev);
      if (!next[seg]) next[seg] = {};
      next[seg][field] = v;
      return next;
    });

  const setNested = (seg: string, sub: string, field: string, v: any) =>
    setDraft((prev) => {
      const next = clone(prev);
      if (!next[seg]) next[seg] = {};
      if (!next[seg][sub]) next[seg][sub] = {};
      next[seg][sub][field] = v;
      return next;
    });

  const dirty = (seg: string) => !eq(draft[seg], sc[seg]);

  // ---- payload builders ----

  function scalarPayload(seg: string): Obj | null {
    const meta = N.segmentFields(seg);
    const d = draft[seg] || {};
    const base = sc[seg] || {};
    const out: Obj = {};
    const seen = new Set<string>();
    for (const [field, m] of Object.entries(meta)) {
      if (m.hidden || m.readonly) continue;
      if (eq(d[field], base[field])) continue;
      const parent = N.compoundParentOf(field);
      if (parent) {
        if (!seen.has(parent)) {
          out[parent] = N.joinCompound(parent, d);
          seen.add(parent);
        }
      } else {
        out[field] = coerce(m, d[field]);
      }
    }
    return Object.keys(out).length ? out : null;
  }

  function nestedPayload(seg: string): Obj | null {
    const spec = N.NESTED_SEGMENTS[seg];
    const d = draft[seg] || {};
    const out: Obj = {};
    for (const sub of Object.keys(d).filter((k) => k.startsWith(spec.subPrefix)).sort()) {
      const src = d[sub] || {};
      const o: Obj = {};
      for (const f of spec.write) if (f in src) o[f] = coerce(spec.fields[f], src[f]);
      out[sub] = o;
    }
    return Object.keys(out).length ? out : null;
  }

  // listPayload sends the whole segment: top scalars + every sub as {LnkParam
  // verbatim, editable fields coerced to the device's original type, CSV rebuilt}.
  function listPayload(seg: string): Obj | null {
    const spec = N.LIST_SEGMENTS[seg];
    const d = draft[seg] || {};
    const base = sc[seg] || {};
    const out: Obj = {};
    for (const [k, m] of Object.entries(spec.top || {})) {
      if (m.hidden || m.readonly) continue;
      if (k in d) out[k] = coerce(m, d[k]);
    }
    for (const s of N.listSubs(spec, d)) {
      const src = d[s.key] || {};
      const bsub = base[s.key] || {};
      const o: Obj = {};
      for (const pf of spec.passthrough) if (pf in src) o[pf] = src[pf];
      for (const [k, m] of Object.entries(spec.direct || {})) if (k in src) o[k] = coerceLike(bsub[k], m, src[k]);
      if (spec.csv) o[spec.csv.field] = N.joinListCsv(spec, src);
      out[s.key] = o;
    }
    return Object.keys(out).length ? out : null;
  }

  function buildPayload(seg: string): Obj | null {
    if (N.LIST_SEGMENTS[seg]) return listPayload(seg);
    if (N.NESTED_SEGMENTS[seg]) return nestedPayload(seg);
    return scalarPayload(seg);
  }

  const RISKY: Record<string, string> = {
    NetCms: "The CMS server is how this unit reaches the gateway — a wrong address or protocol will disconnect it.",
    NetWifi: "Changing Wi-Fi can drop the unit's network connection.",
    NetXg: "Changing the cellular APN can drop the unit's mobile connection.",
    NetWired: "Changing wired networking can drop the unit's connection.",
  };

  async function save(seg: string) {
    if (!dirty(seg)) return;
    const label = N.SEG_TITLE[seg] || seg;
    const body = buildPayload(seg);
    if (!body) return;
    const warn = RISKY[seg] ? ` ${RISKY[seg]}` : "";
    if (!(await confirm({ title: `Save ${label}`, body: `Apply these ${label} changes to the device?${warn}`, confirmLabel: "Save" }))) return;
    setSaving(seg);
    setError(null);
    setNotice(null);
    try {
      const res = await api<{ ok: boolean; sc: Sc }>(`units/${encodeURIComponent(serial)}/config`, {
        method: "PUT",
        body: JSON.stringify({ sc: { [seg]: body } }),
      });
      if (res.sc && res.sc[seg]) ingest({ [seg]: res.sc[seg] });
      else setSc((p) => ({ ...p, [seg]: clone(draft[seg]) }));
      setNotice(`${label} saved.`);
    } catch (e: any) {
      setError(e.message || "Save failed");
    } finally {
      setSaving(null);
    }
  }

  const catDirty = (key: string) => (N.CATEGORIES.find((c) => c.key === key)?.segments || []).some((s) => dirty(s));
  const segments = N.CATEGORIES.find((c) => c.key === cat)?.segments || [];

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-1">
        {N.CATEGORIES.map((c) => (
          <button
            key={c.key}
            onClick={() => setCat(c.key)}
            className={`rounded-full px-3 py-1 text-sm ${cat === c.key ? "bg-indigo-600 text-white" : "bg-edge text-slate-300 hover:bg-edge/70"}`}
          >
            {c.label}
            {catDirty(c.key) && <span className="ml-1 text-amber-300">•</span>}
          </button>
        ))}
        <div className="grow" />
        <button className="btn-ghost" onClick={() => loadCategory(cat)} disabled={!!saving || loading}>
          Reload
        </button>
      </div>

      {error && <div className="rounded-md border border-rose-500/40 bg-rose-500/10 px-3 py-2 text-sm text-rose-200">{error}</div>}
      {notice && <div className="rounded-md border border-emerald-500/40 bg-emerald-500/10 px-3 py-2 text-sm text-emerald-200">{notice}</div>}

      {loading ? (
        <div className="flex items-center gap-3 text-sm text-slate-400">
          <Spinner /> Reading config from the device…
        </div>
      ) : (
        segments.map((seg) => (
          <SegmentView
            key={seg}
            seg={seg}
            obj={draft[seg]}
            base={sc[seg]}
            dirty={dirty(seg)}
            saving={saving === seg}
            onField={setField}
            onNested={setNested}
            onSave={() => save(seg)}
          />
        ))
      )}
    </div>
  );
}

// SegmentView picks the right card for a segment based on its shape registry.
function SegmentView({
  seg,
  obj,
  base,
  dirty,
  saving,
  onField,
  onNested,
  onSave,
}: {
  seg: string;
  obj?: Obj;
  base?: Obj;
  dirty: boolean;
  saving: boolean;
  onField: (seg: string, field: string, v: any) => void;
  onNested: (seg: string, sub: string, field: string, v: any) => void;
  onSave: () => void;
}) {
  const title = N.SEG_TITLE[seg] || seg;
  if (!obj) return <MissingCard title={title} />;

  const list = N.LIST_SEGMENTS[seg];
  if (list) {
    return <ListCard title={title} spec={list} obj={obj} dirty={dirty} saving={saving} onField={onField} onNested={onNested} onSave={onSave} seg={seg} />;
  }
  const nested = N.NESTED_SEGMENTS[seg];
  if (nested) {
    const subs = Object.keys(obj).filter((k) => k.startsWith(nested.subPrefix)).sort();
    return (
      <NestedCard
        title={title}
        note={nested.note}
        subs={subs}
        subLabel={nested.subLabel}
        fields={nested.fields}
        obj={obj}
        dirty={dirty}
        saving={saving}
        onChange={(sub, f, v) => onNested(seg, sub, f, v)}
        onSave={onSave}
      />
    );
  }
  return (
    <ScalarCard
      title={title}
      fields={N.segmentFields(seg)}
      obj={obj}
      dirty={dirty}
      saving={saving}
      onChange={(f, v) => onField(seg, f, v)}
      onSave={onSave}
    />
  );
}

// ---- cards ----

function ScalarCard({
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
  fields: Record<string, N.N62FieldMeta>;
  obj: Obj;
  dirty: boolean;
  saving: boolean;
  onChange: (field: string, v: any) => void;
  onSave: () => void;
}) {
  const keys = Object.keys(fields).filter((k) => k in obj && !fields[k].hidden);
  return (
    <div className="card mb-4">
      <CardHead title={title} dirty={dirty} saving={saving} onSave={onSave} />
      {note && <p className="mb-2 text-xs text-amber-300/80">⚠ {note}</p>}
      <div className="grid grid-cols-1 gap-x-6 md:grid-cols-2">
        {keys.map((k) => (
          <Field key={k} name={k} value={obj[k]} meta={fields[k]} onChange={(v) => onChange(k, v)} />
        ))}
      </div>
    </div>
  );
}

function NestedCard({
  title,
  note,
  subs,
  subLabel,
  fields,
  obj,
  dirty,
  saving,
  onChange,
  onSave,
}: {
  title: string;
  note?: string;
  subs: string[];
  subLabel: (i: number) => string;
  fields: Record<string, N.N62FieldMeta>;
  obj: Obj;
  dirty: boolean;
  saving: boolean;
  onChange: (sub: string, field: string, v: any) => void;
  onSave: () => void;
}) {
  return (
    <div className="card mb-4">
      <CardHead title={title} dirty={dirty} saving={saving} onSave={onSave} />
      {note && <p className="mb-2 text-xs text-amber-300/80">⚠ {note}</p>}
      <div className="grid grid-cols-1 gap-3 lg:grid-cols-2">
        {subs.map((sub, i) => (
          <div key={sub} className="rounded-md border border-edge p-2">
            <div className="mb-1 text-xs font-semibold text-slate-300">{subLabel(i)}</div>
            {Object.keys(fields)
              .filter((k) => k in (obj[sub] || {}) && !fields[k].hidden)
              .map((k) => (
                <Field key={k} name={k} value={obj[sub][k]} meta={fields[k]} onChange={(v) => onChange(sub, k, v)} />
              ))}
          </div>
        ))}
      </div>
    </div>
  );
}

// ListCard renders alarm/ADAS/DMS: top scalars then a sub-card per alarm type with
// its editable fields (direct + CSV columns). LnkParam is preserved, not shown.
function ListCard({
  title,
  spec,
  seg,
  obj,
  dirty,
  saving,
  onField,
  onNested,
  onSave,
}: {
  title: string;
  spec: N.ListSpec;
  seg: string;
  obj: Obj;
  dirty: boolean;
  saving: boolean;
  onField: (seg: string, field: string, v: any) => void;
  onNested: (seg: string, sub: string, field: string, v: any) => void;
  onSave: () => void;
}) {
  const topKeys = Object.keys(spec.top || {}).filter((k) => k in obj);
  const subs = N.listSubs(spec, obj);
  const cols = spec.csv?.cols || [];
  const directKeys = Object.keys(spec.direct || {});
  return (
    <div className="card mb-4">
      <CardHead title={title} dirty={dirty} saving={saving} onSave={onSave} />
      {spec.note && <p className="mb-2 text-xs text-amber-300/80">⚠ {spec.note}</p>}
      {topKeys.length > 0 && (
        <div className="mb-3 grid grid-cols-1 gap-x-6 border-b border-edge pb-2 md:grid-cols-2">
          {topKeys.map((k) => (
            <Field key={k} name={k} value={obj[k]} meta={spec.top![k]} onChange={(v) => onField(seg, k, v)} />
          ))}
        </div>
      )}
      <div className="grid grid-cols-1 gap-3 lg:grid-cols-2">
        {subs.map((s) => {
          const sub = obj[s.key] || {};
          return (
            <div key={s.key} className="rounded-md border border-edge p-2">
              <div className="mb-1 text-xs font-semibold text-slate-300">{s.label}</div>
              {directKeys
                .filter((k) => k in sub)
                .map((k) => (
                  <Field key={k} name={k} value={sub[k]} meta={spec.direct![k]} onChange={(v) => onNested(seg, s.key, k, v)} />
                ))}
              {cols.map((c) => (
                <Field key={c.key} name={c.key} value={sub[c.key]} meta={c.meta} onChange={(v) => onNested(seg, s.key, c.key, v)} />
              ))}
            </div>
          );
        })}
      </div>
    </div>
  );
}

function MissingCard({ title }: { title: string }) {
  return (
    <div className="card mb-4">
      <h3 className="mb-1 text-sm font-semibold text-slate-200">{title}</h3>
      <p className="text-xs text-slate-500">
        The device returned no data for this section. Some firmware doesn’t expose every setting over the gateway link (those are configurable on the
        unit’s own screen). If it’s just a slow read, use Reload.
      </p>
    </div>
  );
}

// ---- primitives ----

function CardHead({ title, dirty, saving, onSave }: { title: string; dirty: boolean; saving: boolean; onSave: () => void }) {
  return (
    <div className="mb-2 flex items-center justify-between">
      <h3 className="text-sm font-semibold text-slate-200">{title}</h3>
      <button className="btn-primary" disabled={!dirty || saving} onClick={onSave}>
        {saving ? "Saving…" : dirty ? "Save" : "Saved"}
      </button>
    </div>
  );
}

function Field({ name, value, meta, onChange }: { name: string; value: any; meta?: N.N62FieldMeta; onChange: (v: any) => void }) {
  if (meta?.hidden) return null;
  const label = meta?.label ?? N.humanize(name);
  const type = meta?.type ?? (typeof value === "number" ? "number" : "text");

  if (meta?.readonly) {
    return (
      <Row label={label} help={meta?.help}>
        <span className="font-mono text-slate-300">{value === "" || value == null ? "—" : String(value)}</span>
      </Row>
    );
  }
  if (type === "select" && meta?.options) {
    // Booleans (some alarm En fields) map onto the 0/1 option set.
    const cur = typeof value === "boolean" ? (value ? "1" : "0") : String(value ?? "");
    return (
      <Row label={label} help={meta?.help}>
        <select className="input w-48" value={cur} onChange={(e) => onChange(e.target.value)}>
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
    <Row label={label} help={meta?.help}>
      <input
        className="input w-48 font-mono"
        // A numeric field uses a number input so the browser yields "" (never
        // free text) for invalid entries — that kept Number(raw)=NaN, which
        // serialized to JSON null and reached the device firmware.
        type={numeric ? "number" : type === "password" ? "password" : "text"}
        min={numeric ? meta?.min : undefined}
        max={numeric ? meta?.max : undefined}
        value={value == null ? "" : String(value)}
        onChange={(e) => {
          const raw = e.target.value;
          onChange(numeric ? (raw === "" ? "" : Number(raw)) : raw);
        }}
      />
    </Row>
  );
}

function Row({ label, help, children }: { label: string; help?: string; children: React.ReactNode }) {
  return (
    <div className="py-0.5">
      <label className="flex items-center justify-between gap-3 text-sm">
        <span className="shrink-0 text-slate-400">{label}</span>
        {children}
      </label>
      {help && <p className="mt-0.5 text-right text-xs text-slate-500">{help}</p>}
    </div>
  );
}
