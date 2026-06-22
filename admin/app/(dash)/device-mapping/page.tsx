"use client";

import { useState } from "react";
import dynamic from "next/dynamic";
import { PageHeader } from "@/components/ui";
import { CodeMappingTable } from "@/components/CodeMappingTable";

// React Flow touches the DOM on mount, so load the canvas editor client-only.
const WorkflowEditor = dynamic(() => import("@/components/WorkflowEditor"), {
  ssr: false,
  loading: () => <div className="text-sm text-slate-400">Loading editor…</div>,
});

type Tab = "table" | "workflow";

export default function DeviceMappingPage() {
  const [tab, setTab] = useState<Tab>("table");

  return (
    <div>
      <PageHeader
        title="Device Mapping"
        subtitle="Map raw device codes to ACM event codes — a simple per-unit table, or a per-model visual workflow."
      />

      <div className="mb-6 flex gap-2">
        <TabButton active={tab === "table"} onClick={() => setTab("table")}>
          Code table
        </TabButton>
        <TabButton active={tab === "workflow"} onClick={() => setTab("workflow")}>
          Visual workflows
        </TabButton>
      </div>

      {tab === "table" ? <CodeMappingTable /> : <WorkflowEditor />}
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
