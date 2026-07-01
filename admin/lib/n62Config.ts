// n62Config.ts — UI metadata for the N62 / JT808 dashcam config editor.
//
// The N62 exposes its config as ULV ParamType "segments" (GenDevInfo, NetCms, …),
// the same JSON the device's own web UI reads/writes. The gateway tunnels those
// Get/Set calls over the JT808 CMS link (0xB050/0xB051), so a segment read here is
// the device's live truth and a write is applied on the device.
//
// Like Cathexis (and unlike Howen's all-string segments), N62 fields have real
// types: numbers, enums, and text. This file is the CURATED OVERLAY that gives the
// practical fields friendly labels + typed controls and groups segments into menus.
// The device merges partial Sets (verified: writing only DevName leaves DevId
// intact), so the editor sends only changed fields — untouched fields, and whole
// segments not covered here, are never written.
//
// Field KEYS below are the device's JSON keys (what a segment Get returns), NOT the
// device web UI's form-input ids — the two differ (e.g. web "Encrypt" → JSON
// "EncryptType"). Enum option values were taken from the device's config pages.

export type N62FieldType = "toggle" | "number" | "text" | "password" | "select";

export interface N62FieldMeta {
  label?: string;
  type?: N62FieldType;
  options?: { value: string; label: string }[];
  readonly?: boolean;
  hidden?: boolean; // not rendered and never written (keeps partial saves safe)
  help?: string;
  min?: number;
  max?: number;
}

export interface N62Category {
  key: string;
  label: string;
  // segments read together when this category is opened (module filter for the GET).
  segments: string[];
}

// Categories map to the device's setup menus; each lists the ULV segments it needs.
export const CATEGORIES: N62Category[] = [
  { key: "general", label: "General", segments: ["GenDevInfo", "GenDateTime", "GenUser"] },
  { key: "vehicle", label: "Vehicle", segments: ["VehBaseInfo", "VehPosition"] },
  { key: "network", label: "Network", segments: ["NetCms", "NetWifi", "NetXg", "NetWired"] },
  { key: "recording", label: "Recording", segments: ["RecAttr", "RecStream_M", "RecStream_S"] },
];

// humanize turns a CamelCase/underscore key into a spaced label as a fallback.
export function humanize(key: string): string {
  return key
    .replace(/_/g, " ")
    .replace(/([a-z])([A-Z])/g, "$1 $2")
    .replace(/\b\w/g, (c) => c.toUpperCase());
}

// ---- shared enum option sets ----

const ON_OFF = [
  { value: "0", label: "Off" },
  { value: "1", label: "On" },
];

// GMT-12 … GMT+12, matching the device's Zone index (0…24).
const ZONE_OPTIONS = Array.from({ length: 25 }, (_, i) => {
  const gmt = i - 12;
  const label = gmt === 0 ? "GMT±00" : `GMT${gmt > 0 ? "+" : "-"}${String(Math.abs(gmt)).padStart(2, "0")}`;
  return { value: String(i), label };
});

const ZONE_OFFSET_OPTIONS = [
  { value: "0", label: "+0 min" },
  { value: "1", label: "+15 min" },
  { value: "2", label: "+30 min" },
  { value: "3", label: "+45 min" },
];

const RES_OPTIONS = [
  { value: "0", label: "CIF" },
  { value: "1", label: "D1" },
  { value: "2", label: "960H" },
  { value: "3", label: "720P" },
  { value: "4", label: "1080N" },
  { value: "5", label: "1080P" },
  { value: "6", label: "4M" },
];

const QUALITY_OPTIONS = [
  { value: "0", label: "Best" },
  { value: "1", label: "Better" },
  { value: "2", label: "Good" },
  { value: "3", label: "General" },
];

