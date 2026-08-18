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
			output, err, calls := runReleasePublish(t, test.mode, true, true)
			if test.wantOK && err != nil {
				t.Fatalf("publish failed: %v\n%s", err, output)
			}
			if !test.wantOK && err == nil {
				t.Fatalf("publish accepted %s readback\n%s", test.mode, output)
			}
			if test.wantError != "" && !strings.Contains(output, test.wantError) {
				t.Fatalf("output %q missing %q", output, test.wantError)
			}
			published := strings.Contains(calls, "--method PATCH repos/owner/repo/releases/42")
			if published != test.wantOK {
				t.Fatalf("release edit observed=%t, want %t; calls:\n%s", published, test.wantOK, calls)
			}
		})
	}
}

func TestPublishReleaseProcessesFinalChecksumWithoutNewline(t *testing.T) {
	output, err, calls := runReleasePublish(t, "match", false, true)
	if err != nil {
		t.Fatalf("publish skipped the final unterminated checksum record: %v\n%s", err, output)
	}
	if !strings.Contains(calls, "releases/42/assets?name=asset.bin") {
		t.Fatalf("final checksum asset was not uploaded; calls:\n%s", calls)
	}
	if !strings.Contains(calls, "--method PATCH repos/owner/repo/releases/42") {
		t.Fatalf("verified release was not published; calls:\n%s", calls)
	}
	if got := strings.Count(calls, "repos/owner/repo/commits/v1.0.0-rc.1"); got != 2 {
		t.Fatalf("release tag was verified %d times, want before upload and before publication; calls:\n%s", got, calls)
	}
}

func TestPublishReleaseUsesReleaseIDForDraftOperations(t *testing.T) {
	output, err, calls := runReleasePublish(t, "match", true, true)
	if err != nil {
		t.Fatalf("publish failed: %v\n%s", err, output)
	}
	for _, want := range []string{
		"repos/owner/repo/releases?per_page=100",
		"https://uploads.github.com/repos/owner/repo/releases/42/assets?name=asset.bin",
		"https://uploads.github.com/repos/owner/repo/releases/42/assets?name=checksums.txt",
		"--method PATCH repos/owner/repo/releases/42",
	} {
		if !strings.Contains(calls, want) {
			t.Fatalf("release publisher did not use the draft release ID for %q; calls:\n%s", want, calls)
		}
	}
	for _, forbidden := range []string{"release upload ", "release edit ", "--hostname uploads.github.com"} {
		if strings.Contains(calls, forbidden) {
			t.Fatalf("draft operation used the tag lookup path %q; calls:\n%s", forbidden, calls)
		}
	}
}

func TestPublishReleaseCreatesDraftAndCapturesID(t *testing.T) {
	output, err, calls := runReleasePublish(t, "match", true, false)
	if err != nil {
		t.Fatalf("publish failed: %v\n%s", err, output)
	}
	createCall := callLineContaining(calls, "--method POST repos/owner/repo/releases ")
	if createCall == "" {
		t.Fatalf("publisher did not create the draft through the release API; calls:\n%s", calls)
	}
	if strings.Contains(createCall, "target_commitish=") {
		t.Fatalf("publisher supplied target_commitish for a preverified existing tag: %q", createCall)
	}
	if strings.Contains(calls, "release create ") {
		t.Fatalf("publisher created a draft without capturing its numeric ID; calls:\n%s", calls)
	}
}

func TestPublishReleaseUsesTagDerivedPrereleaseState(t *testing.T) {
	for _, tt := range []struct {
		tag  string
		want string
	}{
		{tag: "v1.0.0-rc.2", want: "true"},
		{tag: "v1.0.0", want: "false"},
	} {
		t.Run(tt.tag, func(t *testing.T) {
			output, err, calls := runReleasePublishTag(t, "match", true, false, tt.tag)
			if err != nil {
				t.Fatalf("publish failed: %v\n%s", err, output)
			}
			for _, method := range []string{"--method POST repos/owner/repo/releases", "--method PATCH repos/owner/repo/releases/42"} {
				line := callLineContaining(calls, method)
				if !strings.Contains(line, "prerelease="+tt.want) {
					t.Fatalf("%s call = %q, want prerelease=%s", method, line, tt.want)
				}
			}
		})
	}
}

func callLineContaining(calls, value string) string {
	for _, line := range strings.Split(calls, "\n") {
		if strings.Contains(line, value) {
			return line
		}
	}
	return ""
}

func runReleasePublish(t *testing.T, mode string, trailingNewline, existingDraft bool) (string, error, string) {
	return runReleasePublishTag(t, mode, trailingNewline, existingDraft, "v1.0.0-rc.1")
}

