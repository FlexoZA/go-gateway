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
// or null when the unit has no editable device config.
export function deviceConfigSchema(unit?: string): DeviceConfigSchema | null {
  if (!unit) return null;
  return registry[unit] ?? null;
}
