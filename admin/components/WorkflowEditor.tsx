"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import ReactFlow, {
  addEdge,
  Background,
  Controls,
  MarkerType,
  useNodesState,
  useEdgesState,
  type Connection,
  type Edge,
  type Node,
  type OnSelectionChangeParams,
} from "reactflow";
import "reactflow/dist/style.css";
import { api } from "@/lib/api";
import { useFetch } from "@/lib/useFetch";
import { Badge, ErrorBanner } from "@/components/ui";
import { EventCodeDatalist } from "@/components/EventCodeDatalist";

// WorkflowEditor is the visual ("N8N-style") per-model device-mapping editor.
// It edits a node graph that the gateway's flow engine interprets. Nodes:
//   input → switch / condition → setEvent / setField → output
// Branch edges leaving switch/condition carry a label (the engine's sourceHandle):
//   switch: "case:<value>" or "default"   ·   condition: "true" / "false"

type Kind = "input" | "switch" | "condition" | "setEvent" | "setField" | "output";

type WorkflowSummary = { model: string; name: string; is_active: boolean; node_count: number; edge_count: number };

const KIND_LABEL: Record<Kind, string> = {
  input: "Input",
  switch: "Switch",
  condition: "Condition",
  setEvent: "Set event",
  setField: "Set field",
  output: "Output",
};

function rfType(kind: Kind): string {
  if (kind === "input") return "input";
  if (kind === "output") return "output";
  return "default";
}

function summarize(kind: Kind, d: any): string {
  switch (kind) {
    case "input":
      return "▶ Input (frame)";
    case "switch":
      return `⎇ switch: ${d.field || "?"}`;
    case "condition":
      return `? ${d.field || "?"} ${d.op || "eq"} ${d.value ?? ""}`;
    case "setEvent":
      return `＋ event: ${d.event || "?"}`;
    case "setField":
      return `＝ ${d.key || "?"} = ${d.value ?? ""}`;
    case "output":
      return "■ Output";
  }
}

// Convert React Flow state → the engine graph JSON.
function toGraph(nodes: Node[], edges: Edge[]) {
  return {
    nodes: nodes.map((n) => {
      const kind: Kind = n.data.kind;
      const d = n.data;
      const data: any = {};
      if (kind === "switch") data.field = d.field || "";
      if (kind === "condition") {
        data.field = d.field || "";
        data.op = d.op || "eq";
        data.value = coerce(d.value);
      }
      if (kind === "setEvent") data.event = d.event || "";
      if (kind === "setField") {
        data.key = d.key || "";
        data.value = coerce(d.value);
      }
      return { id: n.id, type: kind, data, position: n.position };
    }),
    edges: edges.map((e) => ({
      id: e.id,
      source: e.source,
      target: e.target,
      sourceHandle: (e.data?.branch as string) || undefined,
    })),
  };
}

// Convert an engine graph JSON → React Flow state.
function fromGraph(graph: any): { nodes: Node[]; edges: Edge[] } {
  const nodes: Node[] = (graph?.nodes || []).map((n: any, i: number) => {
    const kind: Kind = n.type;
    const data = { kind, ...(n.data || {}) };
    return {
      id: n.id,
      type: rfType(kind),
      position: n.position && typeof n.position.x === "number" ? n.position : { x: 80 + (i % 4) * 200, y: 60 + Math.floor(i / 4) * 120 },
      data: { ...data, label: summarize(kind, data) },
    };
  });
  const edges: Edge[] = (graph?.edges || []).map((e: any) => ({
    id: e.id || `e_${e.source}_${e.target}_${e.sourceHandle || ""}`,
    source: e.source,
    target: e.target,
    label: e.sourceHandle || undefined,
    data: { branch: e.sourceHandle || "" },
    markerEnd: { type: MarkerType.ArrowClosed },
  }));
  return { nodes, edges };
}

