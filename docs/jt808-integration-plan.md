# JT808 / N62 integration plan

Plan for adding a **JT808** unit type to `device-gateway`. Target device: an
unbranded fleet dashcam, **model N62** (`Ver 1.0.0 EU`, label `ID 000000096750`,
`S/N 08820251010168`), speaking **JT/T 808-2019** (device app mode `JT808_19`).
The N62 supports **GPS/telemetry + events**, **device parameter config** (ULV
`0xB050`/`0xB051`), **live status** (ULV transparent `0x0900`), and **JT1078
video** (live HLS + recorded clips + snapshots).

Deployment: **multi-unit** — register in `cmd/gateway` alongside
howen/fleetiger/cathexis/navtelecom, on its own control port **6608**.

Reference implementation (proven against this exact device): the old Node gateway
`dfm-mvr-gateway/src/tcp/jt808Server.js` (+ `jt808VideoServer.js`,
`jt808PlaybackRegistry.js`). Protocol docs in `dfm-mvr-gateway/docs/jt808/`
(`JT808.md`, `N62_CALL_MAPPING.md`, `N62_EVENT_MAPPING.md`, the JT808-2019 and ULV
PDFs).

## 0. Decisions (confirmed)

- **Unit name** `jt808` → package `internal/jt808`, entrypoint `cmd/jt808`, port
  env `JT808_PORT` (default 6608), registry `protocol = "jt808"`. The universal
  message **`make` is emitted as `"jt808_19"`, `model` as `"N62"`** — this matches
  the message-builder's `jt808Switch` golden special-case
  (`internal/core/message/message.go`), so device `type`/`model` come out as the
  original JS adapter produced. Do not change the emitted make string.
- **Video transport: separate media port — CONFIRMED working.** We control the
  server IP/port inside the `0x9101`/`0x9201` commands, so the N62's JT1078 stream
  is pointed at a dedicated JT808 media port (default **6609**,
  `DefaultMediaPort()`), served by a `MediaServerProvider` listener — mirroring
  howen/cathexis. The control listener on 6608 only ever decodes JT808 `0x7E`
  frames. Validated live: the N62 dials the advertised media port and streams.
  The media stream is the **JT1078 `0x30316364` framing carrying raw H.264** (PT
  98), parsed in `mediacodec.go` — NOT the alternate RTP/MPEG-PS framing — so it
  feeds the core media pipeline directly (h264). The media server logs each
  connection's first bytes to confirm this per device.
- **Build sequence: GPS-first.** Phase 1 lands connect + GPS + events + ULV
  config read/write + status, validated against the live N62, before video.
- **Per-unit timezone** is an editable unit setting (`ConfigurableUnit`,
  `timezone_offset`), read via `Deps.UnitSettings` with the global
  `DEVICE_TZ_OFFSET` as fallback. NB: it seeds to `0` and overrides the global, so
  set it per deployment (the live N62 is SAST → `2`).

## 1. Wire format (what we implement in the codec)

**Framing.** `0x7E … 0x7E` delimited. Unescape `0x7D 0x02 → 0x7E`,
`0x7D 0x01 → 0x7D`. Trailing byte before the closing flag is a 1-byte XOR (BCC)
checksum over everything from the message-ID byte to the last body byte
(computed after unescape). Back-to-back frames may share a flag — tolerate empty
inter-frame content.

**Header (auto-detect 2013 vs 2019 by body-attr bit 14).**

| Field | 2013 | 2019 |
|---|---|---|
| Message ID (u16 BE) | 0–1 | 0–1 |
| Body attrs (u16 BE) | 2–3 | 2–3 |
| Protocol version (=0x01) | — | 4 |
| Terminal phone BCD | 4–9 (BCD[6]) | 5–14 (BCD[10]) |
| Message serial (u16 BE) | 10–11 | 15–16 |
| Subpackage info (if attr bit 13) | +4 bytes | +4 bytes |
| Body | after header | after header |

Body length = attrs bits 0–9. Serial = `JT808_` + phone digits with leading
zeros stripped (`JT808_0` if all-zero), mirroring the JS. The N62 is `JT808_19`,
so 2019/17-byte headers; we support both for robustness.

