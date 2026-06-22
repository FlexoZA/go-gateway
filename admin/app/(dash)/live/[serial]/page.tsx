"use client";

import Link from "next/link";
import { LivePlayer } from "@/components/LivePlayer";
import { PageHeader } from "@/components/ui";

export default function LivePage({ params }: { params: { serial: string } }) {
  const serial = decodeURIComponent(params.serial);
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
      <LivePlayer serial={serial} />
    </div>
  );
}
