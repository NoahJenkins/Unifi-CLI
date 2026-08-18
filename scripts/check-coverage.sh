#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COVERAGE_DIR="$(mktemp -d "${TMPDIR:-/tmp}/unifi-cli-coverage.XXXXXX")"
trap 'rm -rf "$COVERAGE_DIR"' EXIT

cd "$ROOT"

check_floor() {
  local label="$1"
  local package="$2"
  local minimum="$3"
  local profile="$COVERAGE_DIR/${label//\//-}.out"
  local coverage

  go test -coverprofile="$profile" "$package" >/dev/null
  coverage="$(go tool cover -func="$profile" | awk '/^total:/ {gsub(/%/, "", $3); print $3}')"
  if [[ -z "$coverage" ]]; then
    echo "could not determine ${label} coverage" >&2
    exit 1
  fi
  if ! awk -v coverage="$coverage" -v minimum="$minimum" 'BEGIN { exit !(coverage + 0 >= minimum + 0) }'; then
    echo "${label} coverage ${coverage}% is below ${minimum}%" >&2
    exit 1
  fi
  echo "${label} coverage ${coverage}% meets ${minimum}%"
}

check_floor "internal-total" "./internal/..." "75.0"
check_floor "internal/cli" "./internal/cli" "65.0"
check_floor "internal/client" "./internal/client" "85.0"
check_floor "internal/config" "./internal/config" "80.0"
check_floor "internal/domain" "./internal/domain" "75.0"
check_floor "internal/authstore" "./internal/authstore" "65.0"
check_floor "internal/privatefile" "./internal/privatefile" "75.0"