// coerce numeric-looking strings to numbers so the engine compares them as numbers.
function coerce(v: any): any {
  if (typeof v !== "string") return v;
  const t = v.trim();
  if (t === "") return "";
  if (t === "true") return true;
  if (t === "false") return false;
  if (!isNaN(Number(t))) return Number(t);
  return v;
}

function uid(): string {
  if (typeof crypto !== "undefined" && crypto.randomUUID) return crypto.randomUUID();
  return "n_" + Math.abs(Math.floor(Math.random() * 1e9)).toString(36);
}

const STARTER = {
  nodes: [
    { id: "input", type: "input", data: {}, position: { x: 80, y: 40 } },
    { id: "out", type: "output", data: {}, position: { x: 80, y: 320 } },
  ],
  edges: [],
};

export default function WorkflowEditor() {
  const list = useFetch<{ unit: string; workflows: WorkflowSummary[] }>("workflows");
  const unit = list.data?.unit ?? "howen";

  const [model, setModel] = useState("");
  const [name, setName] = useState("");
  const [isActive, setIsActive] = useState(true);
  const [nodes, setNodes, onNodesChange] = useNodesState([]);
  const [edges, setEdges, onEdgesChange] = useEdgesState([]);
  const [selNode, setSelNode] = useState<string | null>(null);
  const [selEdge, setSelEdge] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [status, setStatus] = useState<string | null>(null);
  const [loaded, setLoaded] = useState(false);

  const onConnect = useCallback(
    (c: Connection) => {
      const src = nodes.find((n) => n.id === c.source);
      const kind: Kind | undefined = src?.data?.kind;
      let branch = "";
      if (kind === "condition") {
        const hasTrue = edges.some((e) => e.source === c.source && e.data?.branch === "true");
        branch = hasTrue ? "false" : "true";
      }
      setEdges((eds) =>
        addEdge({ ...c, id: uid(), label: branch || undefined, data: { branch }, markerEnd: { type: MarkerType.ArrowClosed } }, eds),
      );
    },
    [nodes, edges, setEdges],
  );

  const onSelectionChange = useCallback((p: OnSelectionChangeParams) => {
    setSelNode(p.nodes[0]?.id ?? null);
    setSelEdge(p.edges[0]?.id ?? null);
  }, []);

  function addNode(kind: Kind) {
    const id = uid();
    const data: any = { kind };
    if (kind === "condition") data.op = "eq";
    const count = nodes.length;
    setNodes((ns) => [
      ...ns,
      {
        id,
        type: rfType(kind),
        position: { x: 320 + (count % 3) * 40, y: 120 + (count % 6) * 50 },
        data: { ...data, label: summarize(kind, data) },
      },
    ]);
  }

  function patchNode(id: string, patch: any) {
    setNodes((ns) =>
      ns.map((n) => {
        if (n.id !== id) return n;
        const data = { ...n.data, ...patch };
        return { ...n, data: { ...data, label: summarize(data.kind, data) } };
      }),
    );
  }

  function patchEdgeBranch(id: string, branch: string) {
    setEdges((es) => es.map((e) => (e.id === id ? { ...e, label: branch || undefined, data: { branch } } : e)));
  }

  function removeSelected() {
    if (selNode) setNodes((ns) => ns.filter((n) => n.id !== selNode));
    if (selNode) setEdges((es) => es.filter((e) => e.source !== selNode && e.target !== selNode));
    if (selEdge) setEdges((es) => es.filter((e) => e.id !== selEdge));
    setSelNode(null);
    setSelEdge(null);
  }

  async function load(m: string) {
    setError(null);
    setStatus(null);
    setModel(m);
    try {
      const wf = await api<any>(`workflows/${encodeURIComponent(m)}`);
      setName(wf.name || "");
      setIsActive(!!wf.is_active);
      const g = fromGraph(wf.graph);
      setNodes(g.nodes);
      setEdges(g.edges);
      setLoaded(true);
    } catch (e: any) {
      // No workflow yet for this model → start from a blank template.
      if (String(e.message).includes("no workflow")) {
        const g = fromGraph(STARTER);
        setNodes(g.nodes);
        setEdges(g.edges);
        setName("");
        setIsActive(true);
        setLoaded(true);
        setStatus("New workflow — not saved yet.");
      } else {
        setError(e.message || "Load failed");
      }
    }
  }

  async function save() {
    setError(null);
    setStatus(null);
    if (!model.trim()) {
      setError("Enter or pick a model first.");
      return;
    }
    try {
      await api(`workflows/${encodeURIComponent(model.trim())}`, {
        method: "PUT",
        body: JSON.stringify({ name, is_active: isActive, graph: toGraph(nodes, edges) }),
      });
      setStatus("Saved. The gateway reloaded it instantly.");
      list.refresh();
    } catch (e: any) {
      setError(e.message || "Save failed");
    }
  }

  async function del() {
    if (!model.trim() || !confirm(`Delete the workflow for ${model}? That model reverts to the code table.`)) return;
    try {
      await api(`workflows/${encodeURIComponent(model.trim())}`, { method: "DELETE" });
      setStatus("Deleted.");
      setLoaded(false);
      setNodes([]);
      setEdges([]);
      list.refresh();
    } catch (e: any) {
      setError(e.message || "Delete failed");
    }
  }

  const selectedNode = useMemo(() => nodes.find((n) => n.id === selNode) || null, [nodes, selNode]);
  const selectedEdge = useMemo(() => edges.find((e) => e.id === selEdge) || null, [edges, selEdge]);
  const edgeSourceKind: Kind | undefined = useMemo(() => {
    if (!selectedEdge) return undefined;
    return nodes.find((n) => n.id === selectedEdge.source)?.data?.kind;
  }, [selectedEdge, nodes]);

  return (
    <div>
      <EventCodeDatalist id="event-codes" />
      <ErrorBanner message={error || list.error} />
      {status && <div className="mb-4 rounded-md border border-emerald-500/40 bg-emerald-500/10 px-3 py-2 text-sm text-emerald-200">{status}</div>}

      {/* Toolbar: model selection + save/delete */}
      <div className="card mb-4">
        <div className="flex flex-wrap items-end gap-3">
          <div className="min-w-[180px]">
            <label className="text-xs text-slate-400">Model</label>
            <input
              className="input mt-1"
              list="wf-models"
              value={model}
              onChange={(e) => setModel(e.target.value)}
              placeholder="e.g. Hero-MC30-02"
            />
            <datalist id="wf-models">
              {(list.data?.workflows ?? []).map((w) => (
                <option key={w.model} value={w.model} />
              ))}
            </datalist>
          </div>
          <button className="btn-ghost" onClick={() => model.trim() && load(model.trim())} disabled={!model.trim()}>
            Load / New
          </button>
          <div className="grow" />
          <label className="flex items-center gap-2 text-sm text-slate-300">
            <input type="checkbox" checked={isActive} onChange={(e) => setIsActive(e.target.checked)} /> Active
          </label>
          <div>
            <label className="text-xs text-slate-400">Name</label>
            <input className="input mt-1 w-40" value={name} onChange={(e) => setName(e.target.value)} placeholder="optional" />
          </div>
          <button className="btn-primary" onClick={save} disabled={!loaded}>
            Save
          </button>
          <button className="btn-danger" onClick={del} disabled={!loaded}>
            Delete
          </button>
        </div>

        {(list.data?.workflows?.length ?? 0) > 0 && (
          <div className="mt-3 flex flex-wrap gap-2">
            {(list.data?.workflows ?? []).map((w) => (
              <button key={w.model} onClick={() => load(w.model)} className="rounded-full border border-edge bg-ink px-3 py-1 text-xs hover:bg-edge">
                <span className="font-mono">{w.model}</span>{" "}
                <Badge tone={w.is_active ? "green" : "slate"}>{w.node_count}n/{w.edge_count}e</Badge>
              </button>
            ))}
          </div>
        )}
      </div>

      {!loaded ? (
        <div className="card text-center text-sm text-slate-400">
          Pick a model above and click <span className="text-slate-200">Load / New</span> to start a per-model workflow.
        </div>
      ) : (
        <div className="grid grid-cols-1 gap-4 lg:grid-cols-[1fr_320px]">
          {/* Canvas */}
          <div className="card p-0" style={{ height: 520 }}>
            <ReactFlow
              nodes={nodes}
              edges={edges}
              onNodesChange={onNodesChange}
              onEdgesChange={onEdgesChange}
              onConnect={onConnect}
              onSelectionChange={onSelectionChange}
              fitView
              proOptions={{ hideAttribution: true }}
            >
              <Background color="#1e293b" gap={16} />
              <Controls />
            </ReactFlow>
          </div>

          {/* Side panel: palette + inspector */}
          <div className="space-y-4">
            <div className="card">
              <h3 className="mb-2 text-sm font-semibold text-slate-300">Add node</h3>
              <div className="grid grid-cols-2 gap-2">
                {(["switch", "condition", "setEvent", "setField", "output", "input"] as Kind[]).map((k) => (
                  <button key={k} className="btn-ghost" onClick={() => addNode(k)}>
                    {KIND_LABEL[k]}
                  </button>
                ))}
              </div>
              <p className="mt-2 text-xs text-slate-500">Drag from a node’s bottom dot to another node’s top dot to connect.</p>
            </div>

            {selectedNode && (
              <NodeInspector node={selectedNode} onPatch={(p) => patchNode(selectedNode.id, p)} onDelete={removeSelected} />
            )}
            {selectedEdge && (
              <EdgeInspector
                branch={(selectedEdge.data?.branch as string) || ""}
                sourceKind={edgeSourceKind}
                onSet={(b) => patchEdgeBranch(selectedEdge.id, b)}
                onDelete={removeSelected}
              />
            )}
            {!selectedNode && !selectedEdge && (
              <div className="card text-xs text-slate-500">Select a node or edge to edit it.</div>
            )}

            <TestPanel graph={() => toGraph(nodes, edges)} />
          </div>
        </div>
      )}
    </div>
  );
}

