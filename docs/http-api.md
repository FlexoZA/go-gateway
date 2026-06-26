# HTTP API reference

The gateway exposes a management/control HTTP API on `HTTP_PORT` (default `8080`;
set `0` to disable). It is separate from the device TCP server — **devices never
call this API and never send an API key**; they authenticate by serial over TCP.

All responses are JSON. Serve this behind TLS in production.

## Authentication

`GET /healthz` is public. Every route under `/api/` requires an API key in the
`Authorization` header:

```
Authorization: Bearer dgw_<key>
```

Keys are minted with the `apikey` CLI / the panel's API Keys page (see
[configuration](configuration.md)). The scheme name is case-insensitive
(`Bearer`/`bearer`). The admin panel's **internal service token**
(`INTERNAL_API_TOKEN`) is also accepted as a Bearer value — that is how the panel
authenticates (it works before any DB key exists, enabling first-run setup).

| Condition | Status |
|---|---|
| Missing / malformed / unknown / revoked / expired key | `401` |
| Key store (database) not configured | `503` |

## Endpoints

### `GET /healthz`
Liveness check. Public.

```json
200 { "status": "ok" }
```

### `GET /api/ping`
Authenticated connectivity check.

```json
200 { "ok": true }
```

### `GET /api/gateway/info`
This gateway's unit type and **effective** capabilities — what the unit declares
AND what runtime config enables (e.g. `has_video` is true only when the unit
supports video and `MEDIA_ADVERTISE_HOST` is set; `has_clips` also needs a
database). The admin panel reads it once to hide UI for features this build/config
doesn't offer.

```json
200 {
  "unit": "howen",
  "capabilities": {
    "has_video": true, "has_commands": true, "has_config": true,
    "has_status": true, "has_clips": true, "has_mappings": true
  }
}
```

### `GET /api/units`
List currently-connected devices.

```json
200 {
  "units": [
    {
      "serial": "864312087845313",
      "protocol": "howen",
      "model": "Hero-MC30-02",
      "remote_addr": "102.135.1.20:42534",
      "connected_at": "2026-06-20T16:14:51.703Z",
      "commands": ["clear_alarm", "factory_reset", "reboot_unit", "..."]
    }
  ]
}
```

`units` is `[]` when no devices are connected.

### `GET /api/units/{serial}`
One connected device. Returns the same object as a `units[]` element, or:

```json
404 { "error": "unit not connected" }
```

### `GET /api/units/{serial}/status`
The connected device's live status detail — connection/server, mobile network/4G,
module health, storage, vehicle IO, GPS/location, sensors. Backs the device detail
page. `404` if the unit is not connected.

```json
200 {
  "serial": "87845313",
  "connection": { "serial": "...", "protocol": "howen", "model": "...",
                  "state": "online", "remote_addr": "...", "connected_at": "..." },
  "telemetry": { "network": { }, "modules": { }, "storage": [ ],
                 "location": { }, "vehicle": { }, "sensors": { } }
}
```

`telemetry` is `null` until the device has reported a status frame.

### `POST /api/units/{serial}/commands`
Send a control command to a connected device. The gateway forwards it over the
device's live TCP connection and waits for the device's acknowledgement.

Request body:

```json
{ "type": "reboot_unit", "payload": { } }
```

Success returns the device's raw acknowledgement plus the gateway receive time:

```json
200 {
  "data": { "err": "0", "ss": "ctrl_864312087845313_19EE5D04342" },
  "received_at": "2026-06-20T16:14:51.714Z"
}
```

Errors:

| Status | Meaning |
|---|---|
| `400` | Unknown command (body includes `supported_commands`), or invalid/missing payload field, or a destructive command sent without `confirm` |
| `404` | Unit not connected |
| `502` | Device rejected the command (non-zero `err`) |
| `504` | Device did not answer in time |

## Device configuration

