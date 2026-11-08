#!/usr/bin/env bash
#
# scripts/deploy.sh
#
# Cross-compiles Scalper for Linux and deploys it to a remote server (e.g. Oracle Cloud Free Tier)
# running in serve mode.
#
# Environment variables:
#   SERVER_HOST / SERVER_IP / DEPLOY_IP (Required) Remote server IP or hostname (e.g. 192.0.2.1)
#   SSH_PRIVATE_KEY / SSH_KEY           (Required) Path to SSH private key file OR raw private key string
#   SSH_USER / DEPLOY_USER              (Optional) Remote SSH user, default: "opc"
#   SSH_PORT / DEPLOY_PORT              (Optional) Remote SSH port, default: 22
#   DEPLOY_DIR                          (Optional) Remote directory, default: "/home/$DEPLOY_USER/scalper"
#   CONFIG_FILE                         (Optional) Local config file to upload, default: "config.yaml"
#   SERVICE_NAME                        (Optional) systemd service name, default: "scalper"
#   TARGET_ARCH                         (Optional) Override auto-detected architecture: "amd64" or "arm64"
#   SKIP_BUILD                          (Optional) Set to 1 to skip compilation and upload existing binary
#   KOTAK_CONSUMER_KEY                  (Optional) Kotak Neo Consumer Key
#   KOTAK_MPIN                          (Optional) Kotak Neo MPIN
#   KOTAK_MOBILE_NUMBER                 (Optional) Registered Kotak mobile number
#   KOTAK_UCC                           (Optional) Kotak User Client Code
#   KOTAK_NEO_FIN_KEY                   (Optional) Kotak Neo Fin Key (default: neotradeapi)
#   SERVE_AUTH_USERNAME                 (Optional) Web serve mode login username
#   SERVE_AUTH_PASSWORD                 (Optional) Web serve mode login password
#
# Usage:
#   SERVER_HOST="<server-ip>" SSH_KEY="~/.ssh/oci-key.pem" ./scripts/deploy.sh
#   Or via arguments:
#   ./scripts/deploy.sh <server-ip> ~/.ssh/oci-key.pem [DEPLOY_USER]

set -euo pipefail

# ── Color Output Helpers ───────────────────────────────────────────────────
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m' # No Color

info()    { printf "${BLUE}[INFO]${NC} %s\n" "$*"; }
success() { printf "${GREEN}[OK]${NC} %s\n" "$*"; }
warn()    { printf "${YELLOW}[WARN]${NC} %s\n" "$*"; }
error()   { printf "${RED}[ERROR]${NC} %s\n" "$*" >&2; }
fatal()   { printf "${RED}[FATAL]${NC} %s\n" "$*" >&2; exit 1; }

# ── Resolve Environment Variables and Arguments ───────────────────────────
TARGET_IP="${1:-${SERVER_HOST:-${SERVER_IP:-${DEPLOY_IP:-}}}}"
KEY_INPUT="${2:-${SSH_PRIVATE_KEY:-${SSH_KEY:-${DEPLOY_KEY:-}}}}"
USER_NAME="${3:-${SSH_USER:-${DEPLOY_USER:-${SERVER_USER:-opc}}}}"
PORT="${SSH_PORT:-${DEPLOY_PORT:-22}}"
REMOTE_DIR="${DEPLOY_DIR:-/home/${USER_NAME}/scalper}"
LOCAL_CONFIG="${CONFIG_FILE:-config.yaml}"
SVC_NAME="${SERVICE_NAME:-scalper}"
OVERRIDE_ARCH="${TARGET_ARCH:-}"
SKIP_BUILD="${SKIP_BUILD:-0}"
SERVER_DOMAIN="${SERVER_DOMAIN:-${DEPLOY_DOMAIN:-}}"
CADDY_DOMAIN="${SERVER_DOMAIN:-${TARGET_IP}.sslip.io}"

# ── Validate Required Inputs ───────────────────────────────────────────────
if [[ -z "${TARGET_IP}" ]]; then
  echo -e "${BOLD}Scalper Deployment Script${NC}\n"
  error "Missing server IP address."
  echo "Usage:"
  echo "  SERVER_HOST=\"<ip>\" SSH_KEY=\"<path_to_key>\" ./scripts/deploy.sh"
  echo "  ./scripts/deploy.sh <server_ip> <ssh_key_path> [ssh_user]"
  exit 1
