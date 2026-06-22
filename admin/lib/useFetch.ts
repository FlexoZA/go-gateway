"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { api } from "@/lib/api";

// useFetch fetches a BFF path and optionally re-polls. Returns data, error,
// loading, and a manual refresh.
export function useFetch<T = any>(path: string, pollMs = 0) {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const mounted = useRef(true);

  const refresh = useCallback(async () => {
    try {
      const d = await api<T>(path);
      if (mounted.current) {
        setData(d);
        setError(null);
      }
    } catch (e: any) {
      if (mounted.current) setError(e.message || "Request failed");
    } finally {
      if (mounted.current) setLoading(false);
    }
  }, [path]);

  useEffect(() => {
    mounted.current = true;
    refresh();
    if (pollMs > 0) {
      const id = setInterval(refresh, pollMs);
      return () => {
        mounted.current = false;
        clearInterval(id);
      };
    }
    return () => {
      mounted.current = false;
    };
  }, [refresh, pollMs]);

  return { data, error, loading, refresh };
}
