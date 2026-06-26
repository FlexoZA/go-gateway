"use client";

// Client-side helper for calling the BFF proxy. All app data flows through
// /api/gw/* (which attaches the gateway API key server-side). On 401 we bounce
// to the login screen.
export async function api<T = any>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`/api/gw/${path}`, {
    ...init,
    headers: { "Content-Type": "application/json", ...(init?.headers || {}) },
  });
  if (res.status === 401) {
    if (typeof window !== "undefined") window.location.href = "/login";
    throw new Error("unauthenticated");
  }
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    throw new Error((data && (data.error as string)) || `HTTP ${res.status}`);
  }
  return data as T;
}

// apiBinary calls the BFF proxy and returns the raw response body as a Blob —
// for endpoints that respond with binary (e.g. the snapshot JPEG). On error the
// gateway replies with a JSON { error } body, which we surface as the message.
export async function apiBinary(path: string, init?: RequestInit): Promise<Blob> {
  const res = await fetch(`/api/gw/${path}`, init);
  if (res.status === 401) {
    if (typeof window !== "undefined") window.location.href = "/login";
    throw new Error("unauthenticated");
  }
  if (!res.ok) {
    const text = await res.text().catch(() => "");
    let msg = text || `HTTP ${res.status}`;
    try {
      const j = JSON.parse(text);
      if (j && j.error) msg = j.error as string;
    } catch {
      /* not JSON — use the raw text */
    }
    throw new Error(msg);
  }
  return res.blob();
}

export async function logout(): Promise<void> {
  await fetch("/api/logout", { method: "POST" });
  if (typeof window !== "undefined") window.location.href = "/login";
}