fi

KEY_PATH=""
if [[ "${KEY_INPUT}" =~ "BEGIN" ]]; then
  # Raw private key string provided (e.g. from GitHub Actions secrets)
  TMP_KEY_FILE="$(mktemp /tmp/scalper_deploy_key.XXXXXX)"
  printf "%s\n" "${KEY_INPUT}" > "${TMP_KEY_FILE}"
  chmod 600 "${TMP_KEY_FILE}"
  KEY_PATH="${TMP_KEY_FILE}"
  trap 'rm -f "${TMP_KEY_FILE}"' EXIT
elif [[ -n "${KEY_INPUT}" ]]; then
  KEY_PATH="${KEY_INPUT}"
else
  # Check known default key locations as convenience
  for candidate in "${HOME}/.ssh/oci-key.pem" "${HOME}/.ssh/id_rsa" "${HOME}/.ssh/id_ed25519"; do
    if [[ -f "${candidate}" ]]; then
      KEY_PATH="${candidate}"
      info "Using detected SSH key: ${KEY_PATH}"
      break
    fi
  done
fi

if [[ -z "${KEY_PATH}" ]]; then
  error "Missing SSH private key."
  echo "Please set SSH_KEY=\"/path/to/key.pem\" or SSH_PRIVATE_KEY=\"<key-content>\"."
  exit 1
fi

# Expand tilde in key path if string
KEY_PATH="${KEY_PATH/#\~/$HOME}"

if [[ ! -f "${KEY_PATH}" ]]; then
  fatal "SSH key not found at: ${KEY_PATH}"
fi

# Ensure correct private key permissions
chmod 600 "${KEY_PATH}" 2>/dev/null || true

SSH_OPTS=(
  -i "${KEY_PATH}"
  -p "${PORT}"
  -o StrictHostKeyChecking=accept-new
  -o ConnectTimeout=15
  -o ServerAliveInterval=15
  -o BatchMode=yes
)

SCP_OPTS=(
  -i "${KEY_PATH}"
  -P "${PORT}"
  -o StrictHostKeyChecking=accept-new
  -o ConnectTimeout=15
  -o BatchMode=yes
)

echo -e "\n${BOLD}${CYAN}=== Scalper Cloud Deployment ===${NC}"
info "Target Server : ${USER_NAME}@${TARGET_IP}:${PORT}"
info "SSH Key       : ${KEY_PATH}"
info "Remote Dir    : ${REMOTE_DIR}"
info "Service Name  : ${SVC_NAME}"

# ── Pre-flight Credential Validations ──────────────────────────────────────
AUTH_USER="${SERVE_AUTH_USERNAME:-${SCALPER_SERVE_USERNAME:-}}"
AUTH_PASS="${SERVE_AUTH_PASSWORD:-${SCALPER_SERVE_PASSWORD:-}}"
if [[ -z "${AUTH_USER}" || -z "${AUTH_PASS}" ]]; then
  if [[ "${INSECURE_NO_AUTH:-0}" == "1" || "${SCALPER_INSECURE_NO_AUTH:-0}" == "1" ]]; then
    warn "Serve-mode web auth: NOT SET (explicitly bypassed via INSECURE_NO_AUTH=1)"
  else
    fatal "SERVE_AUTH_USERNAME and SERVE_AUTH_PASSWORD are required to protect the remote terminal. Set both variables, or pass INSECURE_NO_AUTH=1 to bypass."
  fi
else
  success "Serve-mode web auth: ENABLED (user: ${AUTH_USER})"
fi

if [[ -n "${KOTAK_CONSUMER_KEY:-}" ]]; then
  success "Kotak Consumer Key : CONFIGURED"
else
  warn "Kotak Consumer Key : NOT SET (Kotak live trading & login will be disabled)"
fi

if [[ -n "${KOTAK_MOBILE_NUMBER:-${KOTAK_MOBILE:-}}" ]]; then
  success "Kotak Mobile Number: CONFIGURED"
fi

if [[ -n "${KOTAK_UCC:-}" ]]; then
  success "Kotak UCC          : CONFIGURED"
fi

# ── Check Remote Connectivity & Architecture ──────────────────────────────
info "Checking SSH connection to remote host..."
REMOTE_ARCH=""
CONNECTED=0
MAX_ATTEMPTS=2

