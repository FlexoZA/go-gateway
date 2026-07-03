"use client";

// useEventCodes fetches the canonical ACM Standard Event Codes (GET /api/event-codes)
// once and caches them at module scope, so every event-code dropdown shares a single
// request instead of refetching per row.

import { useEffect, useState } from "react";
import { api } from "@/lib/api";

export type EventCode = { code: string; category: string; notes: string };

let cache: EventCode[] | null = null;
let inflight: Promise<EventCode[]> | null = null;
const listeners = new Set<() => void>();

function load(force = false): Promise<EventCode[]> {
  if (force) {
    cache = null;
    inflight = null;
  }
  inflight ??= api<{ event_codes: EventCode[] }>("event-codes")
    .then((d) => {
      cache = d.event_codes ?? [];
      listeners.forEach((l) => l());
      return cache;
    })
    .catch((e) => {
      // Don't cache a rejected promise: clear inflight so a transient failure at
      // mount (gateway restart/blip) can be retried instead of permanently leaving
      // every dropdown empty until a full page reload.
      inflight = null;
      throw e;
    });
  return inflight;
}

// refreshEventCodes re-fetches the picklist and notifies every dropdown (call after
// adding a custom code so it becomes selectable everywhere).
export function refreshEventCodes() {
  load(true).catch(() => {});
}

export function useEventCodes(): EventCode[] {
  const [codes, setCodes] = useState<EventCode[]>(cache ?? []);
  useEffect(() => {
    const update = () => setCodes(cache ?? []);
    listeners.add(update);
    load().then(update).catch(() => {});
    return () => {
      listeners.delete(update);
    };
  }, []);
  return codes;
}
