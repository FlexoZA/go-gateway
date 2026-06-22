"use client";

import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";

export default function LoginPage() {
  const router = useRouter();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  // Redirect to the setup wizard if the gateway has no users yet.
  useEffect(() => {
    (async () => {
      try {
        const res = await fetch("/api/setup-status");
        const data = await res.json().catch(() => ({}));
        if (data.needs_setup) router.replace("/setup");
      } catch {
        /* ignore; show login */
      }
    })();
  }, [router]);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const res = await fetch("/api/login", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ email, password }),
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) {
        setError(data.error || "Login failed");
        return;
      }
      router.replace("/");
      router.refresh();
    } catch {
      setError("Network error");
    } finally {
      setBusy(false);
    }
  }

  return (
    <form onSubmit={submit} className="card w-full max-w-sm space-y-4">
      <div>
        <h1 className="text-lg font-semibold text-white">Sign in</h1>
        <p className="mt-1 text-sm text-slate-400">Device Gateway administration</p>
      </div>
      {error && <div className="rounded-md border border-rose-500/40 bg-rose-500/10 px-3 py-2 text-sm text-rose-200">{error}</div>}
      <div className="space-y-1">
        <label className="text-xs text-slate-400">Email</label>
        <input className="input" type="email" autoComplete="username" value={email} onChange={(e) => setEmail(e.target.value)} required />
      </div>
      <div className="space-y-1">
        <label className="text-xs text-slate-400">Password</label>
        <input className="input" type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} required />
      </div>
      <button className="btn-primary w-full" disabled={busy}>
        {busy ? "Signing in…" : "Sign in"}
      </button>
    </form>
  );
}
