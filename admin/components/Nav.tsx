"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { logout } from "@/lib/api";
import { useAggregateCaps, useUnits } from "@/lib/useGatewayInfo";

export function Nav() {
  const pathname = usePathname();
  // Union of capabilities across every hosted unit type: a link shows if ANY unit
  // supports the feature (a GPS-only unit has no Clips; a unit without editable
  // mappings has no Device Mapping). Shown until we positively know it's absent.
  const caps = useAggregateCaps();
  const hasUnitSettings = useUnits().some((u) => (u.schema?.length ?? 0) > 0);
  const links = [
    { href: "/", label: "Dashboard" },
    { href: "/devices", label: "Devices" },
    ...(caps?.has_clips !== false ? [{ href: "/clips", label: "Clips" }] : []),
    ...(caps?.has_mappings !== false ? [{ href: "/device-mapping", label: "Device Mapping" }] : []),
    ...(hasUnitSettings ? [{ href: "/unit-settings", label: "Unit Settings" }] : []),
    { href: "/server-settings", label: "Server Settings" },
    { href: "/users", label: "Users" },
    { href: "/api-keys", label: "API Keys" },
    { href: "/logs", label: "Logs" },
  ];
  return (
    <aside className="flex w-56 shrink-0 flex-col border-r border-edge bg-panel">
      <div className="border-b border-edge px-5 py-4">
        <div className="text-sm font-semibold text-white">Device Gateway</div>
        <div className="text-xs text-slate-400">Admin</div>
      </div>
      <nav className="flex-1 space-y-1 p-3">
        {links.map((l) => {
          const active = l.href === "/" ? pathname === "/" : pathname.startsWith(l.href);
          return (
            <Link
              key={l.href}
              href={l.href}
              className={`block rounded-md px-3 py-2 text-sm ${
                active ? "bg-indigo-600 text-white" : "text-slate-300 hover:bg-edge"
              }`}
            >
              {l.label}
            </Link>
          );
        })}
      </nav>
      <div className="border-t border-edge p-3">
        <button onClick={() => logout()} className="btn-ghost w-full">
          Sign out
        </button>
      </div>
    </aside>
  );
}
