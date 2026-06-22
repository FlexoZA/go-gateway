import { NextResponse, type NextRequest } from "next/server";
import { gatewayConfigured, gatewayFetch } from "@/lib/gateway";
import { SESSION_COOKIE, cookieSecure, createSession, sessionMaxAge } from "@/lib/session";

// POST /api/login — verify credentials against the gateway's user store, then
// issue a signed session cookie. The gateway API key stays server-side.
export async function POST(req: NextRequest) {
  if (!gatewayConfigured()) {
    return NextResponse.json({ error: "gateway API key not configured" }, { status: 503 });
  }

  let email = "";
  let password = "";
  try {
    const body = await req.json();
    email = String(body.email || "").trim();
    password = String(body.password || "");
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
    return NextResponse.json({ error: "invalid credentials" }, { status: 401 });
  }

  const token = await createSession(email);
  const out = NextResponse.json({ ok: true, email });
  out.cookies.set(SESSION_COOKIE, token, {
    httpOnly: true,
    sameSite: "lax",
    secure: cookieSecure(),
    path: "/",
    maxAge: sessionMaxAge,
  });
  return out;
}
