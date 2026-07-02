import Link from "next/link";
import { Callout, CodeBlock, Endpoint } from "@/components/docs/doc-kit";

export const metadata = { title: "Howen integration · Docs" };

export default function HowenDocsPage() {
  return (
    <article className="doc-prose">
      <h1 className="text-2xl font-semibold text-white">Howen integration guide</h1>
      <p>
        This guide is for systems that <strong>read</strong> data from Howen devices through the
        gateway&rsquo;s HTTP API: live GPS &amp; events, device status, live video, and recorded
        clips. It does not cover changing device configuration or sending control commands.
      </p>

      {/* ---------------------------------------------------------------- */}
      <h2 id="overview">Overview</h2>
      <p>
        A Howen device is a vehicle telematics unit that reports GPS and events/alarms, exposes live
        camera streams, and stores recorded video on an SD card that can be pulled on demand.
      </p>
      <p>There are two separate planes — you only ever touch the second one:</p>
      <ul>
        <li>
          <strong>Device plane (TCP).</strong> Devices connect to the gateway over TCP (control on
          port <code>33000</code>, media on <code>33001</code>) and authenticate by serial. You
          never speak this protocol.
        </li>
        <li>
          <strong>Integration plane (HTTP API).</strong> Your system calls the gateway&rsquo;s HTTP
          API (default port <code>8080</code>, served behind TLS in production) with a Bearer API
          key. Everything below is on this plane.
        </li>
      </ul>
      <p>
        Live GPS and events are <strong>pushed</strong> to you via webhooks; status, video, and
        clips are <strong>pulled</strong> on request.
      </p>
      <Callout tone="info" title="Device models">
        The universal message&rsquo;s <code>model</code> is the device&rsquo;s own firmware model
        identifier — e.g. <code>MC30-02H</code> or <code>ME40-02</code>. A trailing firmware-version
        token (the <code>V8</code> in <code>ME40-02V8</code>) is stripped so a model keeps a stable
        identity — and its event-mapping table — across firmware bumps. Both the MC30-02 and the
        newer ME40-02 speak the same Howen H-Protocol, so a new model onboards with no code change;
        its per-model event mappings live alongside the others on the{" "}
        <Link href="/device-mapping">Device Mapping</Link> page.
      </Callout>

      {/* ---------------------------------------------------------------- */}
      <h2 id="auth">Authentication</h2>
      <p>
        Every <code>/api/</code> route requires an API key in the <code>Authorization</code> header.
        Create one on the <Link href="/api-keys">API Keys</Link> page — the full key
        (<code>dgw_…</code>) is shown only once, so store it immediately.
      </p>
      <CodeBlock label="Header">{`Authorization: Bearer dgw_<your-key>`}</CodeBlock>
      <p>
        A missing, malformed, unknown, revoked, or expired key returns <code>401</code>; if the key
        store (database) isn&rsquo;t configured the API returns <code>503</code>. You can try any
        endpoint interactively from the <Link href="/api-console">API Console</Link>.
      </p>

      {/* ---------------------------------------------------------------- */}
      <h2 id="discovery">Discovering devices</h2>
      <p>List the devices currently connected to the gateway:</p>
      <Endpoint method="GET" path="/api/units">
        Currently-connected devices.
      </Endpoint>
      <CodeBlock label="200 OK">{`{
  "units": [
    {
      "serial": "864312087845313",
      "protocol": "howen",
      "model": "MC30-02H",
      "remote_addr": "102.135.1.20:42534",
      "connected_at": "2026-06-20T16:14:51.703Z",
      "commands": ["clear_alarm", "reboot_unit", "..."]
    }
  ]
}`}</CodeBlock>
      <Endpoint method="GET" path="/api/units/{serial}">
        One connected device (same shape as a <code>units[]</code> element); <code>404</code> if not
        connected.
      </Endpoint>
      <Callout tone="info" title="Registry &amp; approval (one-time admin setup)">
        New serials may be quarantined until approved. An admin reviews{" "}
        <code>GET /api/devices/pending</code> and approves with{" "}
        <code>POST /api/devices/&#123;serial&#125;/approve</code> (or manages them on the{" "}
        <Link href="/devices">Devices</Link> page). Once approved a device connects automatically —
        this isn&rsquo;t part of your day-to-day read loop. <code>GET /api/devices</code> lists the
        approved registry.
      </Callout>

      {/* ---------------------------------------------------------------- */}
      <h2 id="telemetry">Live telemetry — GPS &amp; events</h2>
      <p>
        The gateway POSTs every GPS and event message to <strong>all enabled webhooks</strong>{" "}
        (fire-and-forget). Register your sink:
      </p>
      <Endpoint method="GET" path="/api/webhooks">
        List configured sinks.
      </Endpoint>
      <Endpoint method="POST" path="/api/webhooks">
        Add a sink. Body: <code>{`{ "name": "...", "url": "https://…", "is_enabled": true }`}</code>.
        The URL must be http(s); re-posting an existing URL updates it.
      </Endpoint>
      <p>
        Each delivery is one <strong>Universal JSON</strong> message. <code>message_type</code> is{" "}
        <code>"gps"</code> for position reports and <code>"event"</code> for alarms/events. The
        envelope is identical for both; an event message additionally populates the{" "}
        <code>events</code> array. Fields the device didn&rsquo;t report are <code>null</code> (or
        empty arrays).
      </p>
      <CodeBlock label="Webhook POST body — message_type: gps (trimmed)">{`{
  "message_ver": 1,
  "message_type": "gps",
  "gateway": "dfm-gw1.cwe.cloud",
  "transmission": "tcp",
  "timestamp": "2026-06-20T16:14:51+00:00",
  "source": "device",
  "seq_no": 1,
  "valid": true,
  "device": {
    "identifier": "serial_no",
    "imei": "864312087845313",
    "serial_no": "864312087845313",
    "type": "howen",
    "model": "MC30-02H"
  },
  "network": { "remote_ipv4": "102.135.1.20", "remote_port": 42534 },
  "gsm": [ { "signal_lvl": 8, "signal_str": 80, "data_mode": "4",
             "status": ["howen_radio:4g", "data", "network"] } ],
  "gps": {
    "timestamp": "2026-06-20T16:14:51+00:00",
    "latitude": -26.2041, "longitude": 28.0473,
    "altitude": 1680, "speed": 65.2, "heading": 243,
    "satellites": 12, "hdop": 0.8, "gnss": true, "fix": ["fixed"]
  },
  "events": [],
  "sensors": [ ["ignition", "on"], ["disk_status", 1], ["accel_z", 9.81] ]
}`}</CodeBlock>
      <p>
        For an event message, <code>message_type</code> is <code>"event"</code> and{" "}
        <code>events</code> carries the decoded, already-mapped event names (with optional detail):
      </p>
      <CodeBlock label="events[] (message_type: event)">{`"events": [
  ["IGNITION:ON"],
  ["COLLISION"],
  ["AI:CELLPHONE", [["confidence", 0.95]]]
]`}</CodeBlock>
      <Callout tone="info">
        Raw Howen alarm codes are translated to standard event names by the gateway before delivery.
        The code&rarr;event tables are editable on the{" "}
        <Link href="/device-mapping">Device Mapping</Link> page, and the canonical event-name
        picklist is available at <code>GET /api/event-codes</code>.
      </Callout>
      <Callout tone="info" title="OBD / datahub telemetry">
        Vehicles with a CAN/OBD datahub emit a periodic status frame that the gateway forwards as a{" "}
        <code>message_type: "gps"</code> message (it is telemetry, not an alarm) — the OBD fields land
        in <code>sensors</code> (e.g. <code>engine_rpm</code>, <code>obd_speed</code>,{" "}
        <code>coolant_temp_c</code>, <code>fuel_level</code>, <code>obd_distance</code>) plus the
        device&rsquo;s <code>inputs</code>/<code>outputs</code> bit strings. The raw block is preserved
        under <code>howen_datahub</code>.
      </Callout>

      {/* ---------------------------------------------------------------- */}
      <h2 id="status">Device status</h2>
      <p>
        Pull a live snapshot of one device — connection info plus the latest telemetry it reported.
      </p>
      <Endpoint method="GET" path="/api/units/{serial}/status">
        Live status; <code>404</code> if not connected. <code>telemetry</code> is <code>null</code>{" "}
        until the device has sent a status frame.
      </Endpoint>
      <CodeBlock label="200 OK (shape)">{`{
  "serial": "864312087845313",
  "connection": {
    "serial": "864312087845313", "protocol": "howen", "model": "MC30-02H",
    "state": "online", "remote_addr": "102.135.1.20:42534",
    "connected_at": "2026-06-20T16:14:51.703Z"
  },
  "telemetry": {
    "network":  { "type": "4g", "signal_level": 8, "signal_pct": 80, "health": "normal" },
    "modules":  { "mobile": "normal", "gps": "normal", "wifi": "normal", "gsensor": "normal" },
    "storage":  [ { "id": 0, "status": "ok", "size_mb": 31457280, "free_mb": 15728640 } ],
    "location": { "latitude": -26.2041, "longitude": 28.0473, "speed_kmh": 65.2,
                  "satellites": 12, "positioned": true },
    "vehicle":  { "ignition": true, "brake": false },
    "sensors":  { "x": 0.1, "y": 0.05, "z": 9.81 }
  }
}`}</CodeBlock>

      {/* ---------------------------------------------------------------- */}
      <h2 id="video">Live video (HLS)</h2>
      <Callout tone="warn">
        Video requires the gateway to be started with <code>MEDIA_ADVERTISE_HOST</code> set;
        otherwise these routes return <code>503</code>. A device in standby returns{" "}
        <code>409 device_sleeping</code>.
      </Callout>
      <p>Start a stream, play the HLS playlist, then stop it when done.</p>
      <Endpoint method="POST" path="/api/units/{serial}/stream/start">
        Begin a live stream. Body: <code>{`{ "camera": 0, "profile": 0 }`}</code>.
      </Endpoint>
      <CodeBlock label="200 OK">{`{ "ok": true, "session_id": "…",
  "hls_path": "864312087845313/0/0/stream.m3u8", "ready": true }`}</CodeBlock>
      <p>
        <code>camera</code> is 0-based; <code>profile</code> is <code>0</code> for the main
        (high-res) stream or <code>1</code> for the sub (low-res) stream. The gateway waits for the
        first segment before responding, so the playlist is usually ready; <code>ready:false</code>{" "}
        just means it wasn&rsquo;t up within the window — keep retrying the playlist.
      </p>
      <Endpoint method="GET" path="/api/hls/{serial}/{camera}/{profile}/stream.m3u8">
        The HLS playlist (and <code>seg_NNN.ts</code> segments) for the started stream. API-key
        protected like every route — point an HLS player (e.g. <code>hls.js</code>) at it.
      </Endpoint>
      <Endpoint method="POST" path="/api/units/{serial}/stream/stop">
        Stop the stream. Same <code>{`{ "camera", "profile" }`}</code> body. Returns{" "}
        <code>{`{ "ok": true }`}</code>.
      </Endpoint>

      {/* ---------------------------------------------------------------- */}
      <h2 id="clips">Recorded clips</h2>
      <Callout tone="warn">
        Clips need video enabled (<code>MEDIA_ADVERTISE_HOST</code>), server storage
        (<code>CLIPS_ROOT</code>), and a database. An asleep device returns{" "}
        <code>409 device_sleeping</code>.
      </Callout>
      <p>The flow is: query what footage exists → request a clip → poll until ready → download.</p>

      <h3>1. Query available recordings</h3>
      <Endpoint method="GET" path="/api/units/{serial}/recordings?camera=&profile=&start_utc=&end_utc=">
        What footage the device holds for a window. Always query before requesting — playback is
        file-based and only matches existing recordings.
      </Endpoint>
      <p>
        Params: <code>camera</code> (0-based, <code>-1</code> = all; default <code>-1</code>),{" "}
        <code>profile</code> (default <code>1</code>), <code>start_utc</code>/<code>end_utc</code>{" "}
        (Unix seconds, true UTC; default = last 24h).
      </p>
      <CodeBlock label="200 OK">{`{ "recordings": [
  { "camera": 0, "profile": 0, "start_utc": 1750000000, "end_utc": 1750000300,
    "file_name": "…", "size": 31457280, "device_start": "…", "device_end": "…" }
], "count": 1 }`}</CodeBlock>

      <h3>2. Request a clip</h3>
      <Endpoint method="POST" path="/api/units/{serial}/clips">
        Request a clip upload. The <code>.mp4</code> arrives asynchronously; poll the returned{" "}
        <code>clip_id</code>.
      </Endpoint>
      <CodeBlock label="Request / 200">{`// request — times are true-UTC Unix seconds
{ "camera": 0, "profile": 0, "start_utc": 1750000000, "end_utc": 1750000020, "audio": false }
// 200
{ "ok": true, "clip_id": 11, "session_id": "…", "status": "requested" }`}</CodeBlock>

      <h3>3. Poll status</h3>
      <Endpoint method="GET" path="/api/clips/{id}">
        One clip&rsquo;s metadata/status. (List all with{" "}
        <code>GET /api/clips?serial=&amp;limit=&amp;offset=</code>.)
      </Endpoint>
      <CodeBlock label="200 OK">{`{ "id": 11, "serial": "864312087845313", "camera": 0, "profile": 0,
  "start_utc": 1750000000, "end_utc": 1750000020, "duration_secs": 20,
  "status": "ready", "file_size": 16800000, "bytes_received": 16800000,
  "storage_path": "864312087845313/11.mp4", "error": "",
  "created_at": "…", "updated_at": "…" }`}</CodeBlock>
      <p>
        <code>status</code> moves <code>requested</code> → <code>receiving</code> →{" "}
        <code>ready</code> | <code>error</code>. Poll until it&rsquo;s <code>ready</code> or{" "}
        <code>error</code>.
      </p>

      <h3>4. Download</h3>
      <Endpoint method="GET" path="/api/clips/{id}/download">
        Stream the stored <code>.mp4</code> (<code>Content-Type: video/mp4</code>, attachment).{" "}
        <code>409</code> if the clip isn&rsquo;t <code>ready</code>; <code>404</code> if the file is
        missing.
      </Endpoint>

      <h3 id="trimmed">Downloading a trimmed clip</h3>
      <p>
        A clip is <strong>already trimmed</strong> — its length is whatever window you request, not
        a fixed recording chunk. To get a precise cut, set <code>start_utc</code> and{" "}
        <code>end_utc</code> on the clip request to exactly the segment you want: the device streams
        only that window and the gateway remuxes it into a single <code>.mp4</code> (no re-encode).
        There is no separate trim step and nothing to trim on the download call itself — the request
        window <em>is</em> the trim.
      </p>
      <p>
        The window can be any sub-range inside available footage; it doesn&rsquo;t have to line up
        with recording-file boundaries. For example, to pull a 15-second cut:
      </p>
      <CodeBlock label="Request a 15s trimmed clip">{`// 1) confirm footage exists for the window (see step 1)
GET /api/units/864312087845313/recordings?camera=0&profile=0&start_utc=1750000000&end_utc=1750000600

// 2) request just the 15s you want (end_utc - start_utc = 15)
POST /api/units/864312087845313/clips
{ "camera": 0, "profile": 0, "start_utc": 1750000000, "end_utc": 1750000015, "audio": false }

// 3) poll GET /api/clips/{id} until status == "ready", then
GET /api/clips/{id}/download   →   a 15-second .mp4`}</CodeBlock>
      <Callout tone="info">
        The cut starts at the nearest video keyframe to <code>start_utc</code>, so the very first
        fraction of a second can be approximate; <code>end_utc</code> bounds the end. Keep the
        window inside one continuous recording for best results, and always confirm against{" "}
        <code>/recordings</code> first — a window with no footage produces nothing to download.
      </Callout>

      <Callout tone="info" title="Time windows">
        Recording/clip windows are true-UTC Unix seconds in the API. Howen indexes SD footage by the
        device&rsquo;s local wall-clock, so the gateway localizes the window using its{" "}
        <code>DEVICE_TZ_OFFSET</code>. If that offset is wrong, a window won&rsquo;t match any file —
        always confirm against <code>/recordings</code> first.
      </Callout>

      {/* ---------------------------------------------------------------- */}
      <h2 id="snapshots">Snapshots</h2>
      <Callout tone="warn">
        Snapshots need video enabled (<code>MEDIA_ADVERTISE_HOST</code>) — the device uploads the
        captured file over the media port. A device in standby returns <code>409 device_sleeping</code>.
      </Callout>
      <p>
        There are two ways to grab a still. Use <strong>capture-and-fetch</strong> when you just want
        the image back; use <strong>capture-only</strong> when you only need the device to take the
        photo (it writes the JPEG to its SD card and reports the path).
      </p>

      <h3>Capture &amp; fetch the JPEG (one call)</h3>
      <Endpoint method="POST" path="/api/units/{serial}/snapshot/image?camera=0&resolution=0">
        Capture a still on one camera and return the JPEG inline. The gateway triggers the capture
        (<code>0x4020</code>), then pulls the file back over the device&rsquo;s media port
        (file-transfer <code>0x4090</code>). <code>camera</code> is 0-based (default <code>0</code>);{" "}
        <code>resolution</code> is <code>0</code> follow-video, <code>1</code> 1080p, <code>2</code>{" "}
        720p, <code>3</code> VGA, <code>4</code> D1.
      </Endpoint>
      <CodeBlock label="200 OK">{`Content-Type: image/jpeg   (binary JPEG body)`}</CodeBlock>

      <h3>Capture only (device file paths)</h3>
      <Endpoint method="POST" path="/api/units/{serial}/snapshots">
        Capture stills on one or more cameras and return the device-side file paths (no image bytes).
        Body: <code>{`{ "channels": [0], "resolution": 0 }`}</code> — <code>channels</code> are 0-based
        (default <code>[0]</code>).
      </Endpoint>
      <CodeBlock label="200 OK">{`{ "ok": true, "session_id": "snap_…",
  "files": [ { "channel": 1, "device_path": "/mnt/sd1/picture/Pic….jpg" } ] }`}</CodeBlock>
      <Callout tone="info">
        The reported <code>channel</code> is 1-based (the H-Protocol channel = camera + 1), so camera{" "}
        <code>0</code> comes back as channel <code>1</code>.
      </Callout>

      <h3>Search stills stored on the device</h3>
      <Endpoint method="GET" path="/api/units/{serial}/snapshots/search?camera=&start_utc=&end_utc=&kind=">
        List stills already on the device&rsquo;s SD card for a window (file query <code>0x4060</code>).{" "}
        <code>camera</code> 0-based (omit or <code>-1</code> = all); <code>kind</code> <code>general</code>{" "}
        (default) or <code>alarm</code>; times are UTC seconds. For &ldquo;all cameras&rdquo; the gateway
        queries each channel and merges — the device rejects an all-channels snapshot query.
      </Endpoint>
      <CodeBlock label="200 OK">{`{ "count": 1, "snapshots": [
  { "channel": 0, "device_path": "/mnt/sd1/picture/Pic….jpg", "size": 208069,
    "utc": 1782498351, "device_time": "2026-06-26 20:25:51", "kind": "general" } ] }`}</CodeBlock>
      <Endpoint method="GET" path="/api/units/{serial}/snapshots/file?path=<device_path>">
        Download one device-stored still by its path (from a search result), pulled over the
        file-transfer path. Responds <code>image/jpeg</code>.
      </Endpoint>

      <h3>Save snapshots to the gateway</h3>
      <p>
        Persist a still server-side — the JPEG is stored under <code>CLIPS_ROOT/snapshots</code> with a
        row in the <code>snapshots</code> table (needs a database). <code>source: &quot;capture&quot;</code>{" "}
        takes a fresh still; <code>source: &quot;device&quot;</code> copies one from a search result.
      </p>
      <Endpoint method="POST" path="/api/units/{serial}/snapshots/save">
        Body (capture): <code>{`{ "source": "capture", "camera": 0, "resolution": 0 }`}</code>. Body
        (copy from device): <code>{`{ "source": "device", "device_path": "…", "camera": 0, "kind": "general", "captured_utc": 0 }`}</code>.
      </Endpoint>
      <CodeBlock label="200 OK">{`{ "ok": true, "id": 1, "file_size": 208069, "storage_path": "snapshots/<serial>/snap_….jpg" }`}</CodeBlock>
      <Endpoint method="GET" path="/api/snapshots?serial=&limit=&offset=">
        List snapshots saved on the gateway, newest first.
      </Endpoint>
      <Endpoint method="GET" path="/api/snapshots/{id}/download">
        Stream a saved snapshot&rsquo;s JPEG (attachment).
      </Endpoint>
      <Endpoint method="DELETE" path="/api/snapshots/{id}">
        Remove a saved snapshot&rsquo;s row and file.
      </Endpoint>
      <Callout tone="info">
        In the admin panel these are all on the device&rsquo;s <strong>Snapshots</strong> tab: capture
        &amp; save, search what&rsquo;s on the device, and a &ldquo;Saved on the gateway&rdquo; list with
        view/download/delete.
      </Callout>

      <hr />
      <p>
        Want to try these live? Open the <Link href="/api-console">API Console</Link> — the built-in
        collection has every endpoint above ready to send.
      </p>
    </article>
  );
}
