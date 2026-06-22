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

### `GET /api/mappings?unit=howen`
Editable event-code mappings (full rows). `unit` defaults to this gateway's unit
type.

```json
200 { "unit": "howen", "mappings": [
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

### Per-model mapping workflows

The visual ("N8N-style") device-mapping method: a node graph per **model** that
the gateway's flow engine evaluates to produce event codes. Strictly per
`(unit, model)` — a device uses its own model's active workflow, or (if none) the
flat code table. Saves fire the same instant-`NOTIFY` reload.

The graph is `{ "nodes": [...], "edges": [...] }`. Node `type`s: `input` (one),
`switch` (`data.field`; edges labelled `case:<v>` / `default`), `condition`
(`data.{field,op,value}`; edges labelled `true` / `false`), `setEvent`
(`data.event`), `setField` (`data.{key,value}`), `output`.

#### `GET /api/workflows`
List per-model workflow summaries for this unit.
```json
200 { "unit": "howen", "workflows": [
  { "model": "Hero-MC30-02", "name": "phone demo", "is_active": true,
    "node_count": 5, "edge_count": 4, "updated_at": "..." } ] }
```

#### `GET /api/workflows/{model}`
One model's full workflow (graph included). `404` if the model has none.

#### `PUT /api/workflows/{model}`
Create or replace a model's workflow. The graph is validated (one input node,
edges reference real nodes); an invalid graph returns `400`.
```json
{ "name": "phone demo", "is_active": true,
  "graph": { "nodes": [ ... ], "edges": [ ... ] } }
```

#### `DELETE /api/workflows/{model}`
Remove a model's workflow (it reverts to the code table). `404` if absent.

#### `POST /api/workflows/test`
Dry-run a graph against a sample payload (pure compute, no save). Powers the
editor's test panel.
```json
// request
{ "graph": { ... }, "payload": { "eventCode": 30, "detail": { "tp": 34 } } }
// 200
{ "matched": true, "events": ["AI:PHONE_USE"], "fields": {},
  "trace": ["in","sw","sw2","ev","out"] }
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

Video/stream/clip and parameter-config (read/write device settings) commands are
not part of this set yet — they arrive with the media milestone.

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
