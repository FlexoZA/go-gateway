// Server-only helper that calls the gateway HTTP API with the API key. This is
// the ONLY place the key is used; it is read from a server env var and never
// sent to the browser. The admin panel reaches gateway state solely through
// this API — it has no database access of its own.
import "server-only";

const GATEWAY_URL = (process.env.GATEWAY_URL || "http://localhost:8080").replace(/\/$/, "");
// The panel authenticates with the shared internal service token (preferred) so
// it works before any DB API key exists (first-run setup). GATEWAY_API_KEY is
// still accepted for backward compatibility.
const API_KEY = process.env.GATEWAY_API_TOKEN || process.env.GATEWAY_API_KEY || "";

export function gatewayConfigured(): boolean {
  return API_KEY !== "";
}

export async function gatewayFetch(path: string, init: RequestInit = {}): Promise<Response> {
  const headers = new Headers(init.headers);
  // Default to the server-held service key, but let an explicit Authorization
  // header (e.g. the API console testing a specific Bearer key) win.
  if (!headers.has("Authorization")) {
    headers.set("Authorization", `Bearer ${API_KEY}`);
  }
  if (init.body && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }
  return fetch(`${GATEWAY_URL}${path}`, { ...init, headers, cache: "no-store" });
}
