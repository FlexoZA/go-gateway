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
  { key: "general", label: "General", segments: ["GenDevInfo", "GenDateTime", "GenDst", "GenUser"] },
  { key: "vehicle", label: "Vehicle", segments: ["VehBaseInfo", "VehPosition", "VehMileage"] },
  { key: "display", label: "Display", segments: ["PreDisplay", "PreOsd", "PreMargin"] },
  { key: "recording", label: "Recording", segments: ["RecAttr", "RecStream_M", "RecStream_S", "RecCamAttr", "RecCapAttr", "RecOsd", "RecStorage"] },
  { key: "alarm", label: "Alarm", segments: ["AlmSpd", "AlmGsn", "AlmDriving", "AlmSys", "AlmIoIn"] },
  { key: "ai", label: "AI (ADAS/DMS)", segments: ["AiBase", "AiAdas", "AiDms", "AiFace"] },
  { key: "network", label: "Network", segments: ["NetCms", "NetWifi", "NetXg", "NetWired", "NetFtp", "NetUpload"] },
  { key: "peripheral", label: "Peripheral", segments: ["PerIoOutput"] },
];

// Human title per segment (used by the editor for card headings).
export const SEG_TITLE: Record<string, string> = {
  GenDevInfo: "Device info",
  GenDateTime: "Date & time",
  GenDst: "Daylight saving",
  GenUser: "Web login",
  VehBaseInfo: "Vehicle & driver",
  VehPosition: "GPS / positioning",
  VehMileage: "Mileage",
  PreDisplay: "Display & audio",
  PreOsd: "Live-view OSD",
  PreMargin: "Screen margins",
  RecAttr: "Recording",
  RecStream_M: "Main stream (per channel)",
  RecStream_S: "Sub stream (per channel)",
  RecCamAttr: "Camera attributes (per channel)",
  RecCapAttr: "Timed snapshot",
  RecOsd: "Recorded-video OSD",
  RecStorage: "Storage assignment",
  AlmSpd: "Speed alarms",
  AlmGsn: "G-sensor alarms",
  AlmDriving: "Driver fatigue / hours",
  AlmSys: "System alarms",
  AlmIoIn: "I/O input alarms (per input)",
  AiBase: "AI base",
  AiAdas: "ADAS (road-facing)",
  AiDms: "DMS (driver-facing)",
  AiFace: "Face recognition",
  NetCms: "CMS server (gateway)",
  NetWifi: "Wi-Fi",
  NetXg: "Cellular (4G)",
  NetWired: "Wired (Ethernet)",
  NetFtp: "FTP upload",
  NetUpload: "Upload routing",
  PerIoOutput: "I/O outputs",
};

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

const ON_OFF_MASK = ON_OFF; // alias for OSD/enable toggles

export const GEN_DST: Record<string, N62FieldMeta> = {
  Enable: { label: "Enabled", type: "select", options: ON_OFF },
  OffsetTime: { label: "Offset", type: "select", options: [{ value: "0", label: "1 hour" }, { value: "1", label: "2 hours" }] },
  Mode: { label: "Rule mode", type: "select", options: [{ value: "0", label: "By date" }, { value: "1", label: "By weekday" }], help: "The start/end schedule is kept as configured on the device." },
  StartTime: { hidden: true },
  EndTime: { hidden: true },
};

export const VEH_MILEAGE: Record<string, N62FieldMeta> = {
  BaseV: { label: "Base mileage (km)", type: "number", min: 0, max: 1500000 },
};

