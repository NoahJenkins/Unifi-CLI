package releasepipeline_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseAncestryRequiresCommitOnMain(t *testing.T) {
	repo := t.TempDir()
	runGit := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.test", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.test")
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
		return strings.TrimSpace(string(output))
	}
	runGit("init", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "file"), []byte("main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "file")
	runGit("commit", "-m", "main")
	mainCommit := runGit("rev-parse", "HEAD")
	runGit("switch", "-c", "release-side")
	if err := os.WriteFile(filepath.Join(repo, "side"), []byte("side\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "side")
	runGit("commit", "-m", "side")
	sideCommit := runGit("rev-parse", "HEAD")
	runGit("switch", "main")

	for _, tt := range []struct {
		name   string
		commit string
		ok     bool
	}{
		{name: "on main", commit: mainCommit, ok: true},
		{name: "outside main", commit: sideCommit, ok: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command("bash", filepath.Join(repoRoot(t), "scripts", "release-ancestry.sh"), tt.commit, "main")
			cmd.Dir = repo
			output, err := cmd.CombinedOutput()
			if (err == nil) != tt.ok {
				t.Fatalf("ancestry result: err=%v output=%s", err, output)
			}
		})
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return dir
}
