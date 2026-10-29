# Scalper

A low-latency, manual options trading terminal and option chain interface designed for Indian derivatives (NIFTY, BANKNIFTY, MIDCPNIFTY, SENSEX) integrated with the Kotak Neo API.

---

## Features

- **Real-Time Option Chain Ladder**: Fast, interactive ladder with strike sorting, ATM identification, LTP, change percentage, and open interest.
- **Direct Kotak Neo Integration**: Live market quotes, SFeed WebSocket streaming, live order execution, trade book, position book, and limits.
- **Charts**: Interactive multi-timeframe candlestick charts powered by Lightweight Charts.
- **Risk & Rate Limiting Guardrails**: Automated rate-limiter bucket matching Kotak API limits, max position bounds, auto square-off confirmation, and session management.
- **Paper Trading & Live Modes**: Support for paper trading simulations and verified live order execution.

---

## Prerequisites

- **Go**: Version 1.22+ (tested with Go 1.22 / 1.23 / 1.24+).
- **Kotak Neo API Account**: Valid Kotak Neo trading credentials and API Consumer Key.

---

## Installation & Build

### 1. Clone the repository

```bash
git clone https://github.com/akshaykhairmode/scalper.git
cd scalper
```

### 2. Build from source

Using `make`:

```bash
make build
```

This compiles the binary to `bin/scalper`.

Or directly using the Go toolchain:

```bash
go build -trimpath -o bin/scalper ./cmd/scalper
```

---

## Configuration

Copy the example configuration file:

```bash
cp config.example.yaml config.yaml
```

Update `config.yaml` with your mobile number, UCC, database path, and index preferences:

```yaml
app:
  listen: "127.0.0.1:8080"
  db_path: "./data/scalper.db"
  timezone: "Asia/Kolkata"

logging:
  level: "info"              # debug | info | warn | error
  format: "text"             # json | text
  file: "./logs/scalper.log" # empty = stderr only

kotak:
  mobile_number: "+91XXXXXXXXXX"
  ucc: "XXXXXXXX"
  neo_fin_key: "neotradeapi"
```

---

## Environment Variables

For security reasons, sensitive API keys and credentials are **not** stored in `config.yaml` and must be provided via environment variables:

| Variable | Required | Description |
| :--- | :---: | :--- |
| `KOTAK_CONSUMER_KEY` | **Yes** (for live trading/login) | Your Kotak Neo API Consumer Key generated from the developer portal. Required for authentication and all authenticated REST endpoints (quotes, orders, history). |
| `KOTAK_MPIN` | Optional | Your 6-digit Kotak Neo MPIN. If set, you only need to enter your TOTP in the UI login dialog. If omitted, the UI will prompt for both MPIN and TOTP. |

Export them in your environment before running:

```bash
export KOTAK_CONSUMER_KEY="your-consumer-key-here"
export KOTAK_MPIN="your-6-digit-mpin" # Optional
```

---

## Running Scalper

Start the server:

```bash
./bin/scalper -config config.yaml
```

Or run directly via `make`:

```bash
make run
```

Once running, navigate to:

```text
http://127.0.0.1:8080
```

---

## Development & Testing

Common `make` targets:

```bash
make test        # Run unit tests
make race        # Run unit tests with race detection
make cover       # Run tests and verify the coverage gate (>= 55%)
make lint        # Run golangci-lint
make fmt         # Format code with gofmt / goimports
make vet         # Run go vet
make check       # Format check + vet + race + coverage
make clean       # Remove build artifacts
```

---

## License

This project is licensed under the terms specified in the [LICENSE](LICENSE) file.
