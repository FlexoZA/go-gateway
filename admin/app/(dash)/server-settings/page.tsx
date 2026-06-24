"use client";

import { useEffect, useState } from "react";
import { api } from "@/lib/api";
import { useFetch } from "@/lib/useFetch";
import { Badge, Empty, ErrorBanner, PageHeader, Spinner } from "@/components/ui";

type Webhook = { id: number; name: string; url: string; is_enabled: boolean; updated_at: string };
type Setting = { key: string; value: string; updated_at: string };

export default function ServerSettingsPage() {
  const { data, error, loading, refresh } = useFetch<{ webhooks: Webhook[] }>("webhooks");
  const settings = useFetch<{ settings: Setting[] }>("settings");
  const [actionError, setActionError] = useState<string | null>(null);

  const webhooks = data?.webhooks ?? [];
  const enabledCount = webhooks.filter((w) => w.is_enabled).length;

  const settingVal = (k: string) => settings.data?.settings.find((s) => s.key === k)?.value ?? "";

  async function save(id: number, body: { name: string; url: string; is_enabled: boolean }) {
    setActionError(null);
    try {
      await api(`webhooks/${id}`, { method: "PUT", body: JSON.stringify(body) });
      await refresh();
    } catch (e: any) {
      setActionError(e.message || "Save failed");
    }
  }

  async function remove(id: number) {
    setActionError(null);
    try {
      await api(`webhooks/${id}`, { method: "DELETE" });
      await refresh();
    } catch (e: any) {
      setActionError(e.message || "Delete failed");
    }
  }

  async function add(body: { name: string; url: string; is_enabled: boolean }) {
    setActionError(null);
    try {
      await api("webhooks", { method: "POST", body: JSON.stringify(body) });
      await refresh();
    } catch (e: any) {
      setActionError(e.message || "Add failed");
    }
  }

  return (
    <div>
      <PageHeader title="Server Settings" subtitle="Gateway-wide configuration. Changes apply to the running server instantly." />
      <ErrorBanner message={actionError || error} />

      <div className="mb-8 max-w-3xl space-y-4">
        <GatewayNameCard current={settingVal("gateway_name")} onSaved={settings.refresh} />
        <DeviceAuthCard current={settingVal("device_reject_unknown")} onSaved={settings.refresh} />
      </div>

      <div className="max-w-3xl space-y-4">
        <div>
          <h2 className="text-sm font-semibold text-white">GPS / event webhooks</h2>
          <p className="mt-1 text-sm text-slate-400">
            External endpoints that store <span className="text-slate-200">all GPS and event data</span>. Every device message is
            POSTed (the universal JSON package) to each <span className="text-slate-200">enabled</span> webhook.
          </p>
        </div>

        {enabledCount === 0 && !loading && (
          <div className="rounded-md border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-sm text-amber-200">
            No webhook is enabled — GPS/event data is not being stored anywhere.
          </div>
        )}

        {loading ? (
          <Spinner />
        ) : webhooks.length === 0 ? (
          <Empty>No webhooks configured yet. Add one below.</Empty>
        ) : (
          webhooks.map((w) => <WebhookRow key={w.id} webhook={w} onSave={save} onDelete={remove} />)
        )}

        <AddWebhook onAdd={add} />
      </div>
    </div>
  );
}

function WebhookRow({
  webhook,
  onSave,
  onDelete,
}: {
  webhook: Webhook;
  onSave: (id: number, body: { name: string; url: string; is_enabled: boolean }) => Promise<void>;
  onDelete: (id: number) => Promise<void>;
}) {
  const [name, setName] = useState(webhook.name);
  const [url, setUrl] = useState(webhook.url);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    setName(webhook.name);
    setUrl(webhook.url);
  }, [webhook.name, webhook.url]);

  const dirty = name !== webhook.name || url !== webhook.url;

  async function toggle() {
    setBusy(true);
    await onSave(webhook.id, { name, url, is_enabled: !webhook.is_enabled });
    setBusy(false);
  }

  return (
    <div className="card space-y-3">
      <div className="flex items-center justify-between">
        <Badge tone={webhook.is_enabled ? "green" : "slate"}>{webhook.is_enabled ? "Enabled" : "Disabled"}</Badge>
        <label className="flex cursor-pointer items-center gap-2 text-xs text-slate-300">
          <input type="checkbox" checked={webhook.is_enabled} onChange={toggle} disabled={busy} />
          {webhook.is_enabled ? "Disable" : "Enable"}
        </label>
      </div>
      <div className="grid grid-cols-1 gap-3 md:grid-cols-12">
        <div className="md:col-span-3">
          <label className="text-xs text-slate-400">Name</label>
          <input className="input mt-1" value={name} onChange={(e) => setName(e.target.value)} placeholder="e.g. primary DB" />
        </div>
        <div className="md:col-span-9">
          <label className="text-xs text-slate-400">URL</label>
          <input className="input mt-1" type="url" value={url} onChange={(e) => setUrl(e.target.value)} placeholder="https://db.example.net/universal/gps/json/" />
        </div>
      </div>
      <div className="flex items-center justify-between">
        <span className="text-xs text-slate-500">Updated {new Date(webhook.updated_at).toLocaleString()}</span>
        <div className="flex gap-2">
          <button
            className="btn-primary"
            disabled={!dirty || busy || !url.trim()}
            onClick={async () => {
              setBusy(true);
              await onSave(webhook.id, { name, url, is_enabled: webhook.is_enabled });
              setBusy(false);
            }}
          >
            Save
          </button>
          <button
            className="btn-danger"
            disabled={busy}
            onClick={() => {
              if (confirm(`Delete webhook "${webhook.name || webhook.url}"?`)) onDelete(webhook.id);
            }}
          >
            Delete
          </button>
        </div>
      </div>
    </div>
  );
}

