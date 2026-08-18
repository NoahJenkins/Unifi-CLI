#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PERF_DIR="$(mktemp -d "${TMPDIR:-/tmp}/unifi-cli-performance.XXXXXX")"
trap 'rm -rf "$PERF_DIR"' EXIT
cd "$ROOT"

benchmark_output="$PERF_DIR/benchmarks.txt"
go test ./internal/client -run '^$' -bench 'BenchmarkOfficial(Collection10000|DetailReads1000)$' -benchtime=1x -benchmem -count=3 | tee "$benchmark_output"

median_metric() {
  local benchmark="$1"
  local column="$2"
  awk -v prefix="${benchmark}-" -v column="$column" '$1 ~ ("^" prefix) { print $column }' "$benchmark_output" | sort -n | sed -n '2p'
}

collection_ns="$(median_metric BenchmarkOfficialCollection10000 3)"
collection_bytes="$(median_metric BenchmarkOfficialCollection10000 5)"
details_ns="$(median_metric BenchmarkOfficialDetailReads1000 3)"
details_bytes="$(median_metric BenchmarkOfficialDetailReads1000 5)"
if [[ -z "$collection_ns" || -z "$collection_bytes" || -z "$details_ns" || -z "$details_bytes" ]]; then
  echo "performance: could not parse benchmark medians" >&2
  exit 1
fi

binary="$PERF_DIR/unifi"
go build -o "$binary" ./cmd/unifi
"$binary" --help >/dev/null
help_samples="$PERF_DIR/help-seconds.txt"
for _ in 1 2 3; do
  { /usr/bin/time -p "$binary" --help >/dev/null; } 2>&1 | awk '$1 == "real" { print $2 }' >>"$help_samples"
done
help_seconds="$(sort -n "$help_samples" | sed -n '2p')"
if [[ -z "$help_seconds" ]]; then
  echo "performance: could not parse warm help median" >&2
  exit 1
fi

echo "performance medians: collection=${collection_ns}ns ${collection_bytes}B; details=${details_ns}ns ${details_bytes}B; warm-help=${help_seconds}s"

if [[ "${UNIFI_RELEASE_HOST:-0}" != "1" ]]; then
  echo "performance: budgets recorded but not enforced (set UNIFI_RELEASE_HOST=1 on the designated darwin/arm64 release host)"
  exit 0
fi
if [[ "$(go env GOOS)/$(go env GOARCH)" != "darwin/arm64" ]]; then
  echo "performance: UNIFI_RELEASE_HOST=1 requires darwin/arm64" >&2
  exit 1
fi

max_bytes=$((256 * 1024 * 1024))
awk -v value="$collection_ns" 'BEGIN { exit !(value < 2000000000) }' || { echo "performance: collection median exceeds 2 seconds" >&2; exit 1; }
awk -v value="$collection_bytes" -v maximum="$max_bytes" 'BEGIN { exit !(value < maximum) }' || { echo "performance: collection median exceeds 256 MiB" >&2; exit 1; }
awk -v value="$details_ns" 'BEGIN { exit !(value < 10000000000) }' || { echo "performance: detail median exceeds 10 seconds" >&2; exit 1; }
awk -v value="$details_bytes" -v maximum="$max_bytes" 'BEGIN { exit !(value < maximum) }' || { echo "performance: detail median exceeds 256 MiB" >&2; exit 1; }
awk -v value="$help_seconds" 'BEGIN { exit !(value < 0.5) }' || { echo "performance: warm help median exceeds 500 ms" >&2; exit 1; }
echo "performance: all designated release-host budgets pass"
