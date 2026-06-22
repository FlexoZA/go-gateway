"use client";

import { useFetch } from "@/lib/useFetch";

type EventCode = { code: string; category: string; notes: string };

// EventCodeDatalist renders a <datalist> of the canonical ACM Standard Event
// Codes (served from /api/event-codes). Attach an <input list="..."> to it to
// turn a free-text field into a searchable combobox that still allows custom
// values (e.g. editing the ":x" template codes).
export function EventCodeDatalist({ id }: { id: string }) {
  const { data } = useFetch<{ event_codes: EventCode[] }>("event-codes");
  return (
    <datalist id={id}>
      {(data?.event_codes ?? []).map((c) => (
        <option key={c.code} value={c.code}>
          {c.category}
          {c.notes ? ` — ${c.notes}` : ""}
        </option>
      ))}
    </datalist>
  );
}