export const PRE_DISPLAY: Record<string, N62FieldMeta> = {
  Volume: { label: "Volume", type: "number", min: 0, max: 100 },
  Language: {
    label: "Language",
    type: "select",
    options: [
      { value: "0", label: "English" }, { value: "1", label: "简体中文" }, { value: "3", label: "Русский" },
      { value: "4", label: "한국어" }, { value: "5", label: "Türkçe" }, { value: "6", label: "العربية" },
      { value: "7", label: "Português" }, { value: "9", label: "Français" }, { value: "10", label: "Español" },
      { value: "11", label: "Italiano" }, { value: "13", label: "Việt Nam" }, { value: "15", label: "ภาษาไทย" },
      { value: "17", label: "Nederlands" }, { value: "21", label: "Hindi" }, { value: "22", label: "Indonesia" },
    ],
  },
  TimeOut: {
    label: "Screen timeout",
    type: "select",
    options: [
      { value: "30", label: "30s" }, { value: "60", label: "1 min" }, { value: "300", label: "5 min" },
      { value: "600", label: "10 min" }, { value: "-1", label: "Never" },
    ],
  },
  Split: {
    label: "Split view",
    type: "select",
    options: [
      { value: "0", label: "1×1" }, { value: "1", label: "2×1" }, { value: "7", label: "1×2" }, { value: "2", label: "2×2" },
      { value: "8", label: "3×1" }, { value: "9", label: "1×3" }, { value: "3", label: "3×3" },
    ],
  },
  VGA: {
    label: "VGA resolution",
    type: "select",
    options: [
      { value: "1", label: "1024×768" }, { value: "2", label: "1280×800" }, { value: "3", label: "1280×1024" },
      { value: "4", label: "1366×768" }, { value: "5", label: "1440×900" }, { value: "7", label: "1920×1080" },
    ],
  },
  StartUpMusic: { label: "Start-up music", type: "select", options: ON_OFF },
  VoicePackage: { label: "Voice package", type: "select", options: [{ value: "0", label: "Default" }, { value: "2", label: "2" }, { value: "3", label: "3" }] },
  ColorToGray: { label: "Colour→grey threshold", type: "number", min: 0, max: 9999 },
  GrayToColor: { label: "Grey→colour threshold", type: "number", min: 0, max: 9999 },
  CVBS: { hidden: true },
  LanguageMask: { hidden: true },
};

export const PRE_OSD: Record<string, N62FieldMeta> = {
  DateTimeEn: { label: "Date / time", type: "select", options: ON_OFF_MASK },
  ChnEn: { label: "Channel name", type: "select", options: ON_OFF_MASK },
  ChnResEn: { label: "Resolution", type: "select", options: ON_OFF_MASK },
  RecStaEn: { label: "Record status", type: "select", options: ON_OFF_MASK },
  StaIconEn: { label: "Status icons", type: "select", options: ON_OFF_MASK },
  MirrorEn: { label: "Mirror indicator", type: "select", options: ON_OFF_MASK },
  GpsEn: { label: "GPS", type: "select", options: ON_OFF_MASK },
  PlateEn: { label: "Number plate", type: "select", options: ON_OFF_MASK },
  DriverEn: { label: "Driver info", type: "select", options: ON_OFF_MASK },
  AlmInfoEn: { label: "Alarm info", type: "select", options: ON_OFF_MASK },
  AlgInfoEn: { label: "AI info", type: "select", options: ON_OFF_MASK },
  BpcInfoEn: { label: "People-counter info", type: "select", options: ON_OFF_MASK },
};

export const PRE_MARGIN: Record<string, N62FieldMeta> = {
  Left: { label: "Left", type: "number", min: 0, max: 200 },
  Right: { label: "Right", type: "number", min: 0, max: 200 },
  Top: { label: "Top", type: "number", min: 0, max: 200 },
  Bottom: { label: "Bottom", type: "number", min: 0, max: 200 },
};

export const REC_CAP: Record<string, N62FieldMeta> = {
  Enable: { label: "Enabled", type: "select", options: ON_OFF },
  CapRes: { label: "Snapshot stream", type: "select", options: [{ value: "0", label: "Main stream" }, { value: "1", label: "Sub stream" }] },
  Inteval: { label: "Interval — moving (s)", type: "number", min: 0, max: 86400 },
  Inteval_P: { label: "Interval — parked (s)", type: "number", min: 0, max: 86400 },
  SaveDays: { label: "Retention (days)", type: "number", min: 0, max: 365 },
  ChnMask: { hidden: true },
  ChnNum: { hidden: true },
};

