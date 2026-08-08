#!/usr/bin/env bash
set -euo pipefail

: "${GH_TOKEN:?GH_TOKEN is required}"
: "${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required}"
: "${GITHUB_REF_NAME:?GITHUB_REF_NAME is required}"

if [[ $# -ne 2 ]]; then
  echo "usage: publish-release.sh DIST RELEASE_NOTES" >&2
  exit 2
fi
if [[ ! "$GITHUB_REPOSITORY" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]]; then
  echo "release publish: invalid repository identity" >&2
  exit 2
fi
if [[ ! "$GITHUB_REF_NAME" =~ ^v[0-9]+\.[0-9]+\.[0-9]+([+-][0-9A-Za-z.-]+)?$ ]]; then
  echo "release publish: invalid release tag" >&2
  exit 2
fi

dist_dir="$(cd "$1" && pwd -P)"
notes_file="$(cd "$(dirname "$2")" && pwd -P)/$(basename "$2")"
checksums_file="$dist_dir/checksums.txt"
if [[ ! -f "$checksums_file" || -L "$checksums_file" || ! -f "$notes_file" || -L "$notes_file" ]]; then
  echo "release publish: required checksums or release notes are missing" >&2
  exit 1
fi

declare -a asset_names=("checksums.txt")
declare -a asset_paths=("$checksums_file")
while read -r digest filename extra; do
  if [[ -z "${digest:-}" || -z "${filename:-}" || -n "${extra:-}" || ! "$digest" =~ ^[0-9a-f]{64}$ ]]; then
    echo "release publish: malformed checksum manifest" >&2
    exit 1
  fi
  duplicate=false
  for seen_name in "${asset_names[@]}"; do
    if [[ "$seen_name" == "$filename" ]]; then
      duplicate=true
      break
    fi
  done
  if [[ ! "$filename" =~ ^[A-Za-z0-9][A-Za-z0-9._-]*$ || "$duplicate" == true ]]; then
    echo "release publish: unsafe or duplicate asset name" >&2
    exit 1
  fi
  asset_path="$dist_dir/$filename"
  if [[ ! -f "$asset_path" || -L "$asset_path" ]]; then
    echo "release publish: checksum asset is missing or not a regular file: $filename" >&2
    exit 1
  fi
  observed_digest="$(shasum -a 256 "$asset_path" | awk '{print $1}')"
  if [[ "$observed_digest" != "$digest" ]]; then
    echo "release publish: checksum mismatch for $filename" >&2
    exit 1
  fi
  asset_names+=("$filename")
  asset_paths+=("$asset_path")
done < "$checksums_file"
if (( ${#asset_names[@]} < 2 )); then
  echo "release publish: checksum manifest has no release assets" >&2
  exit 1
fi

bash "$(dirname "$0")/release-preflight.sh"

release_endpoint="repos/${GITHUB_REPOSITORY}/releases/tags/${GITHUB_REF_NAME}"
error_file="$(mktemp)"
asset_file="$(mktemp)"
readback_dir="$(mktemp -d)"
trap 'rm -f "$error_file" "$asset_file"; rm -rf "$readback_dir"' EXIT

if ! release_id="$(gh api --method GET "$release_endpoint" --jq '.id' 2>"$error_file")"; then
  if [[ "$(cat "$error_file")" != *"(HTTP 404)"* ]]; then
    cat "$error_file" >&2
    exit 1
  fi
  gh release create "$GITHUB_REF_NAME" --draft --prerelease --verify-tag --title "$GITHUB_REF_NAME" --notes-file "$notes_file" --repo "$GITHUB_REPOSITORY"
  release_id="$(gh api --method GET "$release_endpoint" --jq '.id')"
fi
if [[ ! "$release_id" =~ ^[0-9]+$ ]]; then
  echo "release publish: invalid release ID" >&2
  exit 1
fi

gh release upload "$GITHUB_REF_NAME" "${asset_paths[@]}" --clobber --repo "$GITHUB_REPOSITORY"
gh api --paginate "repos/${GITHUB_REPOSITORY}/releases/${release_id}/assets?per_page=100" --jq '.[] | [.id, .name, .size] | @tsv' > "$asset_file"

remote_count="$(wc -l < "$asset_file" | tr -d ' ')"
if [[ "$remote_count" != "${#asset_names[@]}" ]]; then
  echo "release publish: remote asset count $remote_count does not match verified set ${#asset_names[@]}" >&2
  exit 1
fi

for index in "${!asset_names[@]}"; do
  name="${asset_names[$index]}"
  local_path="${asset_paths[$index]}"
  matches="$(awk -F $'\t' -v name="$name" '$2 == name { print $1 "\t" $3 }' "$asset_file")"
  if [[ -z "$matches" || "$matches" == *$'\n'* ]]; then
    echo "release publish: remote asset $name is missing or duplicated" >&2
    exit 1
  fi
  asset_id="${matches%%$'\t'*}"
  remote_size="${matches#*$'\t'}"
  local_size="$(wc -c < "$local_path" | tr -d ' ')"
  if [[ ! "$asset_id" =~ ^[0-9]+$ || "$remote_size" != "$local_size" ]]; then
    echo "release publish: remote asset metadata mismatch for $name" >&2
    exit 1
  fi
  downloaded="$readback_dir/$name"
  gh api -H "Accept: application/octet-stream" "repos/${GITHUB_REPOSITORY}/releases/assets/${asset_id}" > "$downloaded"
  if ! cmp -s "$local_path" "$downloaded"; then
    echo "release publish: downloaded asset bytes differ for $name" >&2
    exit 1
  fi
done

gh release edit "$GITHUB_REF_NAME" --draft=false --prerelease --repo "$GITHUB_REPOSITORY"
if [[ "$(gh api --method GET "$release_endpoint" --jq '.draft')" != "false" ]]; then
  echo "release publish: release did not become public" >&2
  exit 1
fi
echo "release publish: exact remote asset bytes verified before publication"
