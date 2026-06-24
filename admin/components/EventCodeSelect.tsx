"use client";

import { useMemo, useState } from "react";
import { useEventCodes } from "@/lib/useEventCodes";

// EventCodeSelect is a dropdown of the canonical ACM Standard Event Codes, grouped
// by category, plus an "Add custom code…" entry that switches to free text for a
// code not in the standard list. A non-standard existing value is shown as a
// "(custom)" option so it still displays and is preserved.

const CUSTOM = "__custom__";

export function EventCodeSelect({
  value,
  onChange,
  className = "input",
}: {
  value: string;
  onChange: (v: string) => void;
  className?: string;
}) {
  const codes = useEventCodes();
  const [custom, setCustom] = useState(false);

  const groups = useMemo(() => {
    const g: Record<string, string[]> = {};
    for (const c of codes) (g[c.category || "Other"] ||= []).push(c.code);
    for (const k of Object.keys(g)) g[k].sort();
    return Object.entries(g).sort(([a], [b]) => a.localeCompare(b));
  }, [codes]);

  const known = useMemo(() => new Set(codes.map((c) => c.code)), [codes]);

  // Free-text entry for a custom code. Switching back to the list keeps the value
  // (a custom value reappears as the "(custom)" option below).
  if (custom) {
    return (
      <div className="flex items-center gap-1">
        <input
          className={className}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          placeholder="e.g. COLLISION:SEVERE"
          autoFocus
        />
        <button
          type="button"
          className="btn-ghost px-2 py-1 text-xs"
          title="Choose from the standard list"
          onClick={() => setCustom(false)}
        >
          List
        </button>
      </div>
    );
  }

  return (
    <select
      className={className}
      value={value}
      onChange={(e) => {
        if (e.target.value === CUSTOM) {
          setCustom(true); // keep the current value as the starting text
        } else {
          onChange(e.target.value);
        }
      }}
    >
      <option value="">— select event code —</option>
      {value && !known.has(value) && <option value={value}>{value} (custom)</option>}
      {groups.map(([cat, list]) => (
        <optgroup key={cat} label={cat}>
          {list.map((code) => (
            <option key={code} value={code}>
              {code}
            </option>
          ))}
        </optgroup>
      ))}
      <option value={CUSTOM}>+ Add custom code…</option>
    </select>
  );
}
