"use client";

import { ConsoleProvider } from "@/contexts/console-context";
import { Sidebar } from "./sidebar";
import { RequestBuilder } from "./request-builder";
import { ResponseViewer } from "./response-viewer";

export function ConsoleShell() {
  return (
    <ConsoleProvider>
      <div className="flex h-[calc(100vh-7rem)] gap-4">
        {/* Sidebar */}
        <aside className="w-80 shrink-0 overflow-hidden rounded-lg border border-edge bg-panel">
          <Sidebar />
        </aside>

        {/* Builder + response split */}
        <div className="flex min-w-0 flex-1 flex-col gap-4">
          <section className="card flex min-h-0 flex-1 flex-col overflow-hidden">
            <RequestBuilder />
          </section>
          <section className="card flex min-h-0 flex-1 flex-col overflow-hidden">
            <ResponseViewer />
          </section>
        </div>
      </div>
    </ConsoleProvider>
  );
}
