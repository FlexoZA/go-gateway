"use client";

import { useState } from "react";
import { useConsole } from "@/contexts/console-context";
import { KvEditor } from "./kv-editor";
import { genId } from "@/lib/console/storage";
import type { Environment } from "@/lib/console/types";

export function EnvSwitcher() {
  const { environments, setEnvironments, activeEnv, activeEnvId, setActiveEnvId } = useConsole();
  const [editing, setEditing] = useState(false);

  function addEnv() {
    const env: Environment = { id: genId("env"), name: "New environment", variables: [] };
    setEnvironments((prev) => [...prev, env]);
    setActiveEnvId(env.id);
    setEditing(true);
  }

  function patchActive(patch: Partial<Environment>) {
    if (!activeEnv) return;
    setEnvironments((prev) => prev.map((e) => (e.id === activeEnv.id ? { ...e, ...patch } : e)));
  }

  function deleteActive() {
    if (!activeEnv) return;
    setEnvironments((prev) => prev.filter((e) => e.id !== activeEnv.id));
    setEditing(false);
  }

  return (
    <div className="space-y-2">
      <div className="flex items-center gap-2">
        <select
          value={activeEnvId ?? ""}
          onChange={(e) => setActiveEnvId(e.target.value)}
          className="input py-1 text-xs"
        >
          {environments.map((e) => (
            <option key={e.id} value={e.id}>
              {e.name}
            </option>
          ))}
        </select>
        <button
          type="button"
          onClick={() => setEditing((v) => !v)}
          className="btn-ghost text-xs"
          title="Edit variables"
        >
          {editing ? "Done" : "Vars"}
        </button>
        <button type="button" onClick={addEnv} className="btn-ghost text-xs" title="New environment">
          +
        </button>
      </div>

      {editing && activeEnv && (
        <div className="space-y-2 rounded-md border border-edge p-2">
          <input
            value={activeEnv.name}
            onChange={(e) => patchActive({ name: e.target.value })}
            className="input text-xs"
          />
          <KvEditor
            entries={activeEnv.variables}
            onChange={(variables) => patchActive({ variables })}
            keyPlaceholder="variable"
            valuePlaceholder="value"
          />
          <button type="button" onClick={deleteActive} className="text-xs text-rose-300 hover:underline">
            Delete environment
          </button>
        </div>
      )}
    </div>
  );
}
