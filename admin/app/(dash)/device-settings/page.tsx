"use client";

import { useEffect, useMemo, useState } from "react";
import { api } from "@/lib/api";
import { useFetch } from "@/lib/useFetch";
import { Badge, Empty, ErrorBanner, PageHeader, Spinner } from "@/components/ui";
import { useUnits, refreshGatewayInfo, type Caps, type SettingField, type Unit } from "@/lib/useGatewayInfo";

// Device Settings is the per-unit-type hub: each unit's FEATURES (disable-only
// capability toggles), its listener PORTS, and its unit-specific SETTINGS.

export default function DeviceSettingsPage() {
  const units = useUnits();

  return (
    <div>
      <PageHeader
        title="Device Settings"
        subtitle="Per-unit-type configuration: which features the admin shows, listener ports, and unit-specific settings."
      />
      {units.length === 0 ? (
        <Spinner />
      ) : (
        units.map((u) => <UnitCard key={u.unit} unit={u} />)
      )}
    </div>
  );
}

function UnitCard({ unit }: { unit: Unit }) {
  return (
    <section className="mb-8">
      <h2 className="mb-3 text-base font-semibold text-white">
        <span className="font-mono">{unit.unit}</span>
      </h2>
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        <CapabilitiesCard unit={unit} />
        <UnitPortsCard unit={unit.unit} hasVideo={!!unit.supported?.has_video} />
        {(unit.schema?.length ?? 0) > 0 && <UnitSettingsForm unit={unit.unit} schema={unit.schema!} />}
      </div>
    </section>
  );
}

// ---- Features (capability disable-toggles) ----

const CAP_FIELDS: { key: string; label: string; capKey: keyof Caps; hint: string }[] = [
  { key: "video", label: "Live video", capKey: "has_video", hint: "Live Preview + Clips" },
  { key: "config", label: "Device config", capKey: "has_config", hint: "the Config tab on a device" },
  { key: "commands", label: "Control commands", capKey: "has_commands", hint: "device commands (reboot, etc.)" },
  { key: "status", label: "Status reporting", capKey: "has_status", hint: "the live status detail" },
];

function CapabilitiesCard({ unit }: { unit: Unit }) {
  const [busy, setBusy] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const caps = unit.capabilities; // effective (on/off now)
  const sup = unit.supported; // what the protocol can do

  async function toggle(feature: string, on: boolean) {
    setBusy(feature);
    setErr(null);
    try {
      await api(`unit-types/${encodeURIComponent(unit.unit)}/capabilities`, {
        method: "PUT",
        body: JSON.stringify({ [feature]: on }),
      });
      refreshGatewayInfo(); // re-read so the whole admin reflects the change
    } catch (e: any) {
      setErr(e.message || "Failed to update");
    } finally {
      setBusy(null);
    }
  }

  return (
    <div className="card space-y-3">
      <div>
        <h3 className="text-sm font-semibold text-slate-200">Features</h3>
        <p className="mt-1 text-xs text-slate-400">Turn off a feature to hide it across the admin. A unit can only disable what its protocol supports.</p>
      </div>
      {err && <div className="rounded-md border border-rose-500/40 bg-rose-500/10 px-3 py-2 text-sm text-rose-200">{err}</div>}
      <div className="space-y-2">
        {CAP_FIELDS.map((f) => {
          const supported = !!sup?.[f.capKey];
          const on = !!caps?.[f.capKey];
          return (
            <div key={f.key} className="flex items-center justify-between gap-3">
              <div>
                <div className="text-sm text-slate-200">{f.label}</div>
                <div className="text-xs text-slate-500">{supported ? f.hint : "Not supported by this unit"}</div>
              </div>
              {supported ? (
                <button
                  onClick={() => toggle(f.key, !on)}
                  disabled={busy === f.key}
                  className={`rounded-full px-3 py-1 text-xs font-medium ${
                    on ? "bg-emerald-600/80 text-white" : "bg-edge text-slate-300"
                  }`}
                >
                  {busy === f.key ? "…" : on ? "On" : "Off"}
                </button>
              ) : (
                <Badge tone="slate">—</Badge>
              )}
            </div>
          );
        })}
      </div>
    </div>
  );
}

// ---- Ports (moved here from Server Settings) ----

type Ports = {
  unit: string;
  has_video: boolean;
  device_port: string;
  device_port_active: string;
  media_port?: string;
  media_port_active?: string;
};

const isPort = (v: string) => /^\d+$/.test(v.trim()) && +v >= 1 && +v <= 65535;

