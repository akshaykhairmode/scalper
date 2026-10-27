# Kotak Neo HSM / HSI WebSocket Protocol

> **Status:** Reverse-engineered from the official SDK. The wire layout below is
> complete enough to implement and unit-test the Go codec **without credentials**.
> Live confirmation is scheduled for Phase 0B; see
> [Verification checklist](#11-verification-checklist).
>
> **Sources**
>
> - `Websocket_(2).zip` → `Websocket/hslib.js` (`LIB_VERSION = "3.0.0"`)
> - `Websocket_(2).zip` → `Websocket/demo/demo.js`, `demo.html`, `neo.js`
> - [`kotak-neo-api-v2.md`](../kotak-neo-api-v2.md) §13 (NEO WebSocket)
>
> **Related:** [`PLAN.md`](../PLAN.md) §2.3, §2.4, Phase 0 · [`GO_STANDARDS.md`](../GO_STANDARDS.md)

---

## 1. Summary

| Feed | Endpoint | Encoding | Purpose | Schema status |
| --- | --- | --- | --- | --- |
| **HSM** | `wss://mlhsm.kotaksecurities.com` | Binary, big-endian | Market ticks (index, scrip, depth) | **Known** (this document) |
| **HSI** | `wss://mis.kotaksecurities.com/realtime` (or datacenter host) | JSON text | Order lifecycle events | **Unknown** — capture in Phase 0B |

Key facts that change the implementation:

1. HSM **requests are binary**, even though the JS SDK exposes a JSON-style
   `send()`. The SDK serializes `{"type":"cn", ...}` and `{"type":"mws", ...}`
   into the packet format in §2 before writing to the socket.
2. HSM prices are **fixed-point signed 32-bit integers**, not IEEE-754
   `float32`. Decode with `raw / (multiplier × 10^precision)`.
3. The connection response carries an **ACK threshold**; when it is non-zero,
   every data frame starts with a 4-byte message number and the client must ACK
   every *N* data frames.
4. Topic IDs are assigned by the server in snapshot packets. Updates contain
   only the topic ID and positional field values — no name, symbol or field IDs.
5. No compression is applied on the binary HSM path. `pako` is present in the
   bundle but is not used for HSM frames (and not for HSI text frames in the
   SDK's HSI wrapper).
6. The SDK sends a binary "throttling interval" packet (type 2, value 0) every
   30 seconds as a keepalive.

---

## 2. HSM requests

### 2.1 Primitives

All multi-byte integers are **big-endian** (`hslib.js: buf2Long`, `ByteData`).

| Type | Size | Notes |
| --- | --- | --- |
| `u8` | 1 | |
| `u16` | 2 | |
| `u32` | 4 | message number, topic ID |
| `i32` | 4 | fixed-point value or long field |
| `i64` | 8 | option-chain strike price (type 10 only) |
| bytes | n | ASCII/UTF-8; lengths are byte counts, not runes |

### 2.2 Request frame

```text
u16  payloadLen          // bytes after this field
u8   packetType          // 1..10, see table
u8   fieldCount
repeat fieldCount:
  u8   fieldID
  u16  valueLen
  u8[valueLen] value
```

`payloadLen = 1 + 1 + Σ(1 + 2 + valueLen)`.

The SDK's `markEndOfMsg()` computes exactly this value; the frame is a single
WebSocket binary message (batching multiple request packets is not done by the
SDK).

### 2.3 Request types

| Type | Name | Fields | Purpose |
| --- | --- | --- | --- |
| `1` | `CONNECTION_TYPE` | `1` Authorization token, `2` Sid, `3` source | Authenticate the socket |
| `2` | `THROTTLING_TYPE` | `1` `u32` interval | SDK sends `0` every 30 s (keepalive) |
| `3` | `ACK_TYPE` | `1` `u32` message number | Acknowledge data frames |
| `4` | `SUBSCRIBE_TYPE` | `1` scrip blob, `2` `u8` channel | Subscribe (scrip/index/depth) |
| `5` | `UNSUBSCRIBE_TYPE` | `1` scrip blob, `2` `u8` channel | Unsubscribe |
| `7` | `CHPAUSE_TYPE` | `1` 8-byte channel bitmask | Pause channels |
| `8` | `CHRESUME_TYPE` | `1` 8-byte channel bitmask | Resume channels |
| `9` | `SNAPSHOT` | `1` scrip blob | Request a one-shot snapshot |
| `10` | `OPC_SUBSCRIBE` | `1` key, `2` `i64` strike, `3` high, `4` low, `5` channel | Option-chain stream (not used in v1) |

Field IDs are per packet type; the same numeric ID means different things in
different requests.

### 2.4 Scrip blob

Used by types 4, 5 and 9:

```text
u16  count                       // max 100 (SDK MAX_SCRIPS)
repeat count:
  u8   len
  u8[len] scrip                  // "<prefix>|<exchange_segment>|<instrument>"
```

Prefix by subscription kind (added by the SDK, not by the caller):

| Prefix | Subscribe JSON type | Topic name prefix | Instrument value |
| --- | --- | --- | --- |
| `sf` | `mws` / `mwu` | `sf` | F&O `pSymbol` (option) |
| `if` | `ifs` / `ifu` | `if` | Index name, exact and case-sensitive |
| `dp` | `dps` / `dpu` | `dp` | F&O `pSymbol` (option) |

Examples:

```text
sf|nse_fo|12345
if|nse_cm|Nifty 50
dp|nse_fo|12345
```

The caller-level JSON uses `"scrips":"nse_fo|12345&nse_fo|12346"`; the SDK
prepends the prefix to every `&`-separated item. A trailing `&` is tolerated by
the SDK (it strips it), but Go must not produce one.

### 2.5 Connection

Authorization mode (`demo.js`) is serialized as:

| Field | Value |
| --- | --- |
| 1 | Session token (the HSM `Authorization` value) |
| 2 | Session `Sid` |
| 3 | `JS_API` |

The SDK's alternative mode sends `x-access-token` instead of token + Sid; v1
uses the token + Sid mode because the MPIN validation returns both.

Wire example for token `T`, sid `S` (21 bytes):

```text
00 13 01 03 01 00 01 54 02 00 01 53 03 00 06 4a 53 5f 41 50 49
```

### 2.6 Subscribe / unsubscribe

| Field | Value |
| --- | --- |
| 1 | Scrip blob (§2.4) |
| 2 | Channel number, `u8` (1–16 per docs; SDK pause/resume allows 1–64) |

```text
subscribe("nse_fo|12345", channel 1)
00 1b 04 02 01 00 12 00 01 0f 73 66 7c 6e 73 65 5f 66 6f 7c 31 32 33 34 35 02 00 01 01
```

Unsubscribe is identical with type `05`.

### 2.7 Snapshot request

Type `9`, field `1` = scrip blob. No channel field. The server answers with a
`SNAPSHOT` status response (type 9) and then a data frame containing snapshot
packets (`'S'`). Use it to prime values immediately after a reconnect instead
of waiting for the next tick.

### 2.8 ACK

Type `3`, field `1` = the message number (`u32`) of the data frame being
acknowledged. Frame length is always 11 bytes:

```text
ack(msgNum = 42)
00 09 03 01 01 00 04 00 00 00 2a
```

### 2.9 Keepalive

```text
type 2, field 1 = u32 0
00 09 02 01 01 00 04 00 00 00 00
```

Sent every 30 s by the SDK. Its documented name is "throttling interval"; the
SDK calls it from the heartbeat timer. Replicate the behaviour (30 s, value 0)
and treat a send failure as a connection failure.

### 2.10 Channel pause / resume

Type 7 (pause) and type 8 (resume). Field 1 is 8 bytes holding two 32-bit
bitmasks: channels 33–64 first, then channels 1–32. Only needed if the UI wants
to stop ticks while a tab is hidden; v1 polls snapshots after resume instead.
Not required for the initial implementation.

### 2.11 Option chain (type 10, not used in v1)

Fields: `1` key (string), `2` strike price (`i64`), `3` high-strike flag,
`4` low-strike flag, `5` channel. The response (type 10) carries the key and a
JSON string under `data` containing the chain scrips. Deferred; the ladder is
built from the Scrip Master and polled via REST.

---

## 3. HSM responses

### 3.1 Frame header

```text
u16  payloadLen          // bytes after this field; SDK does not validate it
u8   packetType
```

`payloadLen` is redundant because one WebSocket message holds exactly one
frame, but Go must validate it to detect truncation/corruption early.

### 3.2 Connection response (type 1)

```text
u8   fieldCount
repeat fieldCount:
  u8   fieldID
  u16  valueLen
  u8[valueLen] value
```

- First field value is the status: ASCII `K` = OK, `N` = failure.
- Second field (when `fieldCount >= 2`) is the **ACK threshold**. The SDK reads
  it as a big-endian unsigned integer and stores it globally.
- `fieldCount == 1` means connected with ACK disabled.
- Any other count is a protocol error.

> **Ambiguity to resolve in Phase 0B:** the SDK applies `buf2Long` to the raw
> bytes, which implies a binary integer. A textual number (ASCII digits) would
> decode as `0x35` for `"5"`. The Go codec therefore logs the raw bytes and the
> decoded threshold, accepts both interpretations only when unambiguous
> (multi-byte ASCII digits or a value ≤ 255), and otherwise disables ACK with a
> `warn`. Captured traffic settles this.

### 3.3 Status responses (types 4, 5, 7, 8, 9, 10)

```text
u8   fieldCount
if fieldCount > 0:
  u8   fieldID            // ignored
  u16  valueLen
  u8[valueLen] status     // ASCII "K" or "N"
```

Map to events: subscribe ACK/NACK, unsubscribe ACK/NACK, snapshot ACK/NACK,
channel pause/resume ACK/NACK. Type 10 additionally carries `key` and a JSON
`scrips` payload after the status.

A NACK is a real error: log it with the affected instrument set and surface it
in feed health. Do not silently retry forever.

### 3.4 Data frame (type 6)

```text
u16  payloadLen
u8   packetType = 6
[u32 msgNum]              // present only when ackThreshold > 0
u16  topicPacketCount     // "g" in hslib.js
repeat topicPacketCount:
  u16  packetLen          // length of the topic packet (SDK skips it)
  u8   responseType       // 0x53 'S' = snapshot, 0x55 'U' = update
  u32  topicID
  ... payload depends on responseType ...
```

Notes:

- `msgNum` is consumed **before** the topic count and is the value echoed in
  the ACK.
- The SDK skips `packetLen` without reading it. Go should read it, validate it
  against the bytes consumed by the topic packet, and fail the frame on a
  mismatch (log a bounded hex prefix; never panic).
- A frame may contain several topic packets, mixing snapshots and updates.

### 3.5 Snapshot packet (`'S'`, 0x53)

```text
u32  topicID
u8   nameLen
u8[nameLen] topicName          // "sf|...", "if|...", "dp|..."
u8   valueCount
i32[valueCount] values         // index == field ID (positional)
u8   stringCount
repeat stringCount:
  u8   fieldID                 // 51..54
  u8   len
  u8[len] value
```

- `values[i]` is the raw value for field ID `i`. The array is **positional**,
  starting at field 0; a snapshot may stop after the last populated field.
- Strings are separate and sparse (`51` name, `52` symbol/token, `53` exchange,
  `54` trading symbol).
- The snapshot replaces any cached topic with the same ID and establishes the
  topic name → ID mapping. It also carries `multiplier`/`precision` for the
  instrument, which must be stored before decoding prices.
- Update packets never repeat the name or strings, so a topic must be primed by
  a snapshot first.

### 3.6 Update packet (`'U'`, 0x55)

```text
u32  topicID
u8   valueCount
i32[valueCount] values         // index == field ID (positional)
```

- Values are again positional from field 0. Unchanged fields are sent as the
  sentinel `-2147483648` (`TRASH_VAL`); the SDK ignores those.
- There are no strings and no explicit field IDs. If the topic ID is unknown,
  the SDK crashes; Go must log `unknown topic id` with the ID and skip the
  packet (the frame remains usable).
- Multiplier/precision are not refreshed on updates. A reconnect creates new
  topics and new IDs, so resubscribe + snapshot is mandatory after reconnect.

### 3.7 Worked synthetic data frame

An index snapshot for topic ID 1, name `if|nse_cm|Nifty 50`, with
`multiplier=1`, `precision=2`, LTP raw `2425015` → `24250.15`:

```text
00 60                          frame payload length = 96
06                             data
00 01                          one topic packet
00 5b                          topic packet length = 91
53                             snapshot
00 00 00 01                    topic ID
12                             name length = 18
69 66 7c 6e 73 65 5f 63 6d 7c 4e 69 66 74 79 20 35 30   "if|nse_cm|Nifty 50"
0a                             ten values (fields 0..9)
00 00 00 00  00 00 00 00       fields 0,1 (dates)
00 25 00 b7                    field 2 = LTP 2425015
00 24 ed 20                    field 3 = close 2420000
00 00 00 00  00 00 00 00  00 00 00 00  00 00 00 00   fields 4..7
00 00 00 01                    field 8 = multiplier 1
00 00 00 02                    field 9 = precision 2
03                             three strings
34 08 4e 69 66 74 79 20 35 30    fid 52 (symbol) = "Nifty 50"
35 06 6e 73 65 5f 63 6d          fid 53 (exchange) = "nse_cm"
36 05 4e 49 46 54 59             fid 54 (trading symbol) = "NIFTY"
```

This frame is synthetic; it is a seed for codec tests and will be replaced by a
captured frame in Phase 0B. The same generator should be used to produce
scrip and depth fixtures.

---

## 4. Topics and identity

| Topic name | Feed | Meaning |
| --- | --- | --- |
| `sf\|<exchange>\|<token>` | Scrip | F&O option contract |
| `if\|<exchange>\|<name>` | Index | Index spot |
| `dp\|<exchange>\|<token>` | Depth | 5-level book for the selected contract |

- **Topic ID is server-assigned** and scoped to the connection. Never persist
  it, never assume it equals the token, never reuse it after reconnect.
- The application identity remains the instrument key
  (`exchange_segment|token`), mapped from the topic name.
- Duplicate snapshots for the same ID replace the cached snapshot.
- Updates for an ID that has not been snapshotted are dropped with a `warn`
  (and a metric), never fatal.

---

## 5. Field tables

`FIXED` = fixed-point int32 (§6.1). `LONG` = signed 32-bit integer.
`DATE` = Unix timestamp in seconds.

### 5.1 Scrip (`sf`, options)

| ID | Name | Type | Meaning |
| --- | --- | --- | --- |
| 0 | `ftm0` | DATE | feed time |
| 1 | `dtm1` | DATE | date/time field |
| 2 | `fdtm` | DATE | feed date/time |
| 3 | `ltt` | DATE | last trade time |
| 4 | `v` | LONG | volume |
| 5 | `ltp` | FIXED | last traded price |
| 6 | `ltq` | LONG | last traded quantity |
| 7 | `tbq` | LONG | total buy quantity |
| 8 | `tsq` | LONG | total sell quantity |
| 9 | `bp` | FIXED | best bid |
| 10 | `sp` | FIXED | best ask |
| 11 | `bq` | LONG | best bid quantity |
| 12 | `bs` | LONG | best ask quantity (`bs` in SDK) |
| 13 | `ap` | FIXED | average price / VWAP |
| 14 | `lo` | FIXED | open |
| 15 | `h` | FIXED | high |
| 16 | `lcl` | FIXED | low |
| 17 | `ucl` | FIXED | upper circuit |
| 18 | `yh` | FIXED | year high |
| 19 | `yl` | FIXED | year low |
| 20 | `op` | FIXED | open price |
| 21 | `c` | FIXED | previous close |
| 22 | `oi` | LONG | open interest |
| 23 | `mul` | LONG | multiplier |
| 24 | `prec` | LONG | precision (decimal places) |
| 25 | `cng` | FIXED | change (SDK computes from LTP − close) |
| 26 | `nc` | STRING | percent change (SDK computes) |
| 27 | `to` | FIXED | turnover (SDK computes volume × VWAP) |
| 51 | `name` | STRING | topic/feed name |
| 52 | `tk` | STRING | symbol / token |
| 53 | `e` | STRING | exchange segment |
| 54 | `ts` | STRING | trading symbol |

For our use: **LTP 5, volume 4, previous close 21, OI 22, multiplier 23,
precision 24, best bid 9, best ask 10, last trade time 3**.

### 5.2 Index (`if`)

| ID | Name | Type | Meaning |
| --- | --- | --- | --- |
| 0 | `ftm0` | DATE | feed time |
| 1 | `dtm1` | DATE | date/time field |
| 2 | `iv` | FIXED | index value (LTP) |
| 3 | `ic` | FIXED | previous close |
| 4 | `tvalue` | DATE | time value |
| 5 | `highPrice` | FIXED | day high |
| 6 | `lowPrice` | FIXED | day low |
| 7 | `openingPrice` | FIXED | day open |
| 8 | `mul` | LONG | multiplier |
| 9 | `prec` | LONG | precision |
| 10 | `cng` | FIXED | change |
| 11 | `nc` | STRING | percent change |
| 51–54 | — | STRING | name, symbol, exchange, trading symbol |

For spot: **LTP 2, close 3, multiplier 8, precision 9**.

### 5.3 Depth (`dp`)

Five levels. `bp`/`sp` are level 1, `bp1..bp4`/`sp1..sp4` levels 2–5.

| ID | Name | Type |
| --- | --- | --- |
| 0 | `ftm0` | DATE |
| 1 | `dtm1` | DATE |
| 2–6 | `bp` … `bp4` | FIXED |
| 7–11 | `sp` … `sp4` | FIXED |
| 12–16 | `bq` … `bq4` | LONG |
| 17–21 | `bs` … `bs4` | LONG |
| 22–26 | `bno1` … `bno5` | LONG (bid order counts) |
| 27–31 | `sno1` … `sno5` | LONG (ask order counts) |
| 32 | `mul` | LONG |
| 33 | `prec` | LONG |
| 51–54 | — | STRING |

---

## 6. Value decoding

### 6.1 Fixed point

```text
value = raw / (multiplier × 10^precision)
```

- `multiplier` comes from the snapshot (field 23 scrip, 8 index, 32 depth) and
  defaults to `1`.
- `precision` comes from the snapshot (field 24 scrip, 9 index, 33 depth) and
  defaults to `2`.
- Apply `round(value, precision)` only for display; keep the decimal in the
  data model (use `decimal`/integer paise internally, never binary floats for
  money).
- Do **not** interpret the raw bytes with `math.Float32frombits`; that produces
  nonsense such as `1.2e-38`.

Examples:

| raw | mul | prec | decoded |
| --- | --- | --- | --- |
| `2425015` | 1 | 2 | `24250.15` |
| `12345` | 1 | 2 | `123.45` |
| `12345` | 1 | 0 | `12345` |
| `12345` | 10 | 2 | `12.345` |

### 6.2 Sentinel

`-2147483648` (`TRASH_VAL`) means **field absent / unchanged**. Skip it; never
publish it as a price or quantity. Treat `0` as a real value (e.g. zero OI,
zero volume).

### 6.3 Integer fields

`LONG` values are read as signed 32-bit to match `buf2Long` in the SDK. If a
volume/OI raw value is negative (other than the sentinel), it overflowed
2^31−1; log a `warn` with the raw value and drop that field rather than
publishing a negative quantity.

### 6.4 Dates

`DATE` fields are Unix seconds. `getFormatDate` multiplies by 1000 and formats
in browser-local time, so the SDK proves nothing about the intended timezone.
Candle bucketing uses the **local receipt time in `Asia/Kolkata`** plus the
exchange timestamp when it is plausible (within ±5 minutes of receipt);
otherwise the receipt time wins and a `warn` counter increments. Confirm with
live capture.

### 6.5 Derived fields

The SDK computes change, percent change and turnover on the client. Our decoder
computes them only for display; they are not stored as authoritative data.

---

## 7. ACK rules

1. `ackThreshold` comes from the connection response. `0` means ACK is
   disabled and data frames have no message-number prefix.
2. When enabled, each data frame begins with a 4-byte `msgNum`.
3. Increment a counter for every data frame. When
   `counter == ackThreshold`, send an ACK with the **last** `msgNum` and reset
   the counter to zero.
4. ACK is best-effort but its send error is a connection error: close the
   socket and reconnect (the server will drop us anyway).
5. Never ACK before reading the full frame. A malformed frame is not ACKed; the
   connection is reset instead.
6. Log `ackThreshold` at connect, and log any threshold ≤ 0 as "ACK disabled".

Metric to expose: `hsm_frames_received`, `hsm_acks_sent`, `hsm_ack_failures`.

---

## 8. Keepalive, reconnect and resubscribe

**Keepalive:** send type 2 (value 0) every 30 s. A send failure closes the
connection path and triggers reconnect.

**Reconnect policy (Go, supersedes the SDK's fixed retry count):**

| Step | Behaviour |
| --- | --- |
| 1 | Detect close/error; mark feed `degraded`; log close code and reason |
| 2 | Block **new entries**; keep exits enabled (plan §10.1) |
| 3 | Reconnect with exponential backoff 1 s → 30 s, full jitter, unlimited attempts |
| 4 | On socket open, send the binary connection request |
| 5 | On `K` response, reset backoff, then resend the **full subscription diff** (index spots + chart CE/PE + optional depth) |
| 6 | Optionally send snapshot requests for the same scrips to prime state |
| 7 | Wait for subscribe ACK and the first tick per instrument before marking `fresh` |
| 8 | Reset the topic ID map; drop any cached topic IDs |
| 9 | Log each step with `attempt`, `delay_ms`, `scrip_count` |

Do not trust the SDK's "5 retries then sleep 5 minutes" behaviour for a trading
terminal. Retry until shutdown, but never trade on a stale feed.

**Resubscribe diffing:** keep the desired set (index spots + only the two chart
contracts + optional depth) and compute add/remove sets. Never resubscribe the
whole chain. Stay far below 100 scrips per request and 200 per user; log the
count at startup and on every diff.

---

## 9. HSI order feed

### 9.1 Endpoint selection

| `dataCenter` (login response) | URL |
| --- | --- |
| `adc` | `wss://cis.kotaksecurities.com/realtime` |
| `e21` | `wss://e21.kotaksecurities.com/realtime` |
| `e22` | `wss://e22.kotaksecurities.com/realtime` |
| `e41` | `wss://e41.kotaksecurities.com/realtime` |
| `e43` | `wss://e43.kotaksecurities.com/realtime` |
| anything else / empty | `wss://mis.kotaksecurities.com/realtime` |

Log the chosen host (never the token).

### 9.2 Connect and heartbeat

`demo.js` sends valid JSON:

```json
{"type":"cn","Authorization":"<session token>","Sid":"<session sid>","source":"WEB"}
```

Then `{"type":"hb"}` every 30 s. The SDK wrapper instead sends
`JSON.stringify(req).replace(/"/g,'')`, producing unquoted
`{type:cn,Authorization:<token>,Sid:<sid>,src:WEB}`, and uses `src` rather than
`source`. These cannot both be right; the Go client sends the documented,
valid-JSON `source` form and logs the server's first response. If the server
rejects it, retry once with `src` and record the result in
`docs/spikes.md` before changing the default.

### 9.3 Message schema — still unknown

The SDK and demo merely log raw HSI frames. There is **no documented JSON
schema** for order events in the SDK or the API reference. Until a live frame
is captured:

- Log every raw HSI message once at `debug` (bounded length, redact tokens)
  and keep a counter per distinct top-level key set.
- Do **not** parse HSI into trading state. Order/trade/position truth comes from
  REST polling (`orders`, `trades`, `positions`) every 1–2 s while HSI is
  unknown.
- HSI is an accelerator only: when the schema is captured, add a decoder and
  compare its events against the REST result before trusting it.

Capture plan (Phase 0B): place a 1-lot order, modify it, cancel it, and let one
fill; save each raw frame with its timestamp in `testdata/hsi/` after removing
tokens. Build a table-driven decoder and a golden test per event type. Mark
unknown event types as `debug` + metric, never as errors.

---

## 10. Go implementation notes

### 10.1 Packages

```text
internal/kotak/ws/
  frame.go        # ByteReader/ByteWriter, u8/u16/u32/i32/i64, length validation
  hsm_codec.go    # request builders + response decoder (pure, no I/O)
  hsm_client.go   # dial, connect, keepalive, ACK, subscribe diff, reconnect
  hsi_client.go   # JSON dial/heartbeat, raw-frame capture, decoder (later)
  codec_test.go
  testdata/       # golden frames
```

The codec is pure and fully unit-testable: bytes in, typed events out. The
client owns the socket, timers and reconnect state; it must be usable with a
fake `net.Conn`/`httptest` WebSocket server.

### 10.2 Types

```go
type TopicKind uint8 // scrip, index, depth

type TopicID uint32

type Snapshot struct {
    ID     TopicID
    Name   string
    Kind   TopicKind
    Values map[uint8]int32   // raw, sentinel removed
    Text   map[uint8]string
}

type Update struct {
    ID     TopicID
    Values map[uint8]int32   // raw, sentinel removed
}

type DataEvent struct {
    MsgNum   uint32
    HasMsgNum bool
    Snapshots []Snapshot
    Updates   []Update
}
```

Map lookups are acceptable at these rates, but the decoder should validate all
IDs before allocating (max 128 fields, max 64 topics per frame, string lengths
≤ 1024, counts bounded by remaining bytes).

### 10.3 Errors

Use typed/sentinel errors from `internal/errs` and wrap with context:

```go
var (
    ErrShortFrame    = errors.New("short HSM frame")
    ErrBadPacketType = errors.New("unexpected HSM packet type")
    ErrMalformedFrame = errors.New("malformed HSM frame")
    ErrUnknownTopic  = errors.New("unknown HSM topic id")
)
```

- A short/truncated frame, impossible count, or length mismatch is
  `ErrMalformedFrame`: log the type, length and a bounded hex prefix (≤ 64
  bytes), then reset the connection. Never panic, never index past the buffer.
- An unknown topic ID is `ErrUnknownTopic`: log and skip that topic packet,
  continue with the rest of the frame.
- A NACK is a typed `*BrokerError`-style feed error with the status and the
  affected scrips; mark the feed degraded.
- Every error log includes `component`, `event`, `attempt`, and the frame
  metadata; never the token.

### 10.4 Encoding

Builders must be deterministic (golden-testable) and validate:

- token/sid non-empty; source `JS_API`
- 1 ≤ scrip count ≤ 100; scrip string non-empty; no `&` or `|` injection
- channel 1–64
- no string longer than 255 bytes (length is `u8`)
- total frame length ≤ 65535

Return an error rather than truncating.

### 10.5 Tests

| Test | What it proves |
| --- | --- |
| Golden request frames | connect, subscribe, unsubscribe, snapshot, ACK, keepalive match the bytes above |
| Golden synthetic data | §3.7 index frame decodes LTP 24250.15 |
| Scrip/depth snapshots | field IDs, multiplier/precision, string mapping |
| Update decode | positional values, TRASH skipped, dedupe of unchanged values |
| Unknown topic | skipped, frame still decodes, warning event emitted |
| Malformed frames | truncated header/name/value/string, bad length, huge counts, bad type → `ErrMalformedFrame`, no panic |
| Fuzz | arbitrary bytes never panic and never allocate unbounded |
| ACK logic | threshold 0 → never ACK; threshold N → ACK on the Nth frame with the last msgNum |
| Reconnect | topic IDs cleared, full diff resubscribed, `fresh` only after first tick |
| Property | encode(parse(scrip blob)) round-trips for 1..100 scrips |
| Redaction | connect log contains no token or sid sentinel |

Golden frames are produced from the same builder and replaced by captured
frames when available; a capture test asserts the captured frame still decodes
to the expected event.

### 10.6 Logging

Required events (fields in `GO_STANDARDS.md` §3):

```text
hsm_connect_start / hsm_connected / hsm_connect_failed
hsm_ack_threshold
hsm_subscribe / hsm_subscribe_ack / hsm_subscribe_nack
hsm_unsubscribe
hsm_snapshot_request / hsm_snapshot_received
hsm_decode_error (type, len, hex_prefix, error)
hsm_unknown_topic (topic_id)
hsm_keepalive_failed
hsm_disconnected (code, reason, attempt)
hsm_reconnect_scheduled (delay_ms, attempt)
hsm_feed_state (fresh|degraded|stale, reason)
```

Tick logs remain sampled (`GO_STANDARDS.md` §3.5); state transitions are not.

---

## 11. Verification checklist

Phase 0B, once `KOTAK_ACCESS_TOKEN` + TOTP are available:

- [ ] Capture the real connection response; confirm status field and ACK
      threshold encoding.
- [ ] Confirm whether the 2-byte per-topic prefix equals the topic packet
      length, and whether it is ever needed.
- [ ] Confirm updates are positional from field 0 with `TRASH_VAL` holes.
- [ ] Confirm accepted index subscription strings for NIFTY, BANKNIFTY,
      MIDCPNIFTY, SENSEX and the resulting topic names.
- [ ] Confirm scrip and depth field IDs with a liquid ATM option.
- [ ] Confirm timestamp timezone and whether depth prices use fields 32/33.
- [ ] Confirm keepalive requirement (does the socket drop without type 2?).
- [ ] Capture and save golden frames for index/scrip/depth snapshot and update.
- [ ] Capture HSI frames for place/modify/cancel/fill; document the schema.
- [ ] Replace every synthetic fixture in `testdata/` and re-run the codec tests.

Any discrepancy is a codec bug until proven otherwise; do not "adjust the UI" to
hide a decode error.

---

## 12. Open questions

| # | Question | Impact | Resolution |
| --- | --- | --- | --- |
| 1 | ACK threshold encoding (binary vs ASCII) | ACK cadence, possible disconnects | Capture connection response |
| 2 | Meaning of the 2-byte topic prefix | Frame validation strictness | Capture; start permissive but bounded |
| 3 | Update vector always from field 0? | Decode correctness | Capture one option update |
| 4 | Does the server refresh multiplier/precision in updates? | Price scaling | Capture; re-snapshot if missing |
| 5 | Index subscription token form (`Nifty 50` vs `NIFTY`) | Spot ticks | Capture subscribe ACK + topic name |
| 6 | Depth quota and whether depth counts toward the 200-scrip cap | Subscription budget | Capture / broker support |
| 7 | HSI JSON schema | Order latency (not correctness) | Capture order lifecycle |
| 8 | HSI `source` vs `src` and quoted vs unquoted JSON | Handshake | Capture first response |
| 9 | Is the 30 s type-2 packet mandatory? | Connection stability | Test with/without in a soak |
| 10 | Compression on any HSM/HSI path | Decoder | None observed; assert binary/text path in capture |

---

## Appendix A — Constants

```text
MAX_SCRIPS                 = 100        // per subscribe/snapshot request
TRASH_VAL                  = -2147483648
STRING_INDEX.NAME          = 51
STRING_INDEX.SYMBOL        = 52
STRING_INDEX.EXCHG         = 53
STRING_INDEX.TSYMBOL       = 54

BinRespTypes: CONNECTION 1, THROTTLING 2, ACK 3, SUBSCRIBE 4,
              UNSUBSCRIBE 5, DATA 6, CHPAUSE 7, CHRESUME 8,
              SNAPSHOT 9, OPC 10
ResponseTypes: SNAP 'S' (0x53), UPDATE 'U' (0x55)
BinRespStat:  OK 'K', NOT_OK 'N'
Topic prefixes: sf (scrip), if (index), dp (depth)

Status codes returned by the SDK mapping:
  200   success
  11001 connection failed
  11002 connection invalid
  11011 subscription failed
  11012 unsubscription failed
  11013 snapshot failed
  11031 channel pause failed
  11032 channel resume failed
  11040 option-chain subscription failed
```

## Appendix B — SDK → Go map

| `hslib.js` | Go |
| --- | --- |
| `prepareConnectionRequest2` | `BuildConnect` |
| `prepareSubsUnSubsRequest` | `BuildSubscribe` / `BuildUnsubscribe` |
| `prepareSnapshotRequest` | `BuildSnapshot` |
| `getAcknowledgementReq` | `BuildAck` |
| `prepareThrottlingIntervalRequest` | `BuildKeepalive` |
| `HSWrapper.parseData` | `DecodeFrame` |
| `ScripTopicData` / `IndexTopicData` / `DepthTopicData` | `applyScrip` / `applyIndex` / `applyDepth` |
| `setLongValues` + `TRASH_VAL` | positional value decode + sentinel skip |
| `setMultiplierAndPrec` | fixed-point scale from fields 23/24, 8/9, 32/33 |
| `startServer` / `initiateRetry` | `hsm_client.go` reconnect loop |
| `startHsiServer` / `connectHsi` | `hsi_client.go` (JSON + raw capture) |
