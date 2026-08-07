#!/usr/bin/env bash
set -euo pipefail

: "${GH_TOKEN:?GH_TOKEN is required}"
: "${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required}"
: "${GITHUB_REF_NAME:?GITHUB_REF_NAME is required}"

repository_endpoint="repos/${GITHUB_REPOSITORY}"
release_endpoint="${repository_endpoint}/releases/tags/${GITHUB_REF_NAME}"

if ! gh api --method GET "$repository_endpoint" --silent; then
  echo "release preflight: cannot verify repository access" >&2
  exit 1
fi

error_file="$(mktemp)"
trap 'rm -f "$error_file"' EXIT

if draft="$(gh api --method GET "$release_endpoint" --jq '.draft' 2>"$error_file")"; then
  case "$draft" in
    true)
      echo "release preflight: existing exact-tag draft may be replaced"
      ;;
    false)
      echo "release preflight: exact tag is already published; refusing replacement" >&2
      exit 1
      ;;
    *)
      printf 'release preflight: unexpected draft value %q\n' "$draft" >&2
      exit 1
      ;;
  esac
else
  status=$?
  error_text="$(cat "$error_file")"
  if [[ "$error_text" == *"(HTTP 404)"* ]]; then
    echo "release preflight: no existing exact-tag release"
    exit 0
  fi
  printf '%s\n' "$error_text" >&2
  if (( status == 0 )); then
    exit 1
  fi
  exit "$status"
fi
