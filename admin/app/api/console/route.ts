import { NextResponse, type NextRequest } from "next/server";
import { gatewayConfigured, gatewayFetch } from "@/lib/gateway";

// Dedicated proxy for the API Console. Unlike /api/gw/[...path] (which mirrors a
// single URL path), this accepts a full request *spec* in the POST body so the
// console can set an arbitrary method, custom headers, and a body, and get back
// rich metadata (status, timing, size, response headers). The gateway API key is
// attached server-side by gatewayFetch — the browser never sees it. Session is
// already enforced by middleware, so reaching here means the caller is authed.

interface ConsoleSpec {
  method?: string;
  path?: string;
  headers?: Record<string, string>;
  body?: string | null;
}

interface ConsoleResponse {
  status: number;
  statusText: string;
  ok: boolean;
  durationMs: number;
  sizeBytes: number;
  headers: Array<[string, string]>;
  json: unknown | null;
  rawText: string;
  networkError?: string;
  startedAt: string;
}

// Build a uniform envelope. The handler always returns HTTP 200 with one of
// these (except the 401 produced by middleware), so the client reads results the
// same way whether the gateway answered, errored, or was unreachable.
function envelope(partial: Partial<ConsoleResponse>, startedAt: string): ConsoleResponse {
  return {
    status: 0,
    statusText: "",
    ok: false,
    durationMs: 0,
    sizeBytes: 0,
    headers: [],
    json: null,
    rawText: "",
    startedAt,
    ...partial,
  };
}

const ALLOWED_METHODS = new Set(["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"]);

export async function POST(req: NextRequest) {
  const startedAt = new Date().toISOString();

  let spec: ConsoleSpec;
  try {
    spec = (await req.json()) as ConsoleSpec;
  } catch {
    return NextResponse.json(
      envelope({ networkError: "invalid request spec (not JSON)" }, startedAt),
    );
  }

  const method = (spec.method || "GET").toUpperCase();
  if (!ALLOWED_METHODS.has(method)) {
    return NextResponse.json(
      envelope({ networkError: `unsupported method: ${method}` }, startedAt),
    );
  }

  // Guard: only allow paths into the gateway's own /api/ surface. Reject absolute
  // URLs and traversal so this proxy can never be pointed off the gateway host.
  const path = spec.path || "";
  if (!path.startsWith("/api/") || path.includes("..") || /^https?:\/\//i.test(path)) {
    return NextResponse.json(
      envelope(
        { networkError: `path must start with /api/ (got: ${path || "empty"})` },
        startedAt,
      ),
    );
  }

  if (!gatewayConfigured()) {
    return NextResponse.json(
      envelope({ networkError: "gateway API key not configured" }, startedAt),
    );
  }

  const init: RequestInit = { method };
  if (spec.headers) init.headers = spec.headers;
  if (method !== "GET" && method !== "HEAD" && spec.body) init.body = spec.body;

  const t0 = Date.now();
  let res: Response;
  try {
    res = await gatewayFetch(path, init);
  } catch (err) {
    const message = err instanceof Error ? `${err.name}: ${err.message}` : String(err);
    return NextResponse.json(
      envelope({ networkError: `gateway unreachable — ${message}` }, startedAt),
    );
  }

  const rawText = await res.text();
  const durationMs = Date.now() - t0;

  let json: unknown | null = null;
  const contentType = res.headers.get("content-type") || "";
  if (contentType.includes("application/json") || /^[\s\n]*[[{]/.test(rawText)) {
    try {
      json = JSON.parse(rawText);
    } catch {
      json = null;
    }
  }

  const headers: Array<[string, string]> = [];
  res.headers.forEach((value, key) => headers.push([key, value]));

  return NextResponse.json(
    envelope(
      {
        status: res.status,
        statusText: res.statusText,
        ok: res.ok,
        durationMs,
        sizeBytes: Buffer.byteLength(rawText, "utf8"),
        headers,
        json,
        rawText,
      },
      startedAt,
    ),
  );
}

export const dynamic = "force-dynamic";
