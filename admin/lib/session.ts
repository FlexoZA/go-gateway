// Session handling for the admin panel. The browser never sees the gateway API
// key; instead, after a successful login the BFF issues a signed JWT stored in
// an httpOnly cookie. Middleware and route handlers verify it.
import { SignJWT, jwtVerify, type JWTPayload } from "jose";

export const SESSION_COOKIE = "dgw_admin_session";

const ttlHours = Number(process.env.SESSION_TTL_HOURS || "12");
export const sessionMaxAge = Math.max(1, ttlHours) * 3600; // seconds

function secret(): Uint8Array {
  const s = process.env.SESSION_SECRET;
  if (!s || s.length < 16) {
    // Fail loud in any real deployment; a short/missing secret is a security bug.
    throw new Error("SESSION_SECRET must be set (>=16 chars)");
  }
  return new TextEncoder().encode(s);
}

export async function createSession(email: string): Promise<string> {
  return new SignJWT({ email })
    .setProtectedHeader({ alg: "HS256" })
    .setIssuedAt()
    .setExpirationTime(`${Math.max(1, ttlHours)}h`)
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
