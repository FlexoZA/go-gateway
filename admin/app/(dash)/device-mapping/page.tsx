"use client";

import { useState } from "react";
import { PageHeader } from "@/components/ui";
import { CodeMappingTable } from "@/components/CodeMappingTable";
import { AddEventCode } from "@/components/AddEventCode";
import { useUnits } from "@/lib/useGatewayInfo";

export default function DeviceMappingPage() {
  // Only unit types that drive their output from editable mappings appear here.
  const units = useUnits().filter((u) => u.capabilities?.has_mappings);
  const [picked, setPicked] = useState<string>("");
  const unit = picked || units[0]?.unit || "";

  return (
    <div>
      <PageHeader
        title="Device Mapping"
        subtitle="Map raw device codes to ACM event codes — a per-unit lookup table applied to the running gateway instantly."
      />

      {units.length > 1 && (
        <div className="mb-6 flex items-center gap-2">
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

      <AddEventCode />

      {!unit ? (
        <p className="text-sm text-slate-400">No unit types with editable mappings.</p>
      ) : (
        <CodeMappingTable key={unit} unit={unit} />
      )}
    </div>
  );
}
