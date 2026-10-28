# Kotak Neo SFeed protocol

The production market-data WebSocket uses the `native_batch` SFeed protocol
implemented by the current
[`kotak-neo-python`](https://github.com/Kotak-Neo/kotak-neo-python) SDK. It is
not the older HSM binary request/response protocol described in previous
versions of this document.

The application still places and fills orders locally as paper trades. This
document covers authentication and live market data only.

## Credentials and endpoint selection

REST login, quotes, and Scrip Master requests use
`Authorization: <KOTAK_CONSUMER_KEY>`. After TOTP and MPIN validation, the feed
uses the configured UCC as `user` and the trade-session SID as `auth`. The
feed auth frame does not contain the access token.

The SFeed endpoint is selected in this order:

1. `market.hsm_url`, when explicitly configured.
2. The endpoint resolved from the MPIN response's `dataCenter` using the
   SDK's production configuration service.
3. `feedUrl` from the MPIN response.
4. `wss://sfeed.kotaksecurities.com/apifeed`.

All non-local endpoints must use `wss://` and a Kotak Securities domain.

## Authentication

The client sends a JSON text frame after the WebSocket handshake:

```json
{
  "user": "<UCC>",
  "auth": "<trade-session SID>",
  "format": "native_batch",
  "source": "NEOTRADEAPI",
  "platform": "Web",
  "version": "1.2.3",
  "sdk_version": 2,
  "sdk_date": "2026-08-07T09:41:17.667Z",
  "conn_req_time": 0,
  "sessionValidation": false
}
```

`conn_req_time` is the current Unix time in milliseconds. Authentication
completes on a JSON response with `message_code` 1117 or 1119. A
`native_fallback` response is rejected. The `exchanges` object supplies an
exchange ID and price divider; the decoder defaults to a divider of 100 if an
exchange has no usable divider.

## Subscriptions

Subscriptions are JSON text frames. `inputtoken` is a comma-separated list of
`<exchange>|<token-or-name>` values:

```json
{
  "event": "subscribeIndices",
  "inputtoken": "nse_cm|Nifty 50",
  "ack_symbol": true
}
```

Option instruments use `subscribeScrips`; indices use `subscribeIndices`.
Unsubscribe events use the corresponding `unsubscribeScrips` and
`unsubscribeIndices` names. A response with `message_code` 1109 can contain a
`trading_symbols` map keyed by the same exchange/token string; the application
uses it to label quotes. Subscribe requests wait up to five seconds for this
acknowledgement; quotes without a resolved symbol are held until the ACK or
timeout so the initial tick is not needlessly lost from symbol mapping.

## Market-data frames

Text frames carry control messages. Binary frames contain one or more
little-endian packets concatenated together. Each packet starts with a
9-byte header:

| Offset | Type | Field |
| ---: | --- | --- |
| 0 | `uint16` | Total packet length, including the header |
| 2 | `uint16` | Message code |
| 4 | `int8` | Exchange ID |
| 5 | `uint8` | Feed level |
| 6 | `uint8` | Auction flag |
| 7 | `uint8` | Sequence number |
| 8 | `uint8` | Bitmask length |

Supported exchange IDs include `1=nse_cm`, `2=nse_fo`, `3=cde_fo`,
`4=nse_com`, `5=bse_cm`, `6=bse_fo`, `7=bse_cd`, `8=bse_co`, `9=mcx_fo`,
and `10=ncd_co`.

The decoder handles:

- **Index** (`message_code=7207`): a 78-byte body containing token, OHLC and
  index value, last-trade time, yearly values, net-change percentage, and the
  fixed-width index name.
- **Scrip market picture** (`level=2, 4, 8, or 16`): a 135-byte fixed body
  followed by 16-byte depth rows (`int64` quantity, `int32` price,
  `int32` order count). Level 4 carries one bid and one ask.
- **Mini-touch scrip** (`level=1`): a 45-byte body with token, last-trade
  time/price, close, and net-change values.

Prices are divided by the exchange's configured divider. Feed timestamps are
Unix seconds. Index messages use the index name as their identity; scrip
messages use their exchange token. The application maps these messages into
its quote type, including LTP, previous close, change, change percentage,
volume, open interest, best bid/ask, timestamp, and acknowledged symbol where
available. Market-status and unrelated message types are ignored.

The connection uses native WebSocket ping/pong (20-second client ping
interval); it does not send the legacy HSM application heartbeat.

## Verification

`go test ./internal/hsm` checks auth and subscription JSON, URL validation,
batched packet framing, and synthetic index, market-picture, and mini-touch
decoding. These tests do not require broker credentials. Live endpoint
behavior still requires a user-authenticated session and has not been
validated by the unit tests.
