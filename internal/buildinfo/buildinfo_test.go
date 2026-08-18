package buildinfo

import (
	"runtime"
	"runtime/debug"
	"testing"
)

func TestCurrentVersion(t *testing.T) {
	tests := []struct {
		name    string
		version string
		build   *debug.BuildInfo
		want    string
	}{
		{
			name:    "linker value takes precedence",
			version: "v1.0.0-test",
			build:   &debug.BuildInfo{Main: debug.Module{Path: "github.com/noahjenkins/unifi-cli", Version: "v9.9.9"}},
			want:    "v1.0.0-test",
		},
		{
			name:    "tagged exact module falls back to module version",
			version: "dev",
			build:   &debug.BuildInfo{Main: debug.Module{Path: "github.com/noahjenkins/unifi-cli", Version: "v1.0.0"}},
			want:    "v1.0.0",
		},
		{
			name:    "devel module version stays dev",
			version: "dev",
			build:   &debug.BuildInfo{Main: debug.Module{Path: "github.com/noahjenkins/unifi-cli", Version: "(devel)"}},
			want:    "dev",
		},
		{
			name:    "wrong module stays dev",
			version: "dev",
			build:   &debug.BuildInfo{Main: debug.Module{Path: "example.com/unifi-cli", Version: "v1.0.0"}},
			want:    "dev",
		},
		{
			name:    "missing build info stays dev",
			version: "dev",
			build:   nil,
			want:    "dev",
		},
		{
			name:    "empty module version stays dev",
			version: "dev",
			build:   &debug.BuildInfo{Main: debug.Module{Path: "github.com/noahjenkins/unifi-cli"}},
			want:    "dev",
		},
	}

	originalVersion, originalCommit, originalBuildDate, originalReadBuildInfo := Version, Commit, BuildDate, readBuildInfo
	t.Cleanup(func() {
		Version, Commit, BuildDate, readBuildInfo = originalVersion, originalCommit, originalBuildDate, originalReadBuildInfo
	})

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			Version, Commit, BuildDate = tt.version, "abc123", "2026-08-16T00:00:00Z"
			readBuildInfo = func() (*debug.BuildInfo, bool) { return tt.build, tt.build != nil }

			got := Current()
			if got.Version != tt.want {
				t.Fatalf("Current().Version = %q, want %q", got.Version, tt.want)
			}
			if got.Commit != "abc123" || got.BuildDate != "2026-08-16T00:00:00Z" || got.GoVersion != runtime.Version() {
				t.Fatalf("Current() = %+v", got)
			}
		})
	}
}