**Inbound handlers (phase 1).**

| Msg | Action | Reply |
|---|---|---|
| `0x0100` register | authorize, register in Hub, serial from phone | `0x8100` (serial, result 0, auth `DFM`+last6) then `0x8001` |
| `0x0102` auth | accept | `0x8001` |
| `0x0002` heartbeat | — | `0x8001` |
| `0x0200` location | parse + emit gps/event | `0x8001` |
| `0x0704` batch location | parse each inner `0x0200` + emit | `0x8001` |
| `0x0001` general resp | log / (phase 2: match video acks) | none |
| `0x0107` term attrs | resolve `request_environment` | `0x8001` |
| `0xB051` ULV param resp | resolve config request (JSON body) | `0x8001` |
| `0x4040` vehicle info | resolve `request_vehicle_info` | `0x8001` |
| `0x0900` transparent | parse basic-status (`0xF1`) + SD health → status cache | `0x8001` |
| `0x0702/0x0800/0x0801/0x0d03/0x1206` | ack only (stop retries) | `0x8001` |

**`0x0200` body.** 28 fixed bytes: alarm u32, status u32, lat/lon i32 (÷1e6, sign
from status bits 2/3), altitude u16 m, speed u16 (÷10 km/h), direction u16,
BCD[6] time (device-local → UTC via configured offset). Then additional-info TLVs
(`id`,`len`,`value`): `0x01` mileage (÷10 km), `0x02` fuel (÷10 L), `0x03`
recorder speed, `0x30` signal, `0x31` satellites, video alarms `0x14/0x15/0x17`,
ULV ADAS/DMS/BSD `0x64/0x65/0x67`, vendor `0xE1`/`0x70`, aux fuel `0xEC`.

**Outbound builders.** `0x8001` (ack-serial,ack-id,result), `0x8100`,
`0x8105` (reboot 0x74), `0x8107`, `0xB040`, `0xB050` (4-byte type + 4-byte JSON
len + JSON `{CmdType,ParamType,…}`), `0x8900` (transparent type). Platform serial
counter per connection starting at 0.

## 2. Mapping (`MappingProvider`, editable in admin)

Raw signals → ACM Standard Event Codes, seeded from `N62_EVENT_MAPPING.md`
(confirmed + provisional), editable so the still-unconfirmed vendor subtypes can
be pinned down from live traffic without a redeploy. Map types:

- `alarm` — alarm-bitmask **bit index** → code (1 SPEEDING, 29 COLLISION,
  30 COLLISION:TURN_OVER, 31 ALARM:DOOR_OPEN, 19 PARKING:START, 20–23 ZONE:…,
  27 ALARM:TAMPERING, 28 MOVEMENT:UNAUTHORIZED, 0 PANIC, 14 ALARM).
- `adas` (`0x64` subtype), `dms` (`0x65` subtype), `bsd` (`0x67` subtype).
- `vendor_e1` — `0xE1` value (1 HARSH:ACCELERATION, 2 HARSH:BRAKING,
  3 HARSH:CORNERING, 4 COLLISION).
- `vendor_70` — last-3-bytes-as-u24 (0x020200 IGNITION:ON, 0x040100
  HARSH:CORNERING, 0x050200 COLLISION).

