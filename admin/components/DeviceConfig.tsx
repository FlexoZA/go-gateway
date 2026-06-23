"use client";

import { useCallback, useEffect, useState } from "react";
import { api } from "@/lib/api";
import { Spinner } from "@/components/ui";

// DeviceConfig reads and edits a connected unit's parameter config (Wi-Fi,
// mobile, server, …) over the gateway. It mirrors the device's own groupings and
// only ever writes the fields the user changed (firmware quirk: a full
// read-modify-write writes back garbage string fields).

type Sc = Record<string, any>;

export function DeviceConfig({ serial }: { serial: string }) {
  const [sc, setSc] = useState<Sc | null>(null);
  const [dirty, setDirty] = useState<Sc>({});
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const res = await api<{ sc: Sc }>(`units/${encodeURIComponent(serial)}/config`);
      setSc(res.sc || {});
      setDirty({});
    } catch (e: any) {
      setError(e.message || "Failed to read config (the device may be in standby)");
    } finally {
      setLoading(false);
    }
  }, [serial]);

  useEffect(() => {
    load();
  }, [load]);

  // Flat-segment field accessors (WIFI, DIALUP).
  const get = (seg: string, field: string): string => {
    const d = dirty[seg]?.[field];
    return d !== undefined ? d : (sc?.[seg]?.[field] ?? "");
  };
  const set = (seg: string, field: string, val: string) =>
    setDirty((p) => ({ ...p, [seg]: { ...p[seg], [field]: val } }));

  // Nested SERVER accessors (server0..3).
  const getSrv = (key: string, field: string): string => {
    const d = dirty.SERVER?.[key]?.[field];
    return d !== undefined ? d : (sc?.SERVER?.[key]?.[field] ?? "");
  };
  const setSrv = (key: string, field: string, val: string) =>
    setDirty((p) => ({ ...p, SERVER: { ...p.SERVER, [key]: { ...p.SERVER?.[key], [field]: val } } }));

  const segDirty = (seg: string) => dirty[seg] && Object.keys(dirty[seg]).length > 0;

  async function save(seg: string, confirmMsg?: string) {
    if (!segDirty(seg)) return;
    if (confirmMsg && !confirm(confirmMsg)) return;
    setSaving(seg);
    setError(null);
    setNotice(null);
    try {
      const res = await api<{ sc: Sc }>(`units/${encodeURIComponent(serial)}/config`, {
        method: "PUT",
        body: JSON.stringify({ sc: { [seg]: dirty[seg] } }),
      });
      // Merge the device's read-back and clear this segment's pending edits.
      setSc((p) => ({ ...(p || {}), ...(res.sc || {}) }));
      setDirty((p) => {
        const n = { ...p };
        delete n[seg];
        return n;
      });
      setNotice(`${seg} saved.`);
    } catch (e: any) {
      setError(e.message || "Save failed");
    } finally {
      setSaving(null);
    }
  }

  if (loading) {
    return (
      <div className="flex items-center gap-3 text-sm text-slate-400">
        <Spinner /> Reading configuration from the device…
      </div>
    );
  }
  if (!sc) {
    return (
      <div>
        <ErrBox msg={error} />
        <button className="btn-primary" onClick={load}>Retry</button>
      </div>
    );
  }

  const servers = ["server0", "server1", "server2", "server3"].filter((k) => sc.SERVER?.[k]);

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <p className="text-xs text-slate-500">Changes are written to the unit immediately on Save. Only edited fields are sent.</p>
        <button className="btn-ghost" onClick={load} disabled={!!saving}>Reload from device</button>
      </div>
      <ErrBox msg={error} />
      {notice && <div className="rounded-md border border-emerald-500/40 bg-emerald-500/10 px-3 py-2 text-sm text-emerald-200">{notice}</div>}

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        {/* Device info (read-only) */}
        <Card title="Device info">
          <RO k="Serial / ID" v={sc.JTBASE?.phonenum} />
          <RO k="Firmware" v={sc.VERSIONINFO?.app} />
          <RO k="Kernel" v={sc.VERSIONINFO?.kernel} />
          <RO k="MCU" v={sc.VERSIONINFO?.mcu} />
          <RO k="Hardware" v={sc.VERSIONINFO?.hardware} />
        </Card>

        {/* Wi-Fi */}
        {sc.WIFI && (
          <Card title="Wi-Fi" footer={<SaveBtn dirty={segDirty("WIFI")} saving={saving === "WIFI"} onClick={() => save("WIFI")} />}>
            <Toggle label="Enabled" value={get("WIFI", "isOpen")} onChange={(v) => set("WIFI", "isOpen", v)} />
            <Text label="Network (SSID)" value={get("WIFI", "SSID")} onChange={(v) => set("WIFI", "SSID", v)} />
            <Text label="Password" value={get("WIFI", "Pwd")} onChange={(v) => set("WIFI", "Pwd", v)} />
            <Toggle label="DHCP (automatic IP)" value={get("WIFI", "Dhcp")} onChange={(v) => set("WIFI", "Dhcp", v)} />
            {get("WIFI", "Dhcp") !== "1" && (
              <>
                <Text label="IP address" value={get("WIFI", "IpAddr")} onChange={(v) => set("WIFI", "IpAddr", v)} mono />
                <Text label="Gateway" value={get("WIFI", "GateWay")} onChange={(v) => set("WIFI", "GateWay", v)} mono />
              </>
            )}
          </Card>
        )}

        {/* Mobile data */}
        {sc.DIALUP && (
          <Card title="Mobile data" footer={<SaveBtn dirty={segDirty("DIALUP")} saving={saving === "DIALUP"} onClick={() => save("DIALUP")} />}>
            <Toggle label="Enabled" value={get("DIALUP", "switch")} onChange={(v) => set("DIALUP", "switch", v)} />
            <Text label="APN" value={get("DIALUP", "apn")} onChange={(v) => set("DIALUP", "apn", v)} />
            <Text label="Username" value={get("DIALUP", "user")} onChange={(v) => set("DIALUP", "user", v)} />
            <Text label="Password" value={get("DIALUP", "passwd")} onChange={(v) => set("DIALUP", "passwd", v)} />
            <Text label="Dial number" value={get("DIALUP", "servercode")} onChange={(v) => set("DIALUP", "servercode", v)} mono />
          </Card>
        )}

        {/* Server / platform */}
        {sc.SERVER && (
          <Card
            title="Server / platform"
            footer={
              <SaveBtn
                dirty={segDirty("SERVER")}
                saving={saving === "SERVER"}
                onClick={() => save("SERVER", "Changing the server can disconnect this unit from the gateway. Continue?")}
                danger
              />
            }
          >
            <p className="mb-2 text-xs text-amber-300/80">⚠ These are the platforms the unit reports to. A wrong value can take it offline.</p>
            {servers.map((key) => (
              <div key={key} className="mb-3 rounded-md border border-edge p-2 last:mb-0">
                <div className="mb-1 flex items-center justify-between">
                  <span className="text-xs font-semibold text-slate-300">{key.replace("server", "Server ")}</span>
                  <Toggle label="" inline value={getSrv(key, "enable")} onChange={(v) => setSrv(key, "enable", v)} />
                </div>
                <Text label="Address" value={getSrv(key, "mainip")} onChange={(v) => setSrv(key, "mainip", v)} mono />
                <Text label="Port" value={getSrv(key, "mainport")} onChange={(v) => setSrv(key, "mainport", v)} mono />
              </div>
            ))}
          </Card>
        )}
      </div>
    </div>
  );
}

