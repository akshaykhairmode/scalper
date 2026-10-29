#!/usr/bin/env bash
# Test script for install.sh logic
set -euo pipefail

echo "==> Checking install.sh syntax with sh -n"
sh -n install.sh

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

echo "==> Testing mock installation flow"
MOCK_BIN="$TMP_DIR/scalper"
echo '#!/bin/sh' > "$MOCK_BIN"
echo 'echo "scalper dev mock"' >> "$MOCK_BIN"
chmod +x "$MOCK_BIN"

# Create a mock release archive
ARCHIVE_NAME="scalper_v9.9.9_$(uname -s | tr '[:upper:]' '[:lower:]')_$(uname -m | sed 's/x86_64/amd64/').tar.gz"
tar -czf "$TMP_DIR/$ARCHIVE_NAME" -C "$TMP_DIR" scalper
rm -f "$MOCK_BIN"

# Create mock SHA256SUMS
if command -v sha256sum >/dev/null 2>&1; then
  (cd "$TMP_DIR" && sha256sum "$ARCHIVE_NAME" > SHA256SUMS)
elif command -v shasum >/dev/null 2>&1; then
  (cd "$TMP_DIR" && shasum -a 256 "$ARCHIVE_NAME" > SHA256SUMS)
fi

DEST_BINDIR="$TMP_DIR/installed_bin"
mkdir -p "$DEST_BINDIR"

# Test helper: override download_file and resolve_version for testing
MOCK_INSTALL_SCRIPT="$TMP_DIR/mock_install.sh"
cp install.sh "$MOCK_INSTALL_SCRIPT"

# Substitute download_file with local file copy
sed -i.bak "s|download_file \"\$download_url\" \"\$archive_path\"|cp \"$TMP_DIR/$ARCHIVE_NAME\" \"\$archive_path\"|g" "$MOCK_INSTALL_SCRIPT"
sed -i.bak "s|download_file \"\$checksums_url\" \"\$checksums_path\"|cp \"$TMP_DIR/SHA256SUMS\" \"\$checksums_path\"|g" "$MOCK_INSTALL_SCRIPT"

VERSION="v9.9.9" BINDIR="$DEST_BINDIR" sh "$MOCK_INSTALL_SCRIPT"

if [ ! -x "$DEST_BINDIR/scalper" ]; then
  echo "FAIL: $DEST_BINDIR/scalper was not installed or is not executable" >&2
  exit 1
fi

OUTPUT="$("$DEST_BINDIR/scalper")"
if [ "$OUTPUT" != "scalper dev mock" ]; then
  echo "FAIL: Unexpected mock binary output: $OUTPUT" >&2
  exit 1
fi

echo "==> install.sh tests passed successfully!"
