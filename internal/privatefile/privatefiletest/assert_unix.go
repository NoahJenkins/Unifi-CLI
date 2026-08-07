//go:build !windows

// Package privatefiletest verifies platform-native private permissions in tests.
package privatefiletest

import (
	"os"
	"testing"
)

func AssertDir(t *testing.T, path string) {
	t.Helper()
	assertMode(t, path, 0o700)
}

func AssertFile(t *testing.T, path string) {
	t.Helper()
	assertMode(t, path, 0o600)
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %o, want %o", path, got, want)
	}
}
