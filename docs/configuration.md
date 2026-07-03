# Configuration & operations

The gateway runs one or more unit-type plugins in a single process — the default
`cmd/gateway` build hosts Howen, Fleetiger, and Cathexis; a lean `cmd/<unit>`
build hosts just one. Configuration is flat and environment-driven. Defaults are
applied in `internal/core/config`.

## Environment variables

| Variable | Default | Purpose |
|---|---|---|
| `GATEWAY` | _(empty)_ | Identifier surfaced in the universal message `gateway` field. **Seeds the `gateway_name` server setting on first run**; thereafter edit it live in the panel (Server Settings → Gateway identity) |
| `LISTEN_HOST` | `0.0.0.0` | Bind host for both the device TCP server and the HTTP API |
| `LISTEN_PORT` | `33000` | Generic device TCP port — the **fallback** for a unit that has no port of its own. Per-unit ports override it (see *Per-unit device ports* below). **Seeds the per-unit `device_port` setting on first run**; thereafter the stored value wins and is editable in the panel (Server Settings → Device connection), applied **on next gateway restart** — in Docker also update the published port to match |
| `<UNIT>_PORT` | _(unit default)_ | Per-unit device TCP port when a process hosts several units: `HOWEN_PORT` (33000), `FLEETIGER_PORT` (8050), `CATHEXIS_PORT` (33010). Overrides `LISTEN_PORT` for that unit; the bundled compose sets all three |
| `HTTP_PORT` | `8080` | Management/control HTTP API port; `0` disables the API |
| `INTERNAL_API_TOKEN` | _(empty)_ | Shared secret the admin panel uses to authenticate (accepted as a Bearer alongside DB keys). Lets the panel work before any key exists → first-run setup. Set the same value as the admin's `GATEWAY_API_TOKEN` (compose: both from `ADMIN_API_TOKEN`) |
| `DEVICE_WEBHOOK_URL` | _(empty)_ | Telemetry sink — universal-JSON endpoint that stores all GPS/event data. **Seeds the first `webhooks` row on first run** (with a database, manage one or more webhooks live in the admin panel → Server Settings, each enable/disable). Aliases: `WEBHOOK_URL`, `N8N_WEBHOOK_URL` |
| `DEVICE_WEBHOOK_OUTBOX_MAX` | `200000` | Caps the durable on-DB webhook outbox that buffers telemetry through a webhook outage/restart. Beyond it the oldest undelivered messages are dropped so a long outage can't grow the DB unbounded. `0` = uncapped. Applies only with a database; no-DB builds fall back to best-effort direct delivery. |
| `WEBHOOK_TIMEZONE_OFFSET` | `0` | Hours offset embedded in message timestamps |
| `DEVICE_TZ_OFFSET` | `0` | The device's local-clock offset from UTC (e.g. `2` for SAST). Howen units index SD recordings by **local wall-clock**, so the gateway uses this to localise clip/recording windows (`0x4070`/`0x4060`); clip times stay true-UTC in the API and DB. Wrong value → "related file does not exist" (err=6) |
| `DATABASE_URL` | _(empty)_ | PostgreSQL DSN for the gateway's own DB (device registry, mappings, users, API keys). Alias: `POSTGRES_URL` |
| `DEVICE_AUTH_MODE` | `postgres` if `DATABASE_URL` set, else `allow_all` | `allow_all` or `postgres` |
| `DEVICE_REJECT_UNKNOWN` | `true` | In `postgres` mode, reject serials not already in `devices` until approved (secure by default; set `false` to auto-provision + admit). **Seeds the `device_reject_unknown` setting on first run**; toggle it live in the panel (Server Settings → Device authorization) |
| `MAPPING_REFRESH_SECONDS` | `60` | Safety-net reload interval for event mappings (edits already apply instantly via `NOTIFY`; `0` disables the net) |
| `MEDIA_PORT` | `33001` | Device **media** TCP port (video frames), separate from the `LISTEN_PORT` control channel |
| `MEDIA_ADVERTISE_HOST` | _(empty)_ | Host (no port) the device dials back for media, embedded in the live-preview command. **Empty disables video** — live streaming, recordings query, and clips all return `503`/`404` until set |
| `HLS_ROOT` | `/tmp/hls` | Directory where ffmpeg writes HLS playlists/segments for live streams |
| `CLIPS_ROOT` | `/var/lib/gateway/clips` | Directory where pulled `.mp4` clip files **and** saved snapshot JPEGs (under `snapshots/`) are stored (the server-side "bucket"). Back this with a persistent volume |
| `MEDIA_RETENTION_DAYS` | `30` | **Seeds** the `media_retention_days` server setting on first run: how long stored clips/snapshots are kept before an hourly reaper deletes them (files + rows). `0` = keep forever. Thereafter edit it live in the admin (Server Settings → Clip & snapshot retention); this env value only sets the initial default |
| `BACKUPS_ROOT` | `/var/lib/gateway/backups` | Directory scheduled gateway-DB backups are written to (persist this volume). Empty disables backups |
| `BACKUP_ENABLED` / `BACKUP_TIME` / `BACKUP_RETENTION` | `true` / `02:00` / `7` | **Seed** the backup schedule settings on first run: whether the daily backup runs, its HH:MM (UTC) time, and how many archives to keep. Thereafter edit live in the admin (Server Settings → Database backups). Restore with `cmd/backup restore` (see below) |
| `FFMPEG_PATH` | `ffmpeg` | Path to the ffmpeg binary used to mux HLS and clips |
| `DEBUG` | _(empty)_ | `1`/`true`/`*` for all debug logs, or a namespace like `tcp/howen`, `http` |

