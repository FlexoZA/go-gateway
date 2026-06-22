import { NextResponse, type NextRequest } from "next/server";
import { SESSION_COOKIE, verifySession } from "@/lib/session";

// Public paths that never require a session (login + first-run setup).
const PUBLIC = ["/login", "/api/login", "/setup", "/api/setup", "/api/setup-status"];

export async function middleware(req: NextRequest) {
  const { pathname } = req.nextUrl;

  if (PUBLIC.some((p) => pathname === p || pathname.startsWith(p + "/"))) {
    return NextResponse.next();
  }

  const token = req.cookies.get(SESSION_COOKIE)?.value;
  const session = token ? await verifySession(token) : null;
  if (session) {
    return NextResponse.next();
  }

  // Unauthenticated: APIs get a 401, pages redirect to the login screen.
  if (pathname.startsWith("/api/")) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const url = req.nextUrl.clone();
  url.pathname = "/login";
  url.search = "";
  return NextResponse.redirect(url);
}

export const config = {
  // Run on everything except Next internals and static assets.
  matcher: ["/((?!_next/static|_next/image|favicon.ico).*)"],
};