function UnitPortsCard({ unit, hasVideo }: { unit: string; hasVideo: boolean }) {
  const { data, error, refresh } = useFetch<Ports>(`unit-types/${encodeURIComponent(unit)}/ports`);
  const [device, setDevice] = useState("");
  const [media, setMedia] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    if (data) {
      setDevice(data.device_port || "");
      setMedia(data.media_port || "");
    }
  }, [data]);

  const devDirty = device !== (data?.device_port || "");
  const medDirty = hasVideo && media !== (data?.media_port || "");
  const dirty = devDirty || medDirty;
  const valid = (!devDirty || isPort(device)) && (!medDirty || isPort(media));
  const devRestart = data?.device_port_active && data?.device_port && data.device_port_active !== data.device_port;
  const medRestart = hasVideo && data?.media_port_active && data?.media_port && data.media_port_active !== data.media_port;

  async function save() {
    setBusy(true);
    setErr(null);
    try {
      const body: any = {};
      if (devDirty) body.device_port = +device;
      if (medDirty) body.media_port = +media;
      await api(`unit-types/${encodeURIComponent(unit)}/ports`, { method: "PUT", body: JSON.stringify(body) });
      await refresh();
    } catch (e: any) {
      setErr(e.message || "Save failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="card space-y-3">
      <div>
        <h3 className="text-sm font-semibold text-slate-200">Ports</h3>
        <p className="mt-1 text-xs text-slate-400">
          Listening on <span className="font-mono text-slate-200">{data?.device_port_active || "—"}</span>
          {hasVideo ? (
            <>
              {" · media "}
              <span className="font-mono text-slate-200">{data?.media_port_active || "—"}</span>
            </>
          ) : null}
          .
        </p>
      </div>
      {(err || error) && (
        <div className="rounded-md border border-rose-500/40 bg-rose-500/10 px-3 py-2 text-sm text-rose-200">{err || error}</div>
      )}
      <div className="flex flex-wrap items-end gap-3">
        <div className="w-32">
          <label className="text-xs text-slate-400">Device port</label>
          <input className="input mt-1" inputMode="numeric" value={device} onChange={(e) => setDevice(e.target.value)} placeholder="33000" />
        </div>
        {hasVideo && (
          <div className="w-32">
            <label className="text-xs text-slate-400">Media port</label>
            <input className="input mt-1" inputMode="numeric" value={media} onChange={(e) => setMedia(e.target.value)} placeholder="33001" />
          </div>
        )}
        <button className="btn-primary" onClick={save} disabled={!dirty || busy || !valid}>
          {busy ? "Saving…" : "Save"}
        </button>
      </div>
      <div className="rounded-md border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-xs text-amber-200">
        ⚠ Port changes apply on the next gateway <strong>restart</strong>, and in Docker you must also update the published{" "}
        <span className="font-mono">ports:</span> mapping in <span className="font-mono">docker-compose.yml</span>.
      </div>
      {(devRestart || medRestart) && (
        <div className="rounded-md border border-indigo-500/40 bg-indigo-500/10 px-3 py-2 text-xs text-indigo-200">
          Restart pending —{" "}
          {devRestart && (
            <>
              device <span className="font-mono">{data?.device_port}</span> (running <span className="font-mono">{data?.device_port_active}</span>)
            </>
          )}
          {devRestart && medRestart ? "; " : ""}
          {medRestart && (
            <>
              media <span className="font-mono">{data?.media_port}</span> (running <span className="font-mono">{data?.media_port_active}</span>)
            </>
          )}
          .
        </div>
      )}
    </div>
  );
}

// ---- Unit-specific settings (schema-driven) ----

type Row = { key: string; value: string };

function UnitSettingsForm({ unit, schema }: { unit: string; schema: SettingField[] }) {
  const { data, error, loading, refresh } = useFetch<{ settings: Row[] }>(`unit-types/${encodeURIComponent(unit)}/settings`);

  const current = useMemo(() => {
    const m: Record<string, string> = {};
    for (const f of schema) m[f.key] = f.default ?? "";
    for (const s of data?.settings ?? []) m[s.key] = s.value;
    return m;
  }, [data, schema]);

  const [vals, setVals] = useState<Record<string, string>>({});
  const [ready, setReady] = useState(false);
  const [saving, setSaving] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  useEffect(() => {
    if (!loading) {
      setVals(current);
      setReady(true);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [loading, data]);

  const changed = useMemo(() => schema.filter((f) => (vals[f.key] ?? "") !== (current[f.key] ?? "")), [schema, vals, current]);

  async function save() {
    setSaving(true);
    setActionError(null);
    setNotice(null);
    try {
      for (const f of changed) {
        await api(`unit-types/${encodeURIComponent(unit)}/settings`, {
          method: "PUT",
          body: JSON.stringify({ key: f.key, value: vals[f.key] ?? "" }),
        });
      }
      await refresh();
      setNotice("Saved.");
    } catch (e: any) {
      setActionError(e.message || "Save failed");
    } finally {
      setSaving(false);
    }
  }

  if (loading && !ready) return <div className="card"><Spinner /></div>;

  return (
    <div className="card space-y-3">
      <h3 className="text-sm font-semibold text-slate-200">Settings</h3>
      <ErrorBanner message={actionError || error} />
      {notice && (
        <div className="rounded-md border border-emerald-500/40 bg-emerald-500/10 px-3 py-2 text-sm text-emerald-200">{notice}</div>
      )}
      <div className="space-y-3">
        {schema.map((f) => (
          <Field key={f.key} field={f} value={vals[f.key] ?? ""} onChange={(v) => setVals((p) => ({ ...p, [f.key]: v }))} />
        ))}
      </div>
      <button className="btn-primary" disabled={saving || changed.length === 0} onClick={save}>
        {saving ? "Saving…" : changed.length ? `Save ${changed.length} change${changed.length > 1 ? "s" : ""}` : "Saved"}
      </button>
    </div>
  );
}

function Field({ field, value, onChange }: { field: SettingField; value: string; onChange: (v: string) => void }) {
  return (
    <div>
      <label className="text-sm text-slate-300">{field.label || field.key}</label>
      {field.help && <p className="mb-1 text-xs text-slate-500">{field.help}</p>}
      {field.type === "bool" ? (
        <select className="input mt-1" value={value} onChange={(e) => onChange(e.target.value)}>
          <option value="true">true</option>
          <option value="false">false</option>
        </select>
      ) : field.type === "select" ? (
        <select className="input mt-1" value={value} onChange={(e) => onChange(e.target.value)}>
          {(field.options ?? []).map((o) => (
            <option key={o} value={o}>
              {o}
            </option>
          ))}
        </select>
      ) : (
        <input
          className="input mt-1"
          value={value}
          inputMode={field.type === "number" ? "decimal" : undefined}
          onChange={(e) => onChange(e.target.value)}
        />
      )}
    </div>
  );
}
