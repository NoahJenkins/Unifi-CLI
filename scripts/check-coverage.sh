#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PROFILE="$(mktemp "${TMPDIR:-/tmp}/unifi-cli-coverage.XXXXXX")"
trap 'rm -f "$PROFILE"' EXIT

cd "$ROOT"
go test -coverprofile="$PROFILE" ./internal/cli

coverage="$(go tool cover -func="$PROFILE" | awk '/^total:/ {gsub(/%/, "", $3); print $3}')"
minimum="50.0"
if [[ -z "$coverage" ]]; then
  echo "could not determine internal/cli coverage" >&2
  exit 1
fi

if ! awk -v coverage="$coverage" -v minimum="$minimum" 'BEGIN { exit !(coverage + 0 >= minimum + 0) }'; then
  echo "internal/cli coverage ${coverage}% is below ${minimum}%" >&2
  exit 1
fi

echo "internal/cli coverage ${coverage}% meets ${minimum}%"
