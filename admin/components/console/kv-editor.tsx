"use client";

import type { KeyValueEntry } from "@/lib/console/types";
import { genId } from "@/lib/console/storage";

interface Props {
  entries: KeyValueEntry[];
  onChange: (entries: KeyValueEntry[]) => void;
  keyPlaceholder?: string;
  valuePlaceholder?: string;
  /** Optional datalist suggestions for the key field. */
  keySuggestions?: string[];
}

export function KvEditor({
  entries,
  onChange,
  keyPlaceholder = "key",
  valuePlaceholder = "value",
  keySuggestions,
}: Props) {
  const listId = keySuggestions ? `kv-sug-${genId("dl")}` : undefined;

  function update(id: string, patch: Partial<KeyValueEntry>) {
    onChange(entries.map((e) => (e.id === id ? { ...e, ...patch } : e)));
  }
  function remove(id: string) {
    onChange(entries.filter((e) => e.id !== id));
  }
  function add() {
    onChange([...entries, { id: genId("kv"), enabled: true, key: "", value: "" }]);
  }

  return (
    <div className="space-y-1.5">
      {keySuggestions && (
        <datalist id={listId}>
          {keySuggestions.map((s) => (
            <option key={s} value={s} />
          ))}
        </datalist>
      )}
      {entries.length === 0 && (
        <p className="text-xs text-slate-500">No entries.</p>
      )}
      {entries.map((e) => (
        <div key={e.id} className="flex items-center gap-2">
          <input
            type="checkbox"
            checked={e.enabled}
            onChange={(ev) => update(e.id, { enabled: ev.target.checked })}
            className="h-4 w-4 shrink-0 accent-indigo-500"
            title={e.enabled ? "Enabled" : "Disabled"}
          />
          <input
            value={e.key}
            list={listId}
            onChange={(ev) => update(e.id, { key: ev.target.value })}
            placeholder={keyPlaceholder}
            className="input flex-1 font-mono"
          />
          <input
            value={e.value}
            onChange={(ev) => update(e.id, { value: ev.target.value })}
            placeholder={valuePlaceholder}
            className="input flex-1 font-mono"
          />
          <button
            type="button"
            onClick={() => remove(e.id)}
            className="shrink-0 rounded-md border border-edge px-2 py-1 text-xs text-slate-400 hover:bg-edge hover:text-rose-300"
            title="Remove"
          >
            ✕
          </button>
        </div>
      ))}
      <button type="button" onClick={add} className="btn-ghost text-xs">
        + Add row
      </button>
    </div>
  );
}
