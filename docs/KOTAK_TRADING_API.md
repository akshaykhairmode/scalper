# Kotak order and account APIs

The local web server uses the authenticated trade session created by the
existing TOTP/MPIN flow. Broker credentials remain in memory and are never
returned by the local HTTP API. The terminal operates in live mode only. Live
place, modify, and cancel requests require `confirm: true` when
`trading.confirm_live_orders` is enabled.

## Local routes

| Route | Kotak operation |
| --- | --- |
| `GET /api/broker/orders` | Order book |
| `GET /api/broker/orders/{id}` | Single order |
| `GET /api/broker/orders/{id}/history` | Order history |
| `POST /api/broker/orders` | Place an order |
| `PATCH /api/broker/orders/{id}` | Modify an order |
| `DELETE /api/broker/orders/{id}` | Cancel an order |
| `GET /api/broker/trades` | Trade book |
| `GET /api/broker/positions` | Positions |
| `GET /api/broker/holdings` | Holdings |
| `GET /api/broker/limits` | Account limits |

Order writes use the Kotak form fields and paths from the official Python SDK.
For example:

```json
{
  "exchange_segment": "nse_fo",
  "trading_symbol": "NIFTY...CE",
  "transaction_type": "B",
  "product": "MIS",
  "order_type": "MARKET",
  "quantity": 50,
  "price": 0,
  "validity": "DAY",
  "confirm": true
}
```

## Order feed

After MPIN authentication, the app connects to `wss://<trade-api-host>/realtime`.
It sends the SDK's initial plain-text connection frame with the trade token and
SID, then forwards `order` and `position` events to browser WebSocket clients as
`broker_order` and `broker_position`. The client reconnects with backoff after
disconnects. Browser subscribers should refresh the broker books after either
event; event bodies are passed through in Kotak's SDK field names.

The REST paths and feed framing follow the
[`kotak-neo-python` order service](https://github.com/Kotak-Neo/kotak-neo-python/blob/main/neo_api_client/services/order.py)
and [order-feed client](https://github.com/Kotak-Neo/kotak-neo-python/blob/main/neo_api_client/websocket/orderfeed/client.py).
