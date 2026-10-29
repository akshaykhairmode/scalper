# Scalper

A low-latency, manual options trading terminal and option chain interface designed for Indian derivatives (NIFTY, BANKNIFTY, MIDCPNIFTY, SENSEX) integrated with the Kotak Neo API.

```bash
curl -fsSL https://raw.githubusercontent.com/akshaykhairmode/scalper/main/install.sh | sh
```

---

## Features

- **Real-Time Option Chain Ladder**: Fast, interactive ladder with strike sorting, ATM identification, LTP, change percentage, and open interest.
- **Direct Kotak Neo Integration**: Live market quotes, SFeed WebSocket streaming, live order execution, trade book, position book, and limits.
- **Charts**: Interactive 3-column multi-timeframe candlestick charts (Call, Underlying Spot, Put) powered by Lightweight Charts with auto Support/Resistance and volume badges.
- **Risk & Rate Limiting Guardrails**: Automated rate-limiter bucket matching Kotak API limits, max position bounds, auto square-off confirmation, and session management.
- **Order Pad Flexibility**: Quick toggling between Lot and Share quantities.

---

## Installation

### Quick Install (macOS / Linux)

Run the one-line installer in your terminal. It automatically detects your operating system and CPU architecture, downloads the latest release binary, verifies checksums, and installs `scalper` to your PATH:

```bash
curl -fsSL https://raw.githubusercontent.com/akshaykhairmode/scalper/main/install.sh | sh
```

> **Options**: You can customize the installation directory or pin a specific version:
> ```bash
> # Custom installation directory
> curl -fsSL https://raw.githubusercontent.com/akshaykhairmode/scalper/main/install.sh | BINDIR=~/.local/bin sh
>
> # Specific version
> curl -fsSL https://raw.githubusercontent.com/akshaykhairmode/scalper/main/install.sh | VERSION=v1.0.0 sh
> ```

---

### Manual Download from GitHub Releases

Pre-built binaries for macOS (Apple Silicon / Intel), Linux (x86_64 / arm64), and Windows are available on the [Releases](https://github.com/akshaykhairmode/scalper/releases) page.

1. Download the archive for your operating system and architecture from the [latest release](https://github.com/akshaykhairmode/scalper/releases/latest).
2. Extract the archive:

```bash
# macOS (Apple Silicon) example:
tar -xzf scalper_*_darwin_arm64.tar.gz

# Linux (x86_64) example:
tar -xzf scalper_*_linux_amd64.tar.gz
```

3. Make the binary executable (if needed) and move it to your path:

```bash
chmod +x scalper
sudo mv scalper /usr/local/bin/ # or keep it in your project directory
```

---

### Alternative: Building from Source (Developers)

If you are developing or prefer to build locally from source:

```bash
git clone https://github.com/akshaykhairmode/scalper.git
cd scalper
make build # compiles to bin/scalper
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

Start the server using your configuration file:

```bash
scalper -config config.yaml
# or if running from local build: ./bin/scalper -config config.yaml
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
