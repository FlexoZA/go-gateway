"use client";

import Link from "next/link";
import { useState } from "react";
import { api } from "@/lib/api";
import { useFetch } from "@/lib/useFetch";
import { useGatewayInfo, capsForUnit } from "@/lib/useGatewayInfo";
import { deviceConfigKind } from "@/lib/deviceConfig";
import { Badge, ErrorBanner, PageHeader, Spinner } from "@/components/ui";
import { useConfirm } from "@/components/confirm";
import { DeviceConfig } from "@/components/DeviceConfig";
import { CathexisConfig } from "@/components/CathexisConfig";
import { N62Config } from "@/components/N62Config";
import { DeviceVideo } from "@/components/DeviceVideo";
import { DeviceSnapshots } from "@/components/DeviceSnapshots";

type Conn = {
  serial: string;
  model?: string;
  protocol: string;
  remote_addr: string;
  connected_at: string;
  state?: string;
  commands?: string[];
};
type StatusResp = { serial: string; connection: Conn; telemetry: any };
type Device = { serial: string; protocol: string; status: string; first_seen_at: string; last_seen_at: string };

export default function DeviceDetailPage({ params }: { params: { serial: string } }) {
  const serial = decodeURIComponent(params.serial);
  // Live status (404s when the device isn't connected) + registry (always there).
  const status = useFetch<StatusResp>(`units/${encodeURIComponent(serial)}/status`, 5000);
  const devices = useFetch<{ devices: Device[] }>("devices", 15000);

  // The status endpoint 404s (→ status.error) the moment the device disconnects.
  // Ignore any last-successful body while the latest poll is failing, otherwise a
  // dead device would keep reporting "online" from stale data. On error we fall
  // back to the registry status below.
  const live = status.error ? undefined : status.data;
  const conn = live?.connection;
  const tele = live?.telemetry;
  const reg = devices.data?.devices?.find((d) => d.serial === serial);
  const online = !!conn;
  const sleeping = conn?.state === "sleep";
  const canWake = !!conn?.commands?.includes("wake_device");
  const state = sleeping ? "standby" : online ? "online" : reg?.status || "offline";
  type Tab = "status" | "config" | "video" | "snapshots";
  const [tab, setTab] = useState<Tab>("status");
  // The Config tab only exists when THIS device's unit type supports parameter
  // config AND the admin has a config screen for it; the Video tab only when the
  // unit type supports video (a GPS-only unit has neither).
  const info = useGatewayInfo();
  const unitType = conn?.protocol || reg?.protocol;
  const unitCaps = capsForUnit(info, unitType);
  const configKind = deviceConfigKind(unitType);
  const hasConfig = !!unitCaps?.has_config && !!configKind;
  const hasVideo = !!unitCaps?.has_video;
  const hasClips = !!unitCaps?.has_clips;
  const hasSnapshotCapture = !!unitCaps?.has_snapshots;
  const tabs: Tab[] = [
    "status",
    ...(hasConfig ? (["config"] as const) : []),
    ...(hasVideo ? (["video", "snapshots"] as const) : []),
  ];

  // Waking a standby device (Howen): the banner's button sends wake_device; the
  // 5s status poll then reflects the device coming online.
  const [waking, setWaking] = useState(false);
  const [wakeErr, setWakeErr] = useState<string | null>(null);
  async function wake() {
    setWaking(true);
    setWakeErr(null);
    try {
      await api(`units/${encodeURIComponent(serial)}/commands`, {
        method: "POST",
        body: JSON.stringify({ type: "wake_device" }),
      });
      await status.refresh();
    } catch (e: any) {
      setWakeErr(e.message || "Failed to wake device");
    } finally {
      setWaking(false);
    }
  }

  // Manual device reboot (Status tab). Available whenever the unit advertises the
  // reboot_unit command (Cathexis + Howen).
  const confirm = useConfirm();
  const canReboot = !!conn?.commands?.includes("reboot_unit");
  const [rebooting, setRebooting] = useState(false);
  const [rebootMsg, setRebootMsg] = useState<string | null>(null);
  async function reboot() {
    if (
      !(await confirm({
        title: "Reboot device?",
        body: "The unit will restart and disconnect for about a minute, then reconnect on its own.",
        confirmLabel: "Reboot",
      }))
    )
      return;
    setRebooting(true);
    setRebootMsg(null);
    try {
      await api(`units/${encodeURIComponent(serial)}/commands`, {
        method: "POST",
        body: JSON.stringify({ type: "reboot_unit" }),
      });
      setRebootMsg("Reboot sent — the unit will reconnect shortly.");
    } catch (e: any) {
      setRebootMsg(e.message || "Failed to reboot device");
    } finally {
      setRebooting(false);
    }
  }

  return (
    <div>
      <PageHeader
        title={<span className="font-mono">{serial}</span>}
        subtitle={
          <span className="flex items-center gap-2">
            {conn?.model || reg?.protocol || "device"}
            <Badge tone={state === "online" ? "green" : state === "standby" ? "amber" : "slate"}>{state}</Badge>
          </span>
        }
        action={
          <Link href="/devices" className="btn-ghost">
            ← All devices
          </Link>
        }
      />
      <ErrorBanner message={devices.error} />

      {/* Status | Config tabs (Config only when the unit supports config) */}
      {tabs.length > 1 && (
        <div className="mb-5 flex gap-1 border-b border-edge">
          {tabs.map((t) => (
            <button
              key={t}
              onClick={() => setTab(t)}
              className={`-mb-px border-b-2 px-4 py-2 text-sm capitalize ${
                tab === t ? "border-indigo-500 text-white" : "border-transparent text-slate-400 hover:text-slate-200"
              }`}
              disabled={(t === "config" || t === "video" || t === "snapshots") && !online}
              title={
                (t === "config" || t === "video" || t === "snapshots") && !online
                  ? "Connect the device to use this tab"
                  : undefined
              }
            >
              {t}
            </button>
          ))}
        </div>
      )}

      {/* Standby banner — shown on every tab except Config (which has its own
          wake affordance in the config editor). */}
      {sleeping && tab !== "config" && (
        <div className="mb-4 flex flex-wrap items-center gap-3 rounded-md border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-sm text-amber-200">
          <span>
            ⚠ This device is in <strong>standby</strong> — wake it to get live status, stream video, or pull clips.
          </span>
          {canWake && (
            <button className="btn-primary" onClick={wake} disabled={waking}>
              {waking ? "Waking…" : "Wake device"}
            </button>
          )}
          {wakeErr && (
            <span className="flex items-center gap-2 text-rose-300">
              {wakeErr}
              <button onClick={() => setWakeErr(null)} aria-label="Dismiss" className="text-rose-300/80 hover:text-rose-100">
                ✕
              </button>
            </span>
          )}
        </div>
      )}

      {hasConfig && tab === "config" ? (
        online ? (
          configKind === "cathexis" ? (
            <CathexisConfig serial={serial} />
          ) : configKind === "n62" ? (
            <N62Config serial={serial} unit={unitType!} />
          ) : (
            <DeviceConfig serial={serial} unit={unitType!} />
          )
        ) : (
          <div className="rounded-md border border-edge bg-panel px-4 py-3 text-sm text-slate-400">
            The device must be connected to read or edit its configuration.
          </div>
        )
      ) : hasVideo && tab === "video" ? (
        online ? (
          <DeviceVideo serial={serial} hasClips={hasClips} sleeping={sleeping} />
        ) : (
          <div className="rounded-md border border-edge bg-panel px-4 py-3 text-sm text-slate-400">
            The device must be connected to stream video or pull clips.
          </div>
        )
      ) : hasVideo && tab === "snapshots" ? (
        online ? (
          <DeviceSnapshots serial={serial} sleeping={sleeping} hasCapture={hasSnapshotCapture} />
        ) : (
          <div className="rounded-md border border-edge bg-panel px-4 py-3 text-sm text-slate-400">
            The device must be connected to capture snapshots.
          </div>
        )
      ) : (
        <>
          {!online && (
            <div className="mb-6 rounded-md border border-edge bg-panel px-4 py-3 text-sm text-slate-400">
              This device is not currently connected — showing registry info only. Live status appears when it connects.
            </div>
          )}

          <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        {/* Connection / server */}
        <Card title="Server connection">
          <KV k="State" v={<Badge tone={state === "online" ? "green" : state === "standby" ? "amber" : "slate"}>{state}</Badge>} />
          <KV k="Protocol" v={conn?.protocol || reg?.protocol || "—"} />
          <KV k="Model" v={conn?.model || "—"} />
          <KV k="Remote address" v={conn?.remote_addr || "—"} mono />
          <KV k="Connected at" v={fmt(conn?.connected_at)} />
          <KV k="Last status" v={fmt(tele?.updated_at)} />
          <KV k="First seen" v={fmt(reg?.first_seen_at)} />
          <KV k="Last seen" v={fmt(reg?.last_seen_at)} />
          <KV k="Commands" v={conn?.commands?.length ? `${conn.commands.length} available` : "—"} />
          {online && canReboot && (
            <div className="mt-3 border-t border-edge pt-3">
              <button className="btn-ghost w-full" onClick={reboot} disabled={rebooting}>
                {rebooting ? "Rebooting…" : "Reboot device"}
              </button>
              {rebootMsg && <p className="mt-2 text-xs text-slate-400">{rebootMsg}</p>}
            </div>
          )}
        </Card>

        {/* Mobile network / 4G */}
        {tele?.network && (
          <Card title="Mobile network (4G)">
            <KV k="Type" v={<span className="uppercase">{tele.network.type}</span>} />
            <KV
              k="Signal"
              v={
                tele.network.signal_pct != null ? (
                  <span className="flex items-center gap-2">
                    <Bar pct={tele.network.signal_pct} />
                    <span>{tele.network.signal_level}/10</span>
                  </span>
                ) : (
                  `${tele.network.signal_level ?? "—"}/10`
                )
              }
            />
            {tele.network.health && <KV k="Modem health" v={<Health v={tele.network.health} />} />}
          </Card>
        )}

        {/* Module health */}
        {tele?.modules && (
          <Card title="Modules">
            {tele.modules.mobile != null && <KV k="Mobile" v={<Health v={tele.modules.mobile} />} />}
            {tele.modules.gps != null && <KV k="GPS" v={<Health v={tele.modules.gps} />} />}
            {tele.modules.wifi != null && <KV k="Wi-Fi" v={<Health v={tele.modules.wifi} />} />}
            {tele.modules.gsensor != null && <KV k="G-sensor" v={<Health v={tele.modules.gsensor} />} />}
            {tele.modules.recording_raw != null && <KV k="Recording (raw)" v={String(tele.modules.recording_raw)} />}
          </Card>
        )}

        {/* Storage */}
        {Array.isArray(tele?.storage) && tele.storage.length > 0 && (
          <Card title="Storage">
            {tele.storage.map((d: any) => {
              const usedPct = d.size_mb > 0 ? Math.round(((d.size_mb - d.free_mb) / d.size_mb) * 100) : 0;
              return (
                <div key={d.id} className="mb-3 last:mb-0">
                  <div className="flex items-center justify-between text-sm">
                    <span>Disk {d.id}</span>
                    <Health v={d.status} />
                  </div>
                  {d.size_mb > 0 && (
                    <>
                      <Bar pct={usedPct} />
                      <div className="mt-1 text-xs text-slate-400">
                        {gb(d.size_mb - d.free_mb)} used of {gb(d.size_mb)} · {gb(d.free_mb)} free
                      </div>
                    </>
                  )}
                </div>
              );
            })}
          </Card>
        )}

        {/* GPS */}
        {tele?.location && (
          <Card title="GPS / location">
            <KV k="Position" v={tele.location.positioned ? <Badge tone="green">fixed</Badge> : <Badge tone="amber">no fix</Badge>} />
            <KV k="Latitude" v={num(tele.location.latitude, 6)} mono />
            <KV k="Longitude" v={num(tele.location.longitude, 6)} mono />
            <KV k="Speed" v={`${num(tele.location.speed_kmh, 1)} km/h`} />
            <KV k="Satellites" v={String(tele.location.satellites ?? "—")} />
            <KV k="Altitude" v={`${num(tele.location.altitude_m, 0)} m`} />
            <KV k="Bearing" v={`${tele.location.bearing ?? 0}°`} />
          </Card>
        )}

        {/* Vehicle / IO */}
        {tele?.vehicle && (
          <Card title="Vehicle">
            <div className="flex flex-wrap gap-2">
              <Flag on={tele.vehicle.ignition} label="Ignition" />
              <Flag on={tele.vehicle.brake} label="Brake" />
              <Flag on={tele.vehicle.reverse} label="Reverse" />
              <Flag on={tele.vehicle.turn_left} label="Left" />
              <Flag on={tele.vehicle.turn_right} label="Right" />
              <Flag on={tele.vehicle.door_left_front} label="L door" />
              <Flag on={tele.vehicle.door_right_front} label="R door" />
              <Flag on={tele.vehicle.standby} label="Standby" warn />
            </div>
          </Card>
        )}

        {/* SD card (Cathexis) */}
        {tele?.sd_card && (
          <Card title="SD card">
            <KV k="Status" v={tele.sd_card.present ? <Badge tone="green">present</Badge> : <Badge tone="rose">no card</Badge>} />
            {tele.sd_card.type && tele.sd_card.present && <KV k="Manufacturer" v={tele.sd_card.type} />}
            {tele.sd_card.serial && <KV k="Card serial" v={tele.sd_card.serial} mono />}
            {tele.sd_card.use_percent != null && <KV k="Used" v={`${tele.sd_card.use_percent}%`} />}
            {tele.sd_card.power_cycles != null && <KV k="Power cycles" v={String(tele.sd_card.power_cycles)} />}
          </Card>
        )}

        {/* Environment */}
        {tele?.environment && Object.keys(tele.environment).length > 0 && (
          <Card title="Environment">
            {tele.environment.temp_in_vehicle_c != null && <KV k="Cabin temp" v={`${tele.environment.temp_in_vehicle_c} °C`} />}
            {tele.environment.temp_out_vehicle_c != null && <KV k="Outside temp" v={`${tele.environment.temp_out_vehicle_c} °C`} />}
            {tele.environment.temp_device_c != null && <KV k="Device temp" v={`${num(tele.environment.temp_device_c, 1)} °C`} />}
            {tele.environment.temp_case_c != null && <KV k="Case temp" v={`${num(tele.environment.temp_case_c, 1)} °C`} />}
            {tele.environment.temp_modem_c != null && <KV k="Modem temp" v={`${num(tele.environment.temp_modem_c, 1)} °C`} />}
            {tele.environment.temp_motor_c != null && <KV k="Motor temp" v={`${tele.environment.temp_motor_c} °C`} />}
            {tele.environment.input_voltage_v != null && <KV k="Input voltage" v={`${num(tele.environment.input_voltage_v, 2)} V`} />}
            {tele.environment.input_current_a != null && <KV k="Input current" v={`${num(tele.environment.input_current_a, 2)} A`} />}
            {tele.environment.supercap_voltage_v != null && <KV k="Supercap" v={`${num(tele.environment.supercap_voltage_v, 2)} V`} />}
            {tele.environment.cpu_load_pct != null && <KV k="CPU load" v={`${tele.environment.cpu_load_pct}%`} />}
            {tele.environment.gpu_load_pct != null && <KV k="GPU load" v={`${tele.environment.gpu_load_pct}%`} />}
            {tele.environment.cell_level != null && <KV k="Cell signal" v={`${tele.environment.cell_level}/5`} />}
            {tele.environment.wifi_ssid && <KV k="Wi-Fi SSID" v={tele.environment.wifi_ssid} />}
            {tele.environment.wifi_level != null && <KV k="Wi-Fi signal" v={`${tele.environment.wifi_level}/5`} />}
            {tele.environment.humidity_in_vehicle != null && <KV k="Cabin humidity" v={`${tele.environment.humidity_in_vehicle}%`} />}
            {tele.environment.humidity_out_vehicle != null && <KV k="Outside humidity" v={`${tele.environment.humidity_out_vehicle}%`} />}
          </Card>
        )}

        {/* Motion sensor */}
        {tele?.sensors && (
          <Card title="Motion sensor">
            <KV k="X / Y / Z" v={`${num(tele.sensors.x, 2)} / ${num(tele.sensors.y, 2)} / ${num(tele.sensors.z, 2)}`} mono />
            <KV k="Tilt" v={num(tele.sensors.tilt, 2)} />
            <KV k="Impact" v={num(tele.sensors.impact, 2)} />
          </Card>
        )}
      </div>

          {online && !tele?.network && !tele?.location && !tele?.vehicle && (
            <div className="mt-4 text-sm text-slate-400">
              {status.loading ? <Spinner /> : "Connected — waiting for the device's first status report…"}
            </div>
          )}
        </>
      )}
    </div>
  );
}