const CMS_PROTOCOL_OPTIONS = [
  { value: "0", label: "None" },
  { value: "1", label: "Reserve" },
  { value: "2", label: "JT808_19" },
  { value: "3", label: "JT808_13" },
  { value: "5", label: "JT808_ulv" },
  { value: "6", label: "JT808_vn779" },
];

// ---- per-segment field metadata (keyed by device JSON field name) ----

export const GEN_DEV_INFO: Record<string, N62FieldMeta> = {
  DevName: { label: "Device name", type: "text" },
  DevId: {
    label: "Device ID",
    type: "text",
    readonly: true,
    help: "The JT808 terminal identity the gateway addresses this unit by — changing it disconnects the device.",
  },
  SoftVer: { label: "Firmware", type: "text", readonly: true },
  McuVer: { label: "MCU version", type: "text", readonly: true },
  AlgVer: { label: "Algorithm version", type: "text", readonly: true },
  ResVer: { label: "Resource version", type: "text", readonly: true },
  ChipId: { label: "Chip ID", type: "text", readonly: true },
  AiStatus: { label: "AI features", type: "text", readonly: true },
};

// GenDateTime. Zone ("idx,offset") and NtpSync ("enable,server") are compound in
// the JSON; the editor splits/rejoins them into the *_idx/*_off and *_en/*_srv
// virtual fields below (see splitCompound/joinCompound). DateTime is device-owned
// (GPS/NTP keep it synced) and hidden so a save never pushes a stale wall-clock.
export const GEN_DATETIME: Record<string, N62FieldMeta> = {
  Zone_idx: { label: "Time zone", type: "select", options: ZONE_OPTIONS },
  Zone_off: { label: "Zone offset", type: "select", options: ZONE_OFFSET_OPTIONS },
  DateFormat: {
    label: "Date format",
    type: "select",
    options: [
      { value: "0", label: "MM/DD/YYYY" },
      { value: "1", label: "YYYY/MM/DD" },
      { value: "2", label: "DD/MM/YYYY" },
    ],
  },
  GpsSync: { label: "Sync clock from GPS", type: "select", options: ON_OFF },
  Ntp_en: { label: "Sync clock from NTP", type: "select", options: ON_OFF },
  Ntp_srv: {
    label: "NTP server",
    type: "select",
    options: [
      { value: "0", label: "time.windows.com" },
      { value: "1", label: "pool.ntp.org" },
    ],
  },
  DateTime: { hidden: true },
  TimeFormat: { hidden: true }, // not user-editable on the device
};

// GenUser — the device's built-in web-UI login (not the gateway login).
export const GEN_USER: Record<string, N62FieldMeta> = {
  Enable: { label: "Require web login", type: "select", options: ON_OFF },
  User: {
    label: "Account",
    type: "select",
    options: [
      { value: "0", label: "Admin" },
      { value: "1", label: "Guest" },
    ],
  },
  Password: { label: "Password", type: "password", help: "Up to 8 characters." },
};

export const VEH_BASE_INFO: Record<string, N62FieldMeta> = {
  Company: { label: "Company", type: "text" },
  CarPlate: { label: "Number plate", type: "text" },
  DriverName: { label: "Driver name", type: "text" },
  DriverLic: { label: "Driver licence", type: "text" },
  PhoneNum: { label: "SIM / phone number", type: "text" },
  AssemblyDate: { label: "Assembly date", type: "text", readonly: true },
  ShortName: { hidden: true },
};

export const VEH_POSITION: Record<string, N62FieldMeta> = {
  GpsMode: {
    label: "GNSS mode",
    type: "select",
    options: [
      { value: "3", label: "GPS + BeiDou" },
      { value: "5", label: "GPS + GLONASS" },
    ],
  },
  GpsUpInterval: { label: "Upload interval (s)", type: "number", min: 0, max: 99 },
  GpsBatchNum: { label: "Batch size", type: "number", min: 1, max: 99, help: "Positions per upload." },
  SpdFilter: { label: "Speed filter", type: "number", min: 0, max: 5 },
  SpdCorrV: { label: "Speed correction", type: "number", min: 0, max: 9 },
};