function NodeInspector({ node, onPatch, onDelete }: { node: Node; onPatch: (p: any) => void; onDelete: () => void }) {
  const kind: Kind = node.data.kind;
  const d = node.data;
  return (
    <div className="card space-y-3">
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-semibold text-slate-300">{KIND_LABEL[kind]}</h3>
        <button className="text-xs text-rose-300 hover:underline" onClick={onDelete}>
          delete
        </button>
      </div>
      {kind === "switch" && (
        <Field label="Field (dot-path)" value={d.field || ""} onChange={(v) => onPatch({ field: v })} placeholder="eventCode or detail.tp" />
      )}
      {kind === "condition" && (
        <>
          <Field label="Field" value={d.field || ""} onChange={(v) => onPatch({ field: v })} placeholder="speed" />
          <div>
            <label className="text-xs text-slate-400">Operator</label>
            <select className="input mt-1" value={d.op || "eq"} onChange={(e) => onPatch({ op: e.target.value })}>
              {["eq", "ne", "gt", "gte", "lt", "lte", "contains", "exists"].map((o) => (
                <option key={o} value={o}>
                  {o}
                </option>
              ))}
            </select>
          </div>
          {d.op !== "exists" && <Field label="Value" value={d.value ?? ""} onChange={(v) => onPatch({ value: v })} placeholder="0" />}
        </>
      )}
      {kind === "setEvent" && (
        <Field label="Event code" value={d.event || ""} onChange={(v) => onPatch({ event: v })} placeholder="AI:CELLPHONE" list="event-codes" />
      )}
      {kind === "setField" && (
        <>
          <Field label="Field key" value={d.key || ""} onChange={(v) => onPatch({ key: v })} placeholder="severity" />
          <Field label="Value" value={d.value ?? ""} onChange={(v) => onPatch({ value: v })} placeholder="high" />
        </>
      )}
      {(kind === "input" || kind === "output") && <p className="text-xs text-slate-500">No configuration.</p>}
    </div>
  );
}

