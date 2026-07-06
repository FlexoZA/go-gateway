import { NextResponse, type NextRequest } from "next/server";
import { gatewayConfigured, gatewayFetch } from "@/lib/gateway";
import { sessionFromRequest } from "@/lib/session";

// BFF proxy: every browser call to /api/gw/<path> is forwarded to the gateway's
// /api/<path> with the server-held API key attached. Middleware enforces the
// session, but we re-check it here so a middleware bypass can never reach the
// gateway with the service token (defense in depth — see sessionFromRequest).
async function handle(req: NextRequest, ctx: { params: { path?: string[] } }) {
  if (!(await sessionFromRequest(req))) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

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
  // Forward range headers so the browser can seek within streamed media (clip
  // <video> playback) and resume partial downloads. Only these are relayed — the
  // gateway auth is the server-held key, not anything from the browser request.
  const fwd = new Headers();
  for (const h of ["Range", "If-Range"]) {
    const v = req.headers.get(h);
    if (v) fwd.set(h, v);
  }
  init.headers = fwd;

  let res: Response;
  try {
    res = await gatewayFetch(target, init);
  } catch {
    return NextResponse.json({ error: "gateway unreachable" }, { status: 502 });
  }

  // Forward the response as-is: JSON *and* binary (HLS .ts segments, clip/backup
  // downloads). Stream the body through rather than buffering it in Node memory, and
  // forward the headers a download needs — notably Content-Disposition, without which
  // a clip/backup opens inline instead of downloading with its filename.
  const headers = new Headers();
  const ct = res.headers.get("Content-Type");
  headers.set("Content-Type", ct || "application/json");
  for (const h of ["Cache-Control", "Content-Disposition", "Content-Length", "Accept-Ranges", "Content-Range"]) {
    const v = res.headers.get(h);
    if (v) headers.set(h, v);
  }
  return new NextResponse(res.body, { status: res.status, headers });
}

export const GET = handle;
export const POST = handle;
export const PUT = handle;
export const DELETE = handle;
export const PATCH = handle;

export const dynamic = "force-dynamic";
