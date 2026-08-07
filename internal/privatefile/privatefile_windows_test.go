//go:build windows

package privatefile_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/noahjenkins/unifi-cli/internal/privatefile"
	"github.com/noahjenkins/unifi-cli/internal/privatefile/privatefiletest"
)

func TestEnsureDirUsesProtectedPrivateDACL(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	if err := privatefile.EnsureDir(dir); err != nil {
		t.Fatalf("EnsureDir: %v", err)
	}
	privatefiletest.AssertDir(t, dir)
}

func TestProtectFileUsesProtectedPrivateDACL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key.json")
	if err := os.WriteFile(path, []byte("secret"), 0o600); err != nil {
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
