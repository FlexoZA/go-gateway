import { NextResponse, type NextRequest } from "next/server";
import { gatewayConfigured, gatewayFetch } from "@/lib/gateway";

// BFF proxy: every browser call to /api/gw/<path> is forwarded to the gateway's
// /api/<path> with the server-held API key attached. The session is already
// enforced by middleware, so reaching here means the caller is authenticated.
async function handle(req: NextRequest, ctx: { params: { path?: string[] } }) {
  if (!gatewayConfigured()) {
    return NextResponse.json({ error: "gateway API key not configured" }, { status: 503 });
  }

  const path = (ctx.params.path || []).map(encodeURIComponent).join("/");
  const target = `/api/${path}${req.nextUrl.search}`;

  const init: RequestInit = { method: req.method };
  if (req.method !== "GET" && req.method !== "HEAD") {
    const body = await req.text();
    if (body) init.body = body;
  }

  let res: Response;
  try {
    res = await gatewayFetch(target, init);
  } catch {
    return NextResponse.json({ error: "gateway unreachable" }, { status: 502 });
  }

  // Pass the body through as bytes — JSON *and* binary (HLS .ts video segments,
  // which res.text() would corrupt). Forward the content-type and cache headers.
  const headers = new Headers();
  const ct = res.headers.get("Content-Type");
  headers.set("Content-Type", ct || "application/json");
  const cc = res.headers.get("Cache-Control");
  if (cc) headers.set("Cache-Control", cc);

  const body = await res.arrayBuffer();
  return new NextResponse(body, { status: res.status, headers });
}

export const GET = handle;
export const POST = handle;
export const PUT = handle;
export const DELETE = handle;
export const PATCH = handle;

export const dynamic = "force-dynamic";
