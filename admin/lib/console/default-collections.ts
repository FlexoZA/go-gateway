/**
 * Built-in collections for the API Console.
 *
 * Hand-derived from the gateway's protected route table in
 *   internal/core/httpapi/httpapi.go (the `protected` map).
 * Grouped by resource. Each endpoint carries a one-line description, an optional
 * default JSON body for writes, optional default query params, and path-param
 * bindings (list | enum | free) wired to gateway listing endpoints where the
 * response shape is known.
 *
 * Built-in ids are deterministic: `col_builtin_<group>` for collections and
 * `col_builtin_<group>_r<idx>` for requests, so "Restore defaults" can merge in
 * newly added endpoints without duplicating user data.
 */

import type {
  BodyMode,
  Collection,
  ConsoleRequest,
  HttpMethod,
  PathParamBinding,
} from "./types";

interface RawEndpoint {
  name: string;
  method: HttpMethod;
  path: string;
  description?: string;
  pathParams?: PathParamBinding[];
  params?: Array<{ key: string; value: string }>;
  body?: string | null;
}

interface RawGroup {
  /** Stable slug used in deterministic ids. */
  slug: string;
  name: string;
  description: string;
  endpoints: RawEndpoint[];
}

// --- Reusable path-param bindings -------------------------------------------

const serialParam: PathParamBinding = {
  name: "serial",
  source: "list",
  listEndpoint: "/api/units",
  listArrayField: "units",
  listValueField: "serial",
  listLabelField: "serial",
  listSubLabelField: "model",
};

const unitParam: PathParamBinding = {
  name: "unit",
  source: "list",
  listEndpoint: "/api/gateway/info",
  listArrayField: "units",
  listValueField: "unit",
  listLabelField: "unit",
};

const clipIdParam: PathParamBinding = {
  name: "id",
  source: "list",
  listEndpoint: "/api/clips",
  listArrayField: "clips",
  listValueField: "id",
  listLabelField: "id",
};

const userIdParam: PathParamBinding = {
  name: "id",
  source: "list",
  listEndpoint: "/api/users",
  listArrayField: "users",
  listValueField: "id",
  listLabelField: "email",
};

const apiKeyPrefixParam: PathParamBinding = {
  name: "prefix",
  source: "list",
  listEndpoint: "/api/api-keys",
  listArrayField: "api_keys",
  listValueField: "prefix",
  listLabelField: "name",
};

const webhookIdParam: PathParamBinding = {
  name: "id",
  source: "list",
  listEndpoint: "/api/webhooks",
  listArrayField: "webhooks",
  listValueField: "id",
  listLabelField: "url",
};

const page = [
  { key: "limit", value: "" },
  { key: "offset", value: "" },
];