/* ---------- small field/layout components ---------- */

function Card({ title, children, footer }: { title: string; children: React.ReactNode; footer?: React.ReactNode }) {
  return (
    <div className="card flex flex-col">
      <h3 className="mb-3 text-sm font-semibold text-slate-200">{title}</h3>
      <div className="grow space-y-2">{children}</div>
      {footer && <div className="mt-3 flex justify-end">{footer}</div>}
    </div>
  );
}
function RO({ k, v }: { k: string; v?: string }) {
  return (
    <div className="flex items-center justify-between gap-3 text-sm">
      <span className="text-slate-400">{k}</span>
      <span className="font-mono text-slate-200">{v || "—"}</span>
    </div>
  );
}
function Text({ label, value, onChange, mono }: { label: string; value: string; onChange: (v: string) => void; mono?: boolean }) {
  return (
    <label className="flex items-center justify-between gap-3 text-sm">
      <span className="shrink-0 text-slate-400">{label}</span>
      <input className={`input w-48 ${mono ? "font-mono" : ""}`} value={value} onChange={(e) => onChange(e.target.value)} />
    </label>
  );
}
function Toggle({ label, value, onChange, inline }: { label: string; value: string; onChange: (v: string) => void; inline?: boolean }) {
  const on = value === "1";
  return (
    <label className={`flex items-center ${inline ? "gap-2" : "justify-between"} gap-3 text-sm`}>
      {label && <span className="text-slate-400">{label}</span>}
      <button
        type="button"
        onClick={() => onChange(on ? "0" : "1")}
        className={`relative h-5 w-9 rounded-full transition ${on ? "bg-emerald-500" : "bg-edge"}`}
      >
        <span className={`absolute top-0.5 h-4 w-4 rounded-full bg-white transition ${on ? "left-4" : "left-0.5"}`} />
      </button>
    </label>
  );
}
function SaveBtn({ dirty, saving, onClick, danger }: { dirty?: boolean; saving: boolean; onClick: () => void; danger?: boolean }) {
  return (
    <button className={danger ? "btn-danger" : "btn-primary"} disabled={!dirty || saving} onClick={onClick}>
      {saving ? "Saving…" : dirty ? "Save changes" : "Saved"}
    </button>
  );
}
function ErrBox({ msg }: { msg?: string | null }) {
  if (!msg) return null;
  return <div className="mb-3 rounded-md border border-rose-500/40 bg-rose-500/10 px-3 py-2 text-sm text-rose-200">{msg}</div>;
}
