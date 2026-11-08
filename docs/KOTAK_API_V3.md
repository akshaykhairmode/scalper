# Kotak Neo Trade API v3 Integration & SDK Capabilities

This document provides architectural guidance, endpoint mapping, rate limiting safeguards, and reference links to the official Kotak Neo Trade API v3 Python SDK for Scalper.

## SDK References & Source Code

* **Official Kotak Neo Python SDK v3:** [Kotak-Neo/kotak-neo-python](https://github.com/Kotak-Neo/kotak-neo-python)
* **SDK Documentation & Guides:** [Kotak-Neo/Kotak-neo-api-v2/docs](https://github.com/Kotak-Neo/Kotak-neo-api-v2/tree/main/docs)
  * [Scrip Search Reference](https://github.com/Kotak-Neo/Kotak-neo-api-v2/blob/main/docs/Scrip_Search.md)
  * [Portfolio Holdings Reference](https://github.com/Kotak-Neo/kotak-neo-python/blob/main/docs/functions/portfolio/holdings.md)
  * [Order Service Reference](https://github.com/Kotak-Neo/kotak-neo-python/blob/main/neo_api_client/services/order.py)

---

## Capabilities Comparison & Scalper Implementation

| Capability | Kotak Neo SDK v3 (`neo_api_client`) | Scalper Implementation | Optimization & Safeguards |
| :--- | :--- | :--- | :--- |
| **Pre-Trade Margin** | `client.margin_required(...)` | `POST /api/broker/margin/required`<br>`POST /api/margin/required` | 300ms UI debounce; in-memory server cache (10s TTL) keyed by `(segment, token, side, qty, price, product)`. Zero duplicate Kotak calls. |
| **Order Validity (IOC)** | `validity="IOC"` | Validated `validity: "DAY" \| "IOC"`; Order pad toggle | Validated in Go engine and payload generator; prevents malformed requests. |
| **Order Audit Trail** | `client.order_history(order_id)` | `GET /api/broker/orders/{id}/history` | UI modal on clicking Order ID; displays transitions (`complete`, `rejected`, `fldQty`, `rejRsn`, `exchTmstp`). |
| **Demat Holdings** | `client.holdings()` | `GET /api/broker/holdings` | Integrated into server-side `booksCache` (60s cadence). Dedicated Holdings tab with Invested, Current Value, and P&L. |
| **Rate Limit Telemetry** | Extracts response headers | `X-RateLimit-Limit`, `X-RateLimit-Remaining`, `X-RateLimit-Reset`, `Retry-After` | Parsed thread-safely on every Kotak HTTP response; exposed via `/api/health` and System Status modal. |
| **Dynamic Scrip Search** | `client.search_scrip(...)` | `GET /api/scrip/search` | Queries local indexed `scrip.Manager` in memory. 0 external API calls; sub-millisecond response time. |
| **Gateway Outbound IP** | Gateway IP requirement | `GET /api/diagnostics/ip` | 60s server-side cache. Allows cloud deployment verification for Kotak IP whitelisting. |

---

## Endpoint Details & Schemas

### 1. Pre-Trade Margin Check (`margin_required`)

* **Kotak Route:** `POST <Base URL>/quick/user/check-margin`
* **Scalper Route:** `POST /api/broker/margin/required` (also available at `POST /api/margin/required`)
* **Headers:** `Sid`, `Auth`, `neo-fin-key: neotradeapi`, `Content-Type: application/x-www-form-urlencoded`
* **Payload (`jData` JSON):**
  ```json
  {
    "brkName": "KOTAK",
    "brnchId": "ONLINE",
    "exSeg": "nse_fo",
    "prc": "150.00",
    "prcTp": "MKT",
    "prod": "MIS",
    "qty": "50",
    "tok": "40007",
    "trnsTp": "B"
  }
  ```
* **Sample Response:**
  ```json
  {
    "stat": "Ok",
    "data": {
      "avlCash": "125000.000000",
      "avlMrgn": "125000.000000",
      "ordMrgn": "7500.000000",
      "reqdMrgn": "7500.000000",
      "insufFund": "0.000000",
      "rmsVldtd": "OK"
    }
  }
  ```

### 2. Order History Audit Trail (`order_history`)

* **Kotak Route:** `POST <Base URL>/quick/order/history`
* **Scalper Route:** `GET /api/broker/orders/{id}/history`
* **Payload (`jData` JSON):** `{"nOrdNo": "<orderId>"}`
* **Response:** Array of historical state transitions containing `ordSt` (status), `qty`, `fldQty`, `prc`, `avgPrc`, `rejRsn` (rejection reason), `exchTmstp` (exchange execution timestamp).

### 3. Demat Holdings (`holdings`)

* **Kotak Route:** `GET <Base URL>/portfolio/v1/holdings`
* **Scalper Route:** `GET /api/broker/holdings`
* **Caching:** Handled by `booksCache` in Go with a 60-second refresh cadence. Normalizes `upldPrc` (average price), `mktPrc` (last traded price), and `hldQty` (quantity).

### 4. Scrip Search (`search_scrip`)

* **Scalper Route:** `GET /api/scrip/search?symbol=NIFTY&segment=nse_fo&option_type=CE`
* **Execution:** Evaluated completely in-memory using Scalper's SQLite and RAM index from the daily scrip master download. Generates zero network requests to Kotak.

---

## Rate Limiting & Optimization Architecture

1. **Client-Side Debouncing:**
   Input events on strike order pad lot selectors and price inputs debounce by 300ms before dispatching pre-trade margin calculations.
2. **Server-Side In-Memory Cache:**
   Margin calculations are cached for 10 seconds per unique order signature.
3. **Stale Cache Resilience:**
   If a transient broker error or rate-limit spike occurs, Scalper serves the last known valid state rather than blanking the terminal.
4. **Header Telemetry:**
   `X-RateLimit-Limit`, `X-RateLimit-Remaining`, and `X-RateLimit-Reset` are continuously parsed from Kotak API responses and surfaced to the UI.