for attempt in $(seq 1 ${MAX_ATTEMPTS}); do
  if SSH_CHECK=$(ssh "${SSH_OPTS[@]}" "${USER_NAME}@${TARGET_IP}" "uname -m" 2>/dev/null); then
    RAW_ARCH=$(echo "${SSH_CHECK}" | tail -n 1 | tr -d '[:space:]')
    case "${RAW_ARCH}" in
      x86_64)
        REMOTE_ARCH="amd64"
        ;;
      aarch64|arm64)
        REMOTE_ARCH="arm64"
        ;;
      *)
        warn "Unknown remote architecture '${RAW_ARCH}', defaulting to amd64"
        REMOTE_ARCH="amd64"
        ;;
    esac
    success "Connected! Remote architecture: ${RAW_ARCH} (${REMOTE_ARCH})"
    CONNECTED=1
    break
  else
    if [[ ${attempt} -lt ${MAX_ATTEMPTS} ]]; then
      warn "SSH attempt ${attempt}/${MAX_ATTEMPTS} failed, retrying in 3s..."
      sleep 3
    fi
  fi
done

if [[ "${CONNECTED}" -eq 0 ]]; then
  error "Could not establish SSH connection to ${USER_NAME}@${TARGET_IP}:${PORT}."
  echo -e "\n${BOLD}${YELLOW}Diagnostic Details:${NC}"
  echo "  SSH error: ${SSH_CHECK}"
  echo -e "\n${BOLD}Troubleshooting for Oracle Cloud (OCI):${NC}"
  echo "  1. If you see 'timed out during banner exchange', the instance may be hung or initializing."
  echo "     Go to Oracle Cloud Console -> Compute -> Instances -> Click 'Reboot'."
  echo "  2. Ensure Port 22 Ingress is allowed in your Virtual Cloud Network (VCN) Default Security List."
  echo "  3. Ensure SSH key permissions are 600 (chmod 600 ${KEY_PATH})."
  echo "  4. Use OCI Cloud Shell / Console Connection to check instance logs if it remains unreachable."
  echo ""
  info "Cross-compiling both Linux binaries (amd64 and arm64) locally so they are ready..."

  if [[ -d "web/node_modules" ]]; then
    node web/build.js
  fi
  go run ./scripts/compress_assets.go
  RESOLVED_VER="$(git describe --tags --abbrev=0 2>/dev/null || git tag -l "v*" --sort=-v:refname 2>/dev/null | head -n 1 || true)"
  VERSION="${APP_VERSION:-${RESOLVED_VER:-v0.7.1}}"
  COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo "none")
  BUILD_DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  LDFLAGS="-s -w -X github.com/akshaykhairmode/scalper/internal/version.Version=${VERSION} \
-X github.com/akshaykhairmode/scalper/internal/version.Commit=${COMMIT} \
-X github.com/akshaykhairmode/scalper/internal/version.BuildDate=${BUILD_DATE}"

  mkdir -p bin
  for arch in amd64 arm64; do
    env GOOS=linux GOARCH="${arch}" CGO_ENABLED=0 \
      go build -trimpath -ldflags "${LDFLAGS}" -o "bin/scalper-linux-${arch}" ./cmd/scalper
    success "Built bin/scalper-linux-${arch} ($(ls -lh "bin/scalper-linux-${arch}" | awk '{print $5}'))"
  done

  echo -e "\n${YELLOW}Once the server is reachable, re-run:${NC}"
  echo "  SERVER_IP=\"${TARGET_IP}\" SSH_KEY=\"${KEY_PATH}\" ./scripts/deploy.sh"
  exit 1
fi

if [[ -n "${OVERRIDE_ARCH}" ]]; then
  REMOTE_ARCH="${OVERRIDE_ARCH}"
fi

TARGET_BIN="bin/scalper-linux-${REMOTE_ARCH}"

# ── Step 1: Pre-compress Assets & Compile Binary ───────────────────────────
if [[ "${SKIP_BUILD}" == "1" && -f "${TARGET_BIN}" ]]; then
  info "SKIP_BUILD=1: Using existing binary ${TARGET_BIN}"