function EdgeInspector({
  branch,
  sourceKind,
  onSet,
  onDelete,
}: {
  branch: string;
  sourceKind?: Kind;
  onSet: (b: string) => void;
  onDelete: () => void;
}) {
  const caseVal = branch.startsWith("case:") ? branch.slice(5) : "";
  return (
    <div className="card space-y-3">
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-semibold text-slate-300">Edge branch</h3>
        <button className="text-xs text-rose-300 hover:underline" onClick={onDelete}>
          delete
        </button>
      </div>
      {sourceKind === "condition" ? (
        <select className="input" value={branch || "true"} onChange={(e) => onSet(e.target.value)}>
          <option value="true">true</option>
          <option value="false">false</option>
        </select>
      ) : sourceKind === "switch" ? (
        <div className="space-y-2">
          <Field
            label="Case value (field equals)"
            value={caseVal}
            onChange={(v) => onSet(v ? `case:${v}` : "")}
            placeholder="34"
          />
          <button className="btn-ghost w-full" onClick={() => onSet("default")}>
            Use as default branch
          </button>
          <p className="text-xs text-slate-500">Current: <span className="font-mono">{branch || "(unset)"}</span></p>
        </div>
      ) : (
        <p className="text-xs text-slate-500">This edge leaves a non-branching node; no label needed.</p>
      )}
    </div>
  );
}