const RAW_GROUPS: RawGroup[] = [
  {
    slug: "health",
    name: "Health & Info",
    description: "Liveness and this gateway's unit types + effective capabilities.",
    endpoints: [
      { name: "Ping", method: "GET", path: "/api/ping", description: "Simple { ok: true } liveness check." },
      {
        name: "Gateway info",
        method: "GET",
        path: "/api/gateway/info",
        description: "Hosted unit types and their effective/supported capabilities (drives the admin UI).",
      },
    ],
  },
  {
    slug: "auth",
    name: "Auth & Setup",
    description: "Credential verification and first-run setup.",
    endpoints: [
      {
        name: "Login (verify credentials)",
        method: "POST",
        path: "/api/auth/login",
        description: "Verify a front-end user's email/password (the admin BFF normally calls this).",
        body: '{\n  "email": "admin@example.com",\n  "password": "secret"\n}',
      },
      { name: "Setup status", method: "GET", path: "/api/setup/status", description: "Whether first-run setup is still needed (zero users)." },
      {
        name: "Run setup",
        method: "POST",
        path: "/api/setup",
        description: "Bootstrap: create the first user and core settings. Only effective while there are zero users.",
        body: '{\n  "email": "admin@example.com",\n  "password": "secret",\n  "gateway_name": "Gateway"\n}',
      },
    ],
  },
  {
    slug: "users",
    name: "Users",
    description: "Admin user accounts.",
    endpoints: [
      { name: "List users", method: "GET", path: "/api/users", description: "All admin users." },
      {
        name: "Create user",
        method: "POST",
        path: "/api/users",
        description: "Create an admin account.",
        body: '{\n  "email": "user@example.com",\n  "password": "secret"\n}',
      },
      {
        name: "Update user",
        method: "PUT",
        path: "/api/users/{id}",
        description: "Update a user (password, active status).",
        pathParams: [userIdParam],
        body: '{\n  "active": true\n}',
      },
      {
        name: "Delete user",
        method: "DELETE",
        path: "/api/users/{id}",
        description: "Delete a user account.",
        pathParams: [userIdParam],
      },
    ],
  },
  {
    slug: "apikeys",
    name: "API Keys",
    description: "Bearer keys for external API access.",
    endpoints: [
      { name: "List API keys", method: "GET", path: "/api/api-keys", description: "Active keys (prefix, name, status, last used, expiry)." },
      {
        name: "Create API key",
        method: "POST",
        path: "/api/api-keys",
        description: "Mint a key. The plaintext is returned ONCE — save it immediately.",
        body: '{\n  "name": "my-integration"\n}',
      },
      {
        name: "Revoke API key",
        method: "DELETE",
        path: "/api/api-keys/{prefix}",
        description: "Revoke a key by its prefix.",
        pathParams: [apiKeyPrefixParam],
      },
    ],
  },
  {
    slug: "units",
    name: "Connected Units",
    description: "Live devices via the hub: info, status, config, commands.",
    endpoints: [
      { name: "List units", method: "GET", path: "/api/units", description: "Currently connected devices." },
      { name: "Get unit", method: "GET", path: "/api/units/{serial}", description: "One connected device's info.", pathParams: [serialParam] },
      { name: "Unit status", method: "GET", path: "/api/units/{serial}/status", description: "Live status: connection, network, modules, storage, IO, GPS.", pathParams: [serialParam] },
      { name: "Get config", method: "GET", path: "/api/units/{serial}/config", description: "Read device parameter config.", pathParams: [serialParam] },
      {
        name: "Update config",
        method: "PUT",
        path: "/api/units/{serial}/config",
        description: "Update device parameter config.",
        pathParams: [serialParam],
        body: "{\n  \n}",
      },
      {
        name: "Send command",
        method: "POST",
        path: "/api/units/{serial}/commands",
        description: "Send a control command (e.g. reboot) to the device.",
        pathParams: [serialParam],
        body: '{\n  "type": "reboot",\n  "payload": {}\n}',
      },
    ],
  },
  {
    slug: "video",
    name: "Live Video",
    description: "Start/stop HLS streams and fetch segments.",
    endpoints: [
      {
        name: "Start stream",
        method: "POST",
        path: "/api/units/{serial}/stream/start",
        description: "Begin a live video stream for a channel.",
        pathParams: [serialParam],
        body: '{\n  "channel": 1\n}',
      },
      {
        name: "Stop stream",
        method: "POST",
        path: "/api/units/{serial}/stream/stop",
        description: "Stop a live video stream.",
        pathParams: [serialParam],
        body: '{\n  "channel": 1\n}',
      },
      {
        name: "HLS playlist/segment",
        method: "GET",
        path: "/api/hls/",
        description: "Serve HLS .m3u8/.ts (append the file path; binary segments won't render as JSON).",
      },
    ],
  },
  {
    slug: "clips",
    name: "Recordings & Clips",
    description: "Query device footage and request/download recorded clips.",
    endpoints: [
      {
        name: "Query recordings",
        method: "GET",
        path: "/api/units/{serial}/recordings",
        description: "Discover what footage a device has (file query) before requesting a clip.",
        pathParams: [serialParam],
        params: [
          { key: "start", value: "" },
          { key: "end", value: "" },
          { key: "channel", value: "" },
        ],
      },
      {
        name: "Request clip",
        method: "POST",
        path: "/api/units/{serial}/clips",
        description: "Request a clip download (then poll status / download the .mp4).",
        pathParams: [serialParam],
        body: '{\n  "channel": 1,\n  "start": "2026-01-01T00:00:00Z",\n  "end": "2026-01-01T00:01:00Z"\n}',
      },
      {
        name: "List clips",
        method: "GET",
        path: "/api/clips",
        description: "Clip requests and their status.",
        params: [{ key: "serial", value: "" }, ...page],
      },
      { name: "Get clip", method: "GET", path: "/api/clips/{id}", description: "One clip's metadata/status.", pathParams: [clipIdParam] },
      { name: "Download clip", method: "GET", path: "/api/clips/{id}/download", description: "Stream the stored .mp4 (binary — won't render as JSON).", pathParams: [clipIdParam] },
      { name: "Delete clip", method: "DELETE", path: "/api/clips/{id}", description: "Delete a clip.", pathParams: [clipIdParam] },
    ],
  },
  {
    slug: "devices",
    name: "Device Registry",
    description: "Approve, reject, and remove devices.",
    endpoints: [
      { name: "List devices", method: "GET", path: "/api/devices", description: "Approved devices." },
      { name: "List pending", method: "GET", path: "/api/devices/pending", description: "Devices awaiting approval." },
      {
        name: "Approve device",
        method: "POST",
        path: "/api/devices/{serial}/approve",
        description: "Whitelist a pending serial.",
        pathParams: [{ name: "serial", source: "list", listEndpoint: "/api/devices/pending", listArrayField: "devices", listValueField: "serial", listLabelField: "serial" }],
      },
      {
        name: "Reject device",
        method: "POST",
        path: "/api/devices/{serial}/reject",
        description: "Reject a pending serial.",
        pathParams: [{ name: "serial", source: "list", listEndpoint: "/api/devices/pending", listArrayField: "devices", listValueField: "serial", listLabelField: "serial" }],
      },
      {
        name: "Delete device",
        method: "DELETE",
        path: "/api/devices/{serial}",
        description: "Remove a device from the registry.",
        pathParams: [{ name: "serial", source: "list", listEndpoint: "/api/devices", listArrayField: "devices", listValueField: "serial", listLabelField: "serial" }],
      },
    ],
  },
  {
    slug: "mappings",
    name: "Event Mappings",
    description: "Editable code→event mappings (applied instantly).",
    endpoints: [
      {
        name: "List mappings",
        method: "GET",
        path: "/api/mappings",
        description: "Mappings for a unit/model.",
        params: [{ key: "unit", value: "" }, { key: "model", value: "" }],
      },
      {
        name: "Upsert mapping",
        method: "PUT",
        path: "/api/mappings",
        description: "Create or update a mapping.",
        body: '{\n  "unit": "",\n  "model": "",\n  "code": "",\n  "event": ""\n}',
      },
      {
        name: "Delete mapping",
        method: "DELETE",
        path: "/api/mappings",
        description: "Delete a mapping (identified in the body).",
        body: '{\n  "unit": "",\n  "model": "",\n  "code": ""\n}',
      },
      { name: "List mapping models", method: "GET", path: "/api/mappings/models", description: "Models that have mappings." },
      {
        name: "Copy mappings",
        method: "POST",
        path: "/api/mappings/copy",
        description: "Copy mappings from one model to another.",
        body: '{\n  "from_model": "",\n  "to_model": ""\n}',
      },
    ],
  },
  {
    slug: "settings",
    name: "Server Settings",
    description: "Global gateway settings.",
    endpoints: [
      { name: "List settings", method: "GET", path: "/api/settings", description: "All global settings." },
      {
        name: "Set setting",
        method: "PUT",
        path: "/api/settings",
        description: "Set a global setting (gateway_name, webhook_url, device_port, device_reject_unknown).",
        body: '{\n  "key": "gateway_name",\n  "value": "My Gateway"\n}',
      },
    ],
  },
  {
    slug: "unittypes",
    name: "Unit-Type Settings",
    description: "Per-unit-type settings, ports, and capability toggles.",
    endpoints: [
      { name: "Settings schema", method: "GET", path: "/api/unit-types/{unit}/settings/schema", description: "The unit's declared settings schema.", pathParams: [unitParam] },
      { name: "List unit settings", method: "GET", path: "/api/unit-types/{unit}/settings", description: "Current values for a unit's settings.", pathParams: [unitParam] },
      {
        name: "Set unit setting",
        method: "PUT",
        path: "/api/unit-types/{unit}/settings",
        description: "Update one of a unit's settings.",
        pathParams: [unitParam],
        body: '{\n  "key": "",\n  "value": ""\n}',
      },
      { name: "Get ports", method: "GET", path: "/api/unit-types/{unit}/ports", description: "Device + media ports and whether each is active.", pathParams: [unitParam] },
      {
        name: "Set ports",
        method: "PUT",
        path: "/api/unit-types/{unit}/ports",
        description: "Set device/media ports (applied on restart).",
        pathParams: [unitParam],
        body: '{\n  "device_port": 0\n}',
      },
      {
        name: "Set capabilities",
        method: "PUT",
        path: "/api/unit-types/{unit}/capabilities",
        description: "Disable/enable a supported feature (e.g. { \"video\": false }).",
        pathParams: [unitParam],
        body: '{\n  "video": false\n}',
      },
    ],
  },
  {
    slug: "webhooks",
    name: "Webhooks",
    description: "Telemetry sinks (GPS/event data).",
    endpoints: [
      { name: "List webhooks", method: "GET", path: "/api/webhooks", description: "Webhook targets." },
      {
        name: "Create webhook",
        method: "POST",
        path: "/api/webhooks",
        description: "Add a webhook target.",
        body: '{\n  "url": "https://example.com/hook"\n}',
      },
      {
        name: "Update webhook",
        method: "PUT",
        path: "/api/webhooks/{id}",
        description: "Update a webhook target.",
        pathParams: [webhookIdParam],
        body: '{\n  "url": "https://example.com/hook"\n}',
      },
      { name: "Delete webhook", method: "DELETE", path: "/api/webhooks/{id}", description: "Delete a webhook target.", pathParams: [webhookIdParam] },
    ],
  },
  {
    slug: "reference",
    name: "Reference Data",
    description: "Standard + custom event codes.",
    endpoints: [
      { name: "List event codes", method: "GET", path: "/api/event-codes", description: "Standard ACM event-code picklist." },
      {
        name: "Add event code",
        method: "POST",
        path: "/api/event-codes",
        description: "Add a custom event code.",
        body: '{\n  "code": "",\n  "label": ""\n}',
      },
    ],
  },
  {
    slug: "logs",
    name: "Logs",
    description: "Error logs and the live activity tail.",
    endpoints: [
      { name: "Gateway errors", method: "GET", path: "/api/logs", description: "Paginated gateway error logs.", params: page },
      { name: "Device errors", method: "GET", path: "/api/device-errors", description: "Paginated device-reported errors.", params: page },
      {
        name: "Live logs",
        method: "GET",
        path: "/api/logs/live",
        description: "Cursor-based tail of the gateway's in-memory log stream.",
        params: [{ key: "after", value: "" }, { key: "level", value: "" }, { key: "limit", value: "" }],
      },
      { name: "Get log level", method: "GET", path: "/api/logs/level", description: "Current capture level." },
      {
        name: "Set log level",
        method: "PUT",
        path: "/api/logs/level",
        description: "Set the capture level (error/info/debug).",
        body: '{\n  "level": "info"\n}',
      },
    ],
  },
];

function bodyMode(body: string | null | undefined): BodyMode {
  return body ? "json" : "none";
}

export function buildBuiltInCollections(): Collection[] {
  const now = 0; // deterministic — timestamps for built-ins don't matter
  return RAW_GROUPS.map((group) => {
    const requests: ConsoleRequest[] = group.endpoints.map((ep, idx) => ({
      id: `col_builtin_${group.slug}_r${idx}`,
      name: ep.name,
      method: ep.method,
      path: ep.path,
      description: ep.description,
      params: (ep.params ?? []).map((p, pIdx) => ({
        id: `col_builtin_${group.slug}_r${idx}_p${pIdx}`,
        enabled: false,
        key: p.key,
        value: p.value,
      })),
      headers: [],
      body: { mode: bodyMode(ep.body), text: ep.body ?? "" },
      pathParams: ep.pathParams ? ep.pathParams.map((b) => ({ ...b })) : undefined,
    }));
    return {
      id: `col_builtin_${group.slug}`,
      name: group.name,
      description: group.description,
      requests,
      createdAt: now,
      updatedAt: now,
    };
  });
}
