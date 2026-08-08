#!/usr/bin/env bash
set -euo pipefail

: "${GH_TOKEN:?GH_TOKEN is required}"
: "${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required}"
release_tag="${RELEASE_TAG:-${GITHUB_REF_NAME:-}}"
: "${release_tag:?RELEASE_TAG or GITHUB_REF_NAME is required}"

if [[ $# -ne 2 ]]; then
  echo "usage: publish-release.sh DIST RELEASE_NOTES" >&2
  exit 2
fi
if [[ ! "$GITHUB_REPOSITORY" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]]; then
  echo "release publish: invalid repository identity" >&2
  exit 2
fi
if [[ ! "$release_tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+([+-][0-9A-Za-z.-]+)?$ ]]; then
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

if command -v sha256sum >/dev/null 2>&1; then
  sha256_file() {
    sha256sum "$1" | awk '{print $1}'
  }
elif command -v shasum >/dev/null 2>&1; then
  sha256_file() {
    shasum -a 256 "$1" | awk '{print $1}'
  }
else
  echo "release publish: no SHA-256 tool is available" >&2
  exit 1
fi

declare -a asset_names=("checksums.txt")
declare -a asset_paths=("$checksums_file")
# Bash read returns a failure status when the final record is not newline
# terminated even though it populated the fields. Process that record so this
# publisher has the same complete-manifest semantics as the Go verifier.
while read -r digest filename extra || [[ -n "${digest:-}${filename:-}${extra:-}" ]]; do
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
  observed_digest="$(sha256_file "$asset_path")"
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

releases_endpoint="repos/${GITHUB_REPOSITORY}/releases"
asset_file="$(mktemp)"
existing_asset_file="$(mktemp)"
release_file="$(mktemp)"
readback_dir="$(mktemp -d)"
trap 'rm -f "$asset_file" "$existing_asset_file" "$release_file"; rm -rf "$readback_dir"' EXIT

# Draft releases are intentionally absent from the tag lookup endpoint. Resolve
# an existing exact-tag draft from the authenticated release listing and keep
# using its immutable numeric ID for every draft operation.
gh api --paginate "$releases_endpoint?per_page=100" --jq '.[] | [.id, .tag_name, .draft] | @tsv' > "$release_file"
release_match="$(awk -F $'\t' -v tag="$release_tag" '$2 == tag { print $1 "\t" $3 }' "$release_file")"
if [[ "$release_match" == *$'\n'* ]]; then
  echo "release publish: multiple releases use the exact tag" >&2
  exit 1
fi
if [[ -n "$release_match" ]]; then
  release_id="${release_match%%$'\t'*}"
  release_draft="${release_match#*$'\t'}"
  if [[ "$release_draft" != true ]]; then
    echo "release publish: exact tag is already published; refusing replacement" >&2
    exit 1
  fi
else
  release_id="$(gh api --method POST "$releases_endpoint" \
    -f "tag_name=$release_tag" \
    -f "target_commitish=${RELEASE_COMMIT:-${GITHUB_SHA:-}}" \
    -f "name=$release_tag" \
    -F draft=true \
    -F prerelease=true \
    -F "body=@$notes_file" \
    --jq '.id')"
fi
if [[ ! "$release_id" =~ ^[0-9]+$ ]]; then
  echo "release publish: invalid release ID" >&2
  exit 1
fi

# A retry may resume a partially populated draft. Remove only assets whose
# names are in the locally verified manifest, and reject every unknown asset.
gh api --paginate "$releases_endpoint/${release_id}/assets?per_page=100" --jq '.[] | [.id, .name] | @tsv' > "$existing_asset_file"
while IFS=$'\t' read -r existing_id existing_name; do
  [[ -z "${existing_id:-}${existing_name:-}" ]] && continue
  expected=false
  for name in "${asset_names[@]}"; do
    if [[ "$name" == "$existing_name" ]]; then
      expected=true
      break
    fi
  done
  if [[ ! "$existing_id" =~ ^[0-9]+$ || "$expected" != true ]]; then
    echo "release publish: draft contains an unexpected remote asset" >&2
    exit 1
  fi
  gh api --method DELETE "$releases_endpoint/assets/$existing_id" --silent
done < "$existing_asset_file"

for index in "${!asset_names[@]}"; do
  name="${asset_names[$index]}"
  local_path="${asset_paths[$index]}"
  gh api --method POST \
    -H "Content-Type: application/octet-stream" \
    --input "$local_path" \
    "https://uploads.github.com/$releases_endpoint/${release_id}/assets?name=$name" \
    --silent
done
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

# Close the tag-move window after uploads and remote readback. A moved tag
# leaves the release as a draft and cannot relabel artifacts from GITHUB_SHA.
bash "$(dirname "$0")/release-preflight.sh"
gh api --method PATCH "$releases_endpoint/$release_id" -F draft=false -F prerelease=true --silent
if [[ "$(gh api --method GET "$releases_endpoint/$release_id" --jq '.draft')" != "false" ]]; then
  echo "release publish: release did not become public" >&2
  exit 1
fi
echo "release publish: exact remote asset bytes verified before publication"
