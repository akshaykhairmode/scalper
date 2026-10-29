#!/bin/sh
# Scalper installer script
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/akshaykhairmode/scalper/main/install.sh | sh
#
# Custom options via environment variables:
#   VERSION=v1.0.0      Install a specific release version (defaults to latest)
#   BINDIR=/usr/bin     Destination directory for the binary (defaults to /usr/local/bin or ~/.local/bin)
#   REPO=owner/repo     GitHub repository (defaults to akshaykhairmode/scalper)

set -eu

REPO="${REPO:-akshaykhairmode/scalper}"
BIN_NAME="scalper"

# Formatting helpers
if [ -t 1 ]; then
  BOLD="$(printf '\033[1m')"
  GREEN="$(printf '\033[32m')"
  BLUE="$(printf '\033[34m')"
  YELLOW="$(printf '\033[33m')"
  RED="$(printf '\033[31m')"
  RESET="$(printf '\033[0m')"
else
  BOLD=""
  GREEN=""
  BLUE=""
  YELLOW=""
  RED=""
  RESET=""
fi

log_info() {
  printf "%s==>%s %s\n" "$BLUE" "$RESET" "$*"
}

log_success() {
  printf "%s==>%s %s%s%s\n" "$GREEN" "$RESET" "$BOLD" "$*" "$RESET"
}

log_warn() {
  printf "%sWarning:%s %s\n" "$YELLOW" "$RESET" "$*" >&2
}

log_err() {
  printf "%sError:%s %s\n" "$RED" "$RESET" "$*" >&2
}

cleanup() {
  if [ -n "${TMPDIR_INSTALL:-}" ] && [ -d "$TMPDIR_INSTALL" ]; then
    rm -rf "$TMPDIR_INSTALL"
  fi
}
trap cleanup EXIT INT TERM

detect_os() {
  os="$(uname -s)"
  case "$os" in
    Darwin) echo "darwin" ;;
    Linux)  echo "linux" ;;
    MINGW*|MSYS*|CYGWIN*) echo "windows" ;;
    *)
      log_err "Unsupported operating system: $os"
      exit 1
      ;;
  esac
}

detect_arch() {
  arch="$(uname -m)"
  case "$arch" in
    x86_64|amd64) echo "amd64" ;;
    arm64|aarch64) echo "arm64" ;;
    *)
      log_err "Unsupported architecture: $arch"
      exit 1
      ;;
  esac
}

download_file() {
  url="$1"
  dest="$2"
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$url" -o "$dest"
  elif command -v wget >/dev/null 2>&1; then
    wget -qO "$dest" "$url"
  else
    log_err "Neither curl nor wget is installed. Please install curl or wget to continue."
    exit 1
  fi
}

