#!/usr/bin/env bash
# Smoke checks for unifi-cli.
# Default: build + unit tests.
# Live controller: UNIFI_IT=1 with UNIFI_HOST and UNIFI_API_KEY set.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

BIN="${UNIFI_BIN:-$ROOT/dist/unifi}"

echo "==> build"
mkdir -p "$(dirname "$BIN")"
go build -o "$BIN" ./cmd/unifi

echo "==> unit tests"
go test ./...

if [[ "${UNIFI_IT:-}" != "1" ]]; then
  echo "==> skip live IT (set UNIFI_IT=1 to enable)"
  exit 0
fi

if [[ -z "${UNIFI_HOST:-}" ]]; then
  echo "UNIFI_IT=1 requires UNIFI_HOST" >&2
  exit 1
fi

if [[ -z "${UNIFI_API_KEY:-}" ]]; then
  echo "UNIFI_IT=1 requires UNIFI_API_KEY" >&2
  exit 1
fi

export UNIFI_INSECURE="${UNIFI_INSECURE:-true}"

echo "==> live read-only suite"
go run ./cmd/unifi-live-test --binary "$BIN" --report-dir "$ROOT/dist/test-reports"

echo "==> smoke OK"
