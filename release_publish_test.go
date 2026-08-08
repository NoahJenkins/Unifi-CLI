package releasepipeline_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublishReleaseRequiresRemoteByteEqualityBeforePublication(t *testing.T) {
	for _, test := range []struct {
		name      string
		mode      string
		wantOK    bool
		wantError string
	}{
		{name: "matching readback", mode: "match", wantOK: true},
		{name: "mismatched readback", mode: "corrupt", wantError: "downloaded asset bytes differ"},
	} {
		t.Run(test.name, func(t *testing.T) {
			output, err, calls := runReleasePublish(t, test.mode)
			if test.wantOK && err != nil {
				t.Fatalf("publish failed: %v\n%s", err, output)
			}
			if !test.wantOK && err == nil {
				t.Fatalf("publish accepted %s readback\n%s", test.mode, output)
			}
			if test.wantError != "" && !strings.Contains(output, test.wantError) {
				t.Fatalf("output %q missing %q", output, test.wantError)
			}
			published := strings.Contains(calls, "release edit ")
			if published != test.wantOK {
				t.Fatalf("release edit observed=%t, want %t; calls:\n%s", published, test.wantOK, calls)
			}
		})
	}
}

func runReleasePublish(t *testing.T, mode string) (string, error, string) {
	t.Helper()
	dir := t.TempDir()
	dist := filepath.Join(dir, "dist")
	remote := filepath.Join(dir, "remote")
	if err := os.MkdirAll(dist, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(remote, 0o755); err != nil {
		t.Fatal(err)
	}
	asset := []byte("verified release bytes")
	if err := os.WriteFile(filepath.Join(dist, "asset.bin"), asset, 0o644); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(asset)
	manifest := hex.EncodeToString(digest[:]) + "  asset.bin\n"
	if err := os.WriteFile(filepath.Join(dist, "checksums.txt"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	notes := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(notes, []byte("release notes"), 0o644); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "calls")
	createdPath := filepath.Join(dir, "created")
	publishedPath := filepath.Join(dir, "published")
	ghPath := filepath.Join(dir, "gh")
	fake := `#!/usr/bin/env bash
set -euo pipefail
printf '%s ' "$@" >> "$GH_FAKE_LOG"
printf '\n' >> "$GH_FAKE_LOG"
if [[ "$1" == api ]]; then
  if [[ "$*" == *"repos/owner/repo --silent"* ]]; then
    exit 0
  fi
  if [[ "$*" == *"releases/tags/v1.0.0-rc.1"* && "$*" == *".draft"* ]]; then
    if [[ -f "$GH_FAKE_PUBLISHED" ]]; then echo false; exit 0; fi
    if [[ -f "$GH_FAKE_CREATED" ]]; then echo true; exit 0; fi
    echo 'gh: Not Found (HTTP 404)' >&2
    exit 1
  fi
  if [[ "$*" == *"releases/tags/v1.0.0-rc.1"* && "$*" == *".id"* ]]; then
    if [[ -f "$GH_FAKE_CREATED" ]]; then echo 42; exit 0; fi
    echo 'gh: Not Found (HTTP 404)' >&2
    exit 1
  fi
  if [[ "$*" == *"releases/42/assets?per_page=100"* ]]; then
    printf '101\tasset.bin\t%s\n' "$(wc -c < "$GH_FAKE_REMOTE/asset.bin" | tr -d ' ')"
    printf '102\tchecksums.txt\t%s\n' "$(wc -c < "$GH_FAKE_REMOTE/checksums.txt" | tr -d ' ')"
    exit 0
  fi
  if [[ "$*" == *"releases/assets/101"* ]]; then
    cat "$GH_FAKE_REMOTE/asset.bin"
    if [[ "$GH_FAKE_MODE" == corrupt ]]; then printf 'corrupt'; fi
    exit 0
  fi
  if [[ "$*" == *"releases/assets/102"* ]]; then
    cat "$GH_FAKE_REMOTE/checksums.txt"
    exit 0
  fi
fi
if [[ "$1" == release && "$2" == create ]]; then
  touch "$GH_FAKE_CREATED"
  exit 0
fi
if [[ "$1" == release && "$2" == upload ]]; then
  shift 3
  for item in "$@"; do
    if [[ "$item" == --clobber ]]; then break; fi
    cp "$item" "$GH_FAKE_REMOTE/$(basename "$item")"
  done
  exit 0
fi
if [[ "$1" == release && "$2" == edit ]]; then
  touch "$GH_FAKE_PUBLISHED"
  exit 0
fi
echo "unexpected fake gh call: $*" >&2
exit 2
`
	if err := os.WriteFile(ghPath, []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "./scripts/publish-release.sh", dist, notes)
	cmd.Env = []string{
		"PATH=" + dir + ":/usr/bin:/bin",
		"GH_TOKEN=test-token",
		"GITHUB_REPOSITORY=owner/repo",
		"GITHUB_REF_NAME=v1.0.0-rc.1",
		"GH_FAKE_MODE=" + mode,
		"GH_FAKE_LOG=" + logPath,
		"GH_FAKE_REMOTE=" + remote,
		"GH_FAKE_CREATED=" + createdPath,
		"GH_FAKE_PUBLISHED=" + publishedPath,
	}
	output, err := cmd.CombinedOutput()
	calls, readErr := os.ReadFile(logPath)
	if readErr != nil && !os.IsNotExist(readErr) {
		t.Fatal(readErr)
	}
	return string(output), err, string(calls)
}