else
  info "Step 1/5: Compressing static assets..."
  if [[ -d "web/node_modules" ]]; then
    node web/build.js
  fi
  go run ./scripts/compress_assets.go

  info "Step 2/5: Compiling Scalper binary for linux/${REMOTE_ARCH}..."
  mkdir -p bin

  RESOLVED_VER="$(git describe --tags --abbrev=0 2>/dev/null || git tag -l "v*" --sort=-v:refname 2>/dev/null | head -n 1 || true)"
  VERSION="${APP_VERSION:-${RESOLVED_VER:-v0.7.1}}"
  COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo "none")
  BUILD_DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  LDFLAGS="-s -w -X github.com/akshaykhairmode/scalper/internal/version.Version=${VERSION} \
-X github.com/akshaykhairmode/scalper/internal/version.Commit=${COMMIT} \
-X github.com/akshaykhairmode/scalper/internal/version.BuildDate=${BUILD_DATE}"

  env GOOS=linux GOARCH="${REMOTE_ARCH}" CGO_ENABLED=0 \
    go build -trimpath -ldflags "${LDFLAGS}" -o "${TARGET_BIN}" ./cmd/scalper

  BIN_SIZE=$(ls -lh "${TARGET_BIN}" | awk '{print $5}')
  success "Compiled ${TARGET_BIN} (${BIN_SIZE})"
fi

# ── Step 2: Ensure Remote Directory Exists ─────────────────────────────────
info "Step 3/5: Preparing remote directory ${REMOTE_DIR}..."
ssh "${SSH_OPTS[@]}" "${USER_NAME}@${TARGET_IP}" "mkdir -p ${REMOTE_DIR}"

# ── Step 3: Upload Binary & Configuration ──────────────────────────────────
info "Step 4/5: Uploading binary and configuration..."

# Upload binary safely via temp file and atomic move
scp "${SCP_OPTS[@]}" "${TARGET_BIN}" "${USER_NAME}@${TARGET_IP}:${REMOTE_DIR}/scalper.tmp"
ssh "${SSH_OPTS[@]}" "${USER_NAME}@${TARGET_IP}" "chmod +x ${REMOTE_DIR}/scalper.tmp && mv -f ${REMOTE_DIR}/scalper.tmp ${REMOTE_DIR}/scalper && (sudo chcon -t bin_t ${REMOTE_DIR}/scalper 2>/dev/null || true)"

# Upload config if present locally; otherwise ensure remote config.yaml exists
if [[ -f "${LOCAL_CONFIG}" ]]; then
  info "Uploading ${LOCAL_CONFIG}..."
  scp "${SCP_OPTS[@]}" "${LOCAL_CONFIG}" "${USER_NAME}@${TARGET_IP}:${REMOTE_DIR}/config.yaml"
  ssh "${SSH_OPTS[@]}" "${USER_NAME}@${TARGET_IP}" "chmod 600 ${REMOTE_DIR}/config.yaml"
elif [[ -f "config.example.yaml" ]]; then
  info "Ensuring remote config.yaml is present from config.example.yaml..."
  scp "${SCP_OPTS[@]}" "config.example.yaml" "${USER_NAME}@${TARGET_IP}:${REMOTE_DIR}/config.example.yaml"
  ssh "${SSH_OPTS[@]}" "${USER_NAME}@${TARGET_IP}" "test -f ${REMOTE_DIR}/config.yaml || (cp ${REMOTE_DIR}/config.example.yaml ${REMOTE_DIR}/config.yaml && chmod 600 ${REMOTE_DIR}/config.yaml)"
fi

# Configure remote environment file with credentials if present in environment
ENV_PAYLOAD=""
append_env() {
  local k="$1"
  local v="$2"
  if [[ -n "${v}" ]]; then
    # Escape backslashes and double quotes for systemd EnvironmentFile
    v="${v//\\/\\\\}"
    v="${v//\"/\\\"}"
    ENV_PAYLOAD+="${k}=\"${v}\""$'\n'
  fi
}

append_env "KOTAK_CONSUMER_KEY" "${KOTAK_CONSUMER_KEY:-}"
append_env "KOTAK_MPIN" "${KOTAK_MPIN:-}"
append_env "KOTAK_MOBILE_NUMBER" "${KOTAK_MOBILE_NUMBER:-${KOTAK_MOBILE:-}}"
append_env "KOTAK_UCC" "${KOTAK_UCC:-}"
append_env "KOTAK_NEO_FIN_KEY" "${KOTAK_NEO_FIN_KEY:-}"
append_env "SERVE_AUTH_USERNAME" "${SERVE_AUTH_USERNAME:-${SCALPER_SERVE_USERNAME:-}}"
append_env "SERVE_AUTH_PASSWORD" "${SERVE_AUTH_PASSWORD:-${SCALPER_SERVE_PASSWORD:-}}"
append_env "OPENROUTER_API_KEY" "${OPENROUTER_API_KEY:-}"
append_env "TYPESAFE_API_KEY" "${TYPESAFE_API_KEY:-}"
append_env "AI_AGENT_API_KEY" "${AI_AGENT_API_KEY:-}"
append_env "GEMINI_API_KEY" "${GEMINI_API_KEY:-}"

