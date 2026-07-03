"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { api } from "@/lib/api";

// useFetch fetches a BFF path and optionally re-polls. Returns data, error,
// loading, and a manual refresh.
//
// Each mount/path-change run is fenced by its own guard so a slow in-flight
// response can never land after unmount or after the path changed and overwrite
// the newer resource's state. On a path change the previous resource's data is
// cleared and loading is reset, so a stale body is never shown under a new path.
export function useFetch<T = any>(path: string, pollMs = 0) {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const guardRef = useRef<{ live: boolean }>({ live: true });

  const doFetch = useCallback(
    async (guard: { live: boolean }) => {
      try {
        const d = await api<T>(path);
        if (guard.live) {
          setData(d);
          setError(null);
        }
      } catch (e: any) {
        if (guard.live) setError(e?.message || "Request failed");
      } finally {
        if (guard.live) setLoading(false);
      }
    },
    [path],
  );

  useEffect(() => {
    const guard = { live: true };
    guardRef.current = guard;
    // New path: drop the previous resource's data and show a loading state rather
    // than rendering stale data from the old path.
    setData(null);
    setError(null);
    setLoading(true);
    doFetch(guard);
    if (pollMs > 0) {
      const id = setInterval(() => doFetch(guard), pollMs);
      return () => {
        guard.live = false;
        clearInterval(id);
      };
    }
    return () => {
      guard.live = false;
    };
  }, [doFetch, pollMs]);

  // Manual refresh uses the currently-active guard so it too is fenced on unmount.
  const refresh = useCallback(() => doFetch(guardRef.current), [doFetch]);

  return { data, error, loading, refresh };
}
