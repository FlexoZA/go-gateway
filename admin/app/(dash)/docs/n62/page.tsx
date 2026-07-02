import Link from "next/link";
import { Callout, CodeBlock, Endpoint } from "@/components/docs/doc-kit";

export const metadata = { title: "N62 integration · Docs" };

export default function N62DocsPage() {
  return (
    <article className="doc-prose">
      <h1 className="text-2xl font-semibold text-white">N62 integration guide</h1>
      <p>
        This guide is for systems that <strong>read</strong> data from the <strong>N62</strong>{" "}
        fleet dashcam through the gateway&rsquo;s HTTP API: live GPS &amp; events, device status,
        live video, and device parameter config. The integration surface is the same HTTP API as
        every other unit type — if you already consume Howen or Cathexis, the only differences are
        the device <code>type</code> and a few unit-specific details noted below.
      </p>

      {/* ---------------------------------------------------------------- */}
      <h2 id="overview">Overview</h2>
      <p>
        The N62 is an unbranded fleet dashcam that speaks <strong>JT/T 808-2019</strong> (the Chinese
        national vehicle-terminal protocol) with a vendor ULV extension for config and status. It
        reports GPS and events/alarms, exposes live camera streams over JT1078, and holds device
        configuration that can be read and written over the air.
      </p>
      <p>There are two separate planes — you only ever touch the second one:</p>
      <ul>
        <li>
          <strong>Device plane (TCP).</strong> The device connects to the gateway over TCP (control
          on port <code>6608</code>, JT1078 media on <code>6609</code>) and authenticates by its
          JT808 terminal phone number. You never speak this protocol.
        </li>
        <li>
          <strong>Integration plane (HTTP API).</strong> Your system calls the gateway&rsquo;s HTTP
          API (default port <code>8080</code>, served behind TLS in production) with a Bearer API
          key. Everything below is on this plane.
        </li>
      </ul>
      <p>
        Live GPS and events are <strong>pushed</strong> to you via webhooks; status, video, and
        config are <strong>pulled</strong> on request.
      </p>
      <Callout tone="info" title="Unit type &amp; identifiers">
        The N62 registers as unit type <code>dfm-n62</code> (the <code>protocol</code> field in{" "}
        <code>/api/units</code>). Its serial is <code>JT808_</code> followed by the device&rsquo;s
        JT808 terminal phone digits (e.g. <code>JT808_100000000327</code>). In the universal message
        the device <code>type</code> and <code>model</code> are both <code>"n62"</code>. Adding
        more N62-class devices — or a differently-branded JT808 make — is a gateway config change,
        each on its own port and mapping table.
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
        Currently-connected devices. An N62 reports <code>"protocol": "dfm-n62"</code>.
      </Endpoint>
      <CodeBlock label="200 OK">{`{
  "units": [
    {
      "serial": "JT808_100000000327",
      "protocol": "dfm-n62",
      "model": "N62",
      "remote_addr": "102.135.1.20:41022",
      "connected_at": "2026-06-20T16:14:51.703Z",
      "commands": ["reboot_unit", "request_environment",
                   "request_vehicle_info", "request_basic_status", "stop_playback"]
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
        Each delivery is one <strong>Universal JSON</strong> message — the same envelope as every
        other unit type. <code>message_type</code> is <code>"gps"</code> for position reports and{" "}
        <code>"event"</code> for alarms/events; an event message additionally populates the{" "}
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
    "serial_no": "JT808_100000000327",
    "type": "n62",
    "model": "n62"
  },
  "network": { "remote_ipv4": "102.135.1.20", "remote_port": 41022 },
  "gps": {
    "latitude": -26.2041, "longitude": 28.0473,
    "altitude": 1680, "speed": 65.2, "heading": 243,
    "satellites": 12
  },
  "events": [],
  "sensors": [ ["ignition", "on"], ["mileage", 128345.6] ]
}`}</CodeBlock>
      <Callout tone="warn" title="Device clock &amp; timezone">
        JT808 locations carry the device&rsquo;s <strong>local wall-clock with no timezone</strong>.
        The gateway converts it to true UTC using the unit&rsquo;s <code>timezone_offset</code>
        setting (on the device&rsquo;s <Link href="/device-settings">Device Settings</Link> — e.g.{" "}
        <code>2</code> for SAST, or <code>0</code> if the device already sends UTC). If that offset
        is wrong, every timestamp is skewed — set it per deployment.
      </Callout>
      <p>
        For an event message, <code>message_type</code> is <code>"event"</code> and{" "}
        <code>events</code> carries the decoded, already-mapped standard event names:
      </p>
      <CodeBlock label="events[] (message_type: event)">{`"events": [
  ["SPEEDING"],
  ["COLLISION:TURN_OVER"],
  ["HARSH:BRAKING"]
]`}</CodeBlock>
      <Callout tone="info">
        The N62 signals alarms several ways at once — the JT808 alarm bitmask, ULV ADAS/DMS/BSD
        additional-info fields, and two vendor code families — and the gateway maps each raw signal
        to a standard event code before delivery. Those mapping tables (alarm-bit, ADAS, DMS, BSD,
        and the vendor families) are editable on the <Link href="/device-mapping">Device Mapping</Link>{" "}
        page; unrecognized signals pass through as a stable <code>UNKNOWN</code> token rather than an
        invented code. Use the <Link href="/mapping-test">Mapping Test</Link> to watch live events
        resolve (mapped codes green, unmapped amber → add a mapping). The canonical event-name
        picklist is at <code>GET /api/event-codes</code>.
      </Callout>

      {/* ---------------------------------------------------------------- */}
      <h2 id="status">Device status</h2>
      <p>
        Pull a live snapshot of one device — connection info plus the latest status the device
        reported over its ULV transparent channel (<code>0x0900</code>): a basic-status block (CPU
        temperature, network signal, satellite count) and SD/HDD health. Location and driving state
        are not in this snapshot — they arrive on the GPS/event webhook stream above.
      </p>
      <Endpoint method="GET" path="/api/units/{serial}/status">
        Live status; <code>404</code> if not connected. <code>telemetry</code> is <code>null</code>{" "}
        until the device has pushed a status frame, and each block appears only once its frame
        arrives.
      </Endpoint>
      <CodeBlock label="200 OK (shape)">{`{
  "serial": "JT808_100000000327",
  "connection": {
    "serial": "JT808_100000000327", "protocol": "dfm-n62", "model": "N62",
    "state": "online", "remote_addr": "102.135.1.20:41022",
    "connected_at": "2026-06-20T16:14:51.703Z"
  },
  "telemetry": {
    "basic":     { "network_signal": 24, "cpu_temp_c": 47.5, "satellites": 12 },
    "basic_at":  "2026-06-20T16:15:02Z",
    "sd_card":   { "sd_cards": { "count": 1, "total_mb": [61024], "remaining_mb": [42310] } },
    "sd_card_at":"2026-06-20T16:15:02Z"
  }
}`}</CodeBlock>
      <Callout tone="info">
        Status parsing is best-effort and firmware-dependent — the exact fields a given N62 build
        emits over the vendor channel vary, so blocks may be partial or absent. Location, speed, and
        ignition are always available from the GPS/event webhook regardless. You can also nudge a
        fresh basic-status read with the <code>request_basic_status</code> command (see Commands).
      </Callout>

      {/* ---------------------------------------------------------------- */}
      <h2 id="video">Live video (HLS)</h2>
      <Callout tone="warn">
        Video requires the gateway to be started with <code>MEDIA_ADVERTISE_HOST</code> set;
        otherwise these routes return <code>503</code>.
      </Callout>
      <p>Start a stream, play the HLS playlist, then stop it when done.</p>
      <Endpoint method="POST" path="/api/units/{serial}/stream/start">
        Begin a live stream. Body: <code>{`{ "camera": 0, "profile": 0 }`}</code>. <code>camera</code>{" "}
        is 0-based; <code>profile</code> is <code>0</code> main (high-res) or <code>1</code> sub
        (low-res).
      </Endpoint>
      <CodeBlock label="200 OK">{`{ "ok": true, "session_id": "…",
  "hls_path": "JT808_100000000327/0/0/stream.m3u8", "ready": true }`}</CodeBlock>
      <p>
        The gateway points the device&rsquo;s JT1078 stream (a <code>0x9101</code> command) at its
        media port; the device dials in and streams raw H.264, which the gateway packages into a
        rolling HLS playlist. <code>ready:false</code> just means the first segment wasn&rsquo;t up
        within the window — keep retrying the playlist.
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
        (<code>CLIPS_ROOT</code>), and a database.
      </Callout>
      <p>The flow is: query what footage exists → request a clip → poll until ready → download.</p>

      <h3>1. Query available recordings</h3>
      <Endpoint method="GET" path="/api/units/{serial}/recordings?camera=&profile=&start_utc=&end_utc=">
        What footage the device holds for a window (JT808 <code>0x9205</code> query). Always query
        before requesting — a clip request for a window with no footage produces nothing.
      </Endpoint>
      <p>
        Params: <code>camera</code> (0-based), <code>profile</code> (default <code>1</code>),{" "}
        <code>start_utc</code>/<code>end_utc</code> (Unix seconds, true UTC; default = last 24h).
      </p>
      <CodeBlock label="200 OK">{`{ "recordings": [
  { "camera": 0, "profile": 0, "start_utc": 1750000000, "end_utc": 1750003600 }
], "count": 1 }`}</CodeBlock>

      <h3>2. Request a clip</h3>
      <Endpoint method="POST" path="/api/units/{serial}/clips">
        Request a clip for the window (JT808 <code>0x9201</code> playback upload). The{" "}
        <code>.mp4</code> arrives asynchronously; poll the returned <code>clip_id</code>.
      </Endpoint>
      <CodeBlock label="Request / 200">{`// request — times are true-UTC Unix seconds
{ "camera": 0, "profile": 0, "start_utc": 1750000000, "end_utc": 1750000020 }
// 200
{ "ok": true, "clip_id": 11, "session_id": "…", "status": "requested" }`}</CodeBlock>

      <h3>3. Poll status</h3>
      <Endpoint method="GET" path="/api/clips/{id}">
        One clip&rsquo;s metadata/status. (List all with{" "}
        <code>GET /api/clips?serial=&amp;limit=&amp;offset=</code>.)
      </Endpoint>
      <p>
        <code>status</code> moves <code>requested</code> → <code>receiving</code> →{" "}
        <code>ready</code> | <code>error</code>. Poll until it&rsquo;s <code>ready</code> or{" "}
        <code>error</code>, then download.
      </p>

      <h3>4. Download</h3>
      <Endpoint method="GET" path="/api/clips/{id}/download">
        Stream the stored <code>.mp4</code> (<code>Content-Type: video/mp4</code>, attachment).{" "}
        <code>409</code> if the clip isn&rsquo;t <code>ready</code>; <code>404</code> if the file is
        missing.
      </Endpoint>
      <Callout tone="warn" title="Footage depends on the device having an SD card">
        The recordings query and clip flow are implemented, but recorded playback only works when
        the device has an SD card with footage for the requested window. The validated N62 bench unit
        ships without SD footage, so the clip path (<code>0x9201</code>) is unproven on real
        recordings — expect to validate it against a device with a populated card. Live video (above)
        is validated end-to-end.
      </Callout>

      {/* ---------------------------------------------------------------- */}
      <h2 id="config">Device configuration</h2>
      <p>
        Read and write the device&rsquo;s parameter config over its ULV channel
        (<code>0xB050</code>/<code>0xB051</code>) — the same settings its local web UI exposes. In
        the panel this is the <strong>Config</strong> tab on a device, which presents eight
        categories: <em>General, Vehicle, Display, Recording, Alarm, AI (ADAS/DMS), Network,</em>{" "}
        and <em>Peripheral</em>. Over the API:
      </p>
      <Endpoint method="GET" path="/api/units/{serial}/config?modules=">
        Read config. Returns the parameter segments under <code>sc</code>, keyed by ULV ParamType.
        <code>?modules=</code> optionally narrows which segments are fetched.
      </Endpoint>
      <Endpoint method="PUT" path="/api/units/{serial}/config">
        Write changed config. Body is <code>{`{ "sc": { "<ParamType>": … } }`}</code>. The PUT
        re-reads the affected segments afterward and returns device truth — the firmware silently
        clamps out-of-range values.
      </Endpoint>
      <p>Segments differ in how they must be written (the panel editor handles this for you):</p>
      <ul>
        <li>
          <strong>Scalar segments</strong> — merge partial field sets; send only the fields you
          change.
        </li>
        <li>
          <strong>Nested segments</strong> (e.g. <code>NetCms</code> servers,{" "}
          <code>RecStream</code>/<code>RecCamAttr</code> channels) — sent <em>whole</em>; the
          firmware does not merge partial sub-objects.
        </li>
        <li>
          <strong>Alarm / ADAS / DMS &ldquo;list&rdquo; segments</strong> — sent whole, preserving
          the linkage string verbatim while the tuning knobs stay editable. Some <code>En</code>{" "}
          flags are JSON booleans and others <code>0</code>/<code>1</code> — the editor echoes the
          original type.
        </li>
      </ul>
      <Callout tone="info" title="Some segments read empty over the CMS link">
        Eight ParamTypes (<code>PreDisplay</code>/<code>PreOsd</code>/<code>PreMargin</code>,{" "}
        <code>RecCamAttr</code>/<code>RecCapAttr</code>/<code>RecStorage</code>,{" "}
        <code>AlmDriving</code>, <code>AlmSys</code>) answer a ULV read with an empty body over the
        CMS link even though they read fine on the device&rsquo;s local web UI. The panel shows a
        &ldquo;no data&rdquo; card for these; the gateway retries garbled/empty reads before giving
        up.
      </Callout>

      {/* ---------------------------------------------------------------- */}
      <h2 id="commands">Commands</h2>
      <Endpoint method="POST" path="/api/units/{serial}/commands">
        Send a control command. Body is <code>{`{ "type": "<command>" }`}</code>.
      </Endpoint>
      <ul>
        <li>
          <code>reboot_unit</code> — reboots the device.
        </li>
        <li>
          <code>request_basic_status</code> — asks the device for a fresh basic-status frame
          (CPU temp / signal / satellites); the reply also refreshes the{" "}
          <Link href="#status">status snapshot</Link>.
        </li>
        <li>
          <code>request_environment</code> — queries the device&rsquo;s terminal attributes.
        </li>
        <li>
          <code>request_vehicle_info</code> — queries the device&rsquo;s stored vehicle info.
        </li>
      </ul>

      {/* ---------------------------------------------------------------- */}
      <h2 id="limitations">Not yet supported</h2>
      <p>
        For transparency, these are the things the integration <strong>cannot</strong> do today —
        the device firmware doesn&rsquo;t expose them:
      </p>
      <ul>
        <li>
          <strong>On-demand snapshots</strong> — there is no &ldquo;capture a still now&rdquo;
          command. The validated N62 firmware ignores the JT808 capture message (<code>0x8801</code>),
          so the capture UI is turned off for this model. (The gateway <em>does</em> save any stills
          the device pushes on its own; a future firmware that honours <code>0x8801</code> can enable
          on-demand capture without code changes.)
        </li>
        <li>
          <strong>Recorded clips on real footage</strong> — the clip flow is wired but unproven
          against a device with SD-card footage (see <Link href="#clips">Recorded clips</Link>).
        </li>
        <li>
          <strong>A few config segments over the CMS link</strong> — eight ParamTypes read empty
          remotely (see <Link href="#config">Device configuration</Link>); edit those on the
          device&rsquo;s local web UI.
        </li>
      </ul>

      <hr />
      <p>
        Want to try these live? Open the <Link href="/api-console">API Console</Link> — the built-in
        collection has every endpoint above ready to send.
      </p>
    </article>
  );
}
