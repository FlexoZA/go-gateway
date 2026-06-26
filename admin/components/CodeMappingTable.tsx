"use client";

import { useEffect, useMemo, useState } from "react";
import { useConfirm } from "@/components/confirm";
import { api } from "@/lib/api";
import { useFetch } from "@/lib/useFetch";
import { Empty, ErrorBanner, Spinner } from "@/components/ui";
import { EventCodeSelect } from "@/components/EventCodeSelect";

// CodeMappingTable is the code→event lookup editor: a flat, per-unit table
// editable inline, applied to the running gateway instantly.

type Mapping = {
  id: number;
  unit: string;
  map_type: string;
  code: number;
  event_code: string;
  description: string;
  updated_at: string;
};

// bitHex returns the bitmask a code sets (1 << code) as hex, e.g. code 1 →
// "0x02". BigInt keeps large codes exact (1<<768 would overflow a 32-bit shift).
function bitHex(code: number): string {
  let hex = (1n << BigInt(code)).toString(16);
  if (hex.length < 2) hex = "0" + hex;
  return `0x${hex}`;
}

// BitCell shows "bit N" and toggles to the hex mask ("0x02") when clicked.
function BitCell({ code }: { code: number }) {
  const [showHex, setShowHex] = useState(false);
  if (!Number.isInteger(code) || code < 0) return <span className="text-slate-500">—</span>;
  return (
    <button
      type="button"
      onClick={() => setShowHex((v) => !v)}
      title="Click to toggle hex mask"
      className="font-mono text-slate-400 hover:text-indigo-300"
    >
      {showHex ? bitHex(code) : `bit ${code}`}
    </button>
  );
}

export function CodeMappingTable({ unit, model = "" }: { unit: string; model?: string }) {
  const { data, error, loading, refresh } = useFetch<{ unit: string; mappings: Mapping[] }>(
    `mappings?unit=${encodeURIComponent(unit)}&model=${encodeURIComponent(model)}`,
  );
  const [actionError, setActionError] = useState<string | null>(null);

  const grouped = useMemo(() => {
    const g: Record<string, Mapping[]> = {};
    for (const m of data?.mappings ?? []) {
      (g[m.map_type] ||= []).push(m);
    }
    return g;
  }, [data]);

  async function save(map_type: string, code: number, event_code: string, description: string) {
    setActionError(null);
    try {
      await api("mappings", {
        method: "PUT",
        body: JSON.stringify({ unit, model, map_type, code, event_code, description }),
      });
      await refresh();
    } catch (e: any) {
      setActionError(e.message || "Save failed");
    }
  }

  async function remove(map_type: string, code: number) {
    setActionError(null);
    try {
      await api(`mappings?unit=${encodeURIComponent(unit)}&model=${encodeURIComponent(model)}&map_type=${encodeURIComponent(map_type)}&code=${code}`, { method: "DELETE" });
      await refresh();
    } catch (e: any) {
      setActionError(e.message || "Delete failed");
    }
  }

  return (
    <div>
      <p className="mb-4 text-sm text-slate-400">
        Code→event lookups for <span className="font-mono">{unit}</span>
        {model ? (
          <>
            {" · model "}
            <span className="font-mono text-slate-200">{model}</span>
          </>
        ) : (
          " · default (all models)"
        )}
        . Edits reach the running gateway within milliseconds.
      </p>
      <ErrorBanner message={actionError || error} />

      <AddMapping unit={unit} mapTypes={Object.keys(grouped)} onAdd={save} />

      {loading ? (
        <Spinner />
      ) : (data?.mappings?.length ?? 0) === 0 ? (
        <Empty>
          {model
            ? "This model has no mappings of its own — it falls back to the default. Add rows below, or use “Copy from default” to start from the default table."
            : "No mappings found. The gateway seeds built-in defaults on startup."}
        </Empty>
      ) : (
        Object.entries(grouped).map(([mapType, rows]) => (
          <section key={mapType} className="mb-8">
            <h2 className="mb-3 font-mono text-sm font-semibold text-indigo-300">{mapType}</h2>
            <div className="card overflow-x-auto p-0">
              <table className="min-w-full divide-y divide-edge">
                <thead>
                  <tr>
                    <th className="th w-20">Code</th>
                    <th className="th">Bit (1&lt;&lt;code)</th>
                    <th className="th">Event code</th>
                    <th className="th">Description</th>
                    <th className="th text-right">Actions</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-edge">
                  {rows.map((m) => (
                    <MappingRow key={m.id} m={m} onSave={save} onDelete={remove} />
                  ))}
                </tbody>
              </table>
            </div>
          </section>
        ))
      )}
    </div>
  );
}

