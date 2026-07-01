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
// exchange, so we only fetch a category's segments when it's opened). The N62 merges
// partial top-level Sets, so scalar segments send only changed fields; nested
// segments (NetCms servers, RecStream channels) are NOT merged by the firmware, so
// those are sent whole (every sub-object, matching the device's own web UI).

type Obj = Record<string, any>;
type Sc = Record<string, Obj>;

const clone = <T,>(v: T): T => JSON.parse(JSON.stringify(v));

// coerce turns an editor value into the JSON type the device expects: numeric for
// select/number/toggle controls (all N62 enums are integers), string otherwise.
function coerce(meta: N.N62FieldMeta | undefined, v: any): any {
  const t = meta?.type;
  if (t === "text" || t === "password") return v == null ? "" : String(v);
  if (t === "number" || t === "select" || t === "toggle") {
    if (v === "" || v == null) return v;
    const n = Number(v);
    return Number.isNaN(n) ? v : n;
  }
  return v;
}

const eq = (a: any, b: any) => JSON.stringify(a) === JSON.stringify(b);

export function N62Config({ serial }: { serial: string; unit?: string }) {
  const [cat, setCat] = useState(N.CATEGORIES[0].key);
  const [sc, setSc] = useState<Sc>({}); // device baseline, keyed by segment
  const [draft, setDraft] = useState<Sc>({}); // edited copy
  const [loaded, setLoaded] = useState<Record<string, boolean>>({}); // by category key
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState<string | null>(null); // segment being saved
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const confirm = useConfirm();

  // ingest merges freshly-read segments into sc + draft, splitting compound fields
  // (GenDateTime) so each half gets its own control.
  const ingest = useCallback((fresh: Sc) => {
    const norm: Sc = {};
    for (const [seg, obj] of Object.entries(fresh)) {
      norm[seg] = N.splitCompound(seg, obj || {});
    }
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

  // ---- payload builders (per segment write rule) ----

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
          out[parent] = N.joinCompound(parent, d); // compound stays a "a,b" string
          seen.add(parent);
        }
      } else {
        out[field] = coerce(m, d[field]);
      }
    }
    return Object.keys(out).length ? out : null;
  }

  function nestedPayload(seg: string, subPrefix: string, writeFields: string[], meta: Record<string, N.N62FieldMeta>): Obj | null {
    const d = draft[seg] || {};
    const out: Obj = {};
    for (const sub of Object.keys(d).filter((k) => k.startsWith(subPrefix)).sort()) {
      const src = d[sub] || {};
      const obj: Obj = {};
      for (const f of writeFields) {
        if (f in src) obj[f] = coerce(meta[f], src[f]);
      }
      out[sub] = obj;
    }
    return Object.keys(out).length ? out : null;
  }

  function buildPayload(seg: string): Obj | null {
    if (seg === "NetCms") return nestedPayload(seg, "Server_", N.CMS_SERVER_WRITE, N.CMS_SERVER);
    if (seg === "RecStream_M" || seg === "RecStream_S") return nestedPayload(seg, "Chn_", N.STREAM_CHANNEL_WRITE, N.STREAM_CHANNEL);
    return scalarPayload(seg);
  }

  const RISKY: Record<string, string> = {
    NetCms: "The CMS server is how this unit reaches the gateway — a wrong address or protocol will disconnect it.",
    NetWifi: "Changing Wi-Fi can drop the unit's network connection.",
    NetXg: "Changing the cellular APN can drop the unit's mobile connection.",
    NetWired: "Changing wired networking can drop the unit's connection.",
  };

  async function save(seg: string, label: string) {
    if (!dirty(seg)) return;
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
      // The gateway re-reads written segments; adopt that as the new truth. If the
      // re-read came back empty (device applied but didn't answer the read), fall
      // back to treating the draft as the baseline.
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
        <CategoryBody
          cat={cat}
          sc={sc}
          draft={draft}
          saving={saving}
          dirty={dirty}
          onField={setField}
          onNested={setNested}
          onSave={save}
        />
      )}
    </div>
  );
}

// ---- category rendering ----

