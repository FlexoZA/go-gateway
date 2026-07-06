"use client";

import { LivePlayer } from "@/components/LivePlayer";

// DeviceVideo is the "Video" tab of a device's detail page: the live stream only.
// The saved-footage workflow (search recordings → pull → download) lives on the
// separate "Clips" tab (DeviceClips).
export function DeviceVideo({ serial, sleeping }: { serial: string; sleeping: boolean }) {
  return (
    <section>
      <h3 className="mb-3 text-sm font-semibold text-slate-300">Live stream</h3>
      <LivePlayer serial={serial} disabled={sleeping} />
    </section>
  );
}
