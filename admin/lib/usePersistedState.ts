"use client";

/**
 * localStorage-backed state that survives refreshes and navigation.
 *
 * - The returned setter is reference-stable (useCallback).
 * - A latestRef mirrors the freshest value so functional updaters always compose
 *   against the latest state, even when called from a stale closure. This makes
 *   rapid back-to-back mutations (save + auto-update) safe.
 * - We only hydrate from storage via the lazy initial state — never re-read on
 *   later renders (that would clobber in-flight writes).
 */

import { useCallback, useEffect, useRef, useState } from "react";

type Updater<T> = T | ((val: T) => T);

export function usePersistedState<T>(
  key: string,
  initialValue: T,
): [T, (value: Updater<T>) => void] {
  const initialValueRef = useRef(initialValue);

  const [storedValue, setStoredValue] = useState<T>(() => {
    if (typeof window === "undefined") return initialValueRef.current;
    try {
      const item = window.localStorage.getItem(key);
      return item ? (JSON.parse(item) as T) : initialValueRef.current;
    } catch (error) {
      console.warn(`Error reading localStorage key "${key}":`, error);
      return initialValueRef.current;
    }
  });

  const latestRef = useRef(storedValue);
  useEffect(() => {
    latestRef.current = storedValue;
  }, [storedValue]);

  const setValue = useCallback(
    (value: Updater<T>) => {
      try {
        const nextValue =
          value instanceof Function ? (value as (v: T) => T)(latestRef.current) : value;
        latestRef.current = nextValue;
        setStoredValue(nextValue);
        if (typeof window !== "undefined") {
          window.localStorage.setItem(key, JSON.stringify(nextValue));
        }
      } catch (error) {
        console.warn(`Error setting localStorage key "${key}":`, error);
      }
    },
    [key],
  );

  return [storedValue, setValue];
}
