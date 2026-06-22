# Admin panel

A Next.js + Tailwind web app (`admin/`) for operating the gateway: approve
connecting devices, edit server settings (event mappings), and view logs. It
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
| Dashboard | `/` | `GET /api/units`, `GET /api/devices`, `GET /api/devices/pending` |
| Devices | `/devices` | `GET /api/devices`, `GET /api/devices/pending`, `POST …/approve`, `POST …/reject`, `DELETE /api/devices/{serial}` |
| Device Mapping | `/device-mapping` | **Code table:** `GET/PUT/DELETE /api/mappings` (+ `GET /api/event-codes` picklist). **Visual workflows:** `GET/PUT/DELETE /api/workflows[/{model}]`, `POST /api/workflows/test` |
| Server Settings | `/server-settings` | **Gateway identity / device port / device authorization:** `GET/PUT /api/settings` (`gateway_name` & `device_reject_unknown` live, `device_port` restart-applied). **Webhooks:** `GET/POST /api/webhooks`, `PUT/DELETE /api/webhooks/{id}` (GPS/event sinks; multiple, each enable/disable) |
| Users | `/users` | `GET/POST /api/users`, `PUT/DELETE /api/users/{id}` (create accounts, enable/disable, reset password, delete) |
| API Keys | `/api-keys` | `GET/POST /api/api-keys`, `DELETE /api/api-keys/{prefix}` (generate external API keys — plaintext shown once — and revoke) |
| Logs | `/logs` | `GET /api/logs`, `GET /api/device-errors` |

Device approval only has pending entries when the gateway runs with
`DEVICE_REJECT_UNKNOWN=true` (otherwise unknown serials are auto-admitted).
Mapping edits apply to the running gateway within milliseconds via Postgres
LISTEN/NOTIFY — no redeploy.

### Two mapping methods

**Device Mapping** has two tabs, both editing how raw device codes become ACM
event codes:

- **Code table** — a flat, per-unit `code → event_code` lookup (applies to all
  models). Simple and fast.
- **Visual workflows** — a per-**model** node graph ("N8N-style") edited on a
  React Flow canvas: `input → switch/condition → setEvent/setField → output`.
  Strictly per model; a model with no workflow falls back to the code table. The
  canvas has a live **Test run** panel that dry-runs the graph against a sample
  payload before saving. See [http-api.md](http-api.md#per-model-mapping-workflows)
  for the graph/node schema.

## Configuration

Server-side env vars (see `admin/.env.example`):

| Var | Required | Description |
|-----|----------|-------------|
| `GATEWAY_URL` | yes | Base URL of the gateway HTTP API (e.g. `http://howen:8080`). |
| `GATEWAY_API_KEY` | yes | API key (`dgw_…`) the BFF uses for every gateway call. |
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
#   GATEWAY_API_KEY=dgw_...
#   SESSION_SECRET=$(openssl rand -base64 32)

docker compose -f deploy/docker-compose.yml up -d --build admin
# Panel on http://localhost:3000
```

Local development:

```sh
cd admin
cp .env.example .env.local   # fill in GATEWAY_URL, GATEWAY_API_KEY, SESSION_SECRET
npm install
npm run dev                  # http://localhost:3000
```

## Production notes

- Put the panel behind TLS (a reverse proxy); session cookies are marked
  `Secure` when `NODE_ENV=production`.
- The API key grants full control of the gateway API — treat the panel's env as
  a secret store and rotate the key with `cmd/apikey` if exposed.
- Sessions are stateless JWTs; rotating `SESSION_SECRET` invalidates all logins.