if [[ -n "${ENV_PAYLOAD}" ]]; then
  info "Configuring remote credentials in /etc/default/${SVC_NAME}..."
  printf "%s" "${ENV_PAYLOAD}" | \
    ssh "${SSH_OPTS[@]}" "${USER_NAME}@${TARGET_IP}" "sudo tee /etc/default/${SVC_NAME} >/dev/null && sudo chmod 600 /etc/default/${SVC_NAME}"
fi

# ── Step 5: Configure Host Resources, Firewall & systemd Service ──────────
info "Step 5/5: Configuring host optimization, memory swap, firewall & service..."

REMOTE_SETUP_SCRIPT=$(cat <<EOF
set -e

# 1. Detect Host OS & Cloud Environment
IS_ORACLE=0
if [ -f /etc/os-release ] && grep -qi 'oracle' /etc/os-release; then
  IS_ORACLE=1
elif [ -f /etc/oracle-release ]; then
  IS_ORACLE=1
elif systemctl list-unit-files 2>/dev/null | grep -q 'oracle-cloud-agent'; then
  IS_ORACLE=1
fi

if [ "\${IS_ORACLE}" -eq 1 ]; then
  echo ">>> [Host Optimization] Oracle Linux / OCI host detected."
  # Stop and disable resource-heavy background telemetry agent (consumes ~400MB RAM)
  for svc in oracle-cloud-agent oracle-cloud-agent-updater; do
    if systemctl list-unit-files 2>/dev/null | grep -q "\${svc}"; then
      if systemctl is-active --quiet "\${svc}" 2>/dev/null || systemctl is-enabled --quiet "\${svc}" 2>/dev/null; then
        echo ">>> [Host Optimization] Disabling heavy background agent '\${svc}' to free ~400MB RAM..."
        sudo systemctl stop "\${svc}" 2>/dev/null || true
        sudo systemctl disable "\${svc}" 2>/dev/null || true
      fi
    fi
  done
fi

# Also disable unnecessary high-memory background daemons on low-memory VMs if running
for bloat in packagekit snapd.seeded; do
  if systemctl is-active --quiet "\${bloat}" 2>/dev/null; then
    echo ">>> [Host Optimization] Stopping unused background daemon '\${bloat}'..."
    sudo systemctl stop "\${bloat}" 2>/dev/null || true
    sudo systemctl disable "\${bloat}" 2>/dev/null || true
  fi
done

# 2. Verify Host Memory & Provision Swap
TOTAL_MEM_MB=\$(awk '/MemTotal/{print int(\$2/1024)}' /proc/meminfo 2>/dev/null || echo 0)
TOTAL_SWAP_MB=\$(awk '/SwapTotal/{print int(\$2/1024)}' /proc/meminfo 2>/dev/null || echo 0)
echo ">>> [Memory Check] RAM: \${TOTAL_MEM_MB} MB | Existing Swap: \${TOTAL_SWAP_MB} MB"

# If total swap is under 1.5GB (1536MB) on a server with <= 4GB RAM, ensure a 2GB swapfile is provisioned
if [ "\${TOTAL_SWAP_MB}" -lt 1536 ] && [ "\${TOTAL_MEM_MB}" -lt 4096 ]; then
  if ! swapon --show 2>/dev/null | grep -q '/swapfile'; then
    echo ">>> [Memory Check] Low RAM (\${TOTAL_MEM_MB} MB) and insufficient swap (\${TOTAL_SWAP_MB} MB). Provisioning 2GB swapfile..."
    if [ ! -f /swapfile ]; then
      sudo fallocate -l 2G /swapfile 2>/dev/null || sudo dd if=/dev/zero of=/swapfile bs=1M count=2048 2>/dev/null || true
    fi
    if [ -f /swapfile ]; then
      sudo chmod 600 /swapfile
      if ! file /swapfile 2>/dev/null | grep -q "swap file"; then
        sudo mkswap /swapfile >/dev/null 2>&1 || true
      fi
      sudo swapon /swapfile >/dev/null 2>&1 || true
      grep -q '/swapfile' /etc/fstab || echo '/swapfile none swap sw 0 0' | sudo tee -a /etc/fstab >/dev/null
      echo ">>> [Memory Check] 2GB swapfile successfully active and persisted in /etc/fstab."
    fi
  fi
elif [ -f /swapfile ] && ! swapon --show 2>/dev/null | grep -q '/swapfile'; then
  echo ">>> [Memory Check] Activating existing /swapfile..."
  sudo chmod 600 /swapfile
  sudo swapon /swapfile >/dev/null 2>&1 || true
fi

# Set optimal swappiness (10) so RAM is utilized first and swapping occurs only under genuine pressure
if [ -f /proc/sys/vm/swappiness ]; then
  CURRENT_SWAPPINESS=\$(cat /proc/sys/vm/swappiness 2>/dev/null || echo 60)
  if [ "\${CURRENT_SWAPPINESS}" -gt 20 ]; then
    sudo sysctl -w vm.swappiness=10 >/dev/null 2>&1 || true
    if [ -d /etc/sysctl.d ]; then
      echo "vm.swappiness=10" | sudo tee /etc/sysctl.d/99-scalper-swap.conf >/dev/null 2>&1 || true
    fi
  fi
fi

echo ">>> [Memory Status]:"
free -h 2>/dev/null || true

# 3. Open firewall ports (80 HTTP, 443 HTTPS)
if command -v firewall-cmd >/dev/null 2>&1 && sudo systemctl is-active --quiet firewalld; then
  sudo firewall-cmd --add-service=http --permanent >/dev/null 2>&1 || true
  sudo firewall-cmd --add-service=https --permanent >/dev/null 2>&1 || true
  sudo firewall-cmd --reload >/dev/null 2>&1 || true
elif command -v ufw >/dev/null 2>&1 && sudo ufw status 2>/dev/null | grep -q "Status: active"; then
  sudo ufw allow 80/tcp >/dev/null 2>&1 || true
  sudo ufw allow 443/tcp >/dev/null 2>&1 || true
fi

# 4. Install Caddy reverse proxy with automatic TLS if not already installed
if ! command -v caddy >/dev/null 2>&1 && [ ! -f /usr/local/bin/caddy ]; then
  echo ">>> [Caddy Installation] Caddy not found on remote server. Installing Caddy v2.8.4..."
  ARCH="\$(uname -m)"
  case "\${ARCH}" in
    x86_64) CADDY_ARCH="amd64" ;;
    aarch64|arm64) CADDY_ARCH="arm64" ;;
    *) CADDY_ARCH="amd64" ;;
  esac
  curl -sSL "https://github.com/caddyserver/caddy/releases/download/v2.8.4/caddy_2.8.4_linux_\${CADDY_ARCH}.tar.gz" | sudo tar -xz -C /tmp caddy
  sudo mv /tmp/caddy /usr/local/bin/caddy
  sudo chmod +x /usr/local/bin/caddy
  echo ">>> [Caddy Installation] Successfully installed /usr/local/bin/caddy."
