package releasepipeline_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestReleasePreflightAllowsOnlyAbsentOrDraftExactTag(t *testing.T) {
	tests := []struct {
		name     string
		mode     string
		wantOK   bool
		wantText string
	}{
		{name: "absent", mode: "absent", wantOK: true},
		{name: "existing draft", mode: "draft", wantOK: true},
		{name: "published", mode: "published", wantText: "already published"},
		{name: "API error", mode: "error", wantText: "HTTP 401"},
		{name: "repository auth error", mode: "repo-error", wantText: "HTTP 401"},
		{name: "moved tag", mode: "tag-moved", wantText: "does not resolve to workflow commit"},
		{name: "malformed response", mode: "malformed", wantText: "unexpected draft value"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output, err, _ := runReleasePreflight(t, test.mode, true)
			if test.wantOK && err != nil {
				t.Fatalf("preflight rejected %s release state: %v\n%s", test.name, err, output)
			}
			if !test.wantOK && err == nil {
				t.Fatalf("preflight accepted %s release state\n%s", test.name, output)
			}
			if test.wantText != "" && !strings.Contains(output, test.wantText) {
				t.Fatalf("preflight output %q missing %q", output, test.wantText)
			}
		})
	}
}

func TestReleasePreflightUsesExactRepositoryAndTag(t *testing.T) {
	_, err, args := runReleasePreflight(t, "draft", true)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"api",
		"--method",
		"GET",
		"repos/owner/repo/releases/tags/v1.0.0-rc.1",
		"--jq",
		".draft",
	}
	if !slices.Equal(args, want) {
		t.Fatalf("gh arguments = %#v, want %#v", args, want)
	}
}

func TestReleasePreflightFailsClosedWithoutToken(t *testing.T) {
	output, err, _ := runReleasePreflight(t, "draft", false)
	if err == nil {
		t.Fatalf("preflight accepted missing GH_TOKEN\n%s", output)
	}
}

func runReleasePreflight(t *testing.T, mode string, withToken bool) (string, error, []string) {
	t.Helper()
	dir := t.TempDir()
	argsPath := filepath.Join(dir, "args")
	ghPath := filepath.Join(dir, "gh")
	fake := `#!/usr/bin/env bash
set -eu
printf '%s\n' "$@" > "$GH_FAKE_ARGS"
if [[ "$*" == *"repos/owner/repo --silent"* ]]; then
  if [[ "$GH_FAKE_MODE" == "repo-error" ]]; then
    echo 'gh: authentication failed (HTTP 401)' >&2
    exit 1
  fi
  exit 0
fi
if [[ "$*" == *"repos/owner/repo/commits/v1.0.0-rc.1"* ]]; then
  if [[ "$GH_FAKE_MODE" == "tag-moved" ]]; then
    printf '%040d\n' 2
  else
    printf '%040d\n' 1
  fi
  exit 0
fi
case "$GH_FAKE_MODE" in
  absent) echo 'gh: Not Found (HTTP 404)' >&2; exit 1 ;;
  draft) echo true ;;
  published) echo false ;;
  malformed) echo null ;;
  error) echo 'gh: authentication failed (HTTP 401)' >&2; exit 1 ;;
  *) echo 'unknown fake mode' >&2; exit 2 ;;
esac
`
	if err := os.WriteFile(ghPath, []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "./scripts/release-preflight.sh")
	env := []string{
		"PATH=" + dir + ":/usr/bin:/bin",
		"GITHUB_REPOSITORY=owner/repo",
		"RELEASE_TAG=v1.0.0-rc.1",
		"RELEASE_COMMIT=0000000000000000000000000000000000000001",
		"GITHUB_REF_NAME=wrong-ref",
		"GITHUB_SHA=0000000000000000000000000000000000000002",
		"GH_FAKE_ARGS=" + argsPath,
		"GH_FAKE_MODE=" + mode,
	}
	if withToken {
		env = append(env, "GH_TOKEN=test-token")
	}
	cmd.Env = env
	output, err := cmd.CombinedOutput()
	data, readErr := os.ReadFile(argsPath)
	if readErr != nil && !os.IsNotExist(readErr) {
		t.Fatal(readErr)
	}
	args := strings.Fields(string(data))
	return string(output), err, args
}
