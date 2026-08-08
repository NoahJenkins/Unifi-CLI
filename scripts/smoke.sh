#!/usr/bin/env bash
# Smoke checks for unifi-cli.
# Default: build + vet + unit tests.
# Live controller: UNIFI_IT=1 with UNIFI_HOST and UNIFI_API_KEY set.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

BIN="${UNIFI_BIN:-$ROOT/dist/unifi}"

echo "==> build"
mkdir -p "$(dirname "$BIN")"
go build -o "$BIN" ./cmd/unifi

echo "==> format"
unformatted="$(git ls-files '*.go' | xargs gofmt -l)"
if [[ -n "$unformatted" ]]; then
  echo "Go files need gofmt:" >&2
  echo "$unformatted" >&2
  exit 1
fi

echo "==> vet"
go vet ./...

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

case "${UNIFI_INSECURE:-}" in
  1 | t | T | TRUE | true | True)
    echo "WARNING: TLS certificate verification is disabled by UNIFI_INSECURE=${UNIFI_INSECURE}" >&2
    ;;
esac

echo "==> live read-only suite"
go run ./cmd/unifi-live-test --binary "$BIN" --report-dir "$ROOT/dist/test-reports"

echo "==> smoke OK"
