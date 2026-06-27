# Howen event-code mapping — improvement analysis

Status: analysis + first implementation batch (branch `feature/howen-mappings`).
Authoritative sources used:

- **Official spec PDF** `dfm-mvr-gateway/docs/Howen/Howen Device Communication
  Protocol (H-Protocol)-V4.0.0_eng_2026.03.11.pdf` — §3.3.x alarm-detail field
  definitions (`det`).
- **Online master event-code list** (the spec now points `ec`/`tp` to this sheet):
  <https://docs.google.com/spreadsheets/d/1zLB_oTg2bKf1Dfzwkdfc_yps-AaVYEAtlWOCo_QL9aI>.
  Snapshot saved as `docs/Howen_master_event_codes_2026.csv`.
- Current implementation: `internal/howen/events.go`.

## How mapping works today

`mapHowenEventCodes(model, ec, detail, alarm)` (`internal/howen/events.go:317`)
switches on the main alarm code `ec`. Some codes are resolved by built-in logic
(video-loss channel, speeding/idling start-vs-end, parking/trip literals); codes
`12/15/28/30` dispatch into editable **sub-tables** keyed by a `det` sub-field
(`dt`/`st`/`tp`); everything else falls through to the editable `event_code`
table. All tables are DB-overridable live.

## Confidence model

For every `ec` we have independent **live** confirmation of (12 vibration, 28
voltage, 30 DMS), the master sheet's numbering matches our code. That makes the
sheet's numbering trustworthy transitively, so where the sheet **disagrees** with
us on an unconfirmed code it is very likely our code that is wrong.

Legend: ✅ implemented in this batch · 🔎 needs a live event to confirm before
shipping to `main` · 🧩 new event type (additive) · 📐 architectural.

---

## 1. Main-code (`ec`) errors — wrong label for the code

| ec | Spec meaning (master sheet) | Current mapping | Proposed | Notes |
|----|-----------------------------|-----------------|----------|-------|
| **13** | Geofence (`st` sub-table) | *not handled* — fell to `ALARM` | dispatch to `geofence_status` | ✅ now keyed on 13 (was wrongly on 15) — 🔎 confirm on a drive |
| **15** | Abnormal open/close **door** (`st` 0/1) | dispatched to `geofence_status` ❌ | `ALARM:DOOR_OPEN`/`INPUT:DOOR_CLOSE` | ✅ door handler added — 🔎 confirm on a drive |
| **22** | **Swipe card** (`tp` 1=driver/2=student/3=invalid) | `MESSAGE:BUFFERED` ❌ | `CARD:DRIVER`/`STUDENT`/`INVALID`/`SWIPE` | ✅ implemented — 🔎 confirm with a card swipe |
| **80** | ADAS auto-calibrate **result** (`rc` 0/1) | `ALARM:HARDWARE:FAULT` ❌ | `AI:CALIBRATION` | ✅ clearly not a hardware fault |
| 6 | Low speed alarm | `ALARM` | `SPEED:LOW` | ✅ matches existing vocab |
| 2 | Motion detection | `ALARM` | `ALARM:MOTION` | ✅ |
| 78 | AI camera install self-check abnormal | `AI:TAMPER` | `AI:INSTALL:ABNORMAL` | ✅ tamper ≠ install check |

> The **15→door / 13→geofence** swap is the single highest-impact correction.
> Neither gateway ever had a live geofence capture (old JS has no provenance note
> on `case 15`, unlike `case 41/43` which are marked "inferred from flespi"). The
> spec is internally consistent and current (V4.0.0, MC30 column = supported), so
> the swap is almost certainly correct — but it changes the event emitted for a
> real geofence crossing, so confirm with one drive-through before merging.

## 2. Sub-table corrections (keyed off a confirmed `ec`)

