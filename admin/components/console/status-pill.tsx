function tone(status: number): string {
  if (status === 0) return "bg-rose-500/15 text-rose-300 border-rose-500/30";
  if (status >= 200 && status < 300) return "bg-emerald-500/15 text-emerald-300 border-emerald-500/30";
  if (status >= 300 && status < 400) return "bg-sky-500/15 text-sky-300 border-sky-500/30";
  if (status >= 400 && status < 500) return "bg-amber-500/15 text-amber-300 border-amber-500/30";
  return "bg-rose-500/15 text-rose-300 border-rose-500/30";
}

export function StatusPill({ status, statusText }: { status: number; statusText?: string }) {
  const label = status === 0 ? "ERR" : `${status}${statusText ? ` ${statusText}` : ""}`;
  return (
    <span className={`inline-flex items-center rounded-full border px-2 py-0.5 text-xs font-semibold ${tone(status)}`}>
      {label}
    </span>
  );
}
