#!/usr/bin/env bash
#
# scripts/deploy.sh
#
# Cross-compiles Scalper for Linux and deploys it to a remote server (e.g. Oracle Cloud Free Tier)
# running in serve mode.
#
# Environment variables:
#   SERVER_IP / DEPLOY_IP       (Required) Remote server IP or hostname (e.g. 192.0.2.1)
#   SSH_KEY / DEPLOY_KEY         (Required) Path to SSH private key file
#   DEPLOY_USER / SERVER_USER   (Optional) Remote SSH user, default: "opc"
#   DEPLOY_PORT                 (Optional) Remote SSH port, default: 22
#   DEPLOY_DIR                  (Optional) Remote directory, default: "/home/$DEPLOY_USER/scalper"
#   CONFIG_FILE                 (Optional) Local config file to upload, default: "config.yaml"
#   SERVICE_NAME                (Optional) systemd service name, default: "scalper"
#   TARGET_ARCH                 (Optional) Override auto-detected architecture: "amd64" or "arm64"
#   SKIP_BUILD                  (Optional) Set to 1 to skip compilation and upload existing binary
#
# Usage:
#   SERVER_IP="<server-ip>" SSH_KEY="~/.ssh/oci-key.pem" ./scripts/deploy.sh
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
TARGET_IP="${1:-${SERVER_IP:-${DEPLOY_IP:-}}}"
KEY_PATH="${2:-${SSH_KEY:-${DEPLOY_KEY:-}}}"
USER_NAME="${3:-${DEPLOY_USER:-${SERVER_USER:-opc}}}"
PORT="${DEPLOY_PORT:-22}"
REMOTE_DIR="${DEPLOY_DIR:-/home/${USER_NAME}/scalper}"
LOCAL_CONFIG="${CONFIG_FILE:-config.yaml}"
SVC_NAME="${SERVICE_NAME:-scalper}"
OVERRIDE_ARCH="${TARGET_ARCH:-}"
SKIP_BUILD="${SKIP_BUILD:-0}"

# ── Validate Required Inputs ───────────────────────────────────────────────
if [[ -z "${TARGET_IP}" ]]; then
  echo -e "${BOLD}Scalper Deployment Script${NC}\n"
  error "Missing server IP address."
  echo "Usage:"
  echo "  SERVER_IP=\"<ip>\" SSH_KEY=\"<path_to_key>\" ./scripts/deploy.sh"
  echo "  ./scripts/deploy.sh <server_ip> <ssh_key_path> [ssh_user]"
  exit 1
fi

if [[ -z "${KEY_PATH}" ]]; then
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
  error "Missing SSH private key path."
  echo "Please set SSH_KEY=\"/path/to/key.pem\" or pass it as the second argument."
  exit 1
fi

# Expand tilde in key path
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

  go run ./scripts/compress_assets.go
  VERSION=$(git describe --tags --always --dirty 2>/dev/null || echo "dev")
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
  go run ./scripts/compress_assets.go

  info "Step 2/5: Compiling Scalper binary for linux/${REMOTE_ARCH}..."
  mkdir -p bin

  VERSION=$(git describe --tags --always --dirty 2>/dev/null || echo "dev")
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

# Upload config if present locally and not already configured on server
if [[ -f "${LOCAL_CONFIG}" ]]; then
  info "Uploading ${LOCAL_CONFIG}..."
  scp "${SCP_OPTS[@]}" "${LOCAL_CONFIG}" "${USER_NAME}@${TARGET_IP}:${REMOTE_DIR}/config.yaml"
elif [[ -f "config.example.yaml" ]]; then
  warn "No config.yaml found locally. Uploading config.example.yaml as remote config template..."
  scp "${SCP_OPTS[@]}" "config.example.yaml" "${USER_NAME}@${TARGET_IP}:${REMOTE_DIR}/config.example.yaml"
fi

# Configure remote environment file with Kotak credentials if present in environment
KOTAK_KEY_VAL="${KOTAK_CONSUMER_KEY:-}"
KOTAK_MPIN_VAL="${KOTAK_MPIN:-}"
if [[ -n "${KOTAK_KEY_VAL}" ]]; then
  info "Configuring remote Kotak credentials in /etc/default/${SVC_NAME}..."
  printf "KOTAK_CONSUMER_KEY=%s\nKOTAK_MPIN=%s\n" "${KOTAK_KEY_VAL}" "${KOTAK_MPIN_VAL}" | \
    ssh "${SSH_OPTS[@]}" "${USER_NAME}@${TARGET_IP}" "sudo tee /etc/default/${SVC_NAME} >/dev/null && sudo chmod 600 /etc/default/${SVC_NAME}"
fi

# ── Step 4: Configure systemd Service & Firewall ───────────────────────────
info "Step 5/5: Configuring systemd service and firewall..."

REMOTE_SETUP_SCRIPT=$(cat <<EOF
set -e
# 1. Ensure 2GB swapfile is configured on low-memory VMs to prevent OOM freezes
if ! swapon --show | grep -q '/swapfile'; then
  sudo fallocate -l 2G /swapfile 2>/dev/null || sudo dd if=/dev/zero of=/swapfile bs=1M count=2048 2>/dev/null || true
  if [[ -f /swapfile ]]; then
    sudo chmod 600 /swapfile
    sudo mkswap /swapfile >/dev/null 2>&1 || true
    sudo swapon /swapfile >/dev/null 2>&1 || true
    grep -q '/swapfile' /etc/fstab || echo '/swapfile none swap sw 0 0' | sudo tee -a /etc/fstab >/dev/null
  fi
fi

# 2. Open firewall port 8080 if firewalld or ufw is active
if command -v firewall-cmd >/dev/null 2>&1 && sudo systemctl is-active --quiet firewalld; then
  sudo firewall-cmd --add-port=8080/tcp --permanent >/dev/null 2>&1 || true
  sudo firewall-cmd --reload >/dev/null 2>&1 || true
elif command -v ufw >/dev/null 2>&1 && sudo ufw status | grep -q "Status: active"; then
  sudo ufw allow 8080/tcp >/dev/null 2>&1 || true
fi

# 2. Setup or update systemd service
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

# 3. Reload daemon and start service
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
echo -e "Access URL       : ${CYAN}http://${TARGET_IP}:8080/${NC}"
echo -e "Health Check     : ${CYAN}http://${TARGET_IP}:8080/api/health${NC}"
echo -e "SSH Command      : ${YELLOW}ssh -i ${KEY_PATH} ${USER_NAME}@${TARGET_IP}${NC}"
echo -e "Service Logs     : ${YELLOW}sudo journalctl -u ${SVC_NAME} -f${NC}"
echo -e "Restart Service  : ${YELLOW}sudo systemctl restart ${SVC_NAME}${NC}"
echo ""
