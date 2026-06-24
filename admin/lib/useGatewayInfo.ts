"use client";

// useGatewayInfo fetches this gateway's unit type and effective capabilities
// (GET /api/gateway/info) once per session and caches it at module scope, so the
// nav and pages can hide UI for features this build/config doesn't offer (e.g. a
// GPS-only gateway has no Clips/video/config). Returns null until loaded.
//
// Gate UI with `caps?.has_x !== false`: while loading (null) the feature stays
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

export type GatewayInfo = { unit: string; capabilities: Caps };

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
