# device-gateway

A Go rewrite of the DFM MVR gateway as a **plugin framework** for device protocols.

The old `dfm-mvr-gateway` was one Node.js monolith running 8 TCP servers for 4
device protocols (cathexis, jt808, tramigo, howen) with four copies of nearly
everything — connection registries, request managers, command dispatch, config
validators. Most of those units were only ever wired up for testing, leaving a
lot of confusing, redundant code.

This project inverts that. A small protocol-agnostic framework core handles the
TCP accept loop, configuration, logging, device authorization, and the
all-important universal message. Each unit type is a plugin implementing one
small interface, and `app.Run(...)` takes one or more of them. That yields two
deployment shapes from the same code:

- **Multi-unit** (`cmd/gateway`, the default `deploy/docker-compose.yml`): one
  process hosts every registered unit — today **Howen**, **Fleetiger**, and
  **Cathexis** — each on its own TCP port, behind one shared registry, webhook,
  HTTP API, and admin panel. Simplest to run as a single box.
- **One unit per server** (`scripts/provision-server.sh <unit>`, building the
  lean `cmd/<unit>` image): only that protocol compiles in, so a GPS-only
  tracker carries no video/ffmpeg code and a crash or restart is isolated to
  that one unit. Use this when you want independent deploys.

Scaffolding a new device type (`scripts/new-gateway.sh`) writes both a plugin and
a lean `cmd/<unit>` entrypoint; adding one line to `cmd/gateway` also hosts it in
the multi-unit process.

GPS/event **telemetry** is forwarded to the universal-JSON **webhook** — the
external database that stores all GPS and event data. The gateway's own
**PostgreSQL** holds only gateway state — the **unit registry** (verifying
connecting devices), server settings, event mappings, users, API keys, and clip
metadata; no telemetry is stored there.

**Howen** (GPS + events, live video, recorded clips, device config & status) is
the reference for a full-featured plugin; **Fleetiger** (a GT06-style GPS
tracker) and **Cathexis** (MVR video + config) are the other implemented units.

## Documentation

- [docs/http-api.md](docs/http-api.md) — HTTP API reference (auth, units, commands, devices, mappings, logs)
- [docs/admin-panel.md](docs/admin-panel.md) — Next.js admin panel (approve devices, edit settings, view logs)
- [docs/configuration.md](docs/configuration.md) — env vars, the management CLIs, deployment
- [docs/database.md](docs/database.md) — the gateway database tables (registry, mappings, users, API keys)

The rest of this README is the high-level overview.

## Why this shape

- **No dead code.** A GPS-only tracker doesn't drag along video/clip/command
  machinery — it just doesn't enable those capabilities.
- **Independent deploys (optional).** Run one unit per server
  (`scripts/provision-server.sh`) and a bug or restart in one unit type can't
  take down the others; or host them together in the multi-unit process when a
  single box is simpler.
- **One message, every sink.** Every unit type funnels through the same universal
  builder; the built message is POSTed to the telemetry webhook (and any future
  telemetry sink), identical regardless of device.

## The universal message is the invariant

Every device's GPS and event data becomes the **ACM Universal JSON Message**.
This format must never drift. `internal/core/message` is a faithful port of the
production `universalWebhookAdapter.js`, and `internal/core/message/message_test.go`
is a **golden test** that asserts the Go output is byte-for-byte identical to the
original JS adapter across a range of fixtures (GPS, trip/g-sensor events with
detail rows, IPv6, timezone offsets, the per-device `seq_no` counter,
comma-decimal coercion, …).

The message is built once per device frame and handed to every configured
telemetry **sink** (the webhook today), so the `seq_no` counter advances exactly
once regardless of how many sinks are active.

### The gateway database (PostgreSQL)

PostgreSQL is the gateway's own state — **not** a telemetry store. Today it holds
the unit registry used to verify connecting devices (`devices` /
`unknown_devices`); server-settings tables will be added here later.

```sql
devices(serial PK, protocol, status, first_seen_at, last_seen_at)
unknown_devices(serial PK, source_protocol_guess, remote_ip, last_payload_meta, last_seen_at, status)
event_mappings(id PK, unit, map_type, code, event_code, description, updated_at,
               UNIQUE(unit, map_type, code))
users(id PK, email, password_hash, is_active, created_at, updated_at, last_login_at,
      UNIQUE(lower(email)))
api_keys(id PK, name, key_hash UNIQUE, prefix, is_active, created_at, last_used_at, expires_at)
```

