"use client";

import { useFetch } from "@/lib/useFetch";
import { Badge } from "@/components/ui";

// Retention helpers shared by the clips and snapshots lists. The media-retention
// reaper deletes a clip/snapshot once its created_at is older than the live
// media_retention_days setting (0 = keep forever, sweeps hourly). These helpers
// let a list warn when a row is within a day of that cutoff.

const DAY_MS = 86_400_000;

type Setting = { key: string; value: string };

// useMediaRetentionDays reads the live media_retention_days setting, returning the
// number of days (0 = keep forever) or null while loading / if unset or unparseable.
export function useMediaRetentionDays(): number | null {
  const settings = useFetch<{ settings: Setting[] }>("settings");
  const raw = settings.data?.settings.find((s) => s.key === "media_retention_days")?.value;
  if (raw === undefined) return null;
  const n = Number(raw);
  return Number.isInteger(n) && n >= 0 ? n : null;
}

// retentionMsLeft returns milliseconds until a row is auto-deleted, or null when
// retention is disabled/unknown. A negative value means it is already past the
// cutoff and will be removed on the next hourly sweep.
export function retentionMsLeft(createdAt: string | undefined, retentionDays: number | null): number | null {
  if (!createdAt || !retentionDays || retentionDays <= 0) return null;
  const created = new Date(createdAt).getTime();
  if (Number.isNaN(created)) return null;
  return created + retentionDays * DAY_MS - Date.now();
}

// ExpiryBadge renders an amber warning only when a row is within 24h of (or past)
// its auto-delete cutoff; otherwise it renders nothing.
export function ExpiryBadge({ createdAt, retentionDays }: { createdAt: string | undefined; retentionDays: number | null }) {
  const msLeft = retentionMsLeft(createdAt, retentionDays);
  if (msLeft === null || msLeft > DAY_MS) return null;

  const label = msLeft <= 0 ? "Deleting soon" : `Deletes in ${Math.max(1, Math.ceil(msLeft / 3_600_000))}h`;
  const title =
    msLeft <= 0
      ? "Past the retention window — will be removed on the next hourly cleanup"
      : "Within 24h of the retention window — auto-deleted soon";
  return (
    <span title={title}>
      <Badge tone="amber">{label}</Badge>
    </span>
  );
}