function TestPanel({ graph }: { graph: () => any }) {
  const [payload, setPayload] = useState('{\n  "eventCode": 30,\n  "detail": { "tp": 34 }\n}');
  const [result, setResult] = useState<any>(null);
  const [err, setErr] = useState<string | null>(null);

  async function run() {
    setErr(null);
    setResult(null);
    let parsed: any;
    try {
      parsed = JSON.parse(payload);
    } catch {
      setErr("Payload is not valid JSON");
      return;
    }
    try {
      const res = await api<any>("workflows/test", {
        method: "POST",
        body: JSON.stringify({ graph: graph(), payload: parsed }),
      });
      setResult(res);
    } catch (e: any) {
      setErr(e.message || "Test failed");
    }
  }

  return (
    <div className="card space-y-2">
      <h3 className="text-sm font-semibold text-slate-300">Test run</h3>
      <textarea className="input font-mono text-xs" rows={5} value={payload} onChange={(e) => setPayload(e.target.value)} />
      <button className="btn-primary w-full" onClick={run}>
        Run against sample
      </button>
      {err && <div className="text-xs text-rose-300">{err}</div>}
      {result && (
        <div className="rounded-md border border-edge bg-ink p-2 text-xs">
          <div>
            matched: <span className={result.matched ? "text-emerald-300" : "text-rose-300"}>{String(result.matched)}</span>
          </div>
          <div className="mt-1">
            events:{" "}
            {(result.events || []).length ? (
              (result.events as string[]).map((e) => (
                <span key={e} className="mr-1 font-mono text-indigo-300">
                  {e}
                </span>
              ))
            ) : (
              <span className="text-slate-500">none</span>
            )}
          </div>
          <div className="mt-1 text-slate-500">trace: {(result.trace || []).join(" → ")}</div>
        </div>
      )}
    </div>
  );
}

function Field({
  label,
  value,
  onChange,
  placeholder,
  list,
}: {
  label: string;
  value: any;
  onChange: (v: string) => void;
  placeholder?: string;
  list?: string;
}) {
  return (
    <div>
      <label className="text-xs text-slate-400">{label}</label>
      <input className="input mt-1" list={list} value={value} onChange={(e) => onChange(e.target.value)} placeholder={placeholder} />
    </div>
  );
}
