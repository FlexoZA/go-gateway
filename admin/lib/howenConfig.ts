// howenConfig.ts — UI metadata for the Howen device config editor.
//
// The device exposes ~23 config segments (all string values, often deeply
// nested: per-channel chn0..15, per-region region0..11, per-day week0..6, alarm
// sub-objects). We can't hand-craft every field, so the editor is generic with
// inferred controls; this file is a CURATED OVERLAY that makes the common
// segments friendly (labels, control types, enums) and groups segments into the
// device's own menus. Anything without metadata falls back to inference +
// humanised names — so every setting stays editable.

export type FieldType = "toggle" | "number" | "text" | "select";

export interface FieldMeta {
  label?: string;
  type?: FieldType;
  options?: { value: string; label: string }[];
  readonly?: boolean;
  hidden?: boolean;
}

export interface SegmentMeta {
  label: string;
  readonly?: boolean;
  danger?: boolean; // confirm before saving (can disrupt connectivity/operation)
  note?: string;
  fields?: Record<string, FieldMeta>;
}

export interface Category {
  key: string;
  label: string;
  segments: string[];
}

// Category grouping mirrors the device's own web-UI menus.
export const CATEGORIES: Category[] = [
  { key: "network", label: "Network", segments: ["WIFI", "DIALUP", "SERVER", "ROAMING"] },
  { key: "time", label: "Time", segments: ["CLOCK", "DST"] },
  { key: "power", label: "Power", segments: ["POWER"] },
  { key: "recording", label: "Recording", segments: ["RECORD", "DISPLAY", "OSD", "MASK", "Privacy"] },
  { key: "alarms", label: "Alarms", segments: ["IOSET", "SPEED", "GSENSOR", "MOTIONDETECT", "ACC", "VOLTAGE"] },
  { key: "ptz", label: "PTZ", segments: ["PTZ"] },
  { key: "system", label: "System", segments: ["LANGUAGE", "JTBASE", "UPGRADE", "VERSIONINFO"] },
];

const onOff: FieldMeta = { type: "toggle" };

// Curated metadata for the common, high-value segments. The rest render generic.
export const SEGMENTS: Record<string, SegmentMeta> = {
  VERSIONINFO: {
    label: "Firmware",
    readonly: true,
    fields: {
      app: { label: "Firmware" }, kernel: { label: "Kernel" }, mcu: { label: "MCU" },
      boot: { label: "Bootloader" }, hardware: { label: "Hardware" }, rootfs: { label: "Root FS" },
    },
  },
  JTBASE: {
    label: "Identity",
    fields: {
      phonenum: { label: "Device ID", readonly: true },
      Sn0104: { label: "Serial number", readonly: true },
      ImeiLen: { label: "IMEI length", type: "number" },
      gpsinterval1: { label: "GPS interval (s)", type: "number" },
      gpsinterval2: { label: "GPS interval idle (s)", type: "number" },
      CandataInterval: { label: "CAN data interval (s)", type: "number" },
    },
  },
  WIFI: {
    label: "Wi-Fi",
    fields: {
      isOpen: { label: "Enabled", ...onOff },
      SSID: { label: "Network (SSID)" },
      Pwd: { label: "Password" },
      Dhcp: { label: "DHCP (automatic IP)", ...onOff },
      IpAddr: { label: "IP address" },
      GateWay: { label: "Gateway" },
      SubNet: { label: "Subnet mask" },
      UserName: { label: "Username" },
    },
  },
  DIALUP: {
    label: "Mobile data",
    fields: {
      switch: { label: "Enabled", ...onOff },
      apn: { label: "APN" },
      user: { label: "Username" },
      passwd: { label: "Password" },
      servercode: { label: "Dial number" },
      Telco: { label: "Carrier" },
    },
  },
  SERVER: {
    label: "Server / platform",
    danger: true,
    note: "These are the platforms the unit reports to. A wrong value can take it offline.",
    fields: {
      mainip: { label: "Address" },
      mainport: { label: "Port", type: "number" },
      enable: { label: "Enabled", ...onOff },
      bakip: { label: "Backup address" },
      bakport: { label: "Backup port", type: "number" },
    },
  },
  ROAMING: { label: "Roaming" },
  CLOCK: {
    label: "Clock",
    fields: {
      switch: { label: "NTP sync", ...onOff },
      ntpserver: { label: "NTP server" },
      ntpport: { label: "NTP port", type: "number" },
      timezone: { label: "Timezone" },
      offset: { label: "Offset" },
      buzzerSwitch: { label: "Buzzer", ...onOff },
    },
  },
  DST: {
    label: "Daylight saving",
    fields: { onoff: { label: "Enabled", ...onOff }, offset: { label: "Offset (min)", type: "number" } },
  },
  POWER: {
    label: "Power",
    danger: true,
    note: "Affects sleep, ignition-off recording and scheduled reboot.",
    fields: {
      switch: { label: "Power management", ...onOff },
      delay: { label: "Shutdown delay (s)", type: "number" },
      PowerOffTime: { label: "Power-off time (s)", type: "number" },
      AccOffRecTime: { label: "Record after ignition off (s)", type: "number" },
      LowPowerModeEnable: { label: "Low-power mode", ...onOff },
      TimeRebootEn: { label: "Scheduled reboot", ...onOff },
      RebootTime: { label: "Reboot time" },
      ScreenOffTime: { label: "Screen off (s)", type: "number" },
    },
  },
  LANGUAGE: {
    label: "Language & voice",
    fields: {
      lang: { label: "Language code", type: "number" }, // device-specific index; left raw (mapping unverified)
      VoiceOnOff: { label: "Voice prompts", ...onOff },
      VoiceVolume: { label: "Volume", type: "number" },
      UpgradeVoice: { label: "Upgrade voice", ...onOff },
    },
  },
  UPGRADE: { label: "Upgrade" },
  RECORD: { label: "Recording" },
  DISPLAY: { label: "Image / display" },
  OSD: { label: "On-screen text (OSD)" },
  MASK: { label: "Privacy mask" },
  Privacy: { label: "Privacy" },
  IOSET: { label: "Inputs / outputs" },
  SPEED: { label: "Speed alarms" },
  GSENSOR: { label: "G-sensor" },
  MOTIONDETECT: { label: "Motion detection" },
  ACC: { label: "Ignition (ACC)" },
  VOLTAGE: { label: "Voltage alarms" },
  PTZ: { label: "PTZ" },
};

export function segmentMeta(seg: string): SegmentMeta {
  return SEGMENTS[seg] || { label: humanize(seg) };
}

// Humanise a raw field/segment name: camelCase / snake_case → Title Case.
export function humanize(s: string): string {
  return s
    .replace(/([a-z0-9])([A-Z])/g, "$1 $2")
    .replace(/[_-]+/g, " ")
    .replace(/\s+/g, " ")
    .trim()
    .replace(/\b\w/g, (c) => c.toUpperCase());
}

// Infer a control type from a leaf string value.
export function inferType(v: string): FieldType {
  if (v === "0" || v === "1") return "toggle";
  if (/^-?\d+$/.test(v)) return "number";
  return "text";
}