resolve_version() {
  requested="${VERSION:-latest}"
  if [ "$requested" != "latest" ]; then
    case "$requested" in
      v*) echo "$requested" ;;
      *)  echo "v$requested" ;;
    esac
    return
  fi

  # Query latest release tag from GitHub API
  api_url="https://api.github.com/repos/${REPO}/releases/latest"
  tag=""

  if command -v curl >/dev/null 2>&1; then
    api_res="$(curl -sSfL "$api_url" 2>/dev/null || true)"
  elif command -v wget >/dev/null 2>&1; then
    api_res="$(wget -qO- "$api_url" 2>/dev/null || true)"
  fi

  if [ -n "${api_res:-}" ]; then
    tag="$(printf '%s' "$api_res" | grep '"tag_name":' | head -n 1 | sed -E 's/.*"tag_name":[[:space:]]*"([^"]+)".*/\1/')"
  fi

  # Fallback to HTTP redirect if API was rate-limited
  if [ -z "$tag" ]; then
    if command -v curl >/dev/null 2>&1; then
      redirect_url="$(curl -sSfIL -o /dev/null -w "%{url_effective}" "https://github.com/${REPO}/releases/latest" 2>/dev/null || true)"
      tag="$(printf '%s' "$redirect_url" | sed -E 's|.*/tag/||')"
    fi
  fi

  if [ -z "$tag" ] || [ "$tag" = "latest" ]; then
    log_err "Could not resolve the latest release version from https://github.com/${REPO}."
    log_err "Specify a version explicitly, for example: VERSION=v1.0.0 sh install.sh"
    exit 1
  fi

  echo "$tag"
}

verify_checksum() {
  archive_path="$1"
  checksums_path="$2"
  filename="$(basename "$archive_path")"

  expected="$(grep -E "[[:space:]]${filename}\$" "$checksums_path" 2>/dev/null | head -n 1 | awk '{print $1}')"
  if [ -z "$expected" ]; then
    log_warn "Checksum for ${filename} not found in SHA256SUMS. Skipping integrity check."
    return 0
  fi

  actual=""
  if command -v sha256sum >/dev/null 2>&1; then
    actual="$(sha256sum "$archive_path" | awk '{print $1}')"
  elif command -v shasum >/dev/null 2>&1; then
    actual="$(shasum -a 256 "$archive_path" | awk '{print $1}')"
  else
    log_warn "Neither sha256sum nor shasum is available. Skipping checksum check."
    return 0
  fi

  if [ "$expected" != "$actual" ]; then
    log_err "Checksum verification failed for ${filename}!"
    log_err "  Expected: $expected"
    log_err "  Actual:   $actual"
    exit 1
  fi
  log_info "Checksum verified: ${actual}"
}

select_bindir() {
  if [ -n "${BINDIR:-}" ]; then
    echo "$BINDIR"
    return
  fi

  # Default preferred path is /usr/local/bin
  target="/usr/local/bin"
  if [ -w "$target" ] || [ "$(id -u 2>/dev/null || true)" = "0" ]; then
    echo "$target"
    return
  fi

  # If user has sudo privileges, install system-wide to /usr/local/bin
  if command -v sudo >/dev/null 2>&1; then
    echo "$target"
    return
  fi

  # Otherwise fallback to ~/.local/bin
  echo "${HOME}/.local/bin"
}

main() {
  os="$(detect_os)"
  arch="$(detect_arch)"

  log_info "Detected platform: ${os}/${arch}"

  version="$(resolve_version)"
  log_info "Resolved release version: ${version}"

  if [ "$os" = "windows" ]; then
    archive_name="scalper_${version}_${os}_${arch}.zip"
    binary_name="scalper.exe"
  else
    archive_name="scalper_${version}_${os}_${arch}.tar.gz"
    binary_name="scalper"
  fi

  TMPDIR_INSTALL="$(mktemp -d 2>/dev/null || mktemp -d -t 'scalper-install')"

  archive_path="${TMPDIR_INSTALL}/${archive_name}"
  download_url="https://github.com/${REPO}/releases/download/${version}/${archive_name}"
  checksums_url="https://github.com/${REPO}/releases/download/${version}/SHA256SUMS"

  log_info "Downloading ${download_url} ..."
  download_file "$download_url" "$archive_path"

  checksums_path="${TMPDIR_INSTALL}/SHA256SUMS"
  if download_file "$checksums_url" "$checksums_path" 2>/dev/null; then
    verify_checksum "$archive_path" "$checksums_path"
  fi

  log_info "Extracting ${archive_name} ..."
  if [ "$os" = "windows" ]; then
    if ! command -v unzip >/dev/null 2>&1; then
      log_err "unzip is required to extract ${archive_name} on Windows."
      exit 1
    fi
    unzip -q -o "$archive_path" -d "$TMPDIR_INSTALL"
  else
    tar -xzf "$archive_path" -C "$TMPDIR_INSTALL"
  fi

  extracted_bin="${TMPDIR_INSTALL}/${binary_name}"
  if [ ! -f "$extracted_bin" ]; then
    log_err "Extracted archive does not contain ${binary_name}."
    exit 1
  fi
  chmod +x "$extracted_bin"

  dest_dir="$(select_bindir)"
  target_path="${dest_dir}/${binary_name}"

  log_info "Installing ${binary_name} to ${target_path} ..."

  if [ -w "$dest_dir" ] || [ "$(id -u 2>/dev/null || true)" = "0" ]; then
    mkdir -p "$dest_dir"
    mv "$extracted_bin" "$target_path"
    chmod +x "$target_path"
  elif command -v sudo >/dev/null 2>&1; then
    log_info "Requesting sudo permissions to write to ${dest_dir} ..."
    sudo mkdir -p "$dest_dir"
    sudo mv "$extracted_bin" "$target_path"
    sudo chmod +x "$target_path"
  else
    dest_dir="${HOME}/.local/bin"
    target_path="${dest_dir}/${binary_name}"
    log_warn "Could not write to /usr/local/bin and sudo is not available."
    log_info "Installing to user directory instead: ${target_path}"
    mkdir -p "$dest_dir"
    mv "$extracted_bin" "$target_path"
    chmod +x "$target_path"
  fi

  log_success "Scalper ${version} successfully installed to ${target_path}!"

  case ":$PATH:" in
    *":${dest_dir}:"*) ;;
    *)
      printf "\n%sNote:%s %s is not in your PATH.\n" "$YELLOW" "$RESET" "$dest_dir"
      printf "Add it to your shell configuration (e.g. ~/.bashrc or ~/.zshrc):\n"
      printf "  export PATH=\"%s:\$PATH\"\n\n" "$dest_dir"
      ;;
  esac

  printf "Run '%s -help' or '%s -version' to get started.\n" "$binary_name" "$binary_name"
}

main "$@"