function MappingRow({
  m,
  onSave,
  onDelete,
}: {
  m: Mapping;
  onSave: (mapType: string, code: number, eventCode: string, description: string) => Promise<void>;
  onDelete: (mapType: string, code: number) => Promise<void>;
}) {
  const [eventCode, setEventCode] = useState(m.event_code);
  const [description, setDescription] = useState(m.description);
  const [busy, setBusy] = useState(false);
  const confirm = useConfirm();

  useEffect(() => {
    setEventCode(m.event_code);
    setDescription(m.description);
  }, [m.event_code, m.description]);

  const dirty = eventCode !== m.event_code || description !== m.description;

  return (
    <tr>
      <td className="td font-mono text-slate-400">{m.code}</td>
      <td className="td whitespace-nowrap">
        <BitCell code={m.code} />
      </td>
      <td className="td">
        <EventCodeSelect value={eventCode} onChange={setEventCode} />
      </td>
      <td className="td">
        <input className="input" value={description} onChange={(e) => setDescription(e.target.value)} placeholder="—" />
      </td>
      <td className="td">
        <div className="flex justify-end gap-2">
          <button
            className="btn-primary"
            disabled={!dirty || busy || !eventCode.trim()}
            onClick={async () => {
              setBusy(true);
              await onSave(m.map_type, m.code, eventCode.trim(), description.trim());
              setBusy(false);
            }}
          >
            Save
          </button>
          <button
            className="btn-ghost"
            disabled={busy}
            onClick={async () => {
              if (
                !(await confirm({
                  title: "Reset mapping?",
                  body: `Reset ${m.map_type} code ${m.code} to the built-in default?`,
                  confirmLabel: "Reset",
                }))
              )
                return;
              setBusy(true);
              await onDelete(m.map_type, m.code);
              setBusy(false);
            }}
          >
            Reset
          </button>
        </div>
      </td>
    </tr>
  );
}

function AddMapping({
  unit,
  mapTypes,
  onAdd,
}: {
  unit: string;
  mapTypes: string[];
  onAdd: (mapType: string, code: number, eventCode: string, description: string) => Promise<void>;
}) {
  const [mapType, setMapType] = useState("");
  const [code, setCode] = useState("");
  const [eventCode, setEventCode] = useState("");
  const [description, setDescription] = useState("");
  const [busy, setBusy] = useState(false);

  const valid = mapType.trim() && code.trim() && !isNaN(Number(code)) && eventCode.trim();

  return (
    <div className="card mb-8">
      <h2 className="mb-3 text-sm font-semibold text-slate-300">Add / override a mapping</h2>
      <div className="grid grid-cols-1 gap-3 md:grid-cols-12">
        <div className="md:col-span-4">
          <label className="text-xs text-slate-400">Map type</label>
          <input
            className="input mt-1"
            list="map-types"
            value={mapType}
            onChange={(e) => setMapType(e.target.value)}
            placeholder="e.g. dms_adas"
          />
          <datalist id="map-types">
            {mapTypes.map((t) => (
              <option key={t} value={t} />
            ))}
          </datalist>
        </div>
        <div className="md:col-span-2">
          <label className="text-xs text-slate-400">Code</label>
          <input className="input mt-1" value={code} onChange={(e) => setCode(e.target.value)} placeholder="34" inputMode="numeric" />
        </div>
        <div className="md:col-span-3">
          <label className="text-xs text-slate-400">Event code</label>
          <EventCodeSelect value={eventCode} onChange={setEventCode} className="input mt-1" />
        </div>
        <div className="md:col-span-3">
          <label className="text-xs text-slate-400">Description</label>
          <input className="input mt-1" value={description} onChange={(e) => setDescription(e.target.value)} placeholder="optional" />
        </div>
      </div>
      <div className="mt-3 flex justify-end">
        <button
          className="btn-primary"
          disabled={!valid || busy}
          onClick={async () => {
            setBusy(true);
            await onAdd(mapType.trim(), Number(code), eventCode.trim(), description.trim());
            setBusy(false);
            setCode("");
            setEventCode("");
            setDescription("");
          }}
        >
          {busy ? "Saving…" : "Save mapping"}
        </button>
      </div>
      <p className="mt-2 text-xs text-slate-500">Unit: <span className="font-mono">{unit}</span></p>
    </div>
  );
}
