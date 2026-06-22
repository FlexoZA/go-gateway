import { NextResponse } from "next/server";
import { gatewayConfigured, gatewayFetch } from "@/lib/gateway";

// GET /api/setup-status — public: does the gateway need first-run setup?
export async function GET() {
  if (!gatewayConfigured()) {
    return NextResponse.json({ error: "gateway token not configured" }, { status: 503 });
  }
  try {
    const res = await gatewayFetch("/api/setup/status");
    const text = await res.text();
    return new NextResponse(text, { status: res.status, headers: { "Content-Type": "application/json" } });
  } catch {
    return NextResponse.json({ error: "gateway unreachable" }, { status: 502 });
  }
}
