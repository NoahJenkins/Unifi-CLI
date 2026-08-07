package main

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestNativeCommandVerificationAcceptsPopulatedReleaseMetadata(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	root, err := repositoryRoot()
	if err != nil {
		t.Fatal(err)
	}
	native := target{goos: runtime.GOOS, goarch: runtime.GOARCH}
	binary := filepath.Join(t.TempDir(), native.executableName())
	if err := buildTarget(ctx, root, binary, native); err != nil {
		t.Fatal(err)
	}
	if err := verifyNativeCommands(ctx, root, binary, smokeVersion, smokeCommit, ""); err != nil {
		t.Fatalf("verify native command contract with populated release build date: %v", err)
	}
}
