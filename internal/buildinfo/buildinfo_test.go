package buildinfo

import (
	"runtime"
	"testing"
)

func TestCurrentReturnsLinkerValuesAndRuntime(t *testing.T) {
	originalVersion, originalCommit, originalBuildDate := Version, Commit, BuildDate
	Version, Commit, BuildDate = "v1.0.0-test", "abc123", "2026-08-16T00:00:00Z"
	t.Cleanup(func() { Version, Commit, BuildDate = originalVersion, originalCommit, originalBuildDate })

	got := Current()
	if got.Version != Version || got.Commit != Commit || got.BuildDate != BuildDate || got.GoVersion != runtime.Version() {
		t.Fatalf("Current() = %+v", got)
	}
}
