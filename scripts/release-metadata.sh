#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo "usage: release-metadata.sh TAG" >&2
  exit 2
fi

tag="$1"
semver='^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-([0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*))?(\+([0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*))?$'
if [[ ! "$tag" =~ $semver ]]; then
  echo "release metadata: invalid semantic version tag" >&2
  exit 2
fi

version="${tag#v}"
without_build="${version%%+*}"
prerelease=false
if [[ "$without_build" == *-* ]]; then
  prerelease=true
  prerelease_value="${without_build#*-}"
  IFS='.' read -r -a identifiers <<< "$prerelease_value"
  for identifier in "${identifiers[@]}"; do
    if [[ "$identifier" =~ ^[0-9]+$ && "$identifier" != "0" && "$identifier" == 0* ]]; then
      echo "release metadata: numeric prerelease identifiers must not contain leading zeroes" >&2
      exit 2
    fi
  done
fi

notes="docs/releases/${tag}.md"
source_root="${RELEASE_SOURCE_ROOT:-.}"
notes_path="${source_root%/}/$notes"
if [[ ! -f "$notes_path" || -L "$notes_path" ]]; then
  echo "release metadata: release notes are missing or not a regular file" >&2
  exit 1
fi
expected_heading="# \`${tag}\` release notes"
if [[ "$(head -n 1 "$notes_path")" != "$expected_heading" ]]; then
  echo "release metadata: release notes heading does not match tag" >&2
  exit 1
fi

printf 'version\t%s\n' "$version"
printf 'prerelease\t%s\n' "$prerelease"
printf 'notes\t%s\n' "$notes"
