# Admin panel

A Next.js + Tailwind web app (`admin/`) for operating the gateway: approve
connecting devices, view live device status, configure units, stream live video
and pull recorded clips, edit server settings (event mappings), and view logs. It
runs as its own Docker container.

## Security model

The panel **never touches the database directly** — every read and write goes
through the gateway HTTP API. There are two layers:

1. **User login.** Admins sign in with an account from the gateway's `users`
   table (bcrypt). The panel posts the credentials to the gateway's
   `POST /api/auth/login`; on success it issues its own signed, httpOnly session
   cookie (a JWT signed with `SESSION_SECRET`). Middleware enforces the cookie on
   every page and proxy request.
2. **BFF proxy.** The browser only ever calls the panel's own `/api/gw/*`
   routes. The Next.js server attaches the **internal service token**
   (`GATEWAY_API_TOKEN`, matching the gateway's `INTERNAL_API_TOKEN`) and forwards
   to `/<gateway>/api/*`. The token is a server-only env var — never sent to the
   browser. It is separate from the user-facing API keys (those are purely for
   external systems, managed on the API Keys page), and it works before any key
   exists, which is what makes first-run setup possible.

```
browser ──cookie──▶ Next.js (BFF) ──Bearer internal token──▶ gateway HTTP API ──▶ Postgres
         (no token)                (token server-side)
```

So compromising the browser cannot leak the token, and the panel cannot reach
Postgres except through the endpoints the gateway exposes.

## First-run setup

On a fresh database (no users), the panel sends you to a **setup wizard**
(`/setup`): create the first admin account and optionally set the gateway name
and a webhook. Because the panel authenticates with the internal token, this
works with no CLI step — `docker compose up`, open the panel, fill in the form.
The gateway's `POST /api/setup` only works while there are zero users; once
initialized it returns `409` and the panel routes to normal login.

## Pages

| Page | Path | Gateway endpoints used |
|------|------|------------------------|
| Dashboard | `/` | `GET /api/units`, `GET /api/devices`, `GET /api/devices/pending` — live/standby/offline counts (a unit's `state` can be `online`/`sleep`/`offline`; the dashboard surfaces an **In standby** count and a per-device State badge, and serials link to the device detail page) |
| Devices | `/devices` | `GET /api/devices`, `GET /api/devices/pending`, `POST …/approve`, `POST …/reject`, `DELETE /api/devices/{serial}` |
| Device detail | `/devices/{serial}` | **Status tab:** `GET /api/units/{serial}/status` (live 4G/network, module health, storage, IO, GPS). **Config tab:** `GET/PUT /api/units/{serial}/config` (full device-settings editor, below). `POST …/commands` `wake_device` when the unit is in standby |
| Clips | `/clips` | `GET /api/units/{serial}/recordings` (search footage), `POST /api/units/{serial}/clips` (pull a clip or trimmed section), `GET /api/clips`, `GET /api/clips/{id}`, `/download`, `DELETE /api/clips/{id}`. Live preview uses `POST /api/units/{serial}/stream/start`+`stop` and the `/api/hls/` playlist |
| Device Mapping | `/device-mapping` | `GET/PUT/DELETE /api/mappings`, `GET /api/mappings/models`, `POST /api/mappings/copy`, and `GET/POST /api/event-codes` (the event-code picklist) |
| Device Settings | `/unit-settings` | `GET /api/unit-types/{unit}/settings/schema`, `GET/PUT /api/unit-types/{unit}/settings`, `GET/PUT /api/unit-types/{unit}/ports`, `PUT /api/unit-types/{unit}/capabilities` |
| Server Settings | `/server-settings` | **Gateway identity / device port / device authorization:** `GET/PUT /api/settings` (`gateway_name` & `device_reject_unknown` live, `device_port` restart-applied). **Webhooks:** `GET/POST /api/webhooks`, `PUT/DELETE /api/webhooks/{id}` (GPS/event sinks; multiple, each enable/disable) |
| Users | `/users` | `GET/POST /api/users`, `PUT/DELETE /api/users/{id}` (create accounts, enable/disable, reset password, delete) |
| API Keys | `/api-keys` | `GET/POST /api/api-keys`, `DELETE /api/api-keys/{prefix}` (generate external API keys — plaintext shown once — and revoke) |
| Logs | `/logs` | `GET /api/logs`, `GET /api/device-errors` |
| Live Logs | `/live-logs` | `GET /api/logs/live` (cursor-polled tail), `GET/PUT /api/logs/level` (capture level) |
| API Console | `/api-console` | A Postman-style developer tool that can send any endpoint above (proxied through the BFF so the API key stays server-side) |
| Docs | `/docs` | Static integration guides and the in-admin HTTP API reference |

Device approval only has pending entries when the gateway runs with
`DEVICE_REJECT_UNKNOWN=true` (otherwise unknown serials are auto-admitted).
Mapping edits apply to the running gateway within milliseconds via Postgres
LISTEN/NOTIFY — no redeploy.

### Event mapping

**Device Mapping** edits how raw device codes become ACM event codes via a
`code → event_code` lookup table. Mappings are per-unit by default, with optional
per-**model** overrides (a device uses its model's rows if present, else the
unit-wide defaults); `GET /api/mappings/models` lists models that have overrides
and `POST /api/mappings/copy` clones one model's rows onto another. The
`GET/POST /api/event-codes` picklist supplies (and extends) the available event
names. Edits apply to the running gateway within milliseconds via NOTIFY.

### Device detail & config editor

Clicking a device opens its detail page with two tabs:

- **Status** — a live read-out of the unit (`GET …/status`): connection/server,
  mobile network/4G signal, module health, SD storage, vehicle IO, GPS/location.
- **Config** — a full settings editor (`GET/PUT …/config`) grouped into the
  device's own menu categories (Network, Time, Power, Recording, Alarms, PTZ,
  System). Common segments (Wi-Fi, mobile, server, clock, power, …) get curated
  friendly labels and controls; the technical long-tail renders generically from
  the device's own field names. **Save writes only the fields you changed** — the
  firmware returns garbage in untouched string fields, so the editor deep-diffs
  and sends just the changed leaves (never a read-modify-write). Risky segments
  (Server, Power) confirm before saving; firmware/identity fields are read-only.
  The UI metadata lives in `admin/lib/howenConfig.ts`; add labels/enums there
  without touching the gateway. The unit must be **awake** to read/write — a
  standby device shows a **Wake** button.

## Configuration

Server-side env vars (see `admin/.env.example`):

| Var | Required | Description |
|-----|----------|-------------|
| `GATEWAY_URL` | yes | Base URL of the gateway HTTP API (e.g. `http://gateway:8080`). |
| `GATEWAY_API_TOKEN` | yes | Shared internal service token (same value as the gateway's `INTERNAL_API_TOKEN`) the BFF uses for every gateway call — this works before any DB-minted key exists, enabling first-run setup. A `dgw_…` key in `GATEWAY_API_KEY` is also accepted as a fallback if this is unset. |
| `SESSION_SECRET` | yes | ≥16-char secret signing session cookies. `openssl rand -base64 32`. |
| `SESSION_TTL_HOURS` | no | Session lifetime in hours (default 12). |

## Running

Prerequisites: a gateway with the HTTP API enabled (`HTTP_PORT`), at least one
admin user, and an API key. The user/key CLIs connect via `DATABASE_URL` (see
[configuration.md](configuration.md#management-clis)):

```sh
export DATABASE_URL=postgres://gateway:gateway@localhost:5432/gateway?sslmode=disable

# Create an admin user (password prompted) and an API key for the panel.
make adduser EMAIL=admin@example.com
make apikey ARGS='create --name admin-panel'   # prints the key ONCE

# Put the printed key + a random SESSION_SECRET in deploy/.env:
#   GATEWAY_API_TOKEN=dgw_...   (or the shared INTERNAL_API_TOKEN value)
#   SESSION_SECRET=$(openssl rand -base64 32)

docker compose -f deploy/docker-compose.yml up -d --build admin
# Panel on http://localhost:3000
```

Local development:

```sh
cd admin
cp .env.example .env.local   # fill in GATEWAY_URL, GATEWAY_API_TOKEN, SESSION_SECRET
npm install
npm run dev                  # http://localhost:3000
```

## Production notes

- Put the panel behind TLS (a reverse proxy); session cookies are marked
  `Secure` when `NODE_ENV=production`.
- The API key grants full control of the gateway API — treat the panel's env as
  a secret store and rotate the key with `cmd/apikey` if exposed.
- Sessions are stateless JWTs; rotating `SESSION_SECRET` invalidates all logins.
