#!/usr/bin/env bash
set -euo pipefail

script="$(dirname "$0")/next-version.sh"

assert_version() {
  local expected="$1"
  local current="$2"
  shift 2
  local messages=""
  for message in "$@"; do
    if [[ -n "$messages" ]]; then
      messages+=$'\n'
    fi
    messages+="$message"
  done
  local actual
  actual="$(printf '%s\0' "$messages" | bash "$script" "$current")"
  if [[ "$actual" != "$expected" ]]; then
    printf 'expected %s, got %s\n' "$expected" "$actual" >&2
    exit 1
  fi
}

assert_version v0.0.1 "" "chore: initial commit"
assert_version v1.2.4 v1.2.3 "fix: correct quote rounding"
assert_version v1.3.0 v1.2.3 "feat(ui): add strike search"
assert_version v2.0.0 v1.2.3 "feat!: replace the order API"
assert_version v2.0.0 v1.2.3 "refactor: replace internals" "" "BREAKING CHANGE: old callers are unsupported"

echo "semantic version bump tests passed"
