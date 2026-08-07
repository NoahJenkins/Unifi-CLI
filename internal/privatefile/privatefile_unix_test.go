//go:build !windows

package privatefile_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/noahjenkins/unifi-cli/internal/privatefile"
	"github.com/noahjenkins/unifi-cli/internal/privatefile/privatefiletest"
)

func TestEnsureDirAndProtectFileEnforceOwnerOnlyPermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	if err := os.MkdirAll(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := privatefile.EnsureDir(dir); err != nil {
		t.Fatalf("EnsureDir: %v", err)
	}

	privatefiletest.AssertDir(t, dir)

	path := filepath.Join(dir, "key.json")
	if err := os.WriteFile(path, []byte("secret"), 0o666); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o666); err != nil {
		t.Fatal(err)
	}
	if err := privatefile.ProtectFile(path); err != nil {
		t.Fatalf("ProtectFile: %v", err)
	}

	privatefiletest.AssertFile(t, path)
}

func TestProtectFileRejectsMissingPath(t *testing.T) {
	err := privatefile.ProtectFile(filepath.Join(t.TempDir(), "missing"))
	if err == nil {
		t.Fatal("ProtectFile missing path succeeded, want error")
	}
}