Read and write the unit's own parameter settings (Wi-Fi, mobile data, server,
clock, power, recording, alarms, …) over the H-Protocol param-config channel
(`0x40A0`/`0x10A0`). The unit must be **awake** — a device in standby returns
`409 { "code": "device_sleeping" }`; send the `wake_device` command first.

### `GET /api/units/{serial}/config?modules=WIFI,DIALUP,SERVER`
Read one or more config segments. `modules` is a comma-separated list of segment
names; omitted, it defaults to `VERSIONINFO,JTBASE,WIFI,DIALUP,SERVER`. Segments
the device doesn't have are silently omitted from the reply. Values are all
strings, often deeply nested (per-channel `chn0..15`, per-day `week0..6`, etc.).

```json
200 { "sc": { "WIFI": { "isOpen": "1", "SSID": "Lab", "Pwd": "…", "Dhcp": "1" } } }
```

### `PUT /api/units/{serial}/config`
Write changed fields, then re-read the written segments and return the device's
truth. **Send ONLY the fields being changed** — the firmware returns garbage in
some untouched string fields, so a full read-modify-write would corrupt config.

```json
// request — nested sc with only the changed leaves
{ "sc": { "WIFI": { "SSID": "newssid" } } }
// 200 — re-read of the written segment(s)
{ "ok": true, "sc": { "WIFI": { "SSID": "newssid", "isOpen": "1", "…": "…" } } }
// 400 — empty/malformed body
{ "error": "body must be {\"sc\": {SEGMENT: {field: value}}}" }
```

Both routes use a 25s device timeout; common segment names (curated friendly in
the panel) include `WIFI DIALUP SERVER ROAMING CLOCK DST POWER RECORD DISPLAY OSD
MASK Privacy IOSET SPEED GSENSOR MOTIONDETECT ACC VOLTAGE PTZ LANGUAGE JTBASE
UPGRADE VERSIONINFO`.

## Video, recordings, clips & snapshots

Live video is HLS produced by ffmpeg; clips are `.mp4` files pulled from the
device's SD card and stored server-side under `CLIPS_ROOT` (the "bucket");
snapshots are single JPEG stills captured on demand. All video routes require the
gateway to have video enabled (`MEDIA_ADVERTISE_HOST` set) — otherwise they
return `503 { "error": "… not enabled" }`. A unit in standby returns
`409 device_sleeping`.

### `POST /api/units/{serial}/stream/start`
Begin a live stream. ffmpeg writes the HLS playlist a few seconds after the device
starts sending frames; the handler waits for the first segment so the player never
races a missing manifest. `ready:false` just means the playlist wasn't up within
the window — the player can keep retrying.

```json
// request — camera 0-based; profile 0 = main (high), 1 = sub (low)
{ "camera": 0, "profile": 0 }
// 200
{ "ok": true, "session_id": "…", "hls_path": "87845313/0/0/stream.m3u8", "ready": true }
```

### `POST /api/units/{serial}/stream/stop`
Stop a live stream. Same `{ "camera", "profile" }` body. `200 { "ok": true }`.

### `GET /api/hls/<serial>/<camera>/<profile>/(stream.m3u8|seg_NNN.ts)`
Serve the HLS playlist/segments ffmpeg produced. API-key protected like everything
else (the panel proxies these so the browser player stays authenticated). `404`
when video is not enabled.

### `GET /api/units/{serial}/recordings?camera=&profile=&start_utc=&end_utc=`
Ask the device what footage it holds for a window — **query this before requesting
a clip**, because playback is file-based and only matches existing recordings.
Defaults: `camera=-1` (all), `profile=1`, window = last 24h. `start_utc`/`end_utc`
are Unix seconds (true UTC; the gateway localises to the device clock internally).

```json
200 { "recordings": [
  { "camera": 0, "profile": 0, "start_utc": 1750000000, "end_utc": 1750000300,
    "file_name": "…", "size": 31457280, "device_start": "…", "device_end": "…" } ],
  "count": 1 }
```

### `POST /api/units/{serial}/clips`
Request a clip upload. The `.mp4` arrives asynchronously; the response carries the
`clip_id` to poll via `GET /api/clips/{id}`. Times are true-UTC Unix seconds.