// One CMS server sub-object (Server_00 / Server_01). ServersAddr is "host:port".
// The device derives "enabled" from Protocol (None = disabled) and its own save
// writes only these three fields per server, so that's what the editor sends.
export const CMS_SERVER: Record<string, N62FieldMeta> = {
  ServersAddr: { label: "Server address", type: "text", help: "host:port" },
  Protocol: { label: "Protocol", type: "select", options: CMS_PROTOCOL_OPTIONS, help: "None disables this server." },
  VisitType: {
    label: "Address type",
    type: "select",
    options: [
      { value: "0", label: "IP" },
      { value: "1", label: "Domain" },
    ],
  },
};

// CMS_SERVER_WRITE — the exact fields the device writes for a CMS server.
export const CMS_SERVER_WRITE = ["Protocol", "VisitType", "ServersAddr"];

// STREAM_CHANNEL_WRITE — the fields the device writes per stream channel.
export const STREAM_CHANNEL_WRITE = ["Enable", "Res", "FrmRate", "Qp", "AudioEn"];

export const NET_WIFI: Record<string, N62FieldMeta> = {
  Enable: { label: "Enabled", type: "select", options: ON_OFF },
  Mode: {
    label: "Mode",
    type: "select",
    options: [
      { value: "0", label: "Station (join)" },
      { value: "2", label: "Access point" },
    ],
  },
  SSID: { label: "SSID", type: "text" },
  EncryptType: {
    label: "Encryption",
    type: "select",
    options: [
      { value: "0", label: "None" },
      { value: "1", label: "WEP" },
      { value: "2", label: "WPA/WPA2-PSK" },
      { value: "3", label: "WPA-PSK" },
      { value: "4", label: "WPA2-PSK" },
    ],
  },
  Pwd: { label: "Password", type: "password" },
  DhcpEn: { label: "DHCP", type: "select", options: ON_OFF },
};

export const NET_XG: Record<string, N62FieldMeta> = {
  Enable: { label: "Enabled", type: "select", options: ON_OFF },
  Mode: {
    label: "Network mode",
    type: "select",
    options: [
      { value: "0", label: "Hybrid (auto)" },
      { value: "1", label: "2G" },
      { value: "2", label: "3G WCDMA" },
      { value: "3", label: "3G EVDO" },
      { value: "4", label: "3G TD-SCDMA" },
      { value: "5", label: "4G LTE" },
    ],
  },
  APN: { label: "APN", type: "text" },
  User: { label: "APN user", type: "text" },
  Pwd: { label: "APN password", type: "password" },
  AuthType: {
    label: "Auth type",
    type: "select",
    options: [
      { value: "0", label: "None" },
      { value: "1", label: "CHAP" },
      { value: "2", label: "PAP" },
    ],
  },
  CenterNum: { label: "Dial number", type: "text" },
  RedialInter: { label: "Redial interval (s)", type: "number", min: 0, max: 999 },
  AbRestartEn: { label: "Auto-restart on failure", type: "select", options: ON_OFF },
};

export const NET_WIRED: Record<string, N62FieldMeta> = {
  Enable: { label: "Enabled", type: "select", options: ON_OFF },
  DhcpEn: { label: "DHCP", type: "select", options: ON_OFF },
  IP: { label: "IP address", type: "text" },
  SubMask: { label: "Subnet mask", type: "text" },
  Gateway: { label: "Gateway", type: "text" },
  DNS1: { label: "DNS 1", type: "text" },
  DNS2: { label: "DNS 2", type: "text" },
};

