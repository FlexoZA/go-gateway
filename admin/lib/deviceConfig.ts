// deviceConfig is the per-unit-type registry of device PARAMETER-config schemas
// (the Wi-Fi/mobile/recording menus shown on a device's Config tab). Each unit type
// can have a different config screen; the device's `protocol` selects its schema.
// A unit with no entry (e.g. a GPS-only tracker) has no Config tab.
//
// Curated metadata stays in TypeScript (no Go transcription); this just indexes it
// by unit type instead of hardcoding a single unit.

import {
  CATEGORIES as howenCategories,
  SEGMENTS as howenSegments,
  segmentMeta as howenSegmentMeta,
  type Category,
  type SegmentMeta,
} from "@/lib/howenConfig";

export type DeviceConfigSchema = {
  CATEGORIES: Category[];
  SEGMENTS: Record<string, SegmentMeta>;
  segmentMeta: (seg: string) => SegmentMeta;
};

const registry: Record<string, DeviceConfigSchema> = {
  howen: { CATEGORIES: howenCategories, SEGMENTS: howenSegments, segmentMeta: howenSegmentMeta },
};

// deviceConfigSchema returns the device-parameter-config schema for a unit type,
// or null when the unit has no editable device config (or uses a bespoke editor).
export function deviceConfigSchema(unit?: string): DeviceConfigSchema | null {
  if (!unit) return null;
  return registry[unit] ?? null;
}

// deviceConfigKind selects which Config-tab editor a unit uses: "generic" for the
// schema-driven DeviceConfig (Howen-style, all-string segments), "cathexis" for the
// bespoke type-aware CathexisConfig (mixed types + array segments), "n62" for the
// bespoke JT808/N62 editor (typed ULV ParamType segments), or null for a unit with
// no editable device config. Kept here (not the page) so the editor choice stays a
// registry concern; the bespoke components are imported by the page to avoid a
// lib→component import cycle.
export function deviceConfigKind(unit?: string): "generic" | "cathexis" | "n62" | null {
  if (!unit) return null;
  if (unit === "cathexis") return "cathexis";
  if (unit === "dfm-n62") return "n62"; // JT808-based N62 group (was "jt808")
  return registry[unit] ? "generic" : null;
}