```json
// request
{ "camera": 0, "profile": 0, "start_utc": 1750000000, "end_utc": 1750000020, "audio": false }
// 200
{ "ok": true, "clip_id": 11, "session_id": "…", "status": "requested" }
```

### `GET /api/clips?serial=&limit=&offset=`
List recorded clips, newest first (paginated). `serial` filters to one device.

```json
200 { "clips": [
  { "id": 11, "serial": "87845313", "camera": 0, "profile": 0,
    "start_utc": 1750000000, "end_utc": 1750000020, "duration_secs": 20,
    "status": "ready", "file_size": 16800000, "bytes_received": 16800000,
    "storage_path": "87845313/11.mp4", "error": "",
    "created_at": "…", "updated_at": "…" } ] }
```

`status`: `requested` → `receiving` → `ready` | `error`.

### `GET /api/clips/{id}`
One clip's metadata/status (same object shape as a `clips[]` element). `404` if absent.

### `GET /api/clips/{id}/download`
Stream the stored `.mp4` (`Content-Type: video/mp4`, attachment). `409` if the
clip isn't `ready`; `404` if the file is missing or storage isn't configured.

### `DELETE /api/clips/{id}`
Remove the clip record and its file. `200 { "ok": true }` / `404` if absent.

### `POST /api/units/{serial}/snapshots`
Ask the device to capture a still image on one or more cameras (H-Protocol
`0x4020`). The device writes the JPEG(s) to its SD card and returns the **file
paths** — it does not return the image bytes (use `/snapshot/image` for that).

```json
// request — channels are 0-based camera indexes (default [0]).
// resolution: 0 follow-video, 1 1080p, 2 720p, 3 VGA, 4 D1
{ "channels": [0], "resolution": 0 }
// 200
{ "ok": true, "session_id": "snap_…",
  "files": [ { "channel": 1, "device_path": "/mnt/sd1/picture/Pic….jpg" } ] }
```

### `POST /api/units/{serial}/snapshot/image?camera=0&resolution=0`
Capture a still on one camera **and** fetch the JPEG back inline: the gateway
triggers the capture, then pulls the file over the device's media port
(file-transfer `0x4090`). Query params `camera` (0-based, default 0) and
`resolution` (as above). Responds with the raw image:

```
200  Content-Type: image/jpeg   (binary JPEG body)
```

Needs video/media enabled (`MEDIA_ADVERTISE_HOST`); a unit in standby returns
`409 device_sleeping`, and a capture/transfer failure returns `502`.

## Admin endpoints

