#!/bin/sh
# Explicit public-source download. Tests never call this script.
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cache="$root/.cache/unifi-openapi"
mkdir -p "$cache"
umask 077
tmp=$(mktemp "$cache/.download.XXXXXX")
trap 'rm -f "$tmp"' EXIT HUP INT TERM
for version in 10.4.57 10.6.109; do
  case "$version" in
    10.4.57) expected=3773947b4572b1bdc84272d3d2b9ebb5cad0d9aaa2640c3418568df97c9df59d ;;
    10.6.109) expected=2356b910f76c175db9a22ce18070c2c40af02d9843ca67628410a5afbfc5086c ;;
  esac
  curl --fail --silent --show-error --proto '=https' --connect-timeout 10 --max-time 60 \
    "https://developer.ui.com/network/v$version/openapi.json" -o "$tmp"
  if command -v sha256sum >/dev/null 2>&1; then
    actual=$(sha256sum "$tmp" | cut -d ' ' -f 1)
  else
    actual=$(shasum -a 256 "$tmp" | cut -d ' ' -f 1)
  fi
  if [ "$actual" != "$expected" ]; then
    printf '%s\n' "Pinned digest mismatch for $version; review the source change." >&2
    exit 1
  fi
  mv "$tmp" "$cache/$version.json"
  printf '%s\n' "$version verified: $actual"
done