export const REC_OSD: Record<string, N62FieldMeta> = {
  DateTimeEn: { label: "Date / time", type: "select", options: ON_OFF_MASK },
  ChnEn: { label: "Channel name", type: "select", options: ON_OFF_MASK },
  GpsEn: { label: "GPS", type: "select", options: ON_OFF_MASK },
  StatusEn: { label: "Device status", type: "select", options: ON_OFF_MASK },
  CarPlateEn: { label: "Number plate", type: "select", options: ON_OFF_MASK },
  AlarmInfoEn: { label: "Alarm info", type: "select", options: ON_OFF_MASK },
  DriverInfoEn: { label: "Driver ID", type: "select", options: ON_OFF_MASK },
  AiAlarmEn: { label: "AI alarm", type: "select", options: ON_OFF_MASK },
  SpeedEn: { label: "Speed", type: "select", options: ON_OFF_MASK },
  AlgObjRect: { label: "AI object boxes", type: "select", options: ON_OFF_MASK },
};

const STORAGE_OPTS = [
  { value: "0", label: "None" },
  { value: "1", label: "Main stream" },
  { value: "2", label: "Sub stream" },
];
export const REC_STORAGE: Record<string, N62FieldMeta> = {
  Sd1: { label: "SD card 1", type: "select", options: STORAGE_OPTS },
  Sd2: { label: "SD card 2", type: "select", options: STORAGE_OPTS },
  Hdd1: { label: "HDD 1", type: "select", options: STORAGE_OPTS },
  Hdd2: { label: "HDD 2", type: "select", options: STORAGE_OPTS },
  Usb1: { label: "USB 1", type: "select", options: STORAGE_OPTS },
  Usb2: { label: "USB 2", type: "select", options: STORAGE_OPTS },
  SdNum: { hidden: true },
  HddNum: { hidden: true },
  UsbNum: { hidden: true },
};

export const NET_FTP: Record<string, N62FieldMeta> = {
  Enable: { label: "Enabled", type: "select", options: ON_OFF },
  ServersAddr: { label: "Server address", type: "text", help: "host:port" },
  User: { label: "User", type: "text" },
  Pwd: { label: "Password", type: "password" },
  VisitType: { label: "Address type", type: "select", options: [{ value: "0", label: "IP" }, { value: "1", label: "Domain" }] },
};

const UP_SER = [{ value: "0", label: "None" }, { value: "1", label: "CMS" }, { value: "2", label: "FTP" }];
const UP_NET = [{ value: "0", label: "Wi-Fi" }, { value: "1", label: "4G" }, { value: "2", label: "Wired" }, { value: "3", label: "All" }];
export const NET_UPLOAD: Record<string, N62FieldMeta> = {
  PicSerType: { label: "Pictures → server", type: "select", options: UP_SER },
  PicNetType: { label: "Pictures → network", type: "select", options: UP_NET },
  PicDataType: { label: "Pictures — which", type: "select", options: [{ value: "0", label: "Timed" }, { value: "1", label: "Alarm" }, { value: "2", label: "All" }] },
  RecSerType: { label: "Recordings → server", type: "select", options: [{ value: "0", label: "None" }, { value: "2", label: "FTP" }] },
  RecNetType: { label: "Recordings → network", type: "select", options: UP_NET },
  RecDataType: { label: "Recordings — which", type: "select", options: [{ value: "0", label: "Normal" }, { value: "1", label: "Alarm" }, { value: "2", label: "All" }] },
  AttachSerType: { label: "Attachments → server", type: "select", options: [{ value: "0", label: "None" }, { value: "1", label: "CMS" }] },
  AttachNetType: { label: "Attachments → network", type: "select", options: UP_NET },
  AttachDataType: { label: "Attachments — which", type: "select", options: [{ value: "0", label: "All" }, { value: "1", label: "AI only" }] },
};

