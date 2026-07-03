import { NextResponse, type NextRequest } from "next/server";
import { gatewayConfigured, gatewayFetch } from "@/lib/gateway";
import { SESSION_COOKIE, cookieSecure, createSession, maxAgeFor } from "@/lib/session";
import { clientIp, rateLimit } from "@/lib/rateLimit";

// Per-IP login throttle: brute-force / credential-stuffing speed bump on the one
// public, unauthenticated endpoint.
const LOGIN_LIMIT = 10;
const LOGIN_WINDOW_MS = 5 * 60 * 1000;

// POST /api/login — verify credentials against the gateway's user store, then
// issue a signed session cookie. The gateway API key stays server-side.
export async function POST(req: NextRequest) {
  if (!gatewayConfigured()) {
    return NextResponse.json({ error: "gateway API key not configured" }, { status: 503 });
  }

  const limit = rateLimit(`login:${clientIp(req)}`, LOGIN_LIMIT, LOGIN_WINDOW_MS);
  if (!limit.ok) {
    return NextResponse.json(
      { error: "too many login attempts; try again later" },
      { status: 429, headers: { "Retry-After": String(limit.retryAfterSec) } },
    );
  }

  let email = "";
  let password = "";
  let remember = false;
  try {
    const body = await req.json();
    email = String(body.email || "").trim();
    password = String(body.password || "");
    remember = Boolean(body.remember);
  } catch {
    return NextResponse.json({ error: "invalid request" }, { status: 400 });
  }
  if (!email || !password) {
    return NextResponse.json({ error: "email and password are required" }, { status: 400 });
  }

  let res: Response;
  try {
    res = await gatewayFetch("/api/auth/login", {
      method: "POST",
      body: JSON.stringify({ email, password }),
    });
  } catch {
    return NextResponse.json({ error: "gateway unreachable" }, { status: 502 });
  }

  if (!res.ok) {
    // 401 is a genuine bad password; a shed (429) or unavailable (5xx) gateway is
    // not — surface those distinctly so operators don't chase a phantom bad password.
    if (res.status === 429) {
      return NextResponse.json({ error: "too many login attempts; try again later" }, { status: 429 });
    }
    if (res.status >= 500) {
      return NextResponse.json({ error: "gateway error, try again" }, { status: 502 });
    }
    return NextResponse.json({ error: "invalid credentials" }, { status: 401 });
  }

  const token = await createSession(email, remember);
  const out = NextResponse.json({ ok: true, email });
  out.cookies.set(SESSION_COOKIE, token, {
    httpOnly: true,
    sameSite: "lax",
    secure: cookieSecure(),
    path: "/",
    maxAge: maxAgeFor(remember),
  });
  return out;
}
