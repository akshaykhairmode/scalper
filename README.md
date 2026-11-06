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

For security reasons, sensitive API keys and credentials are **not** required to be stored in `config.yaml` and can be provided via environment variables:

| Variable | Required | Description |
| :--- | :---: | :--- |
| `KOTAK_CONSUMER_KEY` | **Yes** (for live trading/login) | Your Kotak Neo API Consumer Key generated from the developer portal. Required for authentication and all authenticated REST endpoints (quotes, orders, history). |
| `KOTAK_MPIN` | Optional | Your 6-digit Kotak Neo MPIN. If set, only TOTP is requested in the UI login dialog. |
| `KOTAK_MOBILE_NUMBER` | Optional (or in config) | Your registered Kotak mobile number (e.g. `+919876543210`). Overrides `config.yaml`. |
| `KOTAK_UCC` | Optional (or in config) | Your Kotak User Client Code (e.g. `ZD1234`). Overrides `config.yaml`. |
| `SERVE_AUTH_USERNAME` | Optional | Web serve mode login username to lock the UI. Overrides `web.basic_auth.username`. |
| `SERVE_AUTH_PASSWORD` | Optional | Web serve mode login password to lock the UI. Overrides `web.basic_auth.password`. |

Export them in your environment before running:

```bash
export KOTAK_CONSUMER_KEY="your-consumer-key-here"
export KOTAK_MPIN="your-6-digit-mpin" # Optional
```

---

## Cloud Deployment (Fork & Automated CI/CD)

Scalper includes built-in GitHub Actions CI/CD to build and deploy directly to your cloud VM (Oracle Cloud Free Tier, AWS EC2, DigitalOcean, Hetzner, etc.).

Anyone can **fork** this repository, configure their server and Kotak credentials in GitHub Secrets, and have their own private, automated trading server up and running in minutes — **without editing any code or committing secrets into git**.

### How to Fork & Deploy

1. **Fork the Repository**:
   - Click the **Fork** button at the top right of this page to create your own copy.

2. **Add Your GitHub Secrets**:
   - In your forked repository, navigate to:
     **Settings** → **Secrets and variables** → **Actions** → **New repository secret**
   - Add the required secrets listed below.

3. **Whitelist Server IP with Broker**:
   - Log into the [Kotak Developer Portal](https://napi.kotaksecurities.com) and whitelist your server's public IP address (found in `SERVER_HOST`).

4. **Deploy**:
   - **Automatic**: Any push or PR merge to your `main` branch will test, build, and deploy to your server.
   - **Manual**: Go to the **Actions** tab → Select **Deploy to Server** in the left sidebar → Click **Run workflow** → Click **Run workflow**.

---

### Required GitHub Secrets

| Secret Name | Sample Value | Description |
| :--- | :--- | :--- |
| `SERVER_HOST` | `130.210.18.97` | Your remote server's public IPv4 address or domain |
| `SSH_PRIVATE_KEY` | `-----BEGIN OPENSSH PRIVATE KEY...-----` | Full private SSH key file contents (including `BEGIN` and `END` header lines) |
| `KOTAK_CONSUMER_KEY` | `abcdef12-3456-7890-abcd-ef1234567890` | Kotak Neo API Consumer Key from developer portal |
| `KOTAK_MPIN` | `123456` | Your 6-digit Kotak Neo MPIN |
| `KOTAK_MOBILE_NUMBER` | `+919876543210` | Registered 10-digit mobile number with country code (`+91`) |
| `KOTAK_UCC` | `ZD1234` | Your Kotak User Client Code |
| `SERVE_AUTH_USERNAME` | `trader` | Web terminal login username to unlock serve mode |
| `SERVE_AUTH_PASSWORD` | `YourSecurePassword@2026` | Web terminal login password to unlock serve mode |

### Optional Secrets / Variables

Configure under **Secrets** or **Variables** (defaults apply if omitted):

| Name | Default | Sample Value | Description |
| :--- | :---: | :--- | :--- |
| `SSH_USER` | `opc` | `ubuntu` / `opc` / `ec2-user` | SSH user (`opc` for Oracle Linux, `ubuntu` for Ubuntu, `ec2-user` for AWS) |
| `SSH_PORT` | `22` | `22` | SSH port |
| `KOTAK_NEO_FIN_KEY` | `neotradeapi` | `neotradeapi` | Kotak Neo Fin Key (standard for all accounts) |
| `SERVICE_NAME` | `scalper` | `scalper` | systemd service name on remote server |

> [!TIP]
> **Zero Leaks**: All credentials are encrypted by GitHub and injected directly over SSH stdin into `/etc/default/scalper` on your server with permissions `chmod 600` (readable only by root/systemd). You never need to commit personal details into git.
>
> For full architecture and SELinux/firewall details, see the [Server Deployment Guide](docs/DEPLOYMENT.md).

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