The bundled compose file also reads `POSTGRES_USER` / `POSTGRES_PASSWORD` /
`POSTGRES_DB` to provision the Postgres container and build `DATABASE_URL`.

### Per-unit device ports

When a process hosts more than one unit, each binds its own device port,
resolved per unit as: the admin-editable `device_port` setting → `<UNIT>_PORT`
env (`HOWEN_PORT`, `FLEETIGER_PORT`, `CATHEXIS_PORT`) → the unit's built-in
default (Howen 33000, Fleetiger 8050, Cathexis 33010) → `LISTEN_PORT`. Live
media uses `MEDIA_PORT` similarly. Keep each published Docker port in sync with
the resolved value.

## Two independent auth planes

- **Devices → gateway (TCP):** authenticated by **serial** at registration
  against the `devices` registry (`DEVICE_AUTH_MODE`). No token.
- **Clients/front end → gateway (HTTP):** authenticated by **API key**
  (`Authorization: Bearer`) on every `/api/` route.

## Running

```bash
# Build + run the multi-unit gateway (Howen + Fleetiger + Cathexis) + PostgreSQL
docker compose -f deploy/docker-compose.yml up --build

# Build an image directly. UNIT picks which cmd/ compiles: `gateway` = multi-unit
# (the compose default), or a single unit like `howen` for a lean image.
docker build -f deploy/Dockerfile --build-arg UNIT=howen -t device-gateway-howen .
```

Tests run inside the Go image (`make test`); no local Go toolchain required.

## Management CLIs

All connect via `DATABASE_URL`. With the bundled compose stack:

```bash
export DATABASE_URL=postgres://gateway:gateway@localhost:5432/gateway?sslmode=disable
```

### Users — `cmd/adduser`
Create or reset a front-end user (password read with no echo, or piped):

```bash
make adduser EMAIL=alice@dfm.co
# or: printf '%s' "$PW" | go run ./cmd/adduser --email alice@dfm.co
```

### API keys — `cmd/apikey`
Mint, list, and revoke HTTP API keys (the key is printed once on creation):

```bash
make apikey ARGS='create --name frontend'
make apikey ARGS='list'
make apikey ARGS='revoke --prefix dgw_AbCd'
```

### Backups — `cmd/backup`
The gateway takes scheduled database backups on its own (Server Settings → Database
backups; written to `BACKUPS_ROOT`). `cmd/backup` is the manual/restore counterpart —
it dumps or restores the gateway DB (registry, users, keys, mappings, settings, clip
metadata; **not** telemetry). Because the gateway owns its schema (recreated on boot),
this is a logical row dump, no `pg_dump` needed.

```bash
go run ./cmd/backup dump  --out gateway-backup.tar.gz   # or to stdout
go run ./cmd/backup restore --in gateway-backup.tar.gz --yes   # DESTRUCTIVE: truncates + reloads
```

Restore into a database whose schema already exists (start the gateway once, then
restore). A backup downloaded from the admin is the same archive and restores the
same way.

### New unit type — `scripts/new-gateway.sh`
Scaffold a new unit type's **code** from the GPS-only template (a developer task,
once per protocol). The generated `cmd/<unit>/main.go` is just
`app.Run(<unit>.New())` — all shared wiring lives in `internal/core/app`:

```bash
scripts/new-gateway.sh teltonika
# then implement ReadFrame + parsing in internal/teltonika/protocol.go
```

Add optional features by setting the flag in `Capabilities()` and implementing the
matching interface (`VideoController`/`Commander`/`ConfigController`/`StatusReporter`,
or the runner-detected `MappingProvider`/`MediaServerProvider`).

### Provision a server — `scripts/provision-server.sh`
Stand up a server for a unit whose code already exists (an operator task). Builds
the **lean image** (only that unit compiles in) and writes a per-unit stack:

```bash
scripts/provision-server.sh teltonika
docker compose -f deploy/docker-compose.teltonika.yml --env-file deploy/.env up -d --build
```

One unit per server — build-time selection keeps each image free of code (and
dependencies like ffmpeg) for protocols it will never run. (Or register the unit
in `cmd/gateway` to host it in the multi-unit process instead.) The admin panel
reads the running gateway's effective capabilities from `GET /api/gateway/info`
and hides UI for features this build/config lacks.

## Production notes

- Terminate **TLS** in front of the HTTP API (keys are bearer tokens).
- Use `sslmode=require` (or `verify-full`) to PostgreSQL.
- Give the app's DB login least privilege on its own database (it needs
  `SELECT/INSERT/UPDATE` on the gateway tables, not superuser).
