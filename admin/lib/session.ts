// Session handling for the admin panel. The browser never sees the gateway API
// key; instead, after a successful login the BFF issues a signed JWT stored in
// an httpOnly cookie. Middleware and route handlers verify it.
import { SignJWT, jwtVerify, type JWTPayload } from "jose";

export const SESSION_COOKIE = "dgw_admin_session";

const ttlHours = Number(process.env.SESSION_TTL_HOURS || "12");
export const sessionMaxAge = Math.max(1, ttlHours) * 3600; // seconds

// "Remember me" sessions live much longer (default 30 days). The cookie and the
// JWT expiry are kept in sync so a stolen cookie can't outlive its token.
const rememberTtlHours = Number(process.env.SESSION_REMEMBER_TTL_HOURS || "720");
export const rememberMaxAge = Math.max(1, rememberTtlHours) * 3600; // seconds

// Cookie maxAge / TTL (seconds) for a session, depending on "remember me".
export function maxAgeFor(remember: boolean): number {
  return remember ? rememberMaxAge : sessionMaxAge;
}

// Whether the session cookie is marked Secure. Defaults to true in production,
// but can be turned off (SESSION_COOKIE_SECURE=false) for a staging box served
// over plain HTTP — browsers refuse to store Secure cookies over http on a real
// domain, which silently breaks login. ALWAYS keep this on (use HTTPS) in prod.
export function cookieSecure(): boolean {
  const v = process.env.SESSION_COOKIE_SECURE;
  if (v != null && v !== "") return v === "true" || v === "1";
  return process.env.NODE_ENV === "production";
}

function secret(): Uint8Array {
  const s = process.env.SESSION_SECRET;
  if (!s || s.length < 16) {
    // Fail loud in any real deployment; a short/missing secret is a security bug.
    throw new Error("SESSION_SECRET must be set (>=16 chars)");
  }
  return new TextEncoder().encode(s);
}

export async function createSession(email: string, remember = false): Promise<string> {
  const hours = remember ? Math.max(1, rememberTtlHours) : Math.max(1, ttlHours);
  return new SignJWT({ email })
    .setProtectedHeader({ alg: "HS256" })
    .setIssuedAt()
    .setExpirationTime(`${hours}h`)
    .sign(secret());
}

export async function verifySession(token: string): Promise<JWTPayload | null> {
  try {
    const { payload } = await jwtVerify(token, secret());
    return payload;
  } catch {
    return null;
  }
}

// Verify the session straight from a request's cookie. Middleware already gates
// every route, but the secret-forwarding proxies (/api/gw, /api/console) call
// this too so a middleware bypass (e.g. CVE-2025-29927's x-middleware-subrequest
// header) can't reach the gateway with the service token. Defense in depth — do
// not remove.
export async function sessionFromRequest(
  req: { cookies: { get(name: string): { value: string } | undefined } },
): Promise<JWTPayload | null> {
  const token = req.cookies.get(SESSION_COOKIE)?.value;
  return token ? verifySession(token) : null;
}
