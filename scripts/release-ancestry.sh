#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 2 ]]; then
  echo "usage: release-ancestry.sh COMMIT REF" >&2
  exit 2
fi

release_commit="$1"
main_ref="$2"
if [[ ! "$release_commit" =~ ^[0-9a-f]{40}$ ]]; then
  echo "release ancestry: invalid release commit" >&2
  exit 2
fi
if ! git rev-parse --verify "$main_ref^{commit}" >/dev/null 2>&1; then
  echo "release ancestry: main reference is unavailable" >&2
  exit 1
fi
if ! git merge-base --is-ancestor "$release_commit" "$main_ref"; then
  echo "release ancestry: release commit is not reachable from protected main" >&2
  exit 1
fi
echo "release ancestry: release commit is reachable from protected main"
