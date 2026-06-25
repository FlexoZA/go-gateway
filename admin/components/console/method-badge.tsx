import type { HttpMethod } from "@/lib/console/types";

const TONES: Record<HttpMethod, string> = {
  GET: "text-emerald-300",
  POST: "text-amber-300",
  PUT: "text-sky-300",
  PATCH: "text-violet-300",
  DELETE: "text-rose-300",
};

export function MethodBadge({ method, className = "" }: { method: HttpMethod; className?: string }) {
  return (
    <span className={`font-mono text-xs font-bold ${TONES[method] ?? "text-slate-300"} ${className}`}>
      {method}
    </span>
  );
}
