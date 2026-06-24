"use client";

import { useMemo } from "react";
import { useEventCodes } from "@/lib/useEventCodes";

// EventCodeSelect is a dropdown of the canonical ACM Standard Event Codes, grouped
// by category. New codes are added via the "Add event code" section (which persists
// them to the picklist); a non-standard existing value is shown as a "(custom)"
// option so it still displays and is preserved.
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

  const groups = useMemo(() => {
    const g: Record<string, string[]> = {};
    for (const c of codes) (g[c.category || "Other"] ||= []).push(c.code);
    for (const k of Object.keys(g)) g[k].sort();
    return Object.entries(g).sort(([a], [b]) => a.localeCompare(b));
  }, [codes]);

  const known = useMemo(() => new Set(codes.map((c) => c.code)), [codes]);

  return (
    <select className={className} value={value} onChange={(e) => onChange(e.target.value)}>
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
    </select>
  );
}