const IOOUT_OPTS = [{ value: "0", label: "None" }, { value: "1", label: "Alarm linkage" }, { value: "2", label: "Switch" }];
export const PER_IOOUT: Record<string, N62FieldMeta> = {
  IoOut_1: { label: "Output 1", type: "select", options: IOOUT_OPTS },
  IoOut_2: { label: "Output 2", type: "select", options: IOOUT_OPTS },
  IoOut_Num: { hidden: true },
};

// AI base + face are plain scalars (note: face uses "En", not "Enable").
export const AI_BASE: Record<string, N62FieldMeta> = {
  Mode: { label: "AI mode", type: "select", options: [{ value: "0", label: "Off" }, { value: "1", label: "Debug" }, { value: "2", label: "Normal" }] },
  SpdThr_AB: { label: "Speed threshold (km/h)", type: "number", min: 0, max: 250, help: "AI alarms above this speed." },
  HoldTime: { label: "Alarm hold time (s)", type: "number", min: 0, max: 30 },
};
export const AI_FACE: Record<string, N62FieldMeta> = {
  En: { label: "Enabled", type: "select", options: ON_OFF },
  S_Interval: { label: "Success interval (s)", type: "number", min: 0, max: 65535 },
  F_Interval: { label: "Fail interval (s)", type: "number", min: 0, max: 65535 },
  AudioEn: { label: "Audio prompt", type: "select", options: ON_OFF },
};

// Nested per-channel camera attributes (RecCamAttr, Chn_xx sent whole).
export const CAM_CHANNEL: Record<string, N62FieldMeta> = {
  Enable: { label: "Enabled", type: "select", options: ON_OFF },
  Type: {
    label: "Signal",
    type: "select",
    options: [
      { value: "0", label: "AHD" }, { value: "1", label: "CVI" }, { value: "2", label: "TVI" },
      { value: "3", label: "CVBS" }, { value: "4", label: "MIPI" }, { value: "5", label: "IPC" },
    ],
  },
  Res: { label: "Resolution", type: "select", options: RES_OPTIONS.slice(0, 6) },
  FrmRate: { label: "Frame rate", type: "select", options: [{ value: "0", label: "25" }, { value: "1", label: "30" }] },
  Mode: { label: "Mode", type: "select", options: [{ value: "0", label: "Auto" }, { value: "1", label: "Manual" }] },
  Direction: {
    label: "Orientation",
    type: "select",
    options: [
      { value: "0", label: "Normal" }, { value: "1", label: "Mirror" }, { value: "2", label: "Flip" }, { value: "3", label: "Mirror+Flip" },
    ],
  },
};
export const CAM_CHANNEL_WRITE = ["Enable", "Type", "Res", "FrmRate", "Mode", "Direction"];

// NESTED_SEGMENTS — segments made of homogeneous indexed sub-objects sent WHOLE
// (the firmware doesn't merge partial sub-objects). subLabel is 0-indexed.
export interface NestedSpec {
  subPrefix: string;
  subLabel: (i: number) => string;
  fields: Record<string, N62FieldMeta>;
  write: string[];
  note?: string;
}
export const NESTED_SEGMENTS: Record<string, NestedSpec> = {
  NetCms: {
    subPrefix: "Server_",
    subLabel: (i) => `Server ${i + 1}`,
    fields: CMS_SERVER,
    write: CMS_SERVER_WRITE,
    note: "The CMS server is how this unit reaches the gateway — a wrong address or protocol will disconnect it.",
  },
  RecStream_M: { subPrefix: "Chn_", subLabel: (i) => `Channel ${i + 1}`, fields: STREAM_CHANNEL, write: STREAM_CHANNEL_WRITE },
  RecStream_S: { subPrefix: "Chn_", subLabel: (i) => `Channel ${i + 1}`, fields: STREAM_CHANNEL, write: STREAM_CHANNEL_WRITE },
  RecCamAttr: { subPrefix: "Chn_", subLabel: (i) => `Channel ${i + 1}`, fields: CAM_CHANNEL, write: CAM_CHANNEL_WRITE },
};

