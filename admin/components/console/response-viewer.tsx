"use client";

import { useState } from "react";
import { useConsole } from "@/contexts/console-context";
import { StatusPill } from "./status-pill";
import { JsonTree } from "./json-tree";

const TABS = ["Data", "Raw", "Headers"] as const;
type Tab = (typeof TABS)[number];

function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

export function ResponseViewer() {
  const { response, sending } = useConsole();
  const [tab, setTab] = useState<Tab>("Data");

  if (sending) {
    return <div className="flex h-full items-center justify-center text-sm text-slate-400">Sending…</div>;
  }
  if (!response) {
    return (
      <div className="flex h-full items-center justify-center text-sm text-slate-500">
        Send a request to see the response.
      </div>
    );
  }

  return (
    <div className="flex h-full flex-col">
      <div className="flex flex-wrap items-center gap-3 text-xs text-slate-400">
        <StatusPill status={response.status} statusText={response.statusText} />
        <span>{response.durationMs} ms</span>
        <span>{formatSize(response.sizeBytes)}</span>
      </div>

      {response.networkError && (
        <div className="mt-3 rounded-md border border-rose-500/40 bg-rose-500/10 px-3 py-2 text-sm text-rose-200">
          {response.networkError}
        </div>
      )}

      <div className="mt-3 flex gap-1 border-b border-edge">
        {TABS.map((t) => (
          <button
            key={t}
            type="button"
            onClick={() => setTab(t)}
            className={`px-3 py-1.5 text-sm ${
              tab === t ? "border-b-2 border-indigo-500 text-white" : "text-slate-400 hover:text-slate-200"
            }`}
          >
            {t}
          </button>
        ))}
      </div>

      <div className="mt-3 flex-1 overflow-auto">
        {tab === "Data" &&
          (response.json !== null ? (
            <JsonTree data={response.json} />
          ) : (
            <pre className="whitespace-pre-wrap font-mono text-xs text-slate-300">
              {response.rawText || "(empty body)"}
            </pre>
          ))}

        {tab === "Raw" && (
          <pre className="whitespace-pre-wrap font-mono text-xs text-slate-300">
            {response.rawText || "(empty body)"}
          </pre>
        )}

        {tab === "Headers" && (
          <table className="w-full text-xs">
            <tbody>
              {response.headers.map(([k, v], i) => (
                <tr key={`${k}-${i}`} className="border-b border-edge/50">
                  <td className="py-1 pr-3 font-mono text-slate-400 align-top">{k}</td>
                  <td className="py-1 font-mono text-slate-200 break-all">{v}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}
