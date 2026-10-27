#!/usr/bin/env bash
# Enforce the total statement-coverage gate.
#
# Usage: check-coverage.sh <coverage.out> [minimum-percent]
set -euo pipefail

profile="${1:-coverage.out}"
minimum="${2:-85}"

if [[ ! -f "$profile" ]]; then
  echo "coverage profile not found: $profile" >&2
  echo "run: go test ./... -coverprofile=$profile -covermode=atomic" >&2
  exit 2
fi

total="$(go tool cover -func="$profile" | awk '/^total:/ {gsub(/%/, "", $3); print $3}')"
if [[ -z "$total" ]]; then
  echo "could not read total coverage from $profile" >&2
  exit 2
fi

awk -v total="$total" -v min="$minimum" 'BEGIN {
  printf "total coverage: %.1f%% (gate %.1f%%)\n", total, min
  if (total + 0 < min + 0) {
    printf "coverage gate failed: %.1f%% < %.1f%%\n", total, min > "/dev/stderr"
    exit 1
  }
}'
