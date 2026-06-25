import Link from "next/link";
import { PageHeader } from "@/components/ui";

export const metadata = { title: "Docs" };

const guides = [
  {
    href: "/docs/howen",
    title: "Howen integration",
    blurb:
      "Consume live GPS & events, device status, live video, and recorded clips from Howen devices over the HTTP API.",
  },
];

export default function DocsPage() {
  return (
    <div>
      <PageHeader
        title="Docs"
        subtitle="Guides for integrating with the gateway HTTP API."
      />
      <div className="doc-prose">
        <p>
          These guides cover how external systems read data from the gateway. Every request is
          authenticated with a Bearer API key — create one on the{" "}
          <Link href="/api-keys">API Keys</Link> page. To explore endpoints interactively, use the{" "}
          <Link href="/api-console">API Console</Link>.
        </p>
      </div>

      <div className="mt-6 grid gap-4 sm:grid-cols-2">
        {guides.map((g) => (
          <Link
            key={g.href}
            href={g.href}
            className="card transition-colors hover:border-indigo-500/50"
          >
            <div className="text-base font-semibold text-white">{g.title}</div>
            <p className="mt-1 text-sm text-slate-400">{g.blurb}</p>
          </Link>
        ))}
      </div>
    </div>
  );
}
