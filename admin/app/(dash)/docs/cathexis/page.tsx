import Link from "next/link";
import { Callout, CodeBlock, Endpoint } from "@/components/docs/doc-kit";

export const metadata = { title: "Cathexis integration · Docs" };

export default function CathexisDocsPage() {
  return (
    <article className="doc-prose">
      <h1 className="text-2xl font-semibold text-white">Cathexis integration guide</h1>
      <p>
        This guide is for systems that <strong>read</strong> data from Cathexis MVR (mobile DVR)
        units through the gateway&rsquo;s HTTP API: live GPS &amp; events, device status (incl. SD-card
        health and environment stats), live video, recorded clips, event-preview snapshots, and
        device parameter config.
      </p>

      {/* ---------------------------------------------------------------- */}
      <h2 id="overview">Overview</h2>
      <p>
        A Cathexis unit is a vehicle mobile-DVR that reports GPS and driving events, exposes live
        camera streams, and stores recorded video that can be pulled on demand. The integration
        surface is identical to every other unit type — the same HTTP API — so if you already
        consume Howen, the only differences are the device <code>type</code> and a few unit-specific
        details noted below.
      </p>
      <p>There are two separate planes — you only ever touch the second one:</p>
      <ul>
        <li>
          <strong>Device plane (TCP).</strong> Units connect to the gateway over TCP (control on
          port <code>32324</code>, media on <code>32325</code>/<code>32326</code>) and authenticate by
          serial in a <code>welcome</code> message. You never speak this protocol.
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
      <Callout tone="info" title="Cameras &amp; profiles">
        Cameras are 0-based: <code>0</code> = road, <code>1</code> = cab. Each camera has two
        profiles: <code>0</code> = high-res (main) and <code>1</code> = low-res (sub).
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
        Currently-connected devices. Cathexis units report <code>"protocol": "cathexis"</code>.
      </Endpoint>
      <CodeBlock label="200 OK">{`{
  "units": [
    {
      "serial": "MVR5452_4064668",
      "protocol": "cathexis",
      "model": "MVR5452",
      "remote_addr": "102.135.1.20:51120",
      "connected_at": "2026-06-20T16:14:51.703Z",
      "commands": ["reboot_unit"]
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
        <code>"event"</code> for driving events; an event message additionally populates the{" "}
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
    "serial_no": "MVR5452_4064668",
    "type": "cathexis",
    "model": "MVR5452"
  },
  "network": { "remote_ipv4": "102.135.1.20", "remote_port": 51120 },
  "gps": {
    "latitude": -26.2041, "longitude": 28.0473,
    "altitude": 1680, "speed": 65.2, "heading": 243,
    "satellites": 12
  },
  "events": [],
  "sensors": [ ["ignition", "on"] ]
}`}</CodeBlock>
      <Callout tone="info" title="Speed is km/h">
        Cathexis reports speed in metres/second on the wire; the gateway converts it to{" "}
        <strong>km/h</strong> before delivery, so the universal message <code>speed</code> is always
        km/h (consistent with every other unit type).
      </Callout>
      <p>
        For an event message, <code>message_type</code> is <code>"event"</code> and{" "}
        <code>events</code> carries the decoded, already-mapped standard event names:
      </p>
      <CodeBlock label="events[] (message_type: event)">{`"events": [
  ["HARSH:BRAKING"],
  ["COLLISION"],
  ["PANIC"]
]`}</CodeBlock>
      <Callout tone="info">
        Cathexis devices name their events as strings (e.g. <code>harsh_braking</code>); the gateway
        maps each to a standard event code before delivery. The full MVR5 event vocabulary is mapped
        out of the box — ignition, GPS lock/loss, idling (start/stop/periodic), harsh
        braking/cornering/acceleration/impact, speeding, following-distance, the AI/DMS events
        (<code>fatigue</code>, <code>distraction</code>, <code>cellphone</code>, <code>seatbelt</code>,{" "}
        <code>yawn</code>, <code>passenger</code>, <code>tamper</code>), telephony, and power-state
        (standby/wake) events. The device-name&rarr;event-code table is editable on the{" "}
        <Link href="/device-mapping">Device Mapping</Link> page (an unrecognized device event falls
        back to <code>ALARM</code>), and the canonical event-name picklist is available at{" "}
        <code>GET /api/event-codes</code>.
      </Callout>

      {/* ---------------------------------------------------------------- */}
      <h2 id="status">Device status</h2>
      <p>
        Pull a live snapshot of one device — connection info plus the latest telemetry it reported.
        Alongside location and ignition, the gateway periodically polls the unit for{" "}
        <strong>SD-card health</strong> and <strong>environment stats</strong> (input voltage/current,
        board/case/modem temperatures, CPU/GPU load, cell &amp; Wi-Fi signal) and caches them in the
        snapshot. <code>state</code> is <code>online</code> or <code>sleep</code> — the unit reports
        entering/leaving standby, and while it is asleep video/clip/config requests fail fast with{" "}
        <code>409</code> rather than hanging.
      </p>
      <Endpoint method="GET" path="/api/units/{serial}/status">
        Live status; <code>404</code> if not connected. Sections appear as the data arrives
        (<code>location</code>/<code>vehicle</code> after the first GPS frame; <code>sd_card</code>/
        <code>environment</code> after the first poll).
      </Endpoint>
      <CodeBlock label="200 OK (shape)">{`{
  "serial": "MVR5452_4064668",
  "connection": {
    "serial": "MVR5452_4064668", "protocol": "cathexis", "model": "MVR5452",
    "state": "online", "remote_addr": "102.135.1.20:51120",
    "connected_at": "2026-06-20T16:14:51.703Z"
  },
  "telemetry": {
    "updated_at": "2026-06-20T16:15:02Z",
    "location": { "latitude": -26.2041, "longitude": 28.0473, "speed_kmh": 65.2,
                  "altitude_m": 1680, "satellites": 12, "bearing": 243, "positioned": true },
    "vehicle":  { "ignition": true, "standby": false },
    "sd_card":  { "present": true, "type": "WesternDigital", "use_percent": 1 },
    "environment": { "input_voltage_v": 11.99, "temp_device_c": 52.7,
                     "cpu_load_pct": 65, "cell_level": 4 }
  }
}`}</CodeBlock>

      {/* ---------------------------------------------------------------- */}
      <h2 id="snapshots">Event-preview snapshots</h2>
      <p>
        When an event triggers, the unit can push a JPEG snapshot of the road and/or cab camera
        (whichever are enabled for that event in the device&rsquo;s event-preview config). The gateway
        saves each image to its snapshot bucket automatically — there is no on-demand capture for
        Cathexis. Browse and download them on the device&rsquo;s <strong>Snapshots</strong> tab, or
        via the API:
      </p>
      <Endpoint method="GET" path="/api/snapshots?serial={serial}">
        List saved snapshots (newest first). Event-preview snapshots have <code>source: "event"</code>{" "}
        and <code>kind</code> set to the event name.
      </Endpoint>
      <Endpoint method="GET" path="/api/snapshots/{id}/download">Download one snapshot&rsquo;s JPEG.</Endpoint>

      {/* ---------------------------------------------------------------- */}
      <h2 id="video">Live video (HLS)</h2>
      <Callout tone="warn">
        Video requires the gateway to be started with <code>MEDIA_ADVERTISE_HOST</code> set;
        otherwise these routes return <code>503</code>.
      </Callout>
      <p>Start a stream, play the HLS playlist, then stop it when done.</p>
      <Endpoint method="POST" path="/api/units/{serial}/stream/start">
        Begin a live stream. Body: <code>{`{ "camera": 0, "profile": 0 }`}</code> (camera 0 = road,
        1 = cab; profile 0 = high-res, 1 = low-res).
      </Endpoint>
      <CodeBlock label="200 OK">{`{ "ok": true, "session_id": "…",
  "hls_path": "MVR5452_4064668/0/0/stream.m3u8", "ready": true }`}</CodeBlock>
      <p>
        Cathexis sends no control-channel acknowledgement for stream start — the live frames arrive
        on the media connection — so <code>ready</code> may be <code>false</code> the instant you
        start; just keep retrying the playlist until ffmpeg has produced the first segment.
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
        What footage the unit holds for a window, from the device&rsquo;s recording ring summary.
        Always query before requesting — a clip request for a window with no footage fails fast.
      </Endpoint>
      <p>
        Params: <code>camera</code> (0-based), <code>profile</code> (default <code>1</code>),{" "}
        <code>start_utc</code>/<code>end_utc</code> (Unix seconds, true UTC; default = last 24h). The
        result lists the recorded regions that overlap the window.
      </p>
      <CodeBlock label="200 OK">{`{ "recordings": [
  { "camera": 0, "profile": 0, "start_utc": 1750000000, "end_utc": 1750003600 }
], "count": 1 }`}</CodeBlock>

      <h3>2. Request a clip</h3>
      <Endpoint method="POST" path="/api/units/{serial}/clips">
        Request a clip for the window. The device produces and uploads a finished{" "}
        <code>.mp4</code> for that window to the media port; it arrives asynchronously — poll the
        returned <code>clip_id</code>.
      </Endpoint>
      <CodeBlock label="Request / 200">{`// request — times are true-UTC Unix seconds
{ "camera": 0, "profile": 0, "start_utc": 1750000000, "end_utc": 1750000020 }
// 200
{ "ok": true, "clip_id": 11, "session_id": "…", "status": "processing" }`}</CodeBlock>
      <Callout tone="info">
        Unlike Howen (where the gateway remuxes exactly the requested window), a Cathexis unit
        produces the clip file itself for the window you ask for and uploads it whole — the gateway
        stores it as-is. The <code>start_utc</code>/<code>end_utc</code> you send define the clip;
        there is no separate trim step.
      </Callout>

      <h3>3. Poll status</h3>
      <Endpoint method="GET" path="/api/clips/{id}">
        One clip&rsquo;s metadata/status. (List all with{" "}
        <code>GET /api/clips?serial=&amp;limit=&amp;offset=</code>.)
      </Endpoint>
      <CodeBlock label="200 OK">{`{ "id": 11, "serial": "MVR5452_4064668", "camera": 0, "profile": 0,
  "start_utc": 1750000000, "end_utc": 1750000020, "duration_secs": 20,
  "status": "ready", "file_size": 16800000, "bytes_received": 16800000,
  "storage_path": "MVR5452_4064668/11.mp4", "error": "",
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

      <Callout tone="info" title="Time windows">
        Recording and clip windows are true-UTC Unix seconds throughout the API — no timezone
        localization is needed for Cathexis. Still, always confirm against <code>/recordings</code>{" "}
        first: a window with no overlapping footage produces nothing to download.
      </Callout>

      {/* ---------------------------------------------------------------- */}
      <h2 id="config">Device configuration</h2>
      <p>
        Read and write the unit&rsquo;s parameter config — the same settings the device&rsquo;s
        commissioning tool exposes. In the panel this is the <strong>Config</strong> tab on a device;
        over the API:
      </p>
      <Endpoint method="GET" path="/api/units/{serial}/config">
        Returns the whole config under <code>sc</code>: <code>network</code>, <code>general</code>,{" "}
        <code>cameras[]</code>, <code>events[]</code>, <code>eventpreviews[]</code>, and a read-only{" "}
        <code>description</code>. (A <code>?modules=</code> filter is accepted for parity with other
        units but Cathexis always returns everything.)
      </Endpoint>
      <Endpoint method="PUT" path="/api/units/{serial}/config">
        Write changed config. Body is <code>{`{ "sc": { "<segment>": … } }`}</code>.
      </Endpoint>
      <p>Each segment has its own write rule:</p>
      <ul>
        <li>
          <strong>network / general</strong> — send only the fields you change. A{" "}
          <code>general</code> write must include the mandatory <code>account</code> field (the panel
          adds it automatically).
        </li>
        <li>
          <strong>cameras</strong> — send the <em>whole</em> array: both cameras and both profiles,
          each with its <code>index</code> (a device requirement).
        </li>
        <li>
          <strong>events</strong> — send only the changed event objects, each in the device&rsquo;s{" "}
          <code>{`{ "event": [["key","value"], …] }`}</code> form.
        </li>
      </ul>
      <Callout tone="warn" title="Applying config">
        Most changes apply immediately; a few only take effect after the unit restarts — if a setting
        doesn&rsquo;t seem to take, reboot the device (the <strong>Reboot device</strong> button on the
        Status tab, or <code>reboot_unit</code>). <code>eventpreviews</code> is <strong>not</strong>{" "}
        writable via this endpoint. A request while the unit is in standby returns <code>409</code> —
        wake it first (see Commands).
      </Callout>

      {/* ---------------------------------------------------------------- */}
      <h2 id="commands">Commands &amp; standby</h2>
      <p>
        A device reports its <code>state</code> as <code>online</code> or <code>sleep</code>{" "}
        (standby). While asleep it won&rsquo;t service video, clips, or config — those requests fail
        fast with <code>409</code> instead of hanging — so wake it first.
      </p>
      <Endpoint method="POST" path="/api/units/{serial}/commands">
        Send a control command. Body is <code>{`{ "type": "<command>" }`}</code>. Supported:{" "}
        <code>wake_device</code> and <code>reboot_unit</code>.
      </Endpoint>
      <ul>
        <li>
          <code>wake_device</code> — pokes the unit out of standby (sends a lightweight dAPI message;
          the unit wakes and returns to <code>online</code>). Use it when <code>state</code> is{" "}
          <code>sleep</code>, then retry your request.
        </li>
        <li>
          <code>reboot_unit</code> — reboots the device immediately.
        </li>
      </ul>

      {/* ---------------------------------------------------------------- */}
      <h2 id="limitations">Not yet supported</h2>
      <p>
        For transparency, these are the things the integration <strong>cannot</strong> do today —
        either the device firmware doesn&rsquo;t expose them, or they&rsquo;re not built yet:
      </p>
      <ul>
        <li>
          <strong>Recorded playback (&ldquo;stream review&rdquo;)</strong> — live scrubbing/replay of
          SD-card footage from a chosen time. The MVR5 API defines it, but the firmware on the
          current fleet (<code>mdvr_5_2_RC72</code>) silently ignores the request, so it isn&rsquo;t
          offered. To review recorded footage today, pull a <Link href="#clips">clip</Link> for the
          window instead.
        </li>
        <li>
          <strong>Audio in live video</strong> — live HLS streams are video-only. (A pulled clip may
          contain audio if the camera profile recorded it; only the <em>live</em> path drops audio.)
        </li>
        <li>
          <strong>On-demand snapshots</strong> — there is no &ldquo;capture a still now&rdquo;
          command, and no way to list or download stills the device stored on its SD card. The only
          still images are the <Link href="#snapshots">event-preview snapshots</Link> the device
          pushes automatically when an event fires.
        </li>
        <li>
          <strong>Editing event-preview settings via the API</strong> — the <code>eventpreviews</code>{" "}
          config section (which cameras push a preview per event) is read-only here; the device
          rejects writes to it. Change it with the Cathexis commissioning tool.
        </li>
        <li>
          <strong>Driver identification, IMU traces, and event-summary video</strong> — face-ID
          snapshots, raw harsh-event accelerometer traces, and listing/pulling video for a specific
          past event by ID are defined by the device but not yet implemented in the gateway.
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
