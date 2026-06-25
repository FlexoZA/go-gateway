"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";

// In-page docs sidebar. Grows as more guides are added (Fleetiger, etc.).
const sections = [
  {
    title: "Getting started",
    links: [{ href: "/docs", label: "Overview" }],
  },
  {
    title: "Reference",
    links: [{ href: "/docs/api", label: "HTTP API" }],
  },
  {
    title: "Integrations",
    links: [{ href: "/docs/howen", label: "Howen" }],
  },
];

export function DocsNav() {
  const pathname = usePathname();
  return (
    <nav className="space-y-5">
      {sections.map((section) => (
        <div key={section.title}>
          <div className="mb-1.5 px-2 text-xs font-semibold uppercase tracking-wide text-slate-500">
            {section.title}
          </div>
          <div className="space-y-0.5">
            {section.links.map((l) => {
              const active = pathname === l.href;
              return (
                <Link
                  key={l.href}
                  href={l.href}
                  className={`block rounded-md px-2 py-1.5 text-sm ${
                    active ? "bg-indigo-600 text-white" : "text-slate-300 hover:bg-edge"
                  }`}
                >
                  {l.label}
                </Link>
              );
            })}
          </div>
        </div>
      ))}
    </nav>
  );
}