// LIST_SEGMENTS — segments with a set of named/indexed alarm sub-objects, each
// carrying editable fields plus a linkage string we PRESERVE VERBATIM (LnkParam:
// which cameras snapshot/record, IO output, popup — too intricate to expose safely
// and unnecessary for tuning). ADAS/DMS additionally pack their tuning knobs into a
// CSV "Param" field, which we split into virtual columns and rejoin on save. The
// whole segment (top fields + every sub) is sent, mirroring the device's own UI.
export interface ListCol {
  key: string;
  meta: N62FieldMeta;
}
export interface ListSpec {
  top?: Record<string, N62FieldMeta>; // top-level scalar fields (Chn, Range, Mode, …)
  subs?: { key: string; label: string }[]; // explicit named subs (else derived by subPrefix)
  subPrefix?: string;
  subLabel?: (i: number) => string;
  direct?: Record<string, N62FieldMeta>; // editable fields directly on each sub
  csv?: { field: string; cols: ListCol[] };
  passthrough: string[]; // preserved verbatim
  note?: string;
}

const SENS = { label: "Sensitivity", type: "select" as const, options: [{ value: "0", label: "Low" }, { value: "1", label: "Middle" }, { value: "2", label: "High" }] };
const enCol = { key: "en", meta: { label: "Enabled", type: "select" as const, options: ON_OFF } };
const spdCols: ListCol[] = [
  { key: "spd1", meta: { label: "Speed min (km/h)", type: "number", min: 0, max: 250 } },
  { key: "spd2", meta: { label: "Speed max (km/h)", type: "number", min: 0, max: 250 } },
];

