# Server Deployment Guide (GitHub Actions CI/CD)

This guide walks you through automatically deploying Scalper to a cloud VM (Oracle Cloud Free Tier, AWS EC2, DigitalOcean, Hetzner, etc.) using GitHub Actions.

When you fork this repository, you do **not** need to change code or commit credentials into git. All sensitive settings are configured securely in your repository's GitHub Secrets.

---

## 1. Quick Prerequisites

1. **A Linux Server / VM**:
   - Recommended: Oracle Cloud Free Tier (AMD or ARM Ampere), Ubuntu / Debian / Oracle Linux.
   - Recommended: 1 GB+ RAM (our deployment script automatically audits RAM, disables heavy cloud telemetry like `oracle-cloud-agent`, and provisions a 2 GB swapfile if needed).
   - Ingress Firewall: Ports `22` (SSH), `80` (HTTP), and `443` (HTTPS) open.
2. **Kotak Neo API Access**:
   - Consumer Key from [Kotak Developer Portal](https://napi.kotaksecurities.com).
   - Whitelist your server's public IP address in the Kotak Neo developer portal.
3. **SSH Key Pair**:
   - Your private key (`.pem` / OpenSSH key) used to connect to your server.

---

## 2. GitHub Secrets Setup

In your GitHub repository (or fork), navigate to:
**Settings** → **Secrets and variables** → **Actions** → **New repository secret**

Add the following secrets:

### Required Secrets

| Secret Name | Example Value | Description |
| :--- | :--- | :--- |
| `SERVER_HOST` | `130.210.18.97` | Your remote server's public IPv4 address or domain. |
| `SSH_PRIVATE_KEY` | `-----BEGIN OPENSSH PRIVATE KEY...-----` | The full private SSH key used to log in as your server user. Include header and footer lines. |
| `KOTAK_CONSUMER_KEY` | `abcdef12-3456-7890-abcd-ef1234567890` | Kotak Neo API Consumer Key. |
| `KOTAK_MPIN` | `123456` | Your 6-digit Kotak Neo MPIN. |
| `KOTAK_MOBILE_NUMBER` | `+919876543210` | Your registered 10-digit mobile number with country code. |
| `KOTAK_UCC` | `ZD1234` | Your Kotak User Client Code. |
| `SERVE_AUTH_USERNAME` | `trader` | Username required to unlock the web terminal UI. |
| `SERVE_AUTH_PASSWORD` | `YourSecurePassword@2026` | Password required to unlock the web terminal UI. |

### Optional Secrets / Variables

These can be configured under either **Secrets** or **Variables** (if not set, the defaults below apply):

| Name | Default | Description |
| :--- | :--- | :--- |
| `SSH_USER` | `opc` | SSH username (`opc` for Oracle Linux, `ubuntu` for Ubuntu, `ec2-user` for Amazon Linux). |
| `SSH_PORT` | `22` | SSH port. |
| `KOTAK_NEO_FIN_KEY` | `neotradeapi` | Kotak Neo Fin key (default is standard for all accounts). |
| `SERVICE_NAME` | `scalper` | Name of the systemd service on the server. |

---

## 3. How Deployment Works

Whenever you push to the `main` branch or manually trigger the workflow:

1. **GitHub Actions Runner** checks out your repository and runs the test suite.
2. It SSHs into your remote server using `SSH_PRIVATE_KEY` and detects the server CPU architecture (`amd64` or `arm64`).
3. It compiles a native static Linux binary for your architecture and uploads it to `~/scalper/scalper`.
4. It ensures `config.yaml` is initialized from `config.example.yaml` on the server (preserving any custom changes you made on the server).
5. It writes your credentials safely to `/etc/default/scalper` with file permissions `600` (readable only by systemd/root).
6. It audits server resources: detects Oracle Linux/cloud hosts, disables memory-hogging telemetry (`oracle-cloud-agent`), verifies RAM & swap, provisions persistent 2 GB swap if needed, and tunes swappiness.
7. It configures the systemd service unit and restarts `scalper`.
8. It verifies service health via `curl http://127.0.0.1:8080/api/health`.

---

## 4. Manual Deployment Trigger

You don't have to wait for a git commit to deploy. You can trigger it at any time from GitHub:
1. Go to your repository on GitHub.
2. Click the **Actions** tab.
3. In the left sidebar, click **CI/CD**.
4. Click **Run workflow** → select branch `main` → click **Run workflow**.

---

## 5. Security & Isolation

- **Zero Secret Bleeding**: Secrets stored in your fork are private to your GitHub account. No other fork or upstream repository can view them.
- **No Git Leaks**: Your phone number, UCC, passwords, and API keys are injected directly into the server's environment via SSH stdin. They are never committed to git.
- **File System Protection**: Credentials on the server reside in `/etc/default/scalper` with `chmod 600`, inaccessible to unprivileged processes.
- **Database Encryption**: Broker tokens and session IDs in SQLite are encrypted at rest with AES-256-GCM. The key resides at `./data/.store.key` (`chmod 600`) or via `SCALPER_STORE_KEY`.

---

## 6. Oracle Cloud Architecture, Reverse Proxy & Network Hardening

### Automatic Caddy + sslip.io TLS & HTTP-to-HTTPS Redirection
The deployment script (`scripts/deploy.sh`) automatically provisions and manages **Caddy v2.8.4** as the server's TLS-terminating reverse proxy / load balancer:
1. **Automatic Installation**: If Caddy is not installed on the server, the script downloads and installs the official Linux static binary (`amd64` or `arm64`) to `/usr/local/bin/caddy`.
2. **Automatic TLS with sslip.io**: Generates `/etc/caddy/Caddyfile` targeting `<SERVER_HOST>.sslip.io` (or a custom domain if `SERVER_DOMAIN` is provided). Caddy's native ACME client automatically requests and renews free Let's Encrypt certificates.
3. **Automatic HTTP → HTTPS Redirect**: Caddy listens on port 80 and automatically redirects all incoming HTTP traffic to HTTPS (port 443).
4. **WebSocket & Feed Support**: Caddy's `reverse_proxy` proxies WebSocket connections (`/ws`) out-of-the-box to `127.0.0.1:8080`.
5. **Firewall & Security**: Opens OS firewall services (`http`, `https`, `8080`) while keeping Scalper bound internally to `127.0.0.1:8080`.

### Listen Address Recommendations
* **Behind Caddy Reverse Proxy [Default & Recommended]:**
  Scalper binds to `127.0.0.1:8080` on the loopback interface. Caddy manages public TLS on ports 80/443 and proxies traffic to loopback.
* **Direct Internet-Facing (`0.0.0.0:8080`):**
  If Scalper binds directly to `0.0.0.0:8080`, **Basic Auth (`web.basic_auth`) is strictly mandatory**. Scalper refuses to start if bound to `0.0.0.0` without basic authentication (override via `web.insecure_no_auth: true` or `--insecure-no-auth` CLI flag).

### CORS & Mobile Connectivity (PWA vs Native App)
* **PWA (Progressive Web App):** Runs same-origin on your server's domain (`https://scalper.yourdomain.com`). No cross-origin permissions needed.
* **Capacitor Android / iOS Native App:** Communicates cross-origin from the local webview (`capacitor://localhost` or `https://localhost`). Scalper strictly allowlists mobile container schemes, server `Host`, loopback, and private LAN IPs while rejecting arbitrary third-party web origins. Custom domains can be configured in `web.allowed_origins`.

### Protected Resources
* Endpoints `/logs`, `/api/logs`, `/settings`, and `/api/config` require an active authentication mechanism (`web.basic_auth` or enrolled biometric device) and valid credentials. If no authentication mechanism is configured, access is rejected with `403 Forbidden`.
