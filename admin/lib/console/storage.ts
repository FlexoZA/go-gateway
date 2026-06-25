import type { Collection, ConsoleRequest, Environment } from "./types";
import { buildBuiltInCollections } from "./default-collections";

export const STORAGE_KEYS = {
  environments: "console:environments:v1",
  activeEnvId: "console:active-env:v1",
  collections: "console:collections:v1",
  history: "console:history:v1",
  sidebarTab: "console:sidebar-tab:v1",
} as const;

export const HISTORY_LIMIT = 200;

export function genId(prefix = "id"): string {
  return `${prefix}_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 8)}`;
}

export function defaultEnvironments(): Environment[] {
  return [
    {
      id: "env_default",
      name: "Default",
      variables: [
        { id: genId("var"), enabled: true, key: "serial", value: "" },
        { id: genId("var"), enabled: true, key: "unit", value: "" },
      ],
    },
  ];
}

export function emptyRequest(name = "Untitled request"): ConsoleRequest {
  return {
    id: genId("req"),
    name,
    method: "GET",
    path: "/api/",
    params: [],
    headers: [],
    body: { mode: "none", text: "" },
  };
}

export function emptyCollection(name = "New collection"): Collection {
  const now = Date.now();
  return {
    id: genId("col"),
    name,
    requests: [emptyRequest("First request")],
    createdAt: now,
    updatedAt: now,
  };
}

export function defaultCollections(): Collection[] {
  return buildBuiltInCollections();
}

export interface MergeBuiltInsResult {
  collections: Collection[];
  addedCollections: number;
  addedRequests: number;
}

/**
 * Merge built-in collections into the existing list. Identity is by id
 * (built-ins use the deterministic `col_builtin_<group>` prefix), so renaming a
 * built-in collection won't cause "Restore defaults" to re-add a duplicate.
 * Missing built-in collections are appended; existing ones get any new built-in
 * requests merged in by id. Custom collections/requests are never touched.
 */
export function mergeBuiltIns(existing: Collection[]): MergeBuiltInsResult {
  const builtIns = buildBuiltInCollections();
  const existingById = new Map(existing.map((c) => [c.id, c] as const));
  const result: Collection[] = existing.map((c) => ({ ...c }));
  let addedCollections = 0;
  let addedRequests = 0;

  for (const incoming of builtIns) {
    const current = existingById.get(incoming.id);
    if (!current) {
      result.push(incoming);
      addedCollections++;
      continue;
    }
    const haveRequestIds = new Set(current.requests.map((r) => r.id));
    const newRequests = incoming.requests.filter((r) => !haveRequestIds.has(r.id));
    if (newRequests.length === 0) continue;
    const idx = result.findIndex((c) => c.id === incoming.id);
    result[idx] = {
      ...result[idx],
      requests: [...result[idx].requests, ...newRequests],
      updatedAt: Date.now(),
    };
    addedRequests += newRequests.length;
  }
  return { collections: result, addedCollections, addedRequests };
}