These back the [admin panel](admin-panel.md). All require the API key (the
panel's BFF attaches it server-side). They respond `503` when no database is
configured.

### First-run setup

Bootstrap a fresh gateway (no users yet). Both require the internal token (the
panel calls them); they are not open to the public.

#### `GET /api/setup/status`
```json
200 { "needs_setup": true, "initialized": false }
```

#### `POST /api/setup`
Create the first admin and optionally set the gateway name / first webhook.
Returns `409` once any user exists.
```json
// request (gateway_name, webhook_url optional)
{ "email": "admin@acme.net", "password": "min-8-chars",
  "gateway_name": "gateway.acme.net", "webhook_url": "https://db/gps" }
```

### `POST /api/auth/login`
Verify a front-end user's credentials against the `users` table (bcrypt). Used
by the admin panel's login; the gateway issues no session itself.

```json
// request
{ "email": "admin@example.com", "password": "..." }
// 200
{ "ok": true, "email": "admin@example.com" }
// 401
{ "error": "invalid credentials" }
```

### User accounts

Manage admin-panel accounts (bcrypt). Password hashes are never returned. There
are no roles; any account can sign in and manage the gateway. A lock-out guard
refuses to disable or delete the **last active** user.

#### `GET /api/users`
```json
200 { "users": [ { "id": 1, "email": "admin@example.com", "is_active": true,
  "created_at": "...", "last_login_at": "..." } ] }
```

#### `POST /api/users`
Create an account. `409` if the email exists; `400` if the email is invalid or
the password is shorter than 8 chars.
```json
// request
{ "email": "operator@example.com", "password": "min-8-chars" }
```

#### `PUT /api/users/{id}`
Toggle active and/or reset password (send either or both). `409` when disabling
the last active user; `400` for a short password; `404` if absent.
```json
{ "is_active": false }            // disable
{ "password": "new-min-8-chars" } // reset
```

#### `DELETE /api/users/{id}`
Remove an account. `409` when it is the last active user; `404` if absent.

### API keys

The Bearer keys that authenticate to this API (e.g. for external systems). Only a
sha256 hash is stored; the plaintext is returned **once** at creation.

#### `GET /api/api-keys`
Metadata only — never the key or hash.
```json
200 { "api_keys": [ { "name": "fleet-dashboard", "prefix": "dgw_AbCd1234",
  "is_active": true, "created_at": "...", "last_used_at": "...", "expires_at": null } ] }
```

#### `POST /api/api-keys`
Mint a key. The response is the **only** time the plaintext is available.
```json
// request
{ "name": "fleet-dashboard" }
// 200 — save `key` now; it cannot be retrieved again
{ "key": "dgw_…", "name": "fleet-dashboard" }
```

#### `DELETE /api/api-keys/{prefix}`
Revoke (deactivate) the key(s) with that display prefix — effective immediately.
`404` if no active key matches.

### `GET /api/devices`
Approved device registry.

```json
200 { "devices": [ { "serial": "...", "protocol": "howen", "status": "online",
                     "first_seen_at": "...", "last_seen_at": "..." } ] }
```

### `GET /api/devices/pending`
Quarantined serials awaiting approval (only populated when
`DEVICE_REJECT_UNKNOWN=true`).

```json
200 { "devices": [ { "serial": "...", "protocol_guess": "howen",
                     "remote_ip": "...", "last_seen_at": "..." } ] }
```

### `POST /api/devices/{serial}/approve`
Whitelist a serial (insert into `devices`, remove from quarantine).
`200 { "approved": "<serial>" }`

### `POST /api/devices/{serial}/reject`
Drop a serial from quarantine without whitelisting.
`200 { "rejected": "<serial>" }`

### `DELETE /api/devices/{serial}`
Remove a serial from the approved registry. `200 { "deleted": "<serial>" }` /
`404` if absent.

### `GET /api/mappings?unit=howen&model=`
Editable event-code mappings (full rows). `unit` defaults to this gateway's unit
type; the optional `model` narrows to a specific device model (model-specific rows
override the unit-wide defaults).

```json
200 { "unit": "howen", "model": "Hero-MC30-02", "mappings": [
  { "id": 12, "unit": "howen", "map_type": "dms_adas", "code": 34,
    "event_code": "AI:CELLPHONE", "description": "...", "updated_at": "..." } ] }
```

### `PUT /api/mappings`
Create or update one mapping (instant reload via NOTIFY). Missing required fields
return `400`.

```json
// request
{ "unit": "howen", "map_type": "dms_adas", "code": 34,
  "event_code": "AI:CELLPHONE", "description": "Phone use" }
// 200
{ "ok": true, "unit": "howen", "map_type": "dms_adas", "code": 34 }
```

### `DELETE /api/mappings?unit=howen&map_type=dms_adas&code=34`
Remove a mapping (reverts that code to the built-in default). `404` if absent.

### `GET /api/mappings/models`
List device models that have model-specific mappings for this unit.

```json
200 { "unit": "howen", "models": ["Hero-MC30-02", "..."] }
```

### `POST /api/mappings/copy`
Copy all of one model's mappings onto another model (instant reload). `to_model`
is required; `404` for an unknown unit.

```json
// request
{ "unit": "howen", "from_model": "Hero-MC30-02", "to_model": "Hero-ME40" }
// 200
{ "ok": true, "unit": "howen", "to_model": "Hero-ME40" }
```

### `GET /api/logs?limit=200&offset=0`
Recent gateway/system errors, newest first.

```json
200 { "logs": [ { "id": 9, "unit": "howen", "namespace": "...", "event": "...",
                  "message": "...", "fields": { }, "created_at": "..." } ],
      "limit": 200, "offset": 0 }
```

### `GET /api/device-errors?limit=200&offset=0`
Recent device-reported errors, newest first.

```json
200 { "errors": [ { "id": 4, "serial": "...", "error_category": "...",
                    "error_message": "...", "remote_address": "...",
                    "created_at": "..." } ], "limit": 200, "offset": 0 }
```

### `GET /api/logs/live?after=&level=&unit=&q=&limit=`
The in-memory tail of the gateway's live log stream (connects, approvals, GPS
forwards, ACKs, errors) — works without a database. Poll with the returned
`cursor` as the next `after` to follow it. `level` defaults to `info`; `limit`
defaults to 500 (max 2000); `unit` and `q` filter.