export const LIST_SEGMENTS: Record<string, ListSpec> = {
  AiAdas: {
    top: { Chn: { label: "Channel", type: "select", options: CHAN_OPTS() } },
    subs: [
      { key: "Type_00", label: "LDW — lane departure" },
      { key: "Type_01", label: "FCW — forward collision" },
      { key: "Type_02", label: "HMW — headway monitoring" },
      { key: "Type_03", label: "PCW — pedestrian collision" },
    ],
    csv: {
      field: "Param",
      cols: [enCol, { key: "interval", meta: { label: "Interval (s)", type: "number", min: 0, max: 200 } }, { key: "sens", meta: SENS }, ...spdCols],
    },
    passthrough: ["LnkParam"],
  },
  AiDms: {
    top: {
      Chn: { label: "Channel", type: "select", options: CHAN_OPTS() },
      Range: { label: "Steering side", type: "select", options: [{ value: "0", label: "All" }, { value: "1", label: "Left" }, { value: "2", label: "Right" }] },
    },
    subs: [
      { key: "Type_00", label: "Phone call" }, { key: "Type_01", label: "Smoking" }, { key: "Type_02", label: "Driver fatigue" },
      { key: "Type_03", label: "Yawn" }, { key: "Type_04", label: "Distraction" }, { key: "Type_05", label: "No driver" },
      { key: "Type_06", label: "Infrared block" }, { key: "Type_07", label: "Lens covered" }, { key: "Type_08", label: "Seat belt" },
    ],
    csv: {
      field: "Param",
      cols: [
        enCol,
        { key: "interval", meta: { label: "Interval (s)", type: "number", min: 3, max: 900 } },
        { key: "duration", meta: { label: "Duration (s)", type: "number", min: 0, max: 30 } },
        ...spdCols,
      ],
    },
    passthrough: ["LnkParam"],
  },
  AlmSpd: {
    subs: [
      { key: "MaxSpd", label: "Over-speed" },
      { key: "MinSpd", label: "Low-speed" },
      { key: "Parking", label: "Illegal parking" },
    ],
    direct: {
      En: { label: "Enabled", type: "select", options: ON_OFF },
      Thr: { label: "Threshold (km/h)", type: "number", min: 0, max: 9999 },
      Duration: { label: "Duration (s)", type: "number", min: 0, max: 99 },
    },
    passthrough: ["LnkParam"],
  },
  AlmGsn: {
    top: {
      Mode: { label: "Mode", type: "select", options: [{ value: "0", label: "Scene" }, { value: "1", label: "Value" }] },
      Assemble: {
        label: "Install orientation",
        type: "select",
        options: [
          { value: "0", label: "Front / parallel" }, { value: "1", label: "Front / vertical" },
          { value: "2", label: "Rear / parallel" }, { value: "3", label: "Rear / vertical" },
        ],
      },
      InitV: { label: "Calibration", type: "text", readonly: true },
    },
    subs: [
      { key: "RapidSpd", label: "Harsh acceleration" }, { key: "RapidBrk", label: "Harsh braking" },
      { key: "RapidTurn", label: "Sharp turn" }, { key: "Collision", label: "Collision" },
      { key: "Incline", label: "Incline" }, { key: "X", label: "X axis" }, { key: "Y", label: "Y axis" }, { key: "Z", label: "Z axis" },
    ],
    direct: {
      En: { label: "Enabled", type: "select", options: ON_OFF },
      Thr: { label: "Threshold", type: "number", min: 0, max: 1600 },
    },
    passthrough: ["LnkParam"],
  },
  AlmDriving: {
    top: { MinRest: { label: "Min rest (min)", type: "number", min: 0, max: 1440 } },
    subs: [
      { key: "PreTired", label: "Early tired warning" }, { key: "Tired", label: "Tired" },
      { key: "PreTimeOut", label: "Early over-time warning" }, { key: "TimeOut", label: "Over-time driving" },
    ],
    direct: {
      En: { label: "Enabled", type: "select", options: ON_OFF },
      Thr: { label: "Threshold (min)", type: "number", min: 0, max: 1440 },
    },
    passthrough: ["LnkParam"],
  },
  AlmSys: {
    subs: [
      { key: "PowerOff", label: "Power off" }, { key: "AccOff", label: "ACC off" }, { key: "DiskFull", label: "Disk full" },
      { key: "LowVoltage", label: "Low voltage" }, { key: "LockOpen", label: "Lock opened" }, { key: "DiskLoss", label: "Disk lost" },
      { key: "RecLoss", label: "Recording lost" },
    ],
    direct: { En: { label: "Enabled", type: "select", options: ON_OFF } },
    passthrough: ["LnkParam"],
  },
  AlmIoIn: {
    subPrefix: "Chn_",
    subLabel: (i) => `Input ${i + 1}`,
    direct: {
      En: { label: "Enabled", type: "select", options: ON_OFF },
      Type: {
        label: "Sensor",
        type: "select",
        options: [
          { value: "0", label: "Default" }, { value: "1", label: "Air-tight" }, { value: "2", label: "Panic" }, { value: "3", label: "Front door 1" },
          { value: "4", label: "Front door 2" }, { value: "5", label: "Middle door" }, { value: "6", label: "Back door" }, { value: "7", label: "Reversing" },
          { value: "8", label: "Turn left" }, { value: "9", label: "Turn right" }, { value: "10", label: "Radar" }, { value: "11", label: "Lift" },
          { value: "12", label: "Check button" }, { value: "13", label: "Fire" },
        ],
      },
      Thr: { label: "Threshold", type: "number", min: 0, max: 9999 },
    },
    passthrough: ["LnkParam"],
  },
};

