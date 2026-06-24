"use client";

// useGatewayInfo fetches the gateway's hosted unit types and each one's effective
// capabilities (GET /api/gateway/info) once per session and caches it at module
// scope. The gateway hosts one OR MORE unit types; this exposes both the per-unit
// view (capsForUnit) and a union view (useAggregateCaps) the nav uses to show a
// link when ANY unit supports the feature.
//
// Gate UI with `caps?.has_x !== false`: while loading (undefined) the feature stays
// shown, and only disappears once we positively learn it's absent — avoiding a
// flash of missing nav and degrading gracefully if the call ever fails.

import { useEffect, useState } from "react";
import { api } from "@/lib/api";

export type Caps = {
  has_video: boolean;
  has_commands: boolean;
  has_config: boolean;
  has_status: boolean;
  has_clips: boolean;
  has_mappings: boolean;
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

export type Unit = { unit: string; capabilities: Caps; schema?: SettingField[] };

// GatewayInfo is the /api/gateway/info response: a units[] array plus back-compat
// scalar `unit`/`capabilities` (the first unit) for older single-unit panels.
export type GatewayInfo = { unit?: string; capabilities?: Caps; units?: Unit[] };

let cache: GatewayInfo | null = null;
let inflight: Promise<GatewayInfo> | null = null;

export function useGatewayInfo(): GatewayInfo | null {
  const [info, setInfo] = useState<GatewayInfo | null>(cache);
  useEffect(() => {
    if (cache) return;
    inflight ??= api<GatewayInfo>("gateway/info").then((d) => (cache = d));
    inflight.then(setInfo).catch(() => {});
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

// capsForUnit returns a specific unit type's capabilities (undefined until loaded
// or for an unknown unit).
export function capsForUnit(info: GatewayInfo | null, unit?: string): Caps | undefined {
  if (!unit) return undefined;
  return normalizeUnits(info).find((u) => u.unit === unit)?.capabilities;
}

// useAggregateCaps unions capabilities across all hosted units: has_x is true if
// ANY unit offers it. Used by the nav so a feature link shows when at least one
// unit supports it. Undefined until loaded.
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
  };
}