```json
200 { "entries": [ /* log entries, oldest→newest within the page */ ],
      "cursor": 42, "capture_level": "info" }
```

### `GET /api/logs/level`
The current capture level.

```json
200 { "level": "info" }
```

### `PUT /api/logs/level`
Set the capture level. `level` must be `debug`, `info`, or `error`.

```json
// request
{ "level": "debug" }
// 200
{ "ok": true, "level": "debug" }
```

### `GET /api/settings`
All editable server settings (key/value).
```json
200 { "settings": [ { "key": "webhook_url",
  "value": "http://gateway.acmtrack.net:8080/universal/gps/json/", "updated_at": "..." } ] }
```

### `PUT /api/settings`
Upsert one setting. `gateway_name` (the `gateway` field in every universal
message) is applied **live**. `device_port` is validated as a 1–65535 integer and
is applied **on the next gateway restart** (`device_port_active` reports the port
currently bound, so a UI can flag a pending restart). `webhook_url` is legacy
(webhooks are now managed via `/api/webhooks`).
```json
// request
{ "key": "webhook_url", "value": "https://db.example.net/universal/gps/json/" }
// 200
{ "ok": true, "key": "webhook_url" }
// 400 — invalid URL
{ "error": "webhook_url must be a valid http(s) URL" }
```

### Per-unit-type configuration

A gateway hosts one or more **unit types** (e.g. `howen`, `fleetiger`). These
routes manage a unit type's own gateway-side settings, listener ports, and
capability toggles — distinct from a single device's parameter config. `{unit}`
is the unit-type name; `404` for an unknown unit.

#### `GET /api/unit-types/{unit}/settings/schema`
The unit's declared settings schema (field names, types, help text).
```json
200 { "unit": "howen", "schema": [ { "key": "...", "type": "...", "help": "..." } ] }
```

#### `GET /api/unit-types/{unit}/settings`
Current values for the unit's settings.
```json
200 { "unit": "howen", "settings": [ { "key": "...", "value": "..." } ] }
```

#### `PUT /api/unit-types/{unit}/settings`
Set one setting. `400` for an unknown key.
```json
// request
{ "key": "...", "value": "..." }
// 200
{ "ok": true, "unit": "howen", "key": "..." }
```

#### `GET /api/unit-types/{unit}/ports`
The unit's device port (and media port, for video units), plus the port currently
bound (`*_active`) so a UI can flag a pending restart.
```json
200 { "unit": "howen", "has_video": true,
  "device_port": "33000", "device_port_active": "33000",
  "media_port": "33001", "media_port_active": "33001" }
```

#### `PUT /api/unit-types/{unit}/ports`
Set `device_port` and/or `media_port` (1–65535; both optional). Applied on the
next gateway restart. `media_port` is rejected for units without video.
```json
// request
{ "device_port": 33000, "media_port": 33001 }
// 200
{ "ok": true, "unit": "howen" }
```

