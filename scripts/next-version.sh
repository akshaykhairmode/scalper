#!/usr/bin/env bash
set -euo pipefail

latest="${1:-}"
if [[ -n "$latest" ]]; then
  if [[ ! "$latest" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
    echo "latest version must be a v-prefixed semantic version: $latest" >&2
    exit 2
  fi
  major="${BASH_REMATCH[1]}"
  minor="${BASH_REMATCH[2]}"
  patch="${BASH_REMATCH[3]}"
else
  major=0
  minor=0
  patch=0
fi

bump=patch
commits=0
while IFS= read -r -d '' message; do
  commits=$((commits + 1))
  subject="${message%%$'\n'*}"
  breaking=false

  if [[ "$subject" == *"!:"* ]]; then
    breaking=true
  else
    while IFS= read -r line; do
      if [[ "$line" == "BREAKING CHANGE:"* || "$line" == "BREAKING-CHANGE:"* ]]; then
        breaking=true
        break
      fi
    done <<< "$message"
  fi

  if [[ "$breaking" == true ]]; then
    bump=major
  elif [[ "$bump" != major && ( "$subject" == feat:* || "$subject" == feat\(*\):* ) ]]; then
    bump=minor
  fi
done

if (( commits == 0 )); then
  echo "no commits found since the latest release" >&2
  exit 2
fi

case "$bump" in
  major)
    major=$((major + 1))
    minor=0
    patch=0
    ;;
  minor)
    minor=$((minor + 1))
    patch=0
    ;;
  patch)
    patch=$((patch + 1))
    ;;
esac

printf 'v%s.%s.%s\n' "$major" "$minor" "$patch"
