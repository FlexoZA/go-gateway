"use client";

import React, { createContext, useCallback, useContext, useMemo, useRef, useState } from "react";
import { usePersistedState } from "@/lib/usePersistedState";
import { resolveRequest, sendRequest } from "@/lib/console/client";
import { buildBuiltInCollections } from "@/lib/console/default-collections";
import {
  HISTORY_LIMIT,
  STORAGE_KEYS,
  defaultCollections,
  defaultEnvironments,
  emptyCollection,
  emptyRequest,
  genId,
  mergeBuiltIns,
} from "@/lib/console/storage";
import type {
  Collection,
  ConsoleRequest,
  ConsoleResponse,
  Environment,
  HistoryEntry,
} from "@/lib/console/types";

type Updater<T> = T | ((prev: T) => T);

interface ConsoleContextValue {
  environments: Environment[];
  setEnvironments: (value: Updater<Environment[]>) => void;
  activeEnv: Environment | null;
  activeEnvId: string | null;
  setActiveEnvId: (id: string) => void;

  collections: Collection[];
  setCollections: (value: Updater<Collection[]>) => void;
  createCollection: () => Collection;
  addRequestToCollection: (collectionId: string, name?: string) => ConsoleRequest;
  saveRequestToCollection: (collectionId: string, request: ConsoleRequest) => void;
  deleteRequest: (collectionId: string, requestId: string) => void;
  deleteCollection: (collectionId: string) => void;
  renameCollection: (collectionId: string, name: string) => void;
  restoreBuiltInCollections: () => { addedCollections: number; addedRequests: number };
  restoreRequestDefault: (requestId: string) => boolean;

  history: HistoryEntry[];
  clearHistory: () => void;

  draft: ConsoleRequest;
  setDraft: (updater: (r: ConsoleRequest) => ConsoleRequest) => void;
  replaceDraft: (r: ConsoleRequest) => void;

  response: ConsoleResponse | null;
  sending: boolean;
  send: () => Promise<void>;
  cancel: () => void;
}

const ConsoleContext = createContext<ConsoleContextValue | null>(null);

/**
 * Blank any `Authorization` header before a request lands in long-lived storage
 * (the history ring buffer, persisted to localStorage). The gateway service key
 * is never in the draft to begin with (it's added server-side), but a user may
 * have typed a custom Bearer key here.
 */
function scrubSecrets(req: ConsoleRequest): ConsoleRequest {
  return {
    ...req,
    headers: req.headers.map((h) =>
      h.key.toLowerCase() === "authorization" ? { ...h, value: "" } : h,
    ),
  };
}

