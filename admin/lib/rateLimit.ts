// A tiny in-memory fixed-window rate limiter for BFF route handlers. Per server
// instance (state lives in this module), which is sufficient for the single-container
// admin deploy; it is a brute-force speed bump, not a distributed quota. If the admin
// is ever scaled to multiple instances, move this to a shared store (e.g. Redis).
import "server-only";

type Bucket = { count: number; resetAt: number };

const buckets = new Map<string, Bucket>();
const MAX_BUCKETS = 10_000; // bound memory against IP-rotation flooding

function sweep(now: number) {
  if (buckets.size < MAX_BUCKETS) return;
  for (const [key, b] of buckets) {
    if (now >= b.resetAt) buckets.delete(key);
  }
  // Still over budget (many live windows) → drop everything; worst case a few
  // clients get a fresh window early. Bounded memory beats perfect accounting.
  if (buckets.size >= MAX_BUCKETS) buckets.clear();
}

export type RateLimitResult = { ok: boolean; retryAfterSec: number };

// rateLimit records one hit for key and reports whether it is within `limit` hits
// per `windowMs`. The first over-limit hit (and all until the window resets) is
// rejected with the seconds until reset.
export function rateLimit(key: string, limit: number, windowMs: number): RateLimitResult {
  const now = Date.now();
  sweep(now);
  let b = buckets.get(key);
  if (!b || now >= b.resetAt) {
    b = { count: 0, resetAt: now + windowMs };
    buckets.set(key, b);
  }
  b.count++;
  if (b.count > limit) {
    return { ok: false, retryAfterSec: Math.max(1, Math.ceil((b.resetAt - now) / 1000)) };
  }
  return { ok: true, retryAfterSec: 0 };
}

// clientIp extracts the caller's IP from proxy headers (the admin runs behind a
// reverse proxy in production). Falls back to a constant so the limiter still
// applies a global cap when no proxy header is present.
export function clientIp(req: Request): string {
  const xff = req.headers.get("x-forwarded-for");
  if (xff) {
    const first = xff.split(",")[0]?.trim();
    if (first) return first;
  }
  return req.headers.get("x-real-ip")?.trim() || "unknown";
}
