# The gateway database

PostgreSQL is the gateway's **own** state store — **not** a telemetry store.
GPS/event data goes to the webhook; this database holds device verification,
editable event mappings, front-end users, and API keys. The schema is applied
idempotently on startup (`internal/core/postgres`); add new tables there.

## Tables

### `devices` — unit registry (verification)
Connecting devices are checked here. In `postgres` auth mode, unknown serials are
quarantined and rejected until approved (the default; set `device_reject_unknown`
false to auto-provision and admit instead). Status flips `online` / `sleep` /
`offline` as the device reports and disconnects.

```
serial PK, protocol, status, first_seen_at, last_seen_at
```

### `unknown_devices` — quarantine
Only used when `DEVICE_REJECT_UNKNOWN=true`: rejected serials are recorded here.

```
serial PK, source_protocol_guess, remote_ip, last_payload_meta, last_seen_at, status
```

### `event_mappings` — editable code → event lookups
Maps raw device codes to ACM Standard Event Codes per unit. Seeded from the
built-in defaults on startup (existing rows are never overwritten).

```
id PK, unit, map_type, code, event_code, description, updated_at
UNIQUE (unit, map_type, code)
```

Howen `map_type`s: `event_code`, `dms_adas`, `vibration_direction`,
`geofence_status`, `voltage`, `input`, `media_alarm_subtype`.

**Edits apply instantly.** A trigger fires `NOTIFY event_mappings_changed`, and
the gateway (listening on that channel) reloads and atomically swaps the live
maps — no redeploy. `MAPPING_REFRESH_SECONDS` is only a backstop. Built-in
defaults remain the fallback if the DB is unavailable or a `map_type` is empty.

```sql
UPDATE event_mappings SET event_code = 'AI:PHONE_USE'
WHERE unit = 'howen' AND map_type = 'dms_adas' AND code = 34;
-- the running gateway picks this up within milliseconds
```

### `standard_event_codes` — event-code picklist
The canonical ACM Standard Event Codes the admin panel offers when choosing an
`event_code` (so mappings reference real codes, not typos). Seeded on startup
from the embedded CSV (`internal/core/eventcodes/standard_event_codes.csv`, the
official export). Seeding is an upsert: the CSV is the source of truth, but custom
codes added later (not in the CSV) are preserved.

```
code PK, category, notes, device_notes, updated_at
```

Served at `GET /api/event-codes`; the panel renders it as a `<datalist>` combobox
(known codes are suggested, but custom values — e.g. the `:x` template codes like
`INPUT:ON:x` — can still be typed).

### `webhooks` — telemetry sinks (GPS/event data)
The external endpoints that store all GPS/event data. There may be several, each
independently enabled/disabled; the gateway POSTs every device message to **all
enabled** webhooks. On first run a single row is migrated from the legacy
`webhook_url` setting / `DEVICE_WEBHOOK_URL` env. Edits apply to the running
gateway instantly via a `NOTIFY webhooks_changed` trigger (the sink's target list
is swapped live).

```
id PK, name, url UNIQUE, is_enabled, created_at, updated_at
```

Served at `GET /api/webhooks`; managed via `POST /api/webhooks`,
`PUT/DELETE /api/webhooks/{id}` (URLs validated as http(s)).

### `clips` — recorded-clip metadata
Metadata for `.mp4` files pulled from a device's SD card (H-Protocol playback,
`0x4070`). This is **metadata only** — the bytes live on disk under `CLIPS_ROOT`
(the "bucket"); `storage_path` is relative to that root. `status` advances
`requested` → `receiving` → `ready` | `error` as the upload streams in.

```
id PK, serial, camera, profile, start_utc, end_utc, duration_secs,
status, file_size, bytes_received, storage_path, error, created_at, updated_at
INDEX (serial, created_at DESC)
```

`start_utc`/`end_utc` are true-UTC Unix seconds (the gateway localises to the
device clock when requesting playback — see `DEVICE_TZ_OFFSET`). Served at
`GET /api/clips`; one clip at `GET /api/clips/{id}`, downloaded via
`/download`, removed (record + file) via `DELETE /api/clips/{id}`.

### `snapshots` — saved-snapshot metadata
Metadata for JPEG stills saved to the gateway. Like clips, **metadata only** —
the bytes live on disk under `CLIPS_ROOT/snapshots`; `storage_path` is relative
to `CLIPS_ROOT`. `source` is `capture` (taken on demand, `0x4020`) or `device`
(copied off the SD card via file-transfer `0x4060`/`0x4090`).

```
id PK, serial, camera, kind, source, captured_utc, device_path,
storage_path, file_size, created_at
INDEX (serial, created_at DESC)
```

Saved via `POST /api/units/{serial}/snapshots/save`; listed at
`GET /api/snapshots`, downloaded via `GET /api/snapshots/{id}/download`, removed
(record + file) via `DELETE /api/snapshots/{id}`.

### `server_settings` — editable gateway settings
Generic key/value store for runtime-editable, gateway-wide settings, with a
`NOTIFY server_settings_changed` trigger. Keys: `gateway_name` (the identifier in
every universal message's `gateway` field, seeded from `GATEWAY`; applied live),
`device_reject_unknown` (the device-authorization gate, seeded from
`DEVICE_REJECT_UNKNOWN`; applied live), `device_port` (the device TCP port, seeded
from `LISTEN_PORT`; applied on the next gateway restart) and `device_port_active`
(the port the gateway actually bound this run, so the panel can flag a pending
restart). Also holds the legacy `webhook_url` (now migrated to
the `webhooks` table, kept only as the first-run seed source). Served at
`GET /api/settings`, updated via `PUT /api/settings`.

```
key PK, value, updated_at
```

### `users` — front-end accounts
Flat list, no roles yet. Passwords are stored only as a salted **bcrypt** hash.

```
id PK, email, password_hash, is_active, created_at, updated_at, last_login_at
UNIQUE (lower(email))   -- case-insensitive
```

Manage with the admin panel's **Users** page (`/api/users`: create, enable/disable,
reset password, delete — with a guard against removing the last active user) or
the `cmd/adduser` CLI. Logins verify via `Store.VerifyUser(email, password)`
(constant-ish time, records `last_login_at`).

### `api_keys` — HTTP API keys
Random 256-bit tokens; only the **sha256 hash** is stored (a fast hash is correct
for high-entropy keys). The plaintext is shown once at creation.

```
id PK, name, key_hash UNIQUE, prefix, is_active, created_at, last_used_at, expires_at
```

Manage from the admin panel's **API Keys** page (`/api/api-keys`: generate — the
plaintext is shown once — and revoke) or the `cmd/apikey` CLI. Verification looks
up by hash and honours `is_active` and `expires_at`, so revoke/expiry are instant.
`prefix` (e.g. `dgw_AbCd`) lets a UI list keys safely without exposing them.

## Why no telemetry table

The universal GPS/event message is the external system's responsibility (the
`DEVICE_WEBHOOK_URL` sink). Keeping telemetry out of this database keeps it small,
fast, and focused on gateway configuration and verification.