func runReleasePublishTag(t *testing.T, mode string, trailingNewline, existingDraft bool, releaseTag string) (string, error, string) {
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
	manifest := hex.EncodeToString(digest[:]) + "  asset.bin"
	if trailingNewline {
		manifest += "\n"
	}
	if err := os.WriteFile(filepath.Join(dist, "checksums.txt"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	notes := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(notes, []byte("release notes"), 0o644); err != nil {
		t.Fatal(err)
	}
	canonicalNotesDir := filepath.Join(dir, "docs", "releases")
	if err := os.MkdirAll(canonicalNotesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(canonicalNotesDir, releaseTag+".md"), []byte("# `"+releaseTag+"` release notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "calls")
	createdPath := filepath.Join(dir, "created")
	publishedPath := filepath.Join(dir, "published")
	ghPath := filepath.Join(dir, "gh")
	sha256sumPath := filepath.Join(dir, "sha256sum")
	shasumPath := filepath.Join(dir, "shasum")
	fake := `#!/usr/bin/env bash
set -euo pipefail
printf '%s ' "$@" >> "$GH_FAKE_LOG"
printf '\n' >> "$GH_FAKE_LOG"
if [[ "$1" == api ]]; then
  if [[ "$*" == *"repos/owner/repo --silent"* ]]; then
    exit 0
  fi
  if [[ "$*" == *"repos/owner/repo/commits/$RELEASE_TAG"* ]]; then
    echo 0000000000000000000000000000000000000001
    exit 0
  fi
  if [[ "$*" == *"releases/tags/$RELEASE_TAG"* && "$*" == *".draft"* ]]; then
    if [[ -f "$GH_FAKE_PUBLISHED" ]]; then echo false; exit 0; fi
    if [[ -f "$GH_FAKE_CREATED" ]]; then echo true; exit 0; fi
    echo 'gh: Not Found (HTTP 404)' >&2
    exit 1
  fi
  if [[ "$*" == *"releases/tags/$RELEASE_TAG"* && "$*" == *".id"* ]]; then
    if [[ -f "$GH_FAKE_CREATED" ]]; then echo 42; exit 0; fi
    echo 'gh: Not Found (HTTP 404)' >&2
    exit 1
  fi
  if [[ "$*" == *"repos/owner/repo/releases?per_page=100"* ]]; then
    if [[ "$GH_FAKE_EXISTING" == true ]]; then
      printf '42\t%s\ttrue\n' "$RELEASE_TAG"
    fi
    exit 0
  fi
  if [[ "$*" == *"releases/42/assets?per_page=100"* ]]; then
    if [[ "$*" == *".size"* ]]; then
      if [[ -f "$GH_FAKE_REMOTE/asset.bin" ]]; then
        printf '101\tasset.bin\t%s\n' "$(wc -c < "$GH_FAKE_REMOTE/asset.bin" | tr -d ' ')"
      fi
      if [[ -f "$GH_FAKE_REMOTE/checksums.txt" ]]; then
        printf '102\tchecksums.txt\t%s\n' "$(wc -c < "$GH_FAKE_REMOTE/checksums.txt" | tr -d ' ')"
      fi
    fi
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
  if [[ "$*" == *"--method POST"* && "$*" == *"releases/42/assets?name="* ]]; then
    input=""
    destination=""
    previous=""
    for argument in "$@"; do
      if [[ "$previous" == --input ]]; then input="$argument"; fi
      if [[ "$argument" == *"releases/42/assets?name="* ]]; then destination="$argument"; fi
      previous="$argument"
    done
    cp "$input" "$GH_FAKE_REMOTE/${destination##*name=}"
    exit 0
  fi
  if [[ "$*" == *"--method POST repos/owner/repo/releases "* ]]; then
    touch "$GH_FAKE_CREATED"
    echo 42
    exit 0
  fi
  if [[ "$*" == *"--method PATCH repos/owner/repo/releases/42"* ]]; then
    touch "$GH_FAKE_PUBLISHED"
    exit 0
  fi
  if [[ "$*" == *"--method GET repos/owner/repo/releases/42"* && "$*" == *".draft"* ]]; then
    if [[ -f "$GH_FAKE_PUBLISHED" ]]; then echo false; else echo true; fi
    exit 0
  fi
fi
echo "unexpected fake gh call: $*" >&2
exit 2
`
	if err := os.WriteFile(ghPath, []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	portableHash := `#!/usr/bin/env bash
set -euo pipefail
printf '%s  %s\n' "$GH_FAKE_DIGEST" "$1"
`
	if err := os.WriteFile(sha256sumPath, []byte(portableHash), 0o755); err != nil {
		t.Fatal(err)
	}
	wrongHashTool := "#!/usr/bin/env bash\necho 'shasum must not be used when sha256sum is available' >&2\nexit 99\n"
	if err := os.WriteFile(shasumPath, []byte(wrongHashTool), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "./scripts/publish-release.sh", dist, notes)
	cmd.Env = []string{
		"PATH=" + dir + ":/usr/bin:/bin",
		"GH_TOKEN=test-token",
		"GITHUB_REPOSITORY=owner/repo",
		"RELEASE_TAG=" + releaseTag,
		"RELEASE_COMMIT=0000000000000000000000000000000000000001",
		"GITHUB_REF_NAME=wrong-ref",
		"GITHUB_SHA=0000000000000000000000000000000000000002",
		"GH_FAKE_MODE=" + mode,
		"GH_FAKE_EXISTING=" + map[bool]string{true: "true", false: "false"}[existingDraft],
		"GH_FAKE_LOG=" + logPath,
		"GH_FAKE_REMOTE=" + remote,
		"GH_FAKE_CREATED=" + createdPath,
		"GH_FAKE_PUBLISHED=" + publishedPath,
		"GH_FAKE_DIGEST=" + hex.EncodeToString(digest[:]),
		"RELEASE_SOURCE_ROOT=" + dir,
	}
	output, err := cmd.CombinedOutput()
	calls, readErr := os.ReadFile(logPath)
	if readErr != nil && !os.IsNotExist(readErr) {
		t.Fatal(readErr)
	}
	return string(output), err, string(calls)
}