export const REC_ATTR: Record<string, N62FieldMeta> = {
  Mode: {
    label: "Record mode",
    type: "select",
    options: [
      { value: "0", label: "On startup" },
      { value: "1", label: "On alarm" },
      { value: "2", label: "By schedule" },
    ],
  },
  StreamType: {
    label: "Recorded stream",
    type: "select",
    options: [
      { value: "0", label: "Main" },
      { value: "1", label: "Sub" },
      { value: "2", label: "Dual" },
    ],
  },
  VencFormat: {
    label: "Encoding",
    type: "select",
    options: [
      { value: "0", label: "H.264" },
      { value: "1", label: "H.265" },
    ],
  },
  Duration: { label: "Clip length (min)", type: "number", min: 1, max: 60 },
  PreDuration: { label: "Pre-record (s)", type: "number", min: 0, max: 60 },
  DelayDuration: { label: "Post-alarm (s)", type: "number", min: 0, max: 60 },
  SaveDays: { label: "Retention (days)", type: "number", min: 0, max: 365 },
  // Compound/rarely-touched fields left untouched by the editor.
  Encrypt: { hidden: true },
  FileFormat: { hidden: true },
};

// One recording-stream channel (Chn_00 … inside RecStream_M / RecStream_S).
export const STREAM_CHANNEL: Record<string, N62FieldMeta> = {
  Enable: { label: "Enabled", type: "select", options: ON_OFF },
  Res: { label: "Resolution", type: "select", options: RES_OPTIONS },
  FrmRate: { label: "Frame rate (fps)", type: "number", min: 1, max: 30 },
  Qp: { label: "Quality", type: "select", options: QUALITY_OPTIONS },
  AudioEn: { label: "Audio", type: "select", options: ON_OFF },
};

// segmentFields returns the curated metadata map for a scalar object segment.
export function segmentFields(seg: string): Record<string, N62FieldMeta> {
  switch (seg) {
    case "GenDevInfo":
      return GEN_DEV_INFO;
    case "GenDateTime":
      return GEN_DATETIME;
    case "GenUser":
      return GEN_USER;
    case "VehBaseInfo":
      return VEH_BASE_INFO;
    case "VehPosition":
      return VEH_POSITION;
    case "NetWifi":
      return NET_WIFI;
    case "NetXg":
      return NET_XG;
    case "NetWired":
      return NET_WIRED;
    case "RecAttr":
      return REC_ATTR;
    default:
      return {};
  }
}

// ---- compound-field helpers (GenDateTime Zone / NtpSync) ----

// splitCompound expands the device's "a,b" pair fields into virtual editor fields
// so each half gets its own control. Returns a shallow copy; the raw compound keys
// are kept so unedited saves can rebuild them.
export function splitCompound(seg: string, obj: Record<string, any>): Record<string, any> {
  if (seg !== "GenDateTime") return obj;
  const out = { ...obj };
  const zone = String(obj.Zone ?? "").split(",");
  out.Zone_idx = zone[0] ?? "";
  out.Zone_off = zone[1] ?? "0";
  const ntp = String(obj.NtpSync ?? "").split(",");
  out.Ntp_en = ntp[0] ?? "0";
  out.Ntp_srv = ntp[1] ?? "0";
  return out;
}

// compoundParent maps a virtual field to the raw compound key it belongs to, so the
// editor knows a virtual edit dirties (and must rebuild) its parent.
const COMPOUND_PARENT: Record<string, string> = {
  Zone_idx: "Zone",
  Zone_off: "Zone",
  Ntp_en: "NtpSync",
  Ntp_srv: "NtpSync",
};

export function compoundParentOf(field: string): string | undefined {
  return COMPOUND_PARENT[field];
}

// joinCompound rebuilds a raw compound value from its virtual halves in the draft.
export function joinCompound(parent: string, draft: Record<string, any>): string {
  if (parent === "Zone") return `${draft.Zone_idx ?? "0"},${draft.Zone_off ?? "0"}`;
  if (parent === "NtpSync") return `${draft.Ntp_en ?? "0"},${draft.Ntp_srv ?? "0"}`;
  return String(draft[parent] ?? "");
}
