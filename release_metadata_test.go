package releasepipeline_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseMetadataDerivesStableAndPrereleaseValues(t *testing.T) {
	for _, tt := range []struct {
		tag        string
		prerelease string
	}{
		{tag: "v1.0.0", prerelease: "false"},
		{tag: "v1.0.0-rc.2", prerelease: "true"},
		{tag: "v1.0.0+build.7", prerelease: "false"},
		{tag: "v1.0.0-rc.2+build.7", prerelease: "true"},
	} {
		t.Run(tt.tag, func(t *testing.T) {
			root := t.TempDir()
			notesDir := filepath.Join(root, "docs", "releases")
			if err := os.MkdirAll(notesDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(notesDir, tt.tag+".md"), []byte("# `"+tt.tag+"` release notes\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("bash", "./scripts/release-metadata.sh", tt.tag)
			cmd.Env = append(os.Environ(), "RELEASE_SOURCE_ROOT="+root)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("metadata failed: %v\n%s", err, output)
			}
			want := strings.Join([]string{
				"version\t" + strings.TrimPrefix(tt.tag, "v"),
				"prerelease\t" + tt.prerelease,
				"notes\tdocs/releases/" + tt.tag + ".md",
				"",
			}, "\n")
			if string(output) != want {
				t.Fatalf("metadata = %q, want %q", output, want)
			}
		})
	}
}

func TestReleaseMetadataRejectsMalformedTagsAndMismatchedNotes(t *testing.T) {
	for _, tag := range []string{"1.0.0", "v01.0.0", "v1.0", "v1.0.0-", "v1.0.0-rc..2", "v1.0.0+"} {
		t.Run(tag, func(t *testing.T) {
			cmd := exec.Command("bash", "./scripts/release-metadata.sh", tag)
			cmd.Env = append(os.Environ(), "RELEASE_SOURCE_ROOT="+t.TempDir())
			if output, err := cmd.CombinedOutput(); err == nil {
				t.Fatalf("accepted malformed tag %q: %s", tag, output)
			}
		})
	}

	t.Run("mismatched notes", func(t *testing.T) {
		root := t.TempDir()
		notesDir := filepath.Join(root, "docs", "releases")
		if err := os.MkdirAll(notesDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(notesDir, "v1.0.0.md"), []byte("# `v1.0.1` release notes\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("bash", "./scripts/release-metadata.sh", "v1.0.0")
		cmd.Env = append(os.Environ(), "RELEASE_SOURCE_ROOT="+root)
		output, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(output), "does not match") {
			t.Fatalf("mismatched notes result: err=%v output=%q", err, output)
		}
	})
}
