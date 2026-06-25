"use client";

import { useMemo, useState } from "react";
import { useConsole } from "@/contexts/console-context";
import { KvEditor } from "./kv-editor";
import { PathParamChips } from "./path-param-chips";
import { resolveRequest } from "@/lib/console/client";
import { toCurl, toFetch } from "@/lib/console/curl-export";
import {
  buildVariableMap,
  substitute,
  unresolvedPathParams,
  unresolvedVariables,
} from "@/lib/console/variables";
import type { BodyMode, HttpMethod } from "@/lib/console/types";

const METHODS: HttpMethod[] = ["GET", "POST", "PUT", "PATCH", "DELETE"];
const COMMON_HEADERS = ["Accept", "Authorization", "Content-Type", "X-Request-ID"];
const TABS = ["Params", "Headers", "Body", "Snippets"] as const;
type Tab = (typeof TABS)[number];

function CopyButton({ text }: { text: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <button
      type="button"
      onClick={() => {
        navigator.clipboard?.writeText(text);
        setCopied(true);
        setTimeout(() => setCopied(false), 1200);
      }}
      className="btn-ghost text-xs"
    >
      {copied ? "Copied" : "Copy"}
    </button>
  );
}

export function RequestBuilder() {
  const { draft, setDraft, activeEnv, send, cancel, sending, collections, saveRequestToCollection } =
    useConsole();
  const [tab, setTab] = useState<Tab>("Params");
  const [saveTarget, setSaveTarget] = useState<string>("");

  const vars = useMemo(() => buildVariableMap(activeEnv), [activeEnv]);
  const resolved = useMemo(() => {
    try {
      return resolveRequest({ request: draft, environment: activeEnv });
    } catch {
      return null;
    }
  }, [draft, activeEnv]);

  const pathAfterVars = substitute(draft.path, vars);
  const missingVars = unresolvedVariables(draft.path, vars);
  const missingParams = unresolvedPathParams(pathAfterVars, draft.pathParams);

  function setBody(patch: Partial<{ mode: BodyMode; text: string }>) {
    setDraft((d) => ({ ...d, body: { ...d.body, ...patch } }));
  }

  function formatJson() {
    try {
      setBody({ text: JSON.stringify(JSON.parse(draft.body.text), null, 2) });
    } catch {
      /* leave as-is if invalid */
    }
  }

  return (
    <div className="flex h-full flex-col">
      {/* Name + save */}
      <div className="mb-3 flex items-center gap-2">
        <input
          value={draft.name}
          onChange={(e) => setDraft((d) => ({ ...d, name: e.target.value }))}
          placeholder="Request name"
          className="input flex-1 font-medium"
        />
        <select
          value={saveTarget}
          onChange={(e) => setSaveTarget(e.target.value)}
          className="input w-44 py-1 text-xs"
        >
          <option value="">Save to…</option>
          {collections.map((c) => (
            <option key={c.id} value={c.id}>
              {c.name}
            </option>
          ))}
        </select>
        <button
          type="button"
          disabled={!saveTarget}
          onClick={() => saveTarget && saveRequestToCollection(saveTarget, draft)}
          className="btn-ghost text-xs"
        >
          Save
        </button>
      </div>

      {/* Method + path + send */}
      <div className="flex items-center gap-2">
        <select
          value={draft.method}
          onChange={(e) => setDraft((d) => ({ ...d, method: e.target.value as HttpMethod }))}
          className="input w-28 font-mono font-bold"
        >
          {METHODS.map((m) => (
            <option key={m} value={m}>
              {m}
            </option>
          ))}
        </select>
        <input
          value={draft.path}
          onChange={(e) => setDraft((d) => ({ ...d, path: e.target.value }))}
          onKeyDown={(e) => {
            if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) send();
          }}
          placeholder="/api/units/{{serial}}/status"
          className="input flex-1 font-mono"
          spellCheck={false}
        />
        {sending ? (
          <button type="button" onClick={cancel} className="btn-danger">
            Cancel
          </button>
        ) : (
          <button type="button" onClick={send} className="btn-primary">
            Send
          </button>
        )}
      </div>

      {/* Resolved preview + warnings */}
      <div className="mt-2 space-y-1 text-xs">
        {resolved && (
          <div className="font-mono text-slate-500">
            → <span className="text-slate-300">{resolved.path}</span>
          </div>
        )}
        {missingVars.length > 0 && (
          <div className="text-amber-300">Unresolved variables: {missingVars.map((v) => `{{${v}}}`).join(", ")}</div>
        )}
        {missingParams.length > 0 && (
          <div className="text-amber-300">Unfilled path params: {missingParams.map((v) => `{${v}}`).join(", ")}</div>
        )}
      </div>

      {/* Path param chips */}
      <div className="mt-2">
        <PathParamChips
          path={pathAfterVars}
          pathParams={draft.pathParams}
          onChange={(pathParams) => setDraft((d) => ({ ...d, pathParams }))}
        />
      </div>

      {draft.description && <p className="mt-2 text-xs text-slate-400">{draft.description}</p>}

      {/* Auth note */}
      <p className="mt-3 rounded-md border border-edge bg-ink/50 px-3 py-2 text-xs text-slate-400">
        Requests are authorized automatically with the gateway service key. Add an{" "}
        <span className="font-mono text-slate-300">Authorization</span> header below to override it.
      </p>

      {/* Tabs */}
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
        {tab === "Params" && (
          <KvEditor
            entries={draft.params}
            onChange={(params) => setDraft((d) => ({ ...d, params }))}
            keyPlaceholder="param"
          />
        )}

        {tab === "Headers" && (
          <KvEditor
            entries={draft.headers}
            onChange={(headers) => setDraft((d) => ({ ...d, headers }))}
            keyPlaceholder="header"
            keySuggestions={COMMON_HEADERS}
          />
        )}

        {tab === "Body" && (
          <div className="space-y-2">
            <div className="flex items-center gap-2">
              <select
                value={draft.body.mode}
                onChange={(e) => setBody({ mode: e.target.value as BodyMode })}
                className="input w-48 py-1 text-xs"
              >
                <option value="none">None</option>
                <option value="json">JSON</option>
                <option value="raw">Raw</option>
                <option value="form">x-www-form-urlencoded</option>
              </select>
              {draft.body.mode === "json" && (
                <button type="button" onClick={formatJson} className="btn-ghost text-xs">
                  Format
                </button>
              )}
            </div>
            {draft.body.mode !== "none" && (
              <textarea
                value={draft.body.text}
                onChange={(e) => setBody({ text: e.target.value })}
                placeholder={draft.body.mode === "json" ? '{\n  "key": "value"\n}' : "body"}
                rows={12}
                className="input font-mono"
                spellCheck={false}
              />
            )}
          </div>
        )}

        {tab === "Snippets" && resolved && (
          <div className="space-y-4">
            <div>
              <div className="mb-1 flex items-center justify-between">
                <span className="text-xs font-semibold text-slate-400">cURL</span>
                <CopyButton text={toCurl(resolved)} />
              </div>
              <pre className="overflow-auto rounded-md border border-edge bg-ink p-3 text-xs text-slate-200">
                {toCurl(resolved)}
              </pre>
            </div>
            <div>
              <div className="mb-1 flex items-center justify-between">
                <span className="text-xs font-semibold text-slate-400">fetch</span>
                <CopyButton text={toFetch(resolved)} />
              </div>
              <pre className="overflow-auto rounded-md border border-edge bg-ink p-3 text-xs text-slate-200">
                {toFetch(resolved)}
              </pre>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