export function ConsoleProvider({ children }: { children: React.ReactNode }) {
  const [environments, setEnvironments] = usePersistedState<Environment[]>(
    STORAGE_KEYS.environments,
    defaultEnvironments(),
  );
  const [activeEnvId, setActiveEnvId] = usePersistedState<string | null>(
    STORAGE_KEYS.activeEnvId,
    "env_default",
  );
  const [collections, setCollections] = usePersistedState<Collection[]>(
    STORAGE_KEYS.collections,
    defaultCollections(),
  );
  const [history, setHistory] = usePersistedState<HistoryEntry[]>(STORAGE_KEYS.history, []);

  const [draft, setDraftState] = useState<ConsoleRequest>(() => emptyRequest("Untitled request"));
  const [response, setResponse] = useState<ConsoleResponse | null>(null);
  const [sending, setSending] = useState(false);
  const abortRef = useRef<AbortController | null>(null);

  const activeEnv = useMemo<Environment | null>(
    () => environments.find((e) => e.id === activeEnvId) ?? environments[0] ?? null,
    [environments, activeEnvId],
  );

  const setDraft = useCallback(
    (updater: (r: ConsoleRequest) => ConsoleRequest) => setDraftState((d) => updater(d)),
    [],
  );

  const replaceDraft = useCallback((r: ConsoleRequest) => {
    setDraftState({ ...r, id: r.id || genId("req") });
    setResponse(null);
  }, []);

  const createCollection = useCallback((): Collection => {
    const col = emptyCollection("New collection");
    setCollections((prev) => [...prev, col]);
    return col;
  }, [setCollections]);

  const addRequestToCollection = useCallback(
    (collectionId: string, name = "New request"): ConsoleRequest => {
      const req = { ...emptyRequest(name), id: genId("req") };
      setCollections((prev) =>
        prev.map((c) =>
          c.id === collectionId
            ? { ...c, requests: [...c.requests, req], updatedAt: Date.now() }
            : c,
        ),
      );
      return req;
    },
    [setCollections],
  );

  const saveRequestToCollection = useCallback(
    (collectionId: string, request: ConsoleRequest) => {
      setCollections((prev) =>
        prev.map((c) => {
          if (c.id !== collectionId) return c;
          const exists = c.requests.some((r) => r.id === request.id);
          const requests = exists
            ? c.requests.map((r) => (r.id === request.id ? request : r))
            : [...c.requests, request];
          return { ...c, requests, updatedAt: Date.now() };
        }),
      );
    },
    [setCollections],
  );

  const deleteRequest = useCallback(
    (collectionId: string, requestId: string) => {
      setCollections((prev) =>
        prev.map((c) =>
          c.id === collectionId
            ? { ...c, requests: c.requests.filter((r) => r.id !== requestId), updatedAt: Date.now() }
            : c,
        ),
      );
    },
    [setCollections],
  );

  const deleteCollection = useCallback(
    (collectionId: string) => {
      setCollections((prev) => prev.filter((c) => c.id !== collectionId));
    },
    [setCollections],
  );

  const renameCollection = useCallback(
    (collectionId: string, name: string) => {
      setCollections((prev) =>
        prev.map((c) => (c.id === collectionId ? { ...c, name, updatedAt: Date.now() } : c)),
      );
    },
    [setCollections],
  );

  const restoreBuiltInCollections = useCallback((): {
    addedCollections: number;
    addedRequests: number;
  } => {
    let summary = { addedCollections: 0, addedRequests: 0 };
    setCollections((prev) => {
      const result = mergeBuiltIns(prev);
      summary = {
        addedCollections: result.addedCollections,
        addedRequests: result.addedRequests,
      };
      return result.addedCollections + result.addedRequests > 0 ? result.collections : prev;
    });
    return summary;
  }, [setCollections]);

  const restoreRequestDefault = useCallback(
    (requestId: string): boolean => {
      if (!requestId.startsWith("col_builtin_")) return false;
      const builtIns = buildBuiltInCollections();
      let template: ConsoleRequest | null = null;
      for (const col of builtIns) {
        const found = col.requests.find((r) => r.id === requestId);
        if (found) {
          template = found;
          break;
        }
      }
      if (!template) return false;
      const tmpl = template;
      setCollections((prev) =>
        prev.map((c) => {
          const has = c.requests.some((r) => r.id === requestId);
          if (!has) return c;
          return {
            ...c,
            requests: c.requests.map((r) => (r.id === requestId ? { ...tmpl } : r)),
            updatedAt: Date.now(),
          };
        }),
      );
      if (draft.id === requestId) {
        setDraftState({ ...tmpl });
        setResponse(null);
      }
      return true;
    },
    [draft.id, setCollections],
  );

  const clearHistory = useCallback(() => setHistory([]), [setHistory]);

  const cancel = useCallback(() => {
    abortRef.current?.abort();
    abortRef.current = null;
    setSending(false);
  }, []);

  const send = useCallback(async () => {
    if (sending) return;
    setSending(true);
    setResponse(null);
    const controller = new AbortController();
    abortRef.current = controller;
    try {
      const res = await sendRequest({
        request: draft,
        environment: activeEnv,
        signal: controller.signal,
      });
      setResponse(res);
      let resolvedPath = draft.path;
      try {
        resolvedPath = resolveRequest({ request: draft, environment: activeEnv }).path;
      } catch {
        // fall back to raw path
      }
      const entry: HistoryEntry = {
        id: genId("hist"),
        request: scrubSecrets(draft),
        resolvedPath,
        environmentId: activeEnv?.id ?? null,
        response: {
          ...res,
          headers: res.headers.map(([k, v]) =>
            k.toLowerCase() === "authorization" ? [k, ""] : [k, v],
          ),
        },
        status: res.status,
        durationMs: res.durationMs,
        timestamp: Date.now(),
      };
      setHistory((prev) => [entry, ...prev].slice(0, HISTORY_LIMIT));
    } finally {
      setSending(false);
      abortRef.current = null;
    }
  }, [sending, draft, activeEnv, setHistory]);

  const value = useMemo<ConsoleContextValue>(
    () => ({
      environments,
      setEnvironments,
      activeEnv,
      activeEnvId,
      setActiveEnvId,
      collections,
      setCollections,
      createCollection,
      addRequestToCollection,
      saveRequestToCollection,
      deleteRequest,
      deleteCollection,
      renameCollection,
      restoreBuiltInCollections,
      restoreRequestDefault,
      history,
      clearHistory,
      draft,
      setDraft,
      replaceDraft,
      response,
      sending,
      send,
      cancel,
    }),
    [
      environments,
      setEnvironments,
      activeEnv,
      activeEnvId,
      setActiveEnvId,
      collections,
      setCollections,
      createCollection,
      addRequestToCollection,
      saveRequestToCollection,
      deleteRequest,
      deleteCollection,
      renameCollection,
      restoreBuiltInCollections,
      restoreRequestDefault,
      history,
      clearHistory,
      draft,
      setDraft,
      replaceDraft,
      response,
      sending,
      send,
      cancel,
    ],
  );

  return <ConsoleContext.Provider value={value}>{children}</ConsoleContext.Provider>;
}

export function useConsole(): ConsoleContextValue {
  const ctx = useContext(ConsoleContext);
  if (!ctx) throw new Error("useConsole must be used inside <ConsoleProvider>");
  return ctx;
}
