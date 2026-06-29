# Navtelecom (NTCB/FLEX) integration plan

Plan for adding a **Navtelecom** unit type to `device-gateway`. Research-only —
no code yet. Target device: **Navtelecom START S-2011** (GPS-only 2G tracker,
BLE + backup battery; IMEI e.g. `863151075601887`). Deployment decision:
**multi-unit** — register in `cmd/gateway` alongside howen/fleetiger/cathexis on
its own port.

Source: `dfm-mvr-gateway/docs/Navtelecom/en_protocol_ntcb_last.pdf` — "Navtelecom
Communication Protocol v6.2" (2021).

## 1. What the device speaks

Navtelecom devices use two protocols over a server connection:

- **NTCB** (Navtelecom Binary) — transport + command/control. Used over GPRS/USB.
- **FLEX** — a telemetry extension carried inside the same TCP connection. FLEX is
  what streams GPS/IO records.

(There is also **NTCT**, a text protocol for SMS only — out of scope; we talk to
the device over TCP/GPRS.)

The device **always dials out** to the server (like Fleetiger). One TCP
connection carries **both** framings, distinguished by the first byte:

| First byte | Meaning |
|---|---|
| `@` (0x40) | NTCB packet: 16-byte transport header + application body |
| `~` (0x7E) | FLEX message (telemetry / FLEX service), no 16-byte header |
| `0x7F` (DEL) | FLEX keep-alive ping — **no response required** |

So our `ReadFrame` peeks the first byte and reads one of three shapes. This is
the single most important structural fact for the codec.

### 1.1 NTCB transport header (16 bytes)

| Offset | Field | Type | Notes |
|---|---|---|---|
| 0–3 | Preamble | char[4] | always `@NTC` (0x40 0x4E 0x54 0x43) |
| 4–7 | Recipient ID | U32 LE | receiver's device/server ID |
| 8–11 | Sender ID | U32 LE | sender's ID |
| 12–13 | n = body length | U16 LE | ≤ 65535 |
| 14 | CSd | U8 | XOR of the n body bytes |
| 15 | CSp | U8 | XOR of header bytes 0–14 |

IDs come from device config; defaults are server(host)=1, device=0. On a reply
the IDs swap (recipient↔sender). **All integers in NTCB/FLEX are little-endian.**
Checksum = simple XOR fold (spec Annex code). An empty body (n=0) packet is a
valid channel keep-alive and needs no response.

NTCB **command** packets (everything starting `*`, e.g. `*>S`, `*!EDITS`,
`*!1Y`) are application-layer **bodies inside an `@NTC` frame** — `*` is not one
of the first-byte markers above, it lives in the body.

### 1.2 FLEX messages (raw `~`, no 16-byte header)

CRC8 (table-based, Annex B) over the whole `~…` message including the `~`:

| Message | Direction | Server response (mandatory) |
|---|---|---|
| `~A <size:U8> <records…> <crc8>` | dev→srv | `~A <size> <crc8>` (echo count) |
| `~T <eventindex:U32> <record> <crc8>` | dev→srv | `~T <eventindex> <crc8>` |
| `~C <record> <crc8>` | dev→srv | `~C <crc8>` |
| `~E <count:U8> <records…> <crc8>` | dev→srv | `~E <count> <crc8>` (FLEX 2.0+) |
| `~X <eventindex:U32> <record> <crc8>` | dev→srv | `~X <eventindex> <crc8>` (FLEX 2.0+) |
| `0x7F` | dev→srv | none |

- `~A` = array of archived telemetry from non-volatile memory (≤ ~1.3 KB/packet).
- `~T` = a single out-of-order/high-priority event; **the device suspends all
  other traffic until we ACK it**, so the ACK path must be prompt.
- `~C` = current-state record (record index 0, event id `0xFF00`), used in place
  of a ping when there's live data.

The spec is explicit: **"Implementation of responses from the server … is
required for the correct operation of the device."** Miss an ACK and the device
retransmits (20–90 s backoff) and can lock the server after 3 failed attempts.

## 2. Connection handshake (device-initiated, over GPRS)

Three NTCB exchanges, then telemetry begins:

1. **Identity** — dev→`@NTC … *>S:<s>` where `*>S` = `2A 3E 53` and `<s>` is a
   `char[15]` ID string **containing the modem IMEI**. We reply
   `@NTC … *<S` (`*<S` = `2A 3C 53`). The IMEI is our device serial — extract it
   here, run it through `device.NormalizeSerial`, and authorize via `Deps.Auth`.
   (Our S-2011's IMEI is 15 digits → fits `char[15]`.)

2. **FLEX negotiation** — dev→`@NTC … *>FLEX <proto:U8> <proto_ver:U8>
   <struct_ver:U8> <data_size:U8> <bitfield[data_size/8 + 1]>`:
   - `*>FLEX` = `2A 3E 46 4C 45 58`; `<proto>` = `0xB0` (FLEX).
   - `proto_ver`/`struct_ver`: 0x0A=1.0, 0x14=2.0, 0x1E=3.0.
   - `data_size` = number of fields the mask covers (1.0→69, 2.0→122, 3.0→255).
   - **`bitfield`** = one bit per FLEX field; bit `i` set ⇒ field `i+1` is present
     in every telemetry record, packed in field-number order with no gaps.

   We reply `@NTC … *<FLEX <proto> <proto_ver> <struct_ver>` echoing the versions
   we accept. FLEX is backward compatible; if we only support 1.0 we answer 1.0
   and the device renegotiates down. **For v1 we accept whatever the device
   offers and decode by its mask** (we never need to force a version).

3. **Telemetry** — `~A` / `~T` / `~C` (and `~E`/`~X` if FLEX 2.0), each ACKed.

```
Device                         Gateway
  | @NTC *>S:<imei>      ---->  |
  |          <----  @NTC *<S    |   (authorize IMEI here)
  | @NTC *>FLEX,ver,mask ---->  |
  |       <----  @NTC *<FLEX    |   (capture & freeze the mask)
  | ~A <records>         ---->  |
  |            <----  ~A <n>    |   (emit each record, then ACK)
  | ...                         |
```

## 3. The mask-driven record decoder (core design)

Unlike Howen/Fleetiger (fixed frames), a FLEX **record's layout is dynamic**: it
is exactly the present fields (per the negotiated bitfield) concatenated in
field-number order, each at its **fixed, known size**. So:

- Keep a **static size table** indexed by FLEX field number (from Annex A.1).
  Sizes are constant per field (e.g. f1=4, f2=2, f3=4, f10=4, f11=4, f13=4,
  f14=2, f19=2, f29=1; multi-byte composites like f70=8, f73=16, f77=37).
- On handshake, parse the bitfield once → ordered `[]presentField` and a computed
  `recordLen` (sum of present sizes). Store on the session.
- Per `~A`: `count = size`, then slice `count × recordLen` bytes; for each record
  walk `presentField`, reading each by size, decoding only the fields we map
  (below) and skipping the rest by width. This is robust to any mask the device
  is configured with.

Panic-safety: like the other decoders, this parses untrusted bytes — bounds-check
every read, never index past the slice, and add a fuzz target (CLAUDE.md rule).

## 4. FLEX field → universal-message mapping (GPS-only subset)

We care about the FLEX 1.0 fields (1–69). The universal builder keys are in
`templates/gps-only/internal/protocol.go.tmpl`. Proposed mapping:

| FLEX field | Type | Decode | Universal key |
|---|---|---|---|
| 3 Event time | U32 | epoch s | `utc` |
| 2 Event ID | U16 | → mapping table | `event` (see §5); `0xFF00` = state-only |
| 8 GPS status | U8 | bit1=valid, bits2-7=sats | `positioning`, `satellites` |
| 10 Latitude | I32 | `value / 600000.0` | `latitude` |
| 11 Longitude | I32 | `value / 600000.0` | `longitude` |
| 12 Height | I32 | decimeters → m | `altitude` |
| 13 Speed | Float | km/h | `speed` |
| 14 Course | U16 | deg | `bearing` |
| 15 Mileage | Float | km | `mileage_km` / `odometer` |
| 7 GSM level | U8 | 0–31, 99=none | `signal` |
| 19 Main voltage | U16 | mV | `sensors`/`an_inputs` |
| 20 Backup voltage | U16 | mV | `sensors` |
| 21–28 Ain1–8 | U16 | mV | `an_inputs` |
| 29/30 Discrete In | U8 | bits = In1..16 | `inputs` |
| 31/32 Outputs | U8 | bits = Out1..16 | `outputs` |
| 4 Device status | U8 | bitfield (alarm, mode, power-save…) | derive events |

Lat/lon check: `33422389 / 600000 = 55.7040°` ≈ 55°42.2389'. ✓ Sign via I32.

For the **S-2011** specifically: it has IN1/IN2/IN4/IN5 (discrete, field 29),
AIN3 (field 23), O1 (field 31 bit0), main + backup voltage (f19/f20). All covered.

## 5. Events (`event_mappings` / MappingProvider)

FLEX field 2 is a U16 **event code that is device-configuration-specific** — the
spec has no universal event table; codes are assigned in the device's event
setup. This is exactly what our editable `event_mappings` exists for:

- Implement `gateway.MappingProvider` with `map_type` `"event_code"` (raw U16 →
  ACM Standard Event Code), seeded with sensible defaults and admin-editable.
- Reserve `0xFF00` = "current state, no event" (don't emit an `event`).
- Field 4 (Device status) bits give alarm/power-save/mode transitions; decide per
  bit whether to surface as events (e.g. `ALARM`, ignition from field-4/IO).

We'll need the customer's device event-code configuration to seed real defaults;
until then we map the obvious ones and pass unknown codes through verbatim.

## 6. Config capability — **yes, supported** (answers the original question)

The protocol fully supports remote read/write of device configuration over the
GPRS connection, so we can implement `gateway.ConfigController`:

- **Read**: `*!READ <space><page>:<tag>` → `*@READ OK,<page>:<tag>(v1,v2,…)`.
  Multiple page/tag in one command, comma-separated.
- **Write + reboot**: `*!EDITS <space><page>:<tag>(v1,v2,…)` → `*@EDITS OK,…`.
  Empty field (`,,`) = leave unchanged; `!` = reset to zero/erase.
- **Write without reboot**: `*!EDIT …` (same shape).
- Config model = **Page → Tag → ordered Parameters**, e.g.
  `TRANS:SRV1(FLEX,0,1,193.193.165.165,20966)`, `AP1(internet.mts.ru,mts,mts)`.
- These are NTCB command bodies (wrapped in the `@NTC` header), and also work
  over USB/Bluetooth/SMS.

How this maps to our framework:
- `ConfigController.RequestConfig(modules)` → issue `*!READ` per page/tag, parse
  the `*@READ` response into the `sc` map.
- `ConfigController.UpdateConfig(sc)` → build `*!EDITS`/`*!EDIT` from changed
  fields. The HTTP API (`GET`/`PUT /api/units/{serial}/config`) and admin config
  screen then work for free.

**Gap:** the *full* page/tag schema lives in a separate Navtelecom doc ("24xx
26xx Device Configuration") we don't have. We know the mechanism and the key
pages (`TRANS/SRV1-3`, `OBJECT`, `AP1/AP2`) but need that doc (or a config dump
from a real unit) to model the complete editable surface. The **FLEX telemetry
mask** itself is also editable: `*!SETFM <n>:<mask>` or via
`*!EDITS …(,&7[1111]20[00])` bitwise syntax — useful to provision which fields a
unit streams.

**Provisioning prerequisite:** before any of this works the unit must be pointed
at our gateway — `TRANS:SRV1` set to our host/port with `protocol=FLEX`. Done
once via the Navtelecom configurator (USB/BT) or an SMS `*!EDITS` command. The
gateway can't bootstrap a device that has never dialed in.

## 7. Other controllable commands (Annex D) — optional, later

If we later set `Capabilities.HasCommands` and implement `gateway.Commander`:
- Outputs: `*!1Y`/`*!1N` … `*!4Y`/`*!4N`, `*!SETOUT` (S-2011 has O1 → `*!1Y/1N`).
- Inputs: `*!OFF`/`*!ON` (lock/unlock).
- System: `*!DEV_RESET` (reboot), `*?V` (model/firmware), `*?S` (unique ID),
  `*!SETTIME`, `*?DATA` (diagnostics), `*?USSD`, `*!CHNGSIM`.
- Modes: `*!GY`/`*!GN` (arm/disarm security), `*!M` (operating mode).
- Telemetry replay: `*!REP_FL` (resend from NVRAM), `*!SYNC`.

`*Z` / `~Z` are the device's "unsupported message" responses — handle defensively.

## 8. Capabilities & deployment

For v1 (GPS + IO telemetry only):

```go
func (*Protocol) Capabilities() gateway.Capabilities { return gateway.Capabilities{} }
```

Add `HasConfig` (+ `ConfigController`) once §6 is modelled; `HasCommands` (+
`Commander`) if/when output/reboot control is wanted. No video/snapshots ever.

- `DefaultDevicePort()` → pick a free port (fleetiger uses 8050; navtelecom uses 4000).
- `IdleTimeout()` → generous (device pings via 0x7F but data cadence varies);
  start at 6 min like fleetiger and tune.
- Register one line in `cmd/gateway/main.go`: `navtelecom.New()`.
- Lifecycle: authorize on `*>S`, `Hub.Register` after FLEX negotiation, mark
  online/offline via `Deps.Auth.UpdateStatus` (mirror fleetiger).

## 9. Open questions / risks

1. **No real packet capture yet.** Endianness, CRC8 seed, and the exact handshake
   bytes should be validated against a live S-2011 (or a captured session) before
   trusting the decoder. Build a golden test from a real capture.
2. **FLEX version the S-2011 actually negotiates** (1.0 vs 3.0) — decoder is
   mask-driven so it shouldn't matter, but confirm `data_size`/mask length math
   (`data_size/8 + 1`) against a real handshake.
3. **Device event-code table** needed to seed `event_mappings` meaningfully.
4. **Full config page/tag schema** (the 24xx/26xx config doc) needed for a
   complete `ConfigController`; partial is fine to start.
5. **Default IDs / ID-string format** — confirm whether the S-2011 sends non-zero
   NTCB IDs and the exact 15-char ID-string layout around the IMEI.
6. **Reply header construction** — confirm a real unit accepts our `*<S`/`*<FLEX`
   header (ID swap + XOR checksums) and doesn't require matching configured IDs.

## 10. Implementation phases (proposed)

- **P0 (this doc).** Protocol research + plan. ✅
- **P1 — connect + GPS. ✅ DONE — validated against a real START S-2011.**
  Live on staging: a real unit (IMEI 863151075601887) completed the handshake,
  negotiated FLEX 1.0 (20 fields, 51-byte records), and streamed a correct
  Gauteng fix (lat −26.0842, lon 27.9376). One real bug was found and fixed: the
  FLEX negotiation bitfield is **MSB-first** within each byte (field 1 = bit 7 of
  byte 0), not LSB-first — the wrong order mis-aligned record fields while still
  matching record length and CRC. Locked down with a golden test from the real
  capture (`codec_test.go: TestRealCaptureGolden`) + a spec-example bit-order test.
  Plugin in `internal/navtelecom/` (`codec.go` + `protocol.go`), registered in
  `cmd/gateway` on port 4000. Implemented: dual `ReadFrame` (`@`/`~`/`0x7F`),
  NTCB header parse/build with XOR checksums, the `*>S`/`*>FLEX` handshake +
  `*<S`/`*<FLEX` replies (version capped at 1.0), CRC8 table, mask parser +
  static field-size table (fields 1–142), the mask-driven record decoder, GPS/IO
  payload mapping (§4), and the mandatory `~A`/`~T`/`~C` ACKs. Tested:
  `codec_test.go` (CRC8 vs bitwise, NTCB round-trip, mask, record, payload,
  negotiation), `integration_test.go` (full handshake → `~A` → webhook through the
  real server; ping-no-response), and two fuzz targets (`ReadFrame`,
  `decodeRecord`) — all green under `-race`.
  **Still needs:** validation against a real S-2011 capture (handshake bytes,
  CRC8 seed, endianness, lat/lon) and a golden test built from it — see §9.
- **P2 — events.** `MappingProvider` for field-2 codes + field-4 status; seed
  defaults from the customer's event config.
- **P3 — config.** `ConfigController` (`*!READ`/`*!EDITS`) once the page/tag
  schema is available; admin config screen lights up automatically.
- **P4 (optional) — commands.** `Commander` for outputs / reboot / modes.

Validate against staging (`ssh lab`) with a real S-2011 dialled in before merge.