fi

# 5. Configure /etc/caddy/Caddyfile with automatic TLS and WebSocket reverse proxy
sudo mkdir -p /etc/caddy
CADDYFILE_CONTENT="${CADDY_DOMAIN} {
    reverse_proxy 127.0.0.1:8080
}"
CURRENT_CADDYFILE="\$(sudo cat /etc/caddy/Caddyfile 2>/dev/null || true)"
if [ "\${CURRENT_CADDYFILE}" != "\${CADDYFILE_CONTENT}" ]; then
  echo ">>> [Caddy Config] Configuring /etc/caddy/Caddyfile for ${CADDY_DOMAIN}..."
  echo "\${CADDYFILE_CONTENT}" | sudo tee /etc/caddy/Caddyfile >/dev/null
fi

# 6. Setup or update caddy.service
CADDY_SVC_FILE="/etc/systemd/system/caddy.service"
if [ ! -f "\${CADDY_SVC_FILE}" ]; then
  echo ">>> [Caddy Service] Creating \${CADDY_SVC_FILE}..."
  sudo tee "\${CADDY_SVC_FILE}" >/dev/null <<'CADDY_EOF'
[Unit]
Description=Caddy Web Server with Automatic HTTPS
After=network.target network-online.target
Wants=network-online.target

[Service]
Type=notify
User=root
ExecStart=/usr/local/bin/caddy run --environ --config /etc/caddy/Caddyfile
ExecReload=/usr/local/bin/caddy reload --config /etc/caddy/Caddyfile
Restart=always
RestartSec=3s
LimitNOFILE=1048576

