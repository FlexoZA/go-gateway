"use client";

import Link from "next/link";
import { useState } from "react";
import { useFetch } from "@/lib/useFetch";
import { useGatewayInfo, capsForUnit } from "@/lib/useGatewayInfo";
import { deviceConfigSchema } from "@/lib/deviceConfig";
import { Badge, ErrorBanner, PageHeader, Spinner } from "@/components/ui";
import { DeviceConfig } from "@/components/DeviceConfig";
import { DeviceVideo } from "@/components/DeviceVideo";

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

  const conn = status.data?.connection;
  const tele = status.data?.telemetry;
  const reg = devices.data?.devices?.find((d) => d.serial === serial);
  const online = !!conn;
  const sleeping = conn?.state === "sleep";
  const canWake = !!conn?.commands?.includes("wake_device");
  const state = sleeping ? "standby" : online ? "online" : reg?.status || "offline";
  type Tab = "status" | "config" | "video";
  const [tab, setTab] = useState<Tab>("status");
  // The Config tab only exists when THIS device's unit type supports parameter
  // config AND the admin has a config screen for it; the Video tab only when the
  // unit type supports video (a GPS-only unit has neither).
  const info = useGatewayInfo();
  const unitType = conn?.protocol || reg?.protocol;
  const unitCaps = capsForUnit(info, unitType);
  const hasConfig = !!unitCaps?.has_config && !!deviceConfigSchema(unitType);
  const hasVideo = !!unitCaps?.has_video;
  const hasClips = !!unitCaps?.has_clips;
  const tabs: Tab[] = ["status", ...(hasConfig ? (["config"] as const) : []), ...(hasVideo ? (["video"] as const) : [])];

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
              disabled={(t === "config" || t === "video") && !online}
              title={(t === "config" || t === "video") && !online ? "Connect the device to use this tab" : undefined}
            >
              {t}
            </button>
          ))}
        </div>
      )}

      {hasConfig && tab === "config" ? (
        online ? (
          <DeviceConfig serial={serial} unit={unitType!} />
        ) : (
          <div className="rounded-md border border-edge bg-panel px-4 py-3 text-sm text-slate-400">
            The device must be connected to read or edit its configuration.
          </div>
        )
      ) : hasVideo && tab === "video" ? (
        online ? (
          <DeviceVideo serial={serial} hasClips={hasClips} sleeping={sleeping} canWake={canWake} />
        ) : (
          <div className="rounded-md border border-edge bg-panel px-4 py-3 text-sm text-slate-400">
            The device must be connected to stream video or pull clips.
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

        {/* Environment */}
        {tele?.environment && Object.keys(tele.environment).length > 0 && (
          <Card title="Environment">
            {tele.environment.temp_in_vehicle_c != null && <KV k="Cabin temp" v={`${tele.environment.temp_in_vehicle_c} °C`} />}
            {tele.environment.temp_out_vehicle_c != null && <KV k="Outside temp" v={`${tele.environment.temp_out_vehicle_c} °C`} />}
            {tele.environment.temp_device_c != null && <KV k="Device temp" v={`${tele.environment.temp_device_c} °C`} />}
            {tele.environment.temp_motor_c != null && <KV k="Motor temp" v={`${tele.environment.temp_motor_c} °C`} />}
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
