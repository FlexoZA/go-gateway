import { NextResponse, type NextRequest } from "next/server";
import { gatewayConfigured, gatewayFetch } from "@/lib/gateway";

// POST /api/setup — public first-run bootstrap. The gateway only honours this
// while there are zero users; once initialized it returns 409.
export async function POST(req: NextRequest) {
  if (!gatewayConfigured()) {
    return NextResponse.json({ error: "gateway token not configured" }, { status: 503 });
  }
  const body = await req.text();
  try {
    const res = await gatewayFetch("/api/setup", { method: "POST", body });
    const text = await res.text();
    return new NextResponse(text, { status: res.status, headers: { "Content-Type": "application/json" } });
  } catch {
    return NextResponse.json({ error: "gateway unreachable" }, { status: 502 });
  }
}
