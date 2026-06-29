// cathexisConfig.ts — UI metadata for the Cathexis MVR device config editor.
//
// Unlike Howen (all-string segments), a Cathexis config mixes real types: booleans
// (is_metric), numbers (gps_frequency, port), strings (apn), arrays (cameras,
// events), and the events' [[key, value]] pair form. The editor (CathexisConfig)
// is type-aware; this file is the CURATED OVERLAY giving the common scalar fields
// friendly labels/controls and grouping segments into the device's menus. Scalar
// fields with no entry render generically (humanised name, type inferred from the
// value); nested objects/arrays inside network/general are hidden so partial saves
// never touch them.

export type CathexisFieldType = "toggle" | "number" | "text" | "select";

export interface CathexisFieldMeta {
  label?: string;
  type?: CathexisFieldType;
  options?: { value: string; label: string }[];
  readonly?: boolean;
  hidden?: boolean; // not rendered and never written (keeps partial saves safe)
  help?: string;
}

export interface CathexisCategory {
  key: string;
  label: string;
}

export const CATEGORIES: CathexisCategory[] = [
  { key: "network", label: "Network" },
  { key: "general", label: "General" },
  { key: "cameras", label: "Cameras" },
  { key: "events", label: "Events" },
  { key: "description", label: "Device info" },
];

// humanize turns a snake_case key into a Title Case label.
export function humanize(key: string): string {
  return key
    .replace(/_/g, " ")
    .replace(/\b\w/g, (c) => c.toUpperCase())
    .replace(/\bId\b/, "ID")
    .replace(/\bApn\b/, "APN")
    .replace(/\bGps\b/, "GPS")
    .replace(/\bImu\b/, "IMU")
    .replace(/\bIr\b/, "IR")
    .replace(/\bSip\b/, "SIP")
    .replace(/\bSim\b/, "SIM")
    .replace(/\bDapi\b/, "dAPI");
}

// NETWORK_FIELDS — scalar fields of the `network` segment. Nested objects/arrays
// (extra_wifi_configs, allowed_mobile_numbers, sip_config) are hidden: they need a
// dedicated editor and partial writes leave them untouched.
export const NETWORK_FIELDS: Record<string, CathexisFieldMeta> = {
  address: { label: "dAPI server address", type: "text", help: "Where the unit connects to send data" },
  port: { label: "dAPI server port", type: "number", help: "Must be even" },
  cathexis_server: { label: "Cathexis server", type: "text", readonly: true },
  apn: { label: "APN", type: "text" },
  apn_user: { label: "APN user", type: "text" },
  apn_passwd: { label: "APN password", type: "text" },
  simpin: { label: "SIM PIN", type: "text" },
  roaming: { label: "Roaming", type: "toggle" },
  require_dapi_ack: { label: "Require dAPI ack", type: "toggle" },
  wifi_ssid: { label: "Wi-Fi SSID", type: "text" },
  wifi_pwd: { label: "Wi-Fi password", type: "text" },
  // Nested structures — out of scope for the scalar editor.
  extra_wifi_configs: { hidden: true },
  allowed_mobile_numbers: { hidden: true },
  sip_config: { hidden: true },
};

// GENERAL_FIELDS — scalar fields of the `general` segment.
export const GENERAL_FIELDS: Record<string, CathexisFieldMeta> = {
  account: { label: "Account", type: "text", help: "Fleet/account name (sent on every General save)" },
  device_name: { label: "Device name", type: "text" },
  serial_number: { label: "Serial number", type: "text" },
  gps_frequency: { label: "GPS frequency (s)", type: "number" },
  gps_frequency_standby: { label: "GPS frequency, standby (s)", type: "number" },
  is_metric: { label: "Metric units (km/h)", type: "toggle" },
  enable_voice_prompts: { label: "Voice prompts", type: "toggle" },
  disable_audio_prompts: { label: "Disable audio prompts", type: "toggle" },
  disable_microphone: { label: "Disable microphone", type: "toggle" },
  auto_answer_calls: { label: "Auto-answer calls", type: "toggle" },
  enable_live_indicator: { label: "Live indicator", type: "toggle" },
  driver_sits_on_right: { label: "Driver on right", type: "toggle" },
  speaker_volume: { label: "Speaker volume", type: "number" },
  speaker_boost: { label: "Speaker boost", type: "number" },
  standby_sec: { label: "Standby timeout (s)", type: "number" },
  linger_sec: { label: "Linger after off (s)", type: "number" },
  permanent_ir: { label: "Permanent IR", type: "toggle" },
  odometer: { label: "Odometer (m)", type: "number" },
  support_stats_interval: { label: "Stats interval (s)", type: "number" },
  // IMU wake.
  wake_imu_enabled: { label: "IMU wake", type: "toggle" },
  wake_imu_duration_s: { label: "IMU wake duration (s)", type: "number" },
  wake_imu_threshold: { label: "IMU wake threshold (g)", type: "number" },
  // Harsh-event filter.
  harsh_filter_disable: { label: "Disable harsh filter", type: "toggle" },
  harsh_filter_seconds: { label: "Harsh filter (s)", type: "number" },
  pitch_threshold: { label: "Pitch threshold", type: "number" },
  roll_threshold: { label: "Roll threshold", type: "number" },
  // Geo-comms zone.
  geo_comms_enable: { label: "Geo-comms zone", type: "toggle" },
  geo_comms_latitude: { label: "Geo-comms latitude", type: "number" },
  geo_comms_longitude: { label: "Geo-comms longitude", type: "number" },
  geo_comms_radius: { label: "Geo-comms radius (m)", type: "number" },
  geo_comms_override_sec: { label: "Geo-comms override (s)", type: "number" },
};