[Install]
WantedBy=multi-user.target
CADDY_EOF
fi

# 7. Reload and start/reload Caddy
sudo systemctl daemon-reload
sudo systemctl enable caddy >/dev/null 2>&1
if systemctl is-active --quiet caddy; then
  echo ">>> [Caddy Service] Reloading Caddy..."
  sudo systemctl reload caddy 2>/dev/null || sudo systemctl restart caddy
else
  echo ">>> [Caddy Service] Starting Caddy..."
  sudo systemctl start caddy
fi

# 8. Setup or update Scalper systemd service
SERVICE_FILE="/etc/systemd/system/${SVC_NAME}.service"
sudo tee "\${SERVICE_FILE}" >/dev/null <<SERVICE_EOF
[Unit]
Description=Scalper Trading Terminal (Serve Mode)
Documentation=https://github.com/akshaykhairmode/scalper
After=network.target network-online.target
Wants=network-online.target

[Service]
Type=simple
User=${USER_NAME}
WorkingDirectory=${REMOTE_DIR}
EnvironmentFile=-/etc/default/${SVC_NAME}
ExecStart=${REMOTE_DIR}/scalper -serve -config ${REMOTE_DIR}/config.yaml
Restart=always
RestartSec=3
LimitNOFILE=65535
StandardOutput=journal
StandardError=journal
SyslogIdentifier=${SVC_NAME}

[Install]
WantedBy=multi-user.target
SERVICE_EOF

# 9. Reload daemon and start Scalper service
sudo systemctl daemon-reload
sudo systemctl enable "${SVC_NAME}" >/dev/null 2>&1
sudo systemctl restart "${SVC_NAME}"
sleep 1
sudo systemctl status "${SVC_NAME}" --no-pager --lines=8 || true
EOF
)

ssh "${SSH_OPTS[@]}" "${USER_NAME}@${TARGET_IP}" "${REMOTE_SETUP_SCRIPT}"

# ── Health Check Verification ──────────────────────────────────────────────
info "Verifying service health on remote host..."
REMOTE_HEALTH=$(ssh "${SSH_OPTS[@]}" "${USER_NAME}@${TARGET_IP}" "curl -sf http://127.0.0.1:8080/healthz 2>/dev/null || curl -sf http://127.0.0.1:8080/api/health 2>/dev/null || echo 'starting'")

if [[ "${REMOTE_HEALTH}" != "starting" && -n "${REMOTE_HEALTH}" ]]; then
  success "Scalper service is HEALTHY and RUNNING!"
else
  info "Scalper process started (health probe will become active once dependencies load)."
fi

echo -e "\n${BOLD}${GREEN}================================================================${NC}"
echo -e "${BOLD}${GREEN}  Deployment Successful!${NC}"
echo -e "${BOLD}${GREEN}================================================================${NC}"
echo -e "Access URL (HTTPS) : ${CYAN}https://${CADDY_DOMAIN}/${NC}"
echo -e "Access URL (HTTP)  : ${CYAN}http://${CADDY_DOMAIN}/ (Redirects to HTTPS)${NC}"
echo -e "Direct Loopback    : ${YELLOW}http://127.0.0.1:8080/${NC}"
echo -e "Health Check       : ${CYAN}https://${CADDY_DOMAIN}/api/health${NC}"
echo -e "SSH Command        : ${YELLOW}ssh -i ${KEY_PATH} ${USER_NAME}@${TARGET_IP}${NC}"
echo -e "Scalper Logs       : ${YELLOW}sudo journalctl -u ${SVC_NAME} -f${NC}"
echo -e "Caddy Logs         : ${YELLOW}sudo journalctl -u caddy -f${NC}"
echo -e "Restart Service    : ${YELLOW}sudo systemctl restart ${SVC_NAME}${NC}"
echo ""
