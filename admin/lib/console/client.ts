"use client";

/**
 * Client-side request resolution + sending for the API Console.
 *
 * Requests are POSTed to /api/console (the BFF console proxy), which forwards
 * them to the gateway with the service key attached and returns a rich envelope.
 * We never call the gateway directly from the browser.
 */

import type { ConsoleRequest, ConsoleResponse, Environment } from "./types";
import {
  activeKv,
  buildVariableMap,
  substitute,
  substitutePathParams,
} from "./variables";

export interface SendOptions {
  request: ConsoleRequest;
  environment: Environment | null;
  signal?: AbortSignal;
}

export interface ResolvedRequest {
  method: string;
  /** Absolute gateway path including the query string. */
  path: string;
  headers: Record<string, string>;
  body: string | null;
}

function appendQuery(path: string, params: Array<[string, string]>): string {
  if (params.length === 0) return path;
  const hasQ = path.includes("?");
  const qs = params
    .map(([k, v]) => `${encodeURIComponent(k)}=${encodeURIComponent(v)}`)
    .join("&");
  return `${path}${hasQ ? "&" : "?"}${qs}`;
}

export function resolveRequest({
  request,
  environment,
}: Omit<SendOptions, "signal">): ResolvedRequest {
  const vars = buildVariableMap(environment);

  // Two passes: `{{env vars}}` first, then `{pathParams}`. Unresolved
  // placeholders are left intact so the UI can flag them.
  const pathAfterVars = substitute(request.path, vars);
  const pathAfterSub = substitutePathParams(pathAfterVars, request.pathParams);
  const params = activeKv(request.params).map<[string, string]>((p) => [
    substitute(p.key, vars),
    substitute(p.value, vars),
  ]);
  const path = appendQuery(pathAfterSub, params);

  const headers: Record<string, string> = {};
  for (const h of activeKv(request.headers)) {
    headers[substitute(h.key, vars)] = substitute(h.value, vars);
  }

  let body: string | null = null;
  const method = request.method.toUpperCase();
  // Allow bodies on everything except GET — some gateway DELETE endpoints
  // (e.g. /api/mappings) read a JSON body.
  if (method !== "GET" && request.body.mode !== "none") {
    body = substitute(request.body.text, vars);
    if (request.body.mode === "json") {
      if (!Object.keys(headers).some((k) => k.toLowerCase() === "content-type")) {
        headers["Content-Type"] = "application/json";
      }
    } else if (request.body.mode === "form") {
      if (!Object.keys(headers).some((k) => k.toLowerCase() === "content-type")) {
        headers["Content-Type"] = "application/x-www-form-urlencoded";
      }
    }
  }

  return { method, path, headers, body };
}

export async function sendRequest(options: SendOptions): Promise<ConsoleResponse> {
  const startedAt = new Date().toISOString();
  const resolved = resolveRequest(options);

  try {
    const res = await fetch("/api/console", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        method: resolved.method,
        path: resolved.path,
        headers: resolved.headers,
        body: resolved.body,
      }),
      signal: options.signal,
    });

    if (res.status === 401) {
      if (typeof window !== "undefined") window.location.href = "/login";
      throw new Error("unauthenticated");
    }

    // The proxy returns the ConsoleResponse envelope as JSON (HTTP 200).
    return (await res.json()) as ConsoleResponse;
  } catch (error) {
    const message =
      error instanceof Error ? `${error.name}: ${error.message}` : String(error);
    return {
      status: 0,
      statusText: "Request failed",
      ok: false,
      durationMs: 0,
      sizeBytes: 0,
      headers: [],
      json: null,
      rawText: "",
      networkError: message,
      startedAt,
    };
  }
}

/**
 * Fetch a list endpoint through the console proxy — used by path-param pickers.
 * Returns the parsed JSON (or null) and never throws for non-2xx; callers
 * extract the array they expect.
 */
export async function fetchViaProxy(
  path: string,
  signal?: AbortSignal,
): Promise<unknown | null> {
  const res = await fetch("/api/console", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ method: "GET", path }),
    signal,
  });
  if (res.status === 401) {
    if (typeof window !== "undefined") window.location.href = "/login";
    throw new Error("unauthenticated");
  }
  const envelope = (await res.json()) as ConsoleResponse;
  return envelope.json;
}
