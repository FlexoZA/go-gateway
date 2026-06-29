"use client";

// useGatewayInfo fetches the gateway's hosted unit types and each one's
// capabilities (GET /api/gateway/info) and caches it at module scope. Each unit
// reports `capabilities` (EFFECTIVE — what's on now, after operator disable-toggles)
// which drives UI gating, and `supported` (what the unit's protocol can do) which
// drives which toggles to show. refreshGatewayInfo() re-fetches and notifies every
// mounted consumer, so toggling a capability updates the whole admin live.
//
// Gate UI with `caps?.has_x !== false`: while loading (undefined) the feature stays
// shown, and only disappears once we positively learn it's absent.

import { useEffect, useState } from "react";
import { api } from "@/lib/api";

export type Caps = {
  has_video: boolean;
  has_commands: boolean;
  has_config: boolean;
  has_status: boolean;
  has_clips: boolean;
  has_mappings: boolean;
  has_snapshots: boolean;
};

// SettingField mirrors gateway.SettingField — a unit's editable settings schema.
export type SettingField = {
  key: string;
  label: string;
  type: "string" | "number" | "bool" | "select";
  default: string;
  options?: string[];
  help?: string;
  group?: string;
};

export type Unit = { unit: string; capabilities: Caps; supported?: Caps; schema?: SettingField[] };

// GatewayInfo is the /api/gateway/info response: a units[] array plus back-compat
// scalar `unit`/`capabilities` (the first unit) for older single-unit panels.
export type GatewayInfo = { unit?: string; capabilities?: Caps; units?: Unit[] };

let cache: GatewayInfo | null = null;
let inflight: Promise<GatewayInfo> | null = null;
const listeners = new Set<() => void>();

function load(force = false): Promise<GatewayInfo> {
  if (force) {
    cache = null;
    inflight = null;
  }
  inflight ??= api<GatewayInfo>("gateway/info").then((d) => {
    cache = d;
    listeners.forEach((l) => l());
    return d;
  });
  return inflight;
}

// refreshGatewayInfo re-fetches gateway info and notifies all consumers (call after
// changing a capability toggle so the UI reflects it immediately).
export function refreshGatewayInfo() {
  load(true).catch(() => {});
}

export function useGatewayInfo(): GatewayInfo | null {
  const [info, setInfo] = useState<GatewayInfo | null>(cache);
  useEffect(() => {
    const update = () => setInfo(cache);
    listeners.add(update);
    load().then(update).catch(() => {});
    return () => {
      listeners.delete(update);
    };
  }, []);
  return info;
}

// normalizeUnits returns the units[] list, synthesizing it from the back-compat
// scalar fields when an older gateway only returns a single unit.
export function normalizeUnits(info: GatewayInfo | null): Unit[] {
  if (!info) return [];
  if (info.units && info.units.length) return info.units;
  if (info.unit && info.capabilities) return [{ unit: info.unit, capabilities: info.capabilities }];
  return [];
}

// useUnits returns the hosted unit types ([] until loaded).
export function useUnits(): Unit[] {
  return normalizeUnits(useGatewayInfo());
}

// capsForUnit returns a unit type's EFFECTIVE capabilities (undefined until loaded
// or for an unknown unit).
export function capsForUnit(info: GatewayInfo | null, unit?: string): Caps | undefined {
  if (!unit) return undefined;
  return normalizeUnits(info).find((u) => u.unit === unit)?.capabilities;
}

// useAggregateCaps unions effective capabilities across all hosted units: has_x is
// true if ANY unit offers it. Used by the nav. Undefined until loaded.
export function useAggregateCaps(): Caps | undefined {
  const units = useUnits();
  if (units.length === 0) return undefined;
  const some = (k: keyof Caps) => units.some((u) => u.capabilities?.[k]);
  return {
    has_video: some("has_video"),
    has_commands: some("has_commands"),
    has_config: some("has_config"),
    has_status: some("has_status"),
    has_clips: some("has_clips"),
    has_mappings: some("has_mappings"),
    has_snapshots: some("has_snapshots"),
  };
}