Unmapped codes pass through as a stable `UNKNOWN`/raw token (don't invent codes).

## 3. Capabilities & framework seams

```go
Capabilities{HasCommands: true, HasConfig: true, HasStatus: true, // phase 1
             HasVideo: true}                                       // phase 2
// HasSnapshots is false: the N62 firmware ignores the 0x8801 capture command, so
// the on-demand-capture UI would always fail. The Snapshotter impl + auto-push
// 0x0801 save path stay; flip to true for firmware that supports 0x8801.
```
- `Commander` (required for Hub registration; Config/Status are type-asserted off
  the registered commander): `reboot_unit`, `request_environment`,
  `request_vehicle_info`, `request_basic_status` (phase 1); video/`stop_playback`
  (phase 2).
- `ConfigController` — `RequestConfig(paramTypes)` issues `0xB050` Get per type,
  assembles `{ParamType: {...}}`; `UpdateConfig(sc)` issues `0xB050` Set per
  changed segment. Lights up the admin config screen.
- `StatusReporter` — cache last `0x0900` basic-status (cpu temp `(raw-400)/10`,
  signal, satellites) + SD health.
- `DefaultPort()`=6608, `DefaultMediaPort()`=6609, `IdleTimeout()` generous
  (device reports ~10s but can be quiet).
- `ConfigurableUnit` — editable per-unit settings (`timezone_offset`); see §0.
- Phase 2: `VideoController` (`0x9101/0x9102` live, `0x9201/0x9202` clips,
  `0x9205→0x1205` recordings), `MediaServerProvider` (JT1078 stream listener →
  `media.Manager` live / `media.ClipRegistry` clips). `Snapshotter` (`0x8801`
  capture + `0x0801` upload reassembly) is implemented but declared off — the N62
  firmware doesn't honour `0x8801` (no response). Auto-pushed `0x0801` stills are
  reassembled (JPEG bounded by `FFD8`/`FFD9`) and saved via `SnapshotSaver`.

## 4. Files

`internal/jt808/`: `codec.go` (framing, header incl. subpackage, location/TLV
parse, builders, `0x0900` parse), `events.go` (`MappingProvider` + maps),
`gps.go` (normalized payload), `server.go` (Protocol/Session/Commander/lifecycle
+ `ConfigurableUnit` settings), `config.go` (`ConfigController`), `status.go`
(`StatusReporter`); phase 2 adds `mediacodec.go` (JT1078 frame parser), `media.go`
(`MediaServerProvider` + stream-route registry), `video.go` (`VideoController`),
`recordings.go` (`0x9205→0x1205`), `snapshot.go` (`Snapshotter`). Tests:
`codec_test.go`, `events_test.go`, `mediacodec_test.go`, `snapshot_test.go`,
`integration_test.go` (register→location→webhook, unit-settings tz), and three
fuzz targets (`ReadFrame`, `parseLocation`, `readJT1078Frame`). Core media gained
a configurable ffmpeg input format (`RegisterInput`, `RegisterClip` "mpegps",
`ClipRegistry.WriteMux`) as a fallback for the alternate RTP/PS framing — unused
by the N62 (raw-H.264). `cmd/jt808/main.go` = `app.Run(jt808.New())`; one line in
`cmd/gateway/main.go`; `JT808_PORT`/`6608` + `6609` media port in
`deploy/docker-compose.yml`.

## 5. Validation

Validated on staging (`ssh lab`) against a live N62 (`JT808_100000000327` — the
device's JT808 DevId, not the label ID). The unit was repointed via its `NetCms`
config to the lab host on 6608 and approved in the admin (lab runs
`DEVICE_REJECT_UNKNOWN=true`).

## 6. Phases

- **P1 — connect + GPS + events + config + status. ✅ DONE + validated.**
  (`feature/jt808`, merged into `main`.) Live: registration/approval, GPS decode
  (correct lat/lon), UTC timestamps, ULV config read, `request_environment`, and
  events (a tilting event → `COLLISION:TURN_OVER`, speeding → `SPEEDING`). A real
  framework bug was found+fixed: `DEVICE_TZ_OFFSET` was only wired to video units,
  so jt808 emitted local time — hoisted to all units (now also a per-unit
  setting, §0). Firmware gaps: `request_basic_status` (0xF1) and the `0x0107`
  field offsets are best-effort (this N62 only emits a vendor `0x0900` type
  `0xA1`).
- **P2 — video. ✅ DONE (live HLS validated); clips/snapshots firmware-limited.**
  (`feature/jt808-video`, merged into `main`.) Live HLS (`0x9101`) validated
  end-to-end — the N62 dials media port 6609, streams JT1078 `0x30316364` raw
  H.264, and the rolling HLS playlist serves. Recordings query (`0x9205→0x1205`)
  works (returns empty — the bench unit has no SD footage). Recorded clips
  (`0x9201`) are implemented but unvalidated for lack of on-device footage.
  On-demand snapshots (`0x8801`) are unsupported by this firmware (declared off).
- **Unit config. ✅ DONE.** (`feature/jt808-unit-config`.) Editable per-unit
  `timezone_offset` (`ConfigurableUnit`); set to `2` for the SAST N62 and
  validated live.
- **Full config screen + Mapping Test. ✅ DONE.** (`feature/n62-config-screen`,
  `feature/n62-config-full`, `feature/n62-ulv-reassembly`, `feature/jt808-mapping-trace`,
  merged into `main`.) The Config tab has a bespoke N62 editor
  (`admin/components/N62Config.tsx` + `admin/lib/n62Config.ts`, selected by
  `deviceConfigKind("jt808")`) over the ULV `0xB050/0xB051` channel — 8 categories:
  General, Vehicle, Display, Recording, Alarm, AI (ADAS/DMS), Network, Peripheral.
  `ulvParamTypes` (`config.go`) is the verified ParamType set. Three write shapes:
  - **scalar** segments merge partial field Sets (send only changed fields);
  - **nested** segments (`NetCms.Server_xx`, `RecStream_M/S`/`RecCamAttr.Chn_xx`) are
    sent WHOLE — the firmware does not merge partial sub-objects;
  - **alarm/ADAS/DMS "list"** segments (`AlmSpd/Gsn/Driving/Sys/IoIn`, `AiAdas/AiDms`)
    send the whole segment with the `LnkParam` linkage string preserved verbatim and
    the CSV `Param` tuning knobs split into editable fields.
  Live-verified write-type quirk: some alarm `En` fields are JSON booleans, others 0/1
  — the editor echoes the original type. Firmware silently clamps out-of-range values;
  the PUT re-reads and returns device truth.
  - **Firmware limit — 8 ParamTypes unsupported over ULV.** `PreDisplay/PreOsd/PreMargin`,
    `RecCamAttr/RecCapAttr/RecStorage`, `AlmDriving`, `AlmSys` answer a ULV Get with an
    *empty* `0xB051` body (single non-subpackaged frame, `jsonLen=0`; confirmed by
    frame diag). They read fine over the unit's local HTTP UI but not the CMS link, so
    the editor shows a "no data" card. `RequestConfig` retries garbled/empty reads
    (`ulvGetAttempts`).
  - **Subpackage reassembly.** `server.go` reassembles genuinely subpackaged response
    frames (`reassemble`, mirrors the `0x0801` image path) before dispatch — a latent
    bug for large replies. The 8 empty-body segments above are NOT subpackaged, so this
    doesn't recover them.
  - **Mapping Test.** `handleLocation` emits `event_forward` with a `mapping_trace`
    (`resolveEventsTrace`) whenever a raw alarm signal is present, so the N62 renders in
    the live Mapping Test (mapped codes green, unmapped amber → add on Device Mapping).
    Idle ADAS/DMS status TLVs (subtype 0) are skipped to avoid noise.
- **Make/model as unit type. ✅ DONE.** (`feature/dfm-n62-unit-type`.) `jt808` is a
  *protocol/codec*, not a unit — different vendors/makes/models run as **separate unit
  types on separate ports**, sharing the JT808 codec. `jt808.New(Config{...})` is now
  config-driven (unit name, model, make, control/media port, `HasSnapshots`), and the
  per-model mapping state moved from a package global onto the `Protocol` instance so
  co-hosted JT808 units don't clobber each other. The N62 registers via the `jt808.N62()`
  preset as unit **`dfm-n62`** (the unbranded N62 group — add more no-vendor N62-class
  devices here) on ports 6608/6609; add a new vendor with another `jt808.New(...)` line
  and a distinct port. The universal-message `make` stays `jt808_19` (message-builder
  keeps the historical device type; golden test unaffected) and the serial prefix stays
  `JT808_`. A one-time idempotent DB migration in `store.go` renames the old `jt808`
  rows → `dfm-n62` (device registry, event_mappings, unit_settings, `device_port:`/`cap_`
  settings keys), preserving the approved device, its timezone offset, and any custom
  mappings. Admin: `deviceConfigKind("dfm-n62")`. `/unit-settings` → `/device-settings`
  (redirect kept). Adding a second make/model would also want per-model **capabilities**
  (today per-protocol) — the next incremental seam, since mappings are already per-model.