### Input `num` — ec 4/5 (spec §2 / §3.8) — currently several are wrong
| num | Spec | Current | Proposed |
|-----|------|---------|----------|
| 5 | Low beam | *(unmapped→ec map)* | `INPUT:LOW_BEAM` 🧩 |
| 6 | High beam | *(unmapped)* | `INPUT:HIGH_BEAM` 🧩 |
| 9 | Right turn | `ALARM` ❌ | `INPUT:TURN_RIGHT` |
| 10 | Left turn | `ALARM` ❌ | `INPUT:TURN_LEFT` |
| 12 | Reverse | `ALARM` ❌ | `INPUT:REVERSE` |
| **14** | **Front door closed** | `ALARM:DOOR_OPEN` ❌ | `INPUT:DOOR_CLOSE` |
| **15** | **Mid door closed** | `ALARM:DOOR_OPEN` ❌ | `INPUT:DOOR_CLOSE` |
| **16** | **Rear door closed** | `ALARM:DOOR_OPEN` ❌ | `INPUT:DOOR_CLOSE` |
| 17 | Intercom | *(unmapped)* | `INPUT:INTERCOM` 🧩 |
| 18 | Lift | *(unmapped)* | `INPUT:LIFT` 🧩 |
| 23 | Safe to load | *(unmapped)* | `INPUT:SAFE_TO_LOAD` 🧩 |

The 14/15/16 "door **closed**" → `DOOR_OPEN` mapping is an outright inversion.

### Geofence `st` — (spec §7) — 3/4/5/9/10 wrong
| st | Spec | Current | Proposed |
|----|------|---------|----------|
| 3 | Over-speed **warning** | `SPEEDING` (loses zone) | `ZONE:SPEEDING:WARNING` |
| 4 | Low-speed alarm | `ZONE:UNKNOWN_EVENT` ❌ | `ZONE:SPEED_LOW` |
| 5 | Low-speed warning | `ZONE:UNKNOWN_EVENT` ❌ | `ZONE:SPEED_LOW:WARNING` |
| 9 | **Pre-entry** | `ZONE:ENTER` (conflated) | `ZONE:PRE_ENTER` |
| 10 | **Pre-exit** | `ZONE:EXIT` (conflated) | `ZONE:PRE_EXIT` |

### Voltage `dt` — ec 28 (spec §14) — 2/6/7 wrong
| dt | Spec | Current | Proposed |
|----|------|---------|----------|
| 2 | High voltage | `BATTERY:ABNORMAL:EXT` | `BATTERY:HIGH:EXT` |
| 5 | Suspicious disconnection | `BATTERY:DISCONNECTED` ✓ | keep |
| 6 | Abnormal shutdown | `BATTERY:LOW:EXT` ❌ | `POWER:OFF:ABNORMAL` |
| **7** | **Start up** | `BATTERY:ABNORMAL:EXT` ❌ | `POWER:ON` |

### Vibration `dt` — ec 12 (spec §6) — labels OK, two clarified
dt 5 = tilt → `COLLISION:TURN_OVER` (keep); dt 6 = turn-over → `HARSH:CORNERING`
is debatable (spec calls it "turn"/rollover). Left as-is; low priority.

## 3. DMS/ADAS/BSD `tp` additions — ec 30 (master sheet)
Current `dms_adas` table is good but missing newer subtypes:
| tp | Spec | Proposed |
|----|------|----------|
| 41 | Co-driver detection | `AI:CODRIVER` 🧩 |
| 71 | Drinking water | `AI:DRINKING` 🧩 |
| 81 | Driver auth succeeded | `AI:AUTH:OK` 🧩 |
| 82 | Driver auth failed | `AI:AUTH:FAIL` 🧩 (current 82→NO_DRIVER ❌) |
| 87 | Eating | `AI:EATING` 🧩 |
| 88–94 | Drowsiness/yawn/blink levels | `AI:DROWSINESS` family 🧩 |

(BSD 96–107 already collapse to `AI:BLINDSPOT`; level granularity optional.)

## 4. New event types worth mapping (currently `ALARM`/`HARDWARE:FAULT`) 🧩
| ec | Spec | Proposed |
|----|------|----------|
| 33 | GPS antenna break | `GPS:ANTENNA:BREAK` |
| 34 | GPS antenna short | `GPS:ANTENNA:SHORT` |
| 36 | CANBUS connection abnormal | `CANBUS:ABNORMAL` |
| 37 | Towing | `TOWING:START` (already used by other units) |
| 40 | Vehicle move | `VEHICLE:MOVE` |
| 44 | GPS location recover | `GPS:RECOVER` |
| 49 | Load alarm (`dt` 0/1/2) | `LOAD:OVERLOAD` / `LOAD:UNDERLOAD` / `LOAD:ABNORMAL` |
| 50 | SIM card lost | `SIM:LOST` |
| 60 | Satellite modem status | `SATELLITE:ABNORMAL` |
| 61 | Alcohol detection | `ALARM:ALCOHOL` |
| 72/73/74 | Rear seatbelt unbuckled | `AI:SEATBELT:REAR` |
| 76 | GPS signal interfered | `GPS:JAMMED` |
| 769 | Tire pressure | `TIRE:PRESSURE` |
| 770 | Disk detection | `ALARM:HARDWARE:FAULT` (keep) |

