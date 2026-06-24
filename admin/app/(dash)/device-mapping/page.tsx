"use client";

import { useState } from "react";
import dynamic from "next/dynamic";
import { PageHeader } from "@/components/ui";
import { CodeMappingTable } from "@/components/CodeMappingTable";
import { useUnits } from "@/lib/useGatewayInfo";

// React Flow touches the DOM on mount, so load the canvas editor client-only.
const WorkflowEditor = dynamic(() => import("@/components/WorkflowEditor"), {
  ssr: false,
  loading: () => <div className="text-sm text-slate-400">Loading editor…</div>,
});

type Tab = "table" | "workflow";

export default function DeviceMappingPage() {
  const [tab, setTab] = useState<Tab>("table");
  // Only unit types that drive their output from editable mappings appear here.
  const units = useUnits().filter((u) => u.capabilities?.has_mappings);
  const [picked, setPicked] = useState<string>("");
  const unit = picked || units[0]?.unit || "";

  return (
    <div>
      <PageHeader
        title="Device Mapping"
        subtitle="Map raw device codes to ACM event codes — a simple per-unit table, or a per-model visual workflow."
      />

      <div className="mb-6 flex flex-wrap items-center gap-2">
        <TabButton active={tab === "table"} onClick={() => setTab("table")}>
          Code table
        </TabButton>
        <TabButton active={tab === "workflow"} onClick={() => setTab("workflow")}>
          Visual workflows
        </TabButton>
        {units.length > 1 && (
          <div className="ml-auto flex items-center gap-2">
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
      </div>

      {!unit ? (
        <p className="text-sm text-slate-400">No unit types with editable mappings.</p>
      ) : tab === "table" ? (
        <CodeMappingTable key={unit} unit={unit} />
      ) : (
        <WorkflowEditor key={unit} unit={unit} />
      )}
    </div>
  );
}

function TabButton({ active, onClick, children }: { active: boolean; onClick: () => void; children: React.ReactNode }) {
  return (
    <button
      onClick={onClick}
      className={`rounded-md px-4 py-1.5 text-sm font-medium ${
        active ? "bg-indigo-600 text-white" : "border border-edge text-slate-300 hover:bg-edge"
      }`}
    >
      {children}
    </button>
  );
}