// account is mandatory on every `general` write (device rule), so the editor
// always includes it even when only other fields changed.
export const GENERAL_REQUIRED = ["account"];

// CAMERA_PROFILE_FIELDS — fields of one camera profile (the high/low res sub-stream).
export const CAMERA_PROFILE_FIELDS: Record<string, CathexisFieldMeta> = {
  index: { label: "Profile", type: "number", readonly: true },
  enabled: { label: "Enabled", type: "toggle" },
  audio: { label: "Audio", type: "toggle" },
  record_continuous: { label: "Continuous recording", type: "toggle" },
  record_events: { label: "Event recording", type: "toggle" },
  bitrate_bps: { label: "Bitrate (bps)", type: "number" },
  fps: { label: "Frame rate (fps)", type: "number" },
  key_s: { label: "Keyframe interval (s)", type: "number" },
  snapshot_period: { label: "Snapshot period (s)", type: "number" },
};

// EVENT_FIELDS — labels/types for the common keys inside an event's [[k,v]] array.
// Anything not listed renders generically (0/1 → toggle, else text).
export const EVENT_FIELDS: Record<string, CathexisFieldMeta> = {
  name: { label: "Name", readonly: true },
  enable: { label: "Enabled", type: "toggle" },
  record: { label: "Record video", type: "toggle" },
  audio: { label: "Audio alert", type: "toggle" },
  email: { label: "Email", type: "toggle" },
  upload: { label: "Upload", type: "toggle" },
  alert: { label: "Alert", type: "toggle" },
  debug: { label: "Debug", type: "toggle" },
  training: { label: "Training", type: "toggle" },
  threshold: { label: "Threshold", type: "text", help: "Harsh = g-force (0.1–0.8); speeding = m/s" },
  period_ms: { label: "Period (ms)", type: "number" },
  period: { label: "Period (s)", type: "number" },
  pre: { label: "Pre-record (s)", type: "number" },
  post: { label: "Post-record (s)", type: "number" },
  pre_s: { label: "Pre-record (s)", type: "number" },
  post_s: { label: "Post-record (s)", type: "number" },
  speed_mps: { label: "Min speed (m/s)", type: "number" },
  limit: { label: "Limit", type: "number" },
  limit_type: { label: "Limit type", type: "select", options: [{ value: "0", label: "Per hour" }, { value: "1", label: "Per trip" }] },
  camera: { label: "Camera", type: "select", options: [{ value: "0", label: "Road" }, { value: "1", label: "Cab" }] },
  profile: { label: "Profile", type: "select", options: [{ value: "0", label: "High res" }, { value: "1", label: "Low res" }] },
};

// EVENT_LABELS — friendly display names for the known event types.
export const EVENT_LABELS: Record<string, string> = {
  harsh_braking: "Harsh braking",
  harsh_turning: "Harsh turning",
  harsh_acceleration: "Harsh acceleration",
  overspeeding: "Overspeeding",
  speeding: "Speeding",
  impact: "Impact",
  harsh_impact: "Harsh impact",
  motion_start: "Motion start",
  idle: "Idling",
  tamper: "Tamper (AI)",
  fatigue: "Fatigue (AI)",
  distraction: "Distraction (AI)",
  seatbelt: "Seatbelt (AI)",
  yawn: "Yawn (AI)",
  cellphone: "Cellphone (AI)",
  passenger: "Passenger (AI)",
  followingdistance: "Following distance (AI)",
  smoking: "Smoking (AI)",
};

export function eventLabel(name: string): string {
  return EVENT_LABELS[name] || humanize(name);
}

// inferType picks a control for an uncurated value by its JS type/shape.
export function inferType(v: unknown): CathexisFieldType {
  if (typeof v === "boolean") return "toggle";
  if (typeof v === "number") return "number";
  // event values are strings; treat "0"/"1" as a toggle
  if (v === "0" || v === "1") return "toggle";
  if (typeof v === "string" && v !== "" && !isNaN(Number(v))) return "number";
  return "text";
}