## 5. Architectural — `ec=771` Datahub/OBD is telemetry, not an alarm ✅ DONE
`ec=771` carries `det` = `{rpm, spd, fu(el), ct(coolant), ds(distance), ml,
acc, hin/hout/in/out}` — an OBD/CAN snapshot. **Implemented:** `handleAlarmData`
now intercepts ec=771 and forwards it as a **`gps` telemetry message**
(`buildDatahubPayload`), mapping the fields into the universal `sensors`
(`engine_rpm`, `obd_speed`, `coolant_temp_c`, `accel_pedal_pct`, `obd_distance`,
`fuel_level`, `trip_fuel_used_cc`) plus `inputs`/`outputs` bit strings, with the
raw block retained under `howen_datahub`. `resolveSensors` (message builder)
gained the new sensor keys; the contract's `message_type` stays `"gps"`. Golden
test unaffected (new keys only present for ec=771).

`ec=29` people-counting → `PEOPLE:COUNT` and `ec=772` self-check →
`DEVICE:SELF_CHECK` (no longer generic `ALARM`/`HARDWARE:FAULT`); structured
parsing of their payloads (passenger tallies, per-module diagnostics) is future
work.

## 6. Non-mapping completion items
- **Snapshots** `0x4020`/`0x1020` — **Phase 1 DONE.** `RequestSnapshot`
  (`internal/howen/snapshot.go`) sends the 0x4020 capture request and parses the
  0x1020 response; exposed via `gateway.Snapshotter` + `Hub.Snapshotter` and
  `POST /api/units/{serial}/snapshots` (body `{channels:[0..],resolution:0}`).
  The response returns the device-side JPEG **file paths** (per §2.5 the device
  writes them to its SD card, not inline).
- **Snapshots Phase 2 DONE.** `CaptureImage` (`snapshot.go`) captures one camera
  then fetches the JPEG bytes via the file-transfer path (`0x4090`/`0x1090`, new
  codec consts): the device dials the media port and streams the file as `0x0011`
  frames (zero-length frame = EOF), buffered in-memory by `media.SnapshotFetch`
  (shared via `Deps.Snapshots` + `NewMediaServer`). Returned inline by
  `POST /api/units/{serial}/snapshot/image?camera=&resolution=` as `image/jpeg`.
  No DB/bucket — the image is fetched and returned synchronously. End-to-end
  tested (`TestEndToEndSnapshotImage` drives both the control + media connections).
- **Snapshots: search + save DONE.** `SearchSnapshots` lists stills stored on the
  device (file query `0x4060`, `ft=3`/`4`; "all cameras" queries each channel and
  merges — the device rejects `chl=0`). `FetchSnapshotFile` downloads one by path.
  Save-to-gateway persists a JPEG under `CLIPS_ROOT/snapshots` + a `snapshots` DB
  row. API: `/snapshots/search`, `/snapshots/file`, `/snapshots/save`,
  `GET /api/snapshots`, `…/download`, `DELETE`. Admin Snapshots tab has capture,
  capture-and-save, device search, and a saved-on-gateway list (lightbox preview).
  **Future polish:** retention/cleanup of saved snapshots; optional external
  bucket; reuse the file-transfer primitive to download event-media by path.
- Surface the richer `det` numeric fields (speed `max/avg/cur`, voltage `cur`,
  temperature `cur`, alcohol `cur`) on the event payload — today only the mapped
  label is emitted; the magnitudes are dropped.

## Priority / status
1. **Done (branch `feature/howen-mappings`, tested):** §1 (80, 6, 2, 78), §2
   voltage dt 7/2/6, §2 input 14/15/16 inversion + turn/reverse/beams, §2 geofence
   st 3/4/5/9/10, §3 tp 82 fix + 41/71/81/87 additions, §4 additive labels, AND
   the §1 `13↔15` geofence/door swap + ec 22 swipe-card.
2. **Confirm on a live drive before merge to `main`:** the geofence/door swap and
   swipe-card — they change emitted events and had no original live capture.
3. **Separate branches:** §5 OBD/telemetry split (ec 771/29/772), §6 snapshots.
