"use client";

import Link from "next/link";
import { LivePlayer } from "@/components/LivePlayer";
import { useGatewayInfo } from "@/lib/useGatewayInfo";
import { PageHeader } from "@/components/ui";

export default function LivePage({ params }: { params: { serial: string } }) {
  const serial = decodeURIComponent(params.serial);
  const caps = useGatewayInfo()?.capabilities;
  return (
    <div>
      <PageHeader
        title="Live video"
        subtitle={
          <>
            Camera stream for <span className="font-mono text-slate-200">{serial}</span>
          </>
        }
        action={
          <Link href="/" className="btn-ghost">
            ← Back
          </Link>
        }
      />
      {caps && !caps.has_video ? (
        <div className="rounded-md border border-edge bg-panel px-4 py-3 text-sm text-slate-400">
          Video is not enabled for this gateway.
        </div>
      ) : (
        <LivePlayer serial={serial} />
      )}
    </div>
  );
}