#### `PUT /api/unit-types/{unit}/capabilities`
Disable/enable a feature the unit supports (`video`, `commands`, `config`,
`status`; all optional). Enabling a feature the unit doesn't support returns `400`.
```json
// request
{ "video": false }
// 200
{ "ok": true, "unit": "howen" }
```

### Telemetry webhooks

The GPS/event data sinks. There may be several; each is independently
enabled/disabled, and the gateway POSTs every device message to **all enabled**
webhooks. Changes apply to the running gateway instantly (NOTIFY). URLs are
validated as http(s).

#### `GET /api/webhooks`
```json
200 { "webhooks": [ { "id": 1, "name": "default",
  "url": "http://gateway.acmtrack.net:8080/universal/gps/json/",
  "is_enabled": true, "created_at": "...", "updated_at": "..." } ] }
```

#### `POST /api/webhooks`
Add a webhook. `is_enabled` defaults to `true`. Re-posting an existing URL updates
that row.
```json
// request
{ "name": "backup", "url": "https://backup.example.net/gps/", "is_enabled": false }
// 200
{ "ok": true, "id": 2 }
// 400 — invalid URL
{ "error": "url must be a valid http(s) URL" }
```

#### `PUT /api/webhooks/{id}`
Update name, URL, and enabled flag (this is also how you enable/disable). `404`
if absent.

#### `DELETE /api/webhooks/{id}`
Remove a webhook. `404` if absent.

### `GET /api/event-codes`
The canonical ACM Standard Event Codes picklist (seeded from the embedded CSV).
Powers the panel's event-code combobox.
```json
200 { "event_codes": [
  { "code": "AI:CELLPHONE", "category": "AI/DMS", "notes": "...", "device_notes": "" } ] }
```

### `POST /api/event-codes`
Add a custom event code to the picklist. `code` is required.
```json
// request
{ "code": "AI:CUSTOM", "category": "AI/DMS", "notes": "..." }
// 200
{ "ok": true, "code": "AI:CUSTOM" }
```

## Howen command catalog

`type` is one of the following. Destructive commands (marked ⚠) require
`payload.confirm = true`.

| `type` | `payload` | Notes |
|---|---|---|
| `reboot_unit` | — | Restart the device |
| `clear_alarm` | — | Clear active alarms |
| `wake_device` | — | Wake from sleep |
| `gsensor_calibrate` | — | Calibrate the G-sensor |
| `sync_time` | `{ "tm"?: "<epoch>" }` | Sync device clock (defaults to device-side time) |
| `osd_speed` | `{ "ods": "1" }` | Toggle OSD speed overlay |
| `send_message` | `{ "text": "...", "tp"?: "1" }` | Display a message |
| `reset_mileage` | `{ "mile": 0 }` | Reset odometer (numeric) |
| `recording_control` | `{ "open"?: "1;2", "close"?: "3" }` | Open/close recording channels (at least one) |
| `factory_reset` ⚠ | `{ "confirm": true }` | Restore factory settings |
| `format_disk` ⚠ | `{ "disk": "0", "confirm": true }` | Format a storage disk |
| `vehicle_control` ⚠ | `{ "act": "...", "door"?: "1", "confirm": true }` | Actuate vehicle controls |

The per-device list is also returned in each unit's `commands` field, so a front
end can render exactly what a given device accepts.

Live video, recorded clips, snapshots, and read/write device configuration are
**not** part of this command catalog — they have their own dedicated routes (see
[Device configuration](#device-configuration) and
[Video, recordings & clips](#video-recordings--clips) above).

## Example

```bash
KEY=dgw_...        # from: apikey create --name frontend

curl -H "Authorization: Bearer $KEY" http://localhost:8080/api/units

curl -X POST -H "Authorization: Bearer $KEY" \
  -d '{"type":"reboot_unit"}' \
  http://localhost:8080/api/units/864312087845313/commands

curl -X POST -H "Authorization: Bearer $KEY" \
  -d '{"type":"factory_reset","payload":{"confirm":true}}' \
  http://localhost:8080/api/units/864312087845313/commands
```
