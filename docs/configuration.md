# Configuration & operations

One gateway binary serves one unit type; configuration is flat and environment
driven. Defaults are applied in `internal/core/config`.

## Environment variables

| Variable | Default | Purpose |
|---|---|---|
| `GATEWAY` | _(empty)_ | Identifier surfaced in the universal message `gateway` field. **Seeds the `gateway_name` server setting on first run**; thereafter edit it live in the panel (Server Settings → Gateway identity) |
| `LISTEN_HOST` | `0.0.0.0` | Bind host for both the device TCP server and the HTTP API |
| `LISTEN_PORT` | `33000` | Device TCP port (Howen control channel). **Seeds the `device_port` server setting on first run**; thereafter the stored value wins and is editable in the panel (Server Settings → Device connection), applied **on next gateway restart** — in Docker also update the published port to match |
| `HTTP_PORT` | `8080` | Management/control HTTP API port; `0` disables the API |
| `INTERNAL_API_TOKEN` | _(empty)_ | Shared secret the admin panel uses to authenticate (accepted as a Bearer alongside DB keys). Lets the panel work before any key exists → first-run setup. Set the same value as the admin's `GATEWAY_API_TOKEN` (compose: both from `ADMIN_API_TOKEN`) |
| `DEVICE_WEBHOOK_URL` | _(empty)_ | Telemetry sink — universal-JSON endpoint that stores all GPS/event data. **Seeds the first `webhooks` row on first run** (with a database, manage one or more webhooks live in the admin panel → Server Settings, each enable/disable). Aliases: `WEBHOOK_URL`, `N8N_WEBHOOK_URL` |
| `WEBHOOK_TIMEZONE_OFFSET` | `0` | Hours offset embedded in message timestamps |
| `DEVICE_TZ_OFFSET` | `0` | The device's local-clock offset from UTC (e.g. `2` for SAST). Howen units index SD recordings by **local wall-clock**, so the gateway uses this to localise clip/recording windows (`0x4070`/`0x4060`); clip times stay true-UTC in the API and DB. Wrong value → "related file does not exist" (err=6) |
| `DATABASE_URL` | _(empty)_ | PostgreSQL DSN for the gateway's own DB (device registry, mappings, users, API keys). Alias: `POSTGRES_URL` |
| `DEVICE_AUTH_MODE` | `postgres` if `DATABASE_URL` set, else `allow_all` | `allow_all` or `postgres` |
| `DEVICE_REJECT_UNKNOWN` | `true` | In `postgres` mode, reject serials not already in `devices` until approved (secure by default; set `false` to auto-provision + admit). **Seeds the `device_reject_unknown` setting on first run**; toggle it live in the panel (Server Settings → Device authorization) |
| `MAPPING_REFRESH_SECONDS` | `60` | Safety-net reload interval for event mappings (edits already apply instantly via `NOTIFY`; `0` disables the net) |
| `MEDIA_PORT` | `33001` | Device **media** TCP port (video frames), separate from the `LISTEN_PORT` control channel |
| `MEDIA_ADVERTISE_HOST` | _(empty)_ | Host (no port) the device dials back for media, embedded in the live-preview command. **Empty disables video** — live streaming, recordings query, and clips all return `503`/`404` until set |
| `HLS_ROOT` | `/tmp/hls` | Directory where ffmpeg writes HLS playlists/segments for live streams |
| `CLIPS_ROOT` | `/var/lib/gateway/clips` | Directory where pulled `.mp4` clip files are stored (the server-side "bucket"). Back this with a persistent volume |
| `FFMPEG_PATH` | `ffmpeg` | Path to the ffmpeg binary used to mux HLS and clips |
| `DEBUG` | _(empty)_ | `1`/`true`/`*` for all debug logs, or a namespace like `tcp/howen`, `http` |

The bundled compose file also reads `POSTGRES_USER` / `POSTGRES_PASSWORD` /
`POSTGRES_DB` to provision the Postgres container and build `DATABASE_URL`.

## Two independent auth planes

- **Devices → gateway (TCP):** authenticated by **serial** at registration
  against the `devices` registry (`DEVICE_AUTH_MODE`). No token.
- **Clients/front end → gateway (HTTP):** authenticated by **API key**
  (`Authorization: Bearer`) on every `/api/` route.

## Running

```bash
# Build + run the Howen gateway and PostgreSQL together
docker compose -f deploy/docker-compose.yml up --build

# Build a unit-type image directly (UNIT picks which cmd/ to compile)
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

### New unit type — `scripts/new-gateway.sh`
Scaffold a new per-unit-type server from the GPS-only template:

```bash
scripts/new-gateway.sh teltonika
# implement ReadFrame + parsing in internal/teltonika/protocol.go, then:
docker build -f deploy/Dockerfile --build-arg UNIT=teltonika -t device-gateway-teltonika .
```

## Production notes

- Terminate **TLS** in front of the HTTP API (keys are bearer tokens).
- Use `sslmode=require` (or `verify-full`) to PostgreSQL.
- Give the app's DB login least privilege on its own database (it needs
  `SELECT/INSERT/UPDATE` on the gateway tables, not superuser).
