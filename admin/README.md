# Device Gateway — Admin panel

Next.js (App Router) + Tailwind admin UI for the device gateway. It approves
devices, edits event-mapping settings, and shows logs — all through the gateway
HTTP API. It has **no direct database access**.

See [`../docs/admin-panel.md`](../docs/admin-panel.md) for the full reference
(security model, pages, configuration, deployment).

## Quick start (local)

```sh
cp .env.example .env.local   # set GATEWAY_URL, GATEWAY_API_KEY, SESSION_SECRET
npm install
npm run dev                  # http://localhost:3000
```

You need a running gateway with the HTTP API enabled, an admin user
(`cmd/adduser`), and an API key (`cmd/apikey`).

## Layout

```
app/
  (auth)/login        sign-in page (minimal layout)
  (dash)/             dashboard, devices, settings, logs (sidebar layout)
  api/login           verify credentials → issue session cookie
  api/logout          clear session cookie
  api/gw/[...path]    BFF proxy → gateway API with server-held API key
lib/
  session.ts          JWT session sign/verify (jose)
  gateway.ts          server-only gateway fetch (attaches API key)
  api.ts / useFetch   client fetch helpers (call /api/gw/*)
middleware.ts         enforces the session cookie on every route
```
