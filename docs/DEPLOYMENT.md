# Server Deployment Guide (GitHub Actions CI/CD)

This guide walks you through automatically deploying Scalper to a cloud VM (Oracle Cloud Free Tier, AWS EC2, DigitalOcean, Hetzner, etc.) using GitHub Actions.

When you fork this repository, you do **not** need to change code or commit credentials into git. All sensitive settings are configured securely in your repository's GitHub Secrets.

---

## 1. Quick Prerequisites

1. **A Linux Server / VM**:
   - Recommended: Oracle Cloud Free Tier (AMD or ARM Ampere), Ubuntu / Debian / Oracle Linux.
   - Recommended: 1 GB+ RAM (our deployment script automatically provisions a 2 GB swapfile if needed).
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
6. It configures the systemd service unit and restarts `scalper`.
7. It verifies service health via `curl http://127.0.0.1:8080/api/health`.

---

## 4. Manual Deployment Trigger

You don't have to wait for a git commit to deploy. You can trigger it at any time from GitHub:
1. Go to your repository on GitHub.
2. Click the **Actions** tab.
3. In the left sidebar, click **Deploy to Server**.
4. Click **Run workflow** → select branch `main` → click **Run workflow**.

---

## 5. Security & Isolation

- **Zero Secret Bleeding**: Secrets stored in your fork are private to your GitHub account. No other fork or upstream repository can view them.
- **No Git Leaks**: Your phone number, UCC, passwords, and API keys are injected directly into the server's environment via SSH stdin. They are never committed to git.
- **File System Protection**: Credentials on the server reside in `/etc/default/scalper` with `chmod 600`, inaccessible to unprivileged processes.
