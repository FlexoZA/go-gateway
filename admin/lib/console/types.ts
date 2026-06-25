/**
 * API Console — types for the Postman-like gateway console.
 *
 * Persisted shapes (collections, history, environments) live in localStorage;
 * bump the version suffix on the STORAGE_KEYS in ./storage on a breaking change.
 *
 * Adapted from the dfm.cwe.cloud dev-tools console, with two deliberate
 * differences for the device-gateway BFF:
 *   - No auth modes. Requests are authorized server-side by the gateway service
 *     key (see admin/app/api/console/route.ts). A custom `Authorization` header
 *     may still be added under Headers to override it.
 *   - No baseUrl. The request `path` is an absolute gateway path
 *     (e.g. "/api/units/{{serial}}/status"); environments hold variables only.
 */

export type HttpMethod = "GET" | "POST" | "PUT" | "PATCH" | "DELETE";

export interface KeyValueEntry {
  id: string;
  enabled: boolean;
  key: string;
  value: string;
}

export type BodyMode = "none" | "json" | "raw" | "form";

export interface RequestBody {
  mode: BodyMode;
  /** raw text — JSON when mode === "json", arbitrary string when "raw"/"form". */
  text: string;
}

/**
 * How a path placeholder gets a value.
 * - `list`: fetched from a gateway listing endpoint and picked by the user.
 * - `enum`: a fixed set of documented strings.
 * - `free`: free-text input.
 */
export type PathParamSource = "list" | "enum" | "free";

/** One binding per `{name}` placeholder in a request path. */
export interface PathParamBinding {
  /** Placeholder name (without braces). Matches `{name}` in the path. */
  name: string;
  source: PathParamSource;

  // For source === "list":
  /** Gateway path to fetch options from, e.g. "/api/units". */
  listEndpoint?: string;
  /** Property on the JSON response holding the array (e.g. "units", "clips"). */
  listArrayField?: string;
  /** Field on each item to display as the label. Default: the value itself. */
  listLabelField?: string;
  /** Optional secondary label rendered next to the primary one. */
  listSubLabelField?: string;
  /** Field on each item to use as the value. Default: "id". */
  listValueField?: string;

  // For source === "enum":
  enumValues?: string[];

  // Current selection (substituted into the path at send time):
  value?: string;
  /** Display label of the current selection — preserved across reloads. */
  displayLabel?: string;
}

export interface ConsoleRequest {
  id: string;
  name: string;
  method: HttpMethod;
  /** Absolute gateway path. May contain `{{vars}}` and `{pathParams}`. */
  path: string;
  params: KeyValueEntry[];
  headers: KeyValueEntry[];
  body: RequestBody;
  /** Short one-line summary, shown inline in the editor. */
  description?: string;
  /** Free-form user-editable notes — multi-line. */
  notes?: string;
  /** One binding per `{name}` placeholder in the path — resolved at send time. */
  pathParams?: PathParamBinding[];
}

export interface Environment {
  id: string;
  name: string;
  variables: KeyValueEntry[];
}

export interface Collection {
  id: string;
  name: string;
  description?: string;
  requests: ConsoleRequest[];
  createdAt: number;
  updatedAt: number;
}

export interface ConsoleResponse {
  status: number;
  statusText: string;
  ok: boolean;
  durationMs: number;
  sizeBytes: number;
  headers: Array<[string, string]>;
  /** Parsed JSON, or null if the body wasn't JSON. */
  json: unknown | null;
  /** Raw response body as text. */
  rawText: string;
  /** Set if the request failed before/at the proxy (unreachable, bad spec). */
  networkError?: string;
  /** ISO timestamp the request started. */
  startedAt: string;
}

export interface HistoryEntry {
  id: string;
  request: ConsoleRequest;
  /** Resolved path after variable + path-param substitution. */
  resolvedPath: string;
  environmentId: string | null;
  response: ConsoleResponse;
  /** Convenience copies for the history list. */
  status: number;
  durationMs: number;
  timestamp: number;
}
