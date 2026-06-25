import type { Environment, KeyValueEntry, PathParamBinding } from "./types";

/** Build the `{{var}}` substitution map from an environment's variables. */
export function buildVariableMap(env: Environment | null): Record<string, string> {
  const map: Record<string, string> = {};
  if (!env) return map;
  for (const v of env.variables) {
    if (!v.enabled) continue;
    if (!v.key) continue;
    map[v.key] = v.value;
  }
  return map;
}

const VAR_PATTERN = /\{\{\s*([\w.-]+)\s*\}\}/g;

export function substitute(input: string, vars: Record<string, string>): string {
  if (!input) return input;
  return input.replace(VAR_PATTERN, (_match, key: string) => {
    if (key in vars) return vars[key];
    return _match;
  });
}

export function unresolvedVariables(input: string, vars: Record<string, string>): string[] {
  const missing = new Set<string>();
  for (const m of input.matchAll(VAR_PATTERN)) {
    const key = m[1];
    if (!(key in vars)) missing.add(key);
  }
  return [...missing];
}

export function activeKv(entries: KeyValueEntry[]): KeyValueEntry[] {
  return entries.filter((e) => e.enabled && e.key.trim() !== "");
}

/**
 * Single-brace path placeholders, e.g. `{serial}`, `{id}`. The lookbehind /
 * lookahead exclude doubled braces so an unresolved `{{var}}` isn't mistaken for
 * a path placeholder.
 */
const PATH_PARAM_PATTERN = /(?<!\{)\{([A-Za-z_][\w-]*)\}(?!\})/g;

/** Replace `{name}` placeholders with values from path-param bindings. */
export function substitutePathParams(
  input: string,
  bindings: PathParamBinding[] | undefined,
): string {
  if (!input) return input;
  if (!bindings || bindings.length === 0) return input;
  const map = new Map<string, string>();
  for (const b of bindings) {
    if (b.value !== undefined && b.value !== "") map.set(b.name, b.value);
  }
  if (map.size === 0) return input;
  return input.replace(PATH_PARAM_PATTERN, (match, key: string) => {
    const v = map.get(key);
    return v === undefined ? match : encodeURIComponent(v);
  });
}

/** List of `{name}` placeholders present in a string (after var substitution). */
export function pathPlaceholdersIn(input: string): string[] {
  const names = new Set<string>();
  for (const m of input.matchAll(PATH_PARAM_PATTERN)) {
    names.add(m[1]);
  }
  return [...names];
}

/** Subset of placeholders that still lack a binding value. */
export function unresolvedPathParams(
  input: string,
  bindings: PathParamBinding[] | undefined,
): string[] {
  const present = pathPlaceholdersIn(input);
  if (present.length === 0) return [];
  const filled = new Set(
    (bindings ?? [])
      .filter((b) => b.value !== undefined && b.value !== "")
      .map((b) => b.name),
  );
  return present.filter((n) => !filled.has(n));
}
