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
- **Video transport: separate media port.** We control the server IP/port inside
  the `0x9101`/`0x9201` commands, so the N62's JT1078 stream is pointed at a
  dedicated JT808 media port (default **6609**, `DefaultMediaPort()`), served by a
  `MediaServerProvider` listener that parses ULV `0x30316364` frames — mirroring
  howen/cathexis. The control listener on 6608 therefore only ever decodes JT808
  `0x7E` frames. (Risk: confirm the N62 honours the advertised port and doesn't
  insist on the control port; fallback is same-port magic detection in
  `ReadFrame`. Validate early on staging.)
- **Build sequence: GPS-first.** Phase 1 lands connect + GPS + events + ULV
  config read/write + status, validated against the live N62, before video.

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
Capabilities{HasCommands: true, HasConfig: true, HasStatus: true,  // phase 1
             HasVideo: true, HasSnapshots: true}                    // phase 2
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
- Phase 2: `VideoController` (`0x9101/0x9102` live, `0x9201/0x9202` clips,
  `0x9205→0x1205` recordings), `MediaServerProvider` (ULV stream listener →
  `media.Manager` live / `media.ClipRegistry` clips), `Snapshotter` (`0x0800/
  0x0801` push + `0x9206` file pull).

## 4. Files

`internal/jt808/`: `codec.go` (framing, header, location/TLV parse, builders,
`0x0900` parse), `events.go` (`MappingProvider` + maps), `gps.go` (normalized
payload), `server.go` (Protocol/Session/Commander/lifecycle), `config.go`
(`ConfigController`), `status.go` (`StatusReporter`); phase 2 adds `video.go`,
`media.go`, `recordings.go`, `snapshot.go`. Tests: `codec_test.go` (framing/
checksum/header/location golden from a real capture), `events_test.go`,
`integration_test.go` (register→location→webhook), `fuzz_test.go` (ReadFrame +
location decode). `cmd/jt808/main.go` = `app.Run(jt808.New())`; one line in
`cmd/gateway/main.go`; `JT808_PORT`/`6608` + `6609` port mapping in
`deploy/docker-compose.yml`.

## 5. Validation prerequisite

The live N62 currently points at the old servers (`NetCms ServersAddr
185.202.223.35:6608` / `156.38.206.106:6608`) and its LAN UI (`192.168.100.202`)
isn't reachable from the build environment. To validate on staging (`ssh lab`)
the unit must be repointed to the lab host on **6608** with `Protocol` JT808 —
done via the device UI/app, or (once it dials in once) via `update_config`
`NetCms`. Serial expected: `JT808_<phone digits>` (label ID `000000096750`).

## 6. Phases

- **P1 — connect + GPS + events + config + status.** This branch (`feature/jt808`).
  Validate against the live N62 on staging before merge.
- **P2 — video.** Live HLS (`0x9101`), recorded clips (`0x9201`), recordings
  query (`0x9205`), snapshots (`0x0800/0x0801`, `0x9206`) via a separate media
  port. Validate live stream + a pulled clip on staging.
</content>
</invoke>
