"use client";

import { useState } from "react";
import { api } from "@/lib/api";
import { useFetch } from "@/lib/useFetch";
import { PageHeader } from "@/components/ui";
import { CodeMappingTable } from "@/components/CodeMappingTable";
import { AddEventCode } from "@/components/AddEventCode";
import { useUnits } from "@/lib/useGatewayInfo";

export default function DeviceMappingPage() {
  // Only unit types that drive their output from editable mappings appear here.
  const units = useUnits().filter((u) => u.capabilities?.has_mappings);
  const [pickedUnit, setPickedUnit] = useState("");
  const unit = pickedUnit || units[0]?.unit || "";

  const [model, setModel] = useState(""); // "" = the default that applies to all models
  const [nonce, setNonce] = useState(0); // bump to force the table to refetch (after a copy)
  const [copyMsg, setCopyMsg] = useState<string | null>(null);

  // Model suggestions: models that already have a table + models of connected devices of this unit.
  const known = useFetch<{ models: string[] }>(unit ? `mappings/models?unit=${encodeURIComponent(unit)}` : "");
  const connected = useFetch<{ units: { protocol: string; model?: string }[] }>("units");
  const suggestions = Array.from(
    new Set([
      ...(known.data?.models ?? []),
      ...(connected.data?.units ?? []).filter((u) => u.protocol === unit && u.model).map((u) => u.model as string),
    ]),
  ).sort();

  async function copyFromDefault() {
    if (!model) return;
    setCopyMsg(null);
    try {
      await api("mappings/copy", { method: "POST", body: JSON.stringify({ unit, from_model: "", to_model: model }) });
      known.refresh();
      setNonce((n) => n + 1);
      setCopyMsg(`Copied the default mapping into "${model}".`);
    } catch (e: any) {
      setCopyMsg(e.message || "Copy failed");
    }
  }

  return (
    <div>
      <PageHeader
        title="Device Mapping"
        subtitle="Map raw device codes to ACM event codes — per unit type, and optionally per device model. Edits apply to the running gateway instantly."
      />

      <div className="mb-6 flex flex-wrap items-end gap-4">
        {units.length > 1 && (
          <div>
            <label className="text-xs text-slate-400">Unit type</label>
            <select
              className="input mt-1"
              value={unit}
              onChange={(e) => {
                setPickedUnit(e.target.value);
                setModel("");
                setCopyMsg(null);
              }}
            >
              {units.map((u) => (
                <option key={u.unit} value={u.unit}>
                  {u.unit}
                </option>
              ))}
            </select>
          </div>
        )}
        <div>
          <label className="text-xs text-slate-400">Model</label>
          <div className="mt-1 flex items-center gap-2">
            <input
              className="input w-56"
              list="model-suggestions"
              value={model}
              onChange={(e) => {
                setModel(e.target.value);
                setCopyMsg(null);
              }}
              placeholder="Default (all models)"
            />
            <datalist id="model-suggestions">
              {suggestions.map((m) => (
                <option key={m} value={m} />
              ))}
            </datalist>
            {model && (
              <>
                <button className="btn-ghost" onClick={() => { setModel(""); setCopyMsg(null); }}>
                  Use default
                </button>
                <button className="btn-ghost" onClick={copyFromDefault} title="Copy every default row into this model as a starting point">
                  Copy from default
                </button>
              </>
            )}
          </div>
          <p className="mt-1 text-xs text-slate-500">
            Empty = the default that applies to every model. A model with its own table overrides the default for that model.
          </p>
          {copyMsg && <p className="mt-1 text-xs text-emerald-300">{copyMsg}</p>}
        </div>
      </div>

      <AddEventCode />

      {!unit ? (
        <p className="text-sm text-slate-400">No unit types with editable mappings.</p>
      ) : (
        <CodeMappingTable key={`${unit}:${model}:${nonce}`} unit={unit} model={model} />
      )}
    </div>
  );
}
