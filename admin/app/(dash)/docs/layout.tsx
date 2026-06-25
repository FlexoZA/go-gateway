import { type ReactNode } from "react";
import { DocsNav } from "@/components/docs/docs-nav";

export default function DocsLayout({ children }: { children: ReactNode }) {
  return (
    <div className="flex gap-8">
      <aside className="w-48 shrink-0">
        <div className="sticky top-6">
          <DocsNav />
        </div>
      </aside>
      <div className="min-w-0 flex-1">{children}</div>
    </div>
  );
}
