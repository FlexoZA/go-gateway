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

  const text = await res.text();
  return new NextResponse(text, {
    status: res.status,
    headers: { "Content-Type": res.headers.get("Content-Type") || "application/json" },
  });
}

export const GET = handle;
export const POST = handle;
export const PUT = handle;
export const DELETE = handle;
export const PATCH = handle;

export const dynamic = "force-dynamic";