### User accounts

Front-end user accounts live in `users`. Passwords are never stored — only a
salted **bcrypt** hash (cost 12). Create or reset a user with the `adduser` CLI,
which reads the password from a no-echo prompt (or piped stdin) so it never
appears in `ps` or shell history:

```bash
export DATABASE_URL=postgres://gateway:gateway@localhost:5432/gateway?sslmode=disable
make adduser EMAIL=alice@dfm.co            # interactive prompt
# or: printf '%s' "$PW" | go run ./cmd/adduser --email alice@dfm.co
```

Re-running for an existing email resets that user's password (case-insensitive).
Your front end authenticates by calling `Store.VerifyUser(email, password)` —
which compares in constant-ish time and records `last_login_at`. There are no
roles yet; it's a flat list of users.

### HTTP API and API keys

The gateway exposes a management/control HTTP API on `HTTP_PORT` (default 8080;
`0` disables). `GET /healthz` is public; everything under `/api/` requires
`Authorization: Bearer <api-key>`.

Endpoints:

| Method & path | Purpose |
|---|---|
| `GET /healthz` | Liveness (public) |
| `GET /api/units` | List currently-connected devices (+ each one's supported commands) |
| `GET /api/units/{serial}` | One connected device's info (404 if not connected) |
| `POST /api/units/{serial}/commands` | Send a control command; body `{"type":"...","payload":{...}}` |

```bash
curl -H "Authorization: Bearer dgw_…" http://localhost:8080/api/units
curl -X POST -H "Authorization: Bearer dgw_…" \
  -d '{"type":"reboot_unit"}' http://localhost:8080/api/units/<serial>/commands
```

Howen control commands: `reboot_unit`, `clear_alarm`, `wake_device`,
`gsensor_calibrate`, `sync_time`, `osd_speed`, `send_message`, `reset_mileage`,
`recording_control`, and the destructive `factory_reset` / `format_disk` /
`vehicle_control` (which require `payload.confirm = true`). The gateway routes
the command to the live TCP connection and returns the device's acknowledgement.
Status codes: `404` not connected, `400` unsupported/invalid (with a
`supported_commands` list), `504` device didn't answer, `502` device rejected.
Devices authenticate by serial over TCP — they never send an API key.

API keys are random 256-bit tokens; only their **sha256 hash** is stored (a fast
hash is right for high-entropy keys — bcrypt is for passwords). Mint/list/revoke
with the `apikey` CLI; the full key is printed once at creation:

```bash
export DATABASE_URL=postgres://gateway:gateway@localhost:5432/gateway?sslmode=disable
make apikey ARGS='create --name frontend'    # prints dgw_… once
make apikey ARGS='list'
make apikey ARGS='revoke --prefix dgw_AbCd'   # instant; no redeploy

curl -H "Authorization: Bearer dgw_…" http://localhost:8080/api/ping
```

Keys can also carry an `expires_at`. Always serve the API over TLS in production.

### Editable event mappings

The raw-code → Standard-Event-Code lookups (e.g. Howen DMS subtype `34` →
`AI:CELLPHONE`) live in `event_mappings` so a front end can edit them without a
redeploy. On startup the gateway **seeds** the built-in defaults (skipping rows
that already exist) and **loads** the current set into memory. Edits apply
**instantly**: a trigger fires `NOTIFY` on any change and the gateway, listening
on that channel, reloads immediately and atomically swaps the live maps. A
periodic reload (`MAPPING_REFRESH_SECONDS`, default 60) runs as a safety net in
case a notification is missed on a dropped connection. The built-in maps remain
the fallback if a `map_type` is empty or the database is unavailable.

Howen `map_type`s: `event_code`, `dms_adas`, `vibration_direction`,
`geofence_status`, `voltage`, `input`, `media_alarm_subtype`. Some `event_code`
codes (trip/speeding/parking/idling and the sub-table codes) are resolved by
built-in logic and aren't editable rows — they're not seeded, so the admin only
lists `event_code` rows that take effect. Edit a mapping with e.g.:

```sql
UPDATE event_mappings SET event_code = 'AI:PHONE_USE'
WHERE unit = 'howen' AND map_type = 'dms_adas' AND code = 34;
```

Regenerate the golden file (after an intentional change) with:

```bash
node tools/gen-webhook-golden.mjs /path/to/dfm-mvr-gateway
```

## Architecture

```
cmd/gateway/main.go            multi-unit entrypoint: app.Run(howen, fleetiger, cathexis)
cmd/howen/main.go              lean single-unit entrypoint: app.Run(howen.New())
internal/
  core/                        protocol-agnostic framework
    app/                       composition root: wires deps around a Protocol, app.Run()
    config/                    env-driven configuration
    logging/                   structured JSON logger
    device/                    serial normalization + pluggable authorization
    message/                   universal message builder (+ golden parity test)
    mapping/                   neutral type for editable code->event tables
    postgres/                  gateway DB: devices, event_mappings, users, api_keys
    webhook/                   telemetry sink (universal-JSON HTTP)
    httpapi/                   management/control HTTP API + Bearer API-key middleware
    gateway/                   TCP accept loop, Conn.Emit, Hub, Protocol/Sink/Commander ifaces
  howen/                       Howen H-Protocol plugin
    codec.go                   frame I/O + binary status/alarm decoding
    events.go                  event-code maps (DB-editable; raw Howen -> ACM codes)
    gps.go                     status -> normalized payload
    server.go                  Protocol + Session: registration, GPS, alarms
  fleetiger/                   GT06-style GPS tracker plugin (GPS-only)
  cathexis/                    MVR video + config plugin
deploy/                        Dockerfile (generic, UNIT build arg) + compose
scripts/new-gateway.sh         scaffold a new unit type's code
scripts/provision-server.sh    stand up a server for an existing unit
templates/gps-only/            starter plugin for a new GPS-only tracker
```

A unit-type plugin implements `gateway.Protocol`:

```go
type Protocol interface {
    Name() string
    Capabilities() Capabilities          // HasVideo, HasCommands, HasConfig, HasStatus
    ReadFrame(r *bufio.Reader) (Frame, error)
    NewSession(c *Conn) Session
}
```

…and its `Session` handles frames, calling `conn.Emit(serial, make, model, kind,
payload)` to forward a normalized payload. The framework does the rest — all the
shared wiring lives in `internal/core/app`, so a unit-type binary is just:

```go
func main() { app.Run(howen.New()) }
```

`cmd/gateway` instead passes several — `app.Run(howen.New(), fleetiger.New(),
cathexis.New())` — to host all of them in one process.

Richer units add features by setting the matching `Capabilities` flag **and**
implementing the optional interface the framework detects:

| Feature | Set flag | Implement |
|---|---|---|
| Live video / clips | `HasVideo` | `gateway.VideoController` (+ `MediaServerProvider` for the media listener) |
| Control commands | `HasCommands` | `gateway.Commander` |
| Read/write config | `HasConfig` | `gateway.ConfigController` |
| Live status detail | `HasStatus` | `gateway.StatusReporter` |
| Editable event maps | — | `gateway.MappingProvider` |

A plain GPS unit implements none of these; the runner skips that wiring and the
admin panel hides the matching UI (it reads effective capabilities from
`GET /api/gateway/info`). The optional interfaces are inherently protocol-specific
— `internal/howen/` is the worked reference for implementing each (video in
`video.go`/`media.go`, config in `config.go`, status in `status.go`, commands and
mappings in `commands.go`/`events.go`).

## Quick start

```bash
# Build and run the multi-unit gateway (howen + fleetiger + cathexis) + PostgreSQL
docker compose -f deploy/docker-compose.yml up --build

# inspect the unit registry (telemetry itself goes to the webhook)
docker compose -f deploy/docker-compose.yml exec postgres \
  psql -U gateway -d gateway -c \
  "select serial, protocol, status, last_seen_at from devices;"
```

GPS/event telemetry is delivered to `DEVICE_WEBHOOK_URL`. To run against an
external PostgreSQL registry, set `DATABASE_URL`:

```bash
docker build -f deploy/Dockerfile --build-arg UNIT=howen -t device-gateway-howen .
docker run -p 33000:33000 \
  -e DEVICE_WEBHOOK_URL=http://your-db/universal/gps/json/ \
  -e DATABASE_URL=postgres://user:pass@host:5432/gateway?sslmode=disable \
  device-gateway-howen
```

## Configuration

All configuration is environment-driven and flat — no per-unit config branching.
A multi-unit process shares one config; each unit takes its own device port via
`<UNIT>_PORT` (e.g. `HOWEN_PORT`, `FLEETIGER_PORT`, `CATHEXIS_PORT`).

| Variable | Default | Purpose |
|---|---|---|
| `GATEWAY` | _(empty)_ | Identifier surfaced in the message `gateway` field |
| `LISTEN_HOST` | `0.0.0.0` | Device TCP bind host |
| `LISTEN_PORT` | `33000` | Device TCP bind port (Howen control port) |
| `DEVICE_WEBHOOK_URL` | _(empty)_ | Telemetry sink — universal-JSON endpoint storing all GPS/event data |
| `DATABASE_URL` | _(empty)_ | PostgreSQL DSN for the gateway's unit registry (device verification) |
| `HTTP_PORT` | `8080` | Management/control HTTP API port (API-key protected); `0` disables |
| `DEVICE_AUTH_MODE` | `postgres` if DB set, else `allow_all` | `allow_all` or `postgres` |
| `DEVICE_REJECT_UNKNOWN` | `false` | `postgres` mode: reject serials not already in `devices` |
| `MAPPING_REFRESH_SECONDS` | `60` | Safety-net mapping reload interval; edits already apply instantly via NOTIFY (0 disables the net) |
| `WEBHOOK_TIMEZONE_OFFSET` | `0` | Hours offset embedded in message timestamps |
| `DEBUG` | _(empty)_ | `1`/`*` for all, or a namespace like `tcp/howen` |

### Device authorization

- **`allow_all`** — every connecting device is admitted; no registry. Used when
  `DATABASE_URL` is unset.
- **`postgres`** (default when a database is configured) — tracks each serial in
  the `devices` table. By default unknown serials are **auto-provisioned and
  admitted** (so data always flows) and lifecycle status (online/sleep/offline)
  is written back. Set `DEVICE_REJECT_UNKNOWN=true` for the stricter behaviour:
  unknown serials are recorded in `unknown_devices` and rejected.

## Adding a unit / standing up a server

Two separate flows:

**1. Author a new protocol's code** (a developer task — only once per unit type):

```bash
scripts/new-gateway.sh teltonika
```

This generates `internal/teltonika/protocol.go` (a GPS-only skeleton with newline
framing and a placeholder CSV parser) and a `cmd/teltonika/main.go` shim
(`app.Run(teltonika.New())`). Then implement `ReadFrame` and the parser for your
device's real wire format, emitting a payload whose keys map into the universal
message (`latitude`, `longitude`, `speed`, `utc`, `event`, …). Add optional
features per the capability table above.

**2. Provision a server** for a unit whose code already exists (an operator task):

```bash
scripts/provision-server.sh teltonika
```

This builds the **lean image** `device-gateway-teltonika` (only that unit's code
compiles in — a GPS-only unit carries no video/ffmpeg machinery) and writes a
per-unit stack `deploy/docker-compose.teltonika.yml`. Edit `deploy/.env`, then
`docker compose -f deploy/docker-compose.teltonika.yml --env-file deploy/.env up -d --build`.

One unit per server; build-time selection keeps each image free of code for
protocols it will never run. Alternatively, register the unit in `cmd/gateway` to
host it alongside the others in the multi-unit process (one box, shared DB).

Devices with video/control set `Capabilities{HasVideo: true, ...}` and implement
the additional milestone-2 hooks (see Roadmap).

## Testing

```bash
make test          # runs go test ./... inside the golang Docker image
```

Coverage includes: Howen binary decoding against real captured packets
(`codec_test.go`), the full event-code map (`events_test.go`), an end-to-end
register→subscribe→alarm→sink flow (`integration_test.go`), and the universal
message golden parity test (`internal/core/message`).

## Roadmap

- **Milestone 1 (done):** framework core, universal message (golden-tested),
  Howen registration + GPS + events delivered to the telemetry webhook; Postgres
  unit registry for device verification.
- **Control API (done):** API-key-protected HTTP endpoints to list connected
  units and send Howen control commands (`Capabilities.HasCommands`), plus user
  accounts and editable event mappings.
- **Milestone 2 (done):** Howen video/media behind `Capabilities.HasVideo` —
  live HLS via ffmpeg, recorded-clip ingest to server-side storage (`CLIPS_ROOT`),
  footage/recordings query, live device status, and full device configuration
  (read/write all parameter segments) from the admin panel.
- **Milestone 3 (in progress):** additional unit types via the scaffold —
  **Fleetiger** (GT06-style GPS) and **Cathexis** (MVR video + config) are wired
  into the multi-unit gateway.