function Card({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="card">
      <h3 className="mb-3 text-sm font-semibold text-slate-200">{title}</h3>
      <div className="space-y-1.5">{children}</div>
    </div>
  );
}
function KV({ k, v, mono }: { k: string; v: React.ReactNode; mono?: boolean }) {
  return (
    <div className="flex items-center justify-between gap-3 text-sm">
      <span className="text-slate-400">{k}</span>
      <span className={mono ? "font-mono text-slate-200" : "text-slate-200"}>{v}</span>
    </div>
  );
}
function Health({ v }: { v: string }) {
  const tone = v === "normal" || v === "ok" ? "green" : v === "abnormal" || v === "error" || v === "full" ? "rose" : v === "not_exist" || v === "none" ? "slate" : "amber";
  return <Badge tone={tone as any}>{v}</Badge>;
}
function Flag({ on, label, warn }: { on: boolean; label: string; warn?: boolean }) {
  const tone = on ? (warn ? "amber" : "green") : "slate";
  return <Badge tone={tone}>{label}{on ? " ●" : " ○"}</Badge>;
}
function Bar({ pct }: { pct: number }) {
  const p = Math.max(0, Math.min(100, pct));
  const color = p > 80 ? "bg-rose-500" : p > 50 ? "bg-amber-500" : "bg-emerald-500";
  return (
    <span className="inline-block h-2 w-24 overflow-hidden rounded bg-edge align-middle">
      <span className={`block h-full ${color}`} style={{ width: `${p}%` }} />
    </span>
  );
}
function num(v: any, d: number): string {
  return typeof v === "number" ? v.toFixed(d) : "—";
}
function gb(mb: number): string {
  if (mb == null) return "—";
  return mb >= 1024 ? `${(mb / 1024).toFixed(1)} GB` : `${mb} MB`;
}
function fmt(ts?: string): string {
  if (!ts) return "—";
  const d = new Date(ts);
  return isNaN(d.getTime()) ? ts : d.toLocaleString();
}