function CategoryBody({
  cat,
  sc,
  draft,
  saving,
  dirty,
  onField,
  onNested,
  onSave,
}: {
  cat: string;
  sc: Sc;
  draft: Sc;
  saving: string | null;
  dirty: (seg: string) => boolean;
  onField: (seg: string, field: string, v: any) => void;
  onNested: (seg: string, sub: string, field: string, v: any) => void;
  onSave: (seg: string, label: string) => void;
}) {
  const scalar = (seg: string, title: string, note?: string) =>
    draft[seg] ? (
      <ScalarCard
        key={seg}
        title={title}
        note={note}
        fields={N.segmentFields(seg)}
        obj={draft[seg]}
        dirty={dirty(seg)}
        saving={saving === seg}
        onChange={(f, v) => onField(seg, f, v)}
        onSave={() => onSave(seg, title)}
      />
    ) : (
      <MissingCard key={seg} title={title} />
    );

  if (cat === "general")
    return (
      <>
        {scalar("GenDevInfo", "Device info")}
        {scalar("GenDateTime", "Date & time")}
        {scalar("GenUser", "Web login")}
      </>
    );
  if (cat === "vehicle")
    return (
      <>
        {scalar("VehBaseInfo", "Vehicle & driver")}
        {scalar("VehPosition", "GPS / positioning")}
      </>
    );
  if (cat === "network")
    return (
      <>
        {draft.NetCms ? (
          <NestedCard
            key="NetCms"
            title="CMS server (gateway)"
            note={"The CMS server is how this unit reaches the gateway — a wrong address or protocol will disconnect it."}
            seg="NetCms"
            subs={serversOf(draft.NetCms)}
            subLabel={(i) => `Server ${i + 1}`}
            fields={N.CMS_SERVER}
            obj={draft.NetCms}
            dirty={dirty("NetCms")}
            saving={saving === "NetCms"}
            onChange={(sub, f, v) => onNested("NetCms", sub, f, v)}
            onSave={() => onSave("NetCms", "CMS server")}
          />
        ) : (
          <MissingCard title="CMS server (gateway)" />
        )}
        {scalar("NetWifi", "Wi-Fi", "Changing Wi-Fi can drop the unit's network connection.")}
        {scalar("NetXg", "Cellular (4G)", "Changing the APN can drop the unit's mobile connection.")}
        {scalar("NetWired", "Wired (Ethernet)")}
      </>
    );
  if (cat === "recording")
    return (
      <>
        {scalar("RecAttr", "Recording")}
        {draft.RecStream_M ? (
          <NestedCard
            key="RecStream_M"
            title="Main stream (per channel)"
            seg="RecStream_M"
            subs={channelsOf(draft.RecStream_M)}
            subLabel={(i) => `Channel ${i + 1}`}
            fields={N.STREAM_CHANNEL}
            obj={draft.RecStream_M}
            dirty={dirty("RecStream_M")}
            saving={saving === "RecStream_M"}
            onChange={(sub, f, v) => onNested("RecStream_M", sub, f, v)}
            onSave={() => onSave("RecStream_M", "Main stream")}
          />
        ) : (
          <MissingCard title="Main stream (per channel)" />
        )}
        {draft.RecStream_S ? (
          <NestedCard
            key="RecStream_S"
            title="Sub stream (per channel)"
            seg="RecStream_S"
            subs={channelsOf(draft.RecStream_S)}
            subLabel={(i) => `Channel ${i + 1}`}
            fields={N.STREAM_CHANNEL}
            obj={draft.RecStream_S}
            dirty={dirty("RecStream_S")}
            saving={saving === "RecStream_S"}
            onChange={(sub, f, v) => onNested("RecStream_S", sub, f, v)}
            onSave={() => onSave("RecStream_S", "Sub stream")}
          />
        ) : (
          <MissingCard title="Sub stream (per channel)" />
        )}
      </>
    );
  return null;
}

const serversOf = (obj: Obj) => Object.keys(obj).filter((k) => k.startsWith("Server_")).sort();
const channelsOf = (obj: Obj) => Object.keys(obj).filter((k) => k.startsWith("Chn_")).sort();

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
  // Render curated fields (in metadata order) that the device actually returned.
  const keys = Object.keys(fields).filter((k) => k in obj && !fields[k].hidden);
  return (
    <div className="card mb-4">
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
  seg: string;
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
      <div className="mb-2 flex items-center justify-between">
        <h3 className="text-sm font-semibold text-slate-200">{title}</h3>
        <SaveButton dirty={dirty} saving={saving} onSave={onSave} />
      </div>
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

function MissingCard({ title }: { title: string }) {
  return (
    <div className="card mb-4">
      <h3 className="mb-1 text-sm font-semibold text-slate-200">{title}</h3>
      <p className="text-xs text-slate-500">The device didn’t return this section. Try Reload.</p>
    </div>
  );
}

// ---- typed field control ----

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
    const cur = String(value ?? "");
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
        type={type === "password" ? "password" : "text"}
        inputMode={numeric ? "numeric" : undefined}
        value={value == null ? "" : String(value)}
        onChange={(e) => {
          const raw = e.target.value;
          onChange(numeric ? (raw === "" ? "" : Number(raw)) : raw);
        }}
      />
    </Row>
  );
}

function SaveButton({ dirty, saving, onSave }: { dirty: boolean; saving: boolean; onSave: () => void }) {
  return (
    <button className="btn-primary" disabled={!dirty || saving} onClick={onSave}>
      {saving ? "Saving…" : dirty ? "Save" : "Saved"}
    </button>
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
