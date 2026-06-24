"use client";

import { useEffect, useMemo, useState } from "react";
import { api } from "@/lib/api";
import { useFetch } from "@/lib/useFetch";
import { Empty, ErrorBanner, PageHeader, Spinner } from "@/components/ui";
import { useUnits, type SettingField } from "@/lib/useGatewayInfo";

// Unit Settings is the per-unit-type gateway-settings screen. Each unit declares
// its own schema (GET /api/gateway/info → units[].schema), rendered generically
// here. Values are stored per unit and applied to the running gateway instantly.

export default function UnitSettingsPage() {
  const units = useUnits().filter((u) => (u.schema?.length ?? 0) > 0);
  const [picked, setPicked] = useState("");
  const unit = picked || units[0]?.unit || "";
  const schema = units.find((u) => u.unit === unit)?.schema ?? [];

  return (
    <div>
      <PageHeader
        title="Unit Settings"
        subtitle="Per-unit-type gateway settings. Changes apply to the running gateway within milliseconds."
      />

      {units.length === 0 ? (
        <Empty>No hosted unit type has editable settings.</Empty>
      ) : (
        <>
          {units.length > 1 && (
            <div className="mb-5 flex items-center gap-2">
              <label className="text-xs text-slate-400">Unit type</label>
              <select className="input" value={unit} onChange={(e) => setPicked(e.target.value)}>
                {units.map((u) => (
                  <option key={u.unit} value={u.unit}>
                    {u.unit}
                  </option>
                ))}
              </select>
            </div>
          )}
          <UnitSettingsForm key={unit} unit={unit} schema={schema} />
        </>
      )}
    </div>
  );
}

type Row = { key: string; value: string };

function UnitSettingsForm({ unit, schema }: { unit: string; schema: SettingField[] }) {
  const { data, error, loading, refresh } = useFetch<{ settings: Row[] }>(
    `unit-types/${encodeURIComponent(unit)}/settings`,
  );

  // Stored values over schema defaults.
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

  // Initialize the editable copy once the stored values have loaded.
  useEffect(() => {
    if (!loading) {
      setVals(current);
      setReady(true);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [loading, data]);

  const changed = useMemo(
    () => schema.filter((f) => (vals[f.key] ?? "") !== (current[f.key] ?? "")),
    [schema, vals, current],
  );

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

  if (loading && !ready) return <Spinner />;

  return (
    <div className="card max-w-2xl">
      <ErrorBanner message={actionError || error} />
      {notice && (
        <div className="mb-3 rounded-md border border-emerald-500/40 bg-emerald-500/10 px-3 py-2 text-sm text-emerald-200">
          {notice}
        </div>
      )}
      <div className="space-y-4">
        {schema.map((f) => (
          <Field key={f.key} field={f} value={vals[f.key] ?? ""} onChange={(v) => setVals((p) => ({ ...p, [f.key]: v }))} />
        ))}
      </div>
      <div className="mt-5 flex items-center gap-3">
        <button className="btn-primary" disabled={saving || changed.length === 0} onClick={save}>
          {saving ? "Saving…" : changed.length ? `Save ${changed.length} change${changed.length > 1 ? "s" : ""}` : "Saved"}
        </button>
        <span className="text-xs text-slate-500">
          Unit: <span className="font-mono">{unit}</span>
        </span>
      </div>
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