function DeviceAuthCard({ current, onSaved }: { current: string; onSaved: () => void }) {
  const reject = ["true", "1", "yes", "on"].includes(current.trim().toLowerCase());
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function set(v: boolean) {
    setBusy(true);
    setError(null);
    try {
      await api("settings", { method: "PUT", body: JSON.stringify({ key: "device_reject_unknown", value: v ? "true" : "false" }) });
      onSaved();
    } catch (e: any) {
      setError(e.message || "Save failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="card space-y-3">
      <div>
        <h2 className="text-sm font-semibold text-white">Device authorization</h2>
        <p className="mt-1 text-sm text-slate-400">How the gateway handles a device whose serial isn’t in the registry yet. Applies instantly.</p>
      </div>
      {error && <div className="rounded-md border border-rose-500/40 bg-rose-500/10 px-3 py-2 text-sm text-rose-200">{error}</div>}
      <label className="flex cursor-pointer items-center justify-between gap-3 rounded-md border border-edge bg-ink px-3 py-2">
        <span className="text-sm text-slate-200">Require approval for unknown devices</span>
        <input type="checkbox" checked={reject} disabled={busy} onChange={(e) => set(e.target.checked)} />
      </label>
      <p className="text-xs text-slate-500">
        {reject
          ? "On (default) — unknown serials are quarantined and rejected until you approve them on the Devices page."
          : "Off — unknown serials are auto-registered and admitted immediately."}
      </p>
    </div>
  );
}

function GatewayNameCard({ current, onSaved }: { current: string; onSaved: () => void }) {
  const [value, setValue] = useState(current);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);

  useEffect(() => {
    setValue(current);
  }, [current]);

  const dirty = value !== current;

  async function save() {
    setBusy(true);
    setError(null);
    setSaved(false);
    try {
      await api("settings", { method: "PUT", body: JSON.stringify({ key: "gateway_name", value: value.trim() }) });
      setSaved(true);
      onSaved();
    } catch (e: any) {
      setError(e.message || "Save failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="card space-y-3">
      <div>
        <h2 className="text-sm font-semibold text-white">Gateway identity</h2>
        <p className="mt-1 text-sm text-slate-400">
          The gateway name sent in the <span className="font-mono text-slate-200">&quot;gateway&quot;</span> field of every universal
          JSON message to the webhook. Applies to the running gateway instantly.
        </p>
      </div>

      {error && <div className="rounded-md border border-rose-500/40 bg-rose-500/10 px-3 py-2 text-sm text-rose-200">{error}</div>}
      {saved && !dirty && (
        <div className="rounded-md border border-emerald-500/40 bg-emerald-500/10 px-3 py-2 text-sm text-emerald-200">
          Saved. New messages carry this name immediately.
        </div>
      )}

      <div className="flex items-end gap-3">
        <div className="grow">
          <label className="text-xs text-slate-400">Gateway name</label>
          <input className="input mt-1" value={value} onChange={(e) => setValue(e.target.value)} placeholder="gateway.someserver.net" />
        </div>
        <button className="btn-primary" onClick={save} disabled={!dirty || busy}>
          {busy ? "Saving…" : "Save"}
        </button>
      </div>

      {!current && (
        <div className="rounded-md border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-xs text-amber-200">
          No gateway name set — the <span className="font-mono">gateway</span> field will be null in outgoing messages.
        </div>
      )}
    </div>
  );
}

function AddWebhook({ onAdd }: { onAdd: (body: { name: string; url: string; is_enabled: boolean }) => Promise<void> }) {
  const [name, setName] = useState("");
  const [url, setUrl] = useState("");
  const [enabled, setEnabled] = useState(true);
  const [busy, setBusy] = useState(false);

  return (
    <div className="card space-y-3 border-dashed">
      <h3 className="text-sm font-semibold text-slate-300">Add webhook</h3>
      <div className="grid grid-cols-1 gap-3 md:grid-cols-12">
        <div className="md:col-span-3">
          <label className="text-xs text-slate-400">Name</label>
          <input className="input mt-1" value={name} onChange={(e) => setName(e.target.value)} placeholder="optional" />
        </div>
        <div className="md:col-span-9">
          <label className="text-xs text-slate-400">URL</label>
          <input className="input mt-1" type="url" value={url} onChange={(e) => setUrl(e.target.value)} placeholder="https://db.example.net/universal/gps/json/" />
        </div>
      </div>
      <div className="flex items-center justify-between">
        <label className="flex items-center gap-2 text-sm text-slate-300">
          <input type="checkbox" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} /> Enabled
        </label>
        <button
          className="btn-primary"
          disabled={busy || !url.trim()}
          onClick={async () => {
            setBusy(true);
            await onAdd({ name: name.trim(), url: url.trim(), is_enabled: enabled });
            setBusy(false);
            setName("");
            setUrl("");
            setEnabled(true);
          }}
        >
          {busy ? "Adding…" : "Add webhook"}
        </button>
      </div>
    </div>
  );
}