// segmentFields returns the curated metadata map for a scalar object segment.
export function segmentFields(seg: string): Record<string, N62FieldMeta> {
  switch (seg) {
    case "GenDevInfo":
      return GEN_DEV_INFO;
    case "GenDateTime":
      return GEN_DATETIME;
    case "GenDst":
      return GEN_DST;
    case "GenUser":
      return GEN_USER;
    case "VehBaseInfo":
      return VEH_BASE_INFO;
    case "VehPosition":
      return VEH_POSITION;
    case "VehMileage":
      return VEH_MILEAGE;
    case "PreDisplay":
      return PRE_DISPLAY;
    case "PreOsd":
      return PRE_OSD;
    case "PreMargin":
      return PRE_MARGIN;
    case "RecAttr":
      return REC_ATTR;
    case "RecCapAttr":
      return REC_CAP;
    case "RecOsd":
      return REC_OSD;
    case "RecStorage":
      return REC_STORAGE;
    case "AiBase":
      return AI_BASE;
    case "AiFace":
      return AI_FACE;
    case "NetWifi":
      return NET_WIFI;
    case "NetXg":
      return NET_XG;
    case "NetWired":
      return NET_WIRED;
    case "NetFtp":
      return NET_FTP;
    case "NetUpload":
      return NET_UPLOAD;
    case "PerIoOutput":
      return PER_IOOUT;
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

// ---- list-segment helpers (alarm / ADAS / DMS) ----

// CHAN_OPTS is the 8-channel select used by ADAS/DMS. Function declaration so it's
// hoisted for the LIST_SEGMENTS literal above.
export function CHAN_OPTS(): { value: string; label: string }[] {
  return Array.from({ length: 8 }, (_, i) => ({ value: String(i), label: `Ch${i + 1}` }));
}

// listSubs resolves a list segment's sub-objects: explicit `subs`, else every key
// matching `subPrefix` in the loaded object (0-indexed labels).
export function listSubs(spec: ListSpec, obj: Record<string, any>): { key: string; label: string }[] {
  if (spec.subs) return spec.subs.filter((s) => obj && s.key in obj);
  if (spec.subPrefix) {
    return Object.keys(obj || {})
      .filter((k) => k.startsWith(spec.subPrefix!))
      .sort()
      .map((k, i) => ({ key: k, label: spec.subLabel ? spec.subLabel(i) : k }));
  }
  return [];
}

// splitListCsv expands each sub's CSV field (ADAS/DMS "Param") into virtual column
// fields so each knob gets its own control. Returns a copy; the raw CSV stays for
// rebuild. No-op for segments without a CSV.
export function splitListCsv(seg: string, obj: Record<string, any>): Record<string, any> {
  const spec = LIST_SEGMENTS[seg];
  if (!spec || !spec.csv) return obj;
  const out = { ...obj };
  for (const s of listSubs(spec, obj)) {
    const sub = obj[s.key];
    if (!sub || typeof sub !== "object") continue;
    const parts = String(sub[spec.csv.field] ?? "").split(",");
    const nsub = { ...sub };
    spec.csv.cols.forEach((c, i) => (nsub[c.key] = parts[i] ?? ""));
    // Preserve any trailing columns the firmware packed beyond the curated set so
    // they can be re-appended on save instead of being silently dropped.
    if (parts.length > spec.csv.cols.length) nsub.__csvExtra = parts.slice(spec.csv.cols.length);
    out[s.key] = nsub;
  }
  return out;
}

// joinListCsv rebuilds a sub's CSV string from its virtual columns (order matters),
// re-appending any trailing columns the firmware sent beyond the curated set (kept
// by splitListCsv) so a round-trip doesn't truncate them.
export function joinListCsv(spec: ListSpec, sub: Record<string, any>): string {
  const base = spec.csv!.cols.map((c) => String(sub[c.key] ?? ""));
  const extra = Array.isArray(sub.__csvExtra) ? sub.__csvExtra.map((x: any) => String(x)) : [];
  return [...base, ...extra].join(",");
}
