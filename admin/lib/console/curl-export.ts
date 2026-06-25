import type { ResolvedRequest } from "./client";

// Snippets target the gateway path through the admin BFF proxy. The gateway
// service key is injected server-side, so it never appears in the snippet —
// these are meant for copy/paste against the admin app itself (with a session
// cookie), mirroring exactly what the console sends.

function shellEscape(value: string): string {
  return `'${value.replace(/'/g, "'\\''")}'`;
}

/** A curl hitting the admin's /api/gw proxy (which attaches the key server-side). */
export function toCurl(r: ResolvedRequest): string {
  const url = `/api/gw${r.path.replace(/^\/api/, "")}`;
  const parts: string[] = ["curl"];
  if (r.method !== "GET") parts.push("-X", r.method);
  parts.push(shellEscape(url));
  for (const [k, v] of Object.entries(r.headers)) {
    parts.push("-H", shellEscape(`${k}: ${v}`));
  }
  if (r.body !== null && r.body !== "") {
    parts.push("--data-raw", shellEscape(r.body));
  }
  return parts.join(" ");
}

export function toFetch(r: ResolvedRequest): string {
  const url = `/api/gw${r.path.replace(/^\/api/, "")}`;
  const init: Record<string, unknown> = { method: r.method };
  if (Object.keys(r.headers).length > 0) init.headers = r.headers;
  if (r.body !== null && r.body !== "") init.body = r.body;
  return `await fetch(${JSON.stringify(url)}, ${JSON.stringify(init, null, 2)});`;
}
