"use client";

import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";

export default function SetupPage() {
  const router = useRouter();
  const [checking, setChecking] = useState(true);
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [gatewayName, setGatewayName] = useState("");
  const [webhookUrl, setWebhookUrl] = useState("");
  const [devicePort, setDevicePort] = useState("33000");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  // If the gateway is already initialized, there is nothing to set up.
  useEffect(() => {
    (async () => {
      try {
        const res = await fetch("/api/setup-status");
        const data = await res.json().catch(() => ({}));
        if (!data.needs_setup) {
          router.replace("/login");
          return;
        }
      } catch {
        /* show the form anyway; submit will surface errors */
      }
      setChecking(false);
    })();
  }, [router]);

  const portValid = devicePort.trim() === "" || (/^\d+$/.test(devicePort.trim()) && +devicePort >= 1 && +devicePort <= 65535);
  const valid = /.+@.+\..+/.test(email) && password.length >= 8 && password === confirm && portValid;

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const res = await fetch("/api/setup", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          email: email.trim(),
          password,
          gateway_name: gatewayName.trim(),
          webhook_url: webhookUrl.trim(),
          device_port: devicePort.trim(),
        }),
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) {
        setError(data.error || "Setup failed");
        return;
      }
      // Auto-login with the credentials just created.
      await fetch("/api/login", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ email: email.trim(), password }),
      });
      router.replace("/");
      router.refresh();
    } catch {
      setError("Network error");
    } finally {
      setBusy(false);
    }
  }

  if (checking) {
    return <div className="text-sm text-slate-400">Loading…</div>;
  }

  return (
    <form onSubmit={submit} className="card w-full max-w-md space-y-4">
      <div>
        <h1 className="text-lg font-semibold text-white">Welcome — set up the gateway</h1>
        <p className="mt-1 text-sm text-slate-400">Create the first administrator account. You can change everything later in the panel.</p>
      </div>
      {error && <div className="rounded-md border border-rose-500/40 bg-rose-500/10 px-3 py-2 text-sm text-rose-200">{error}</div>}

      <div className="space-y-1">
        <label className="text-xs text-slate-400">Admin email</label>
        <input className="input" type="email" autoComplete="username" value={email} onChange={(e) => setEmail(e.target.value)} required />
      </div>
      <div className="grid grid-cols-2 gap-3">
        <div className="space-y-1">
          <label className="text-xs text-slate-400">Password (min 8)</label>
          <input className="input" type="password" autoComplete="new-password" value={password} onChange={(e) => setPassword(e.target.value)} required />
        </div>
        <div className="space-y-1">
          <label className="text-xs text-slate-400">Confirm</label>
          <input className="input" type="password" autoComplete="new-password" value={confirm} onChange={(e) => setConfirm(e.target.value)} required />
        </div>
      </div>
      {confirm && password !== confirm && <p className="text-xs text-rose-300">Passwords don’t match.</p>}

      <div className="border-t border-edge pt-3">
        <p className="mb-2 text-xs text-slate-500">Optional — you can set these later in Server Settings.</p>
        <div className="space-y-3">
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1">
              <label className="text-xs text-slate-400">Gateway name</label>
              <input className="input" value={gatewayName} onChange={(e) => setGatewayName(e.target.value)} placeholder="gateway.someserver.net" />
            </div>
            <div className="space-y-1">
              <label className="text-xs text-slate-400">Device port</label>
              <input className="input" inputMode="numeric" value={devicePort} onChange={(e) => setDevicePort(e.target.value)} placeholder="33000" />
            </div>
          </div>
          {!portValid && <p className="text-xs text-rose-300">Port must be a whole number between 1 and 65535.</p>}
          <div className="space-y-1">
            <label className="text-xs text-slate-400">GPS webhook URL</label>
            <input className="input" type="url" value={webhookUrl} onChange={(e) => setWebhookUrl(e.target.value)} placeholder="https://db.example.net/universal/gps/json/" />
          </div>
          <p className="text-xs text-slate-500">
            The device port is the TCP port units connect to. In Docker the container’s published port must match it.
          </p>
        </div>
      </div>

      <button className="btn-primary w-full" disabled={!valid || busy}>
        {busy ? "Setting up…" : "Finish setup"}
      </button>
    </form>
  );
}
