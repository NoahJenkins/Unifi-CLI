package buildinfo

import (
	"runtime"
	"runtime/debug"
)

// These values are defaults for local builds. Release builds populate them
// with -ldflags -X assignments.
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"

	readBuildInfo = debug.ReadBuildInfo
)

type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"build_date"`
	GoVersion string `json:"go_version"`
}

func Current() Info {
	version := Version
	if version == "dev" {
		if build, ok := readBuildInfo(); ok && build.Main.Path == "github.com/noahjenkins/unifi-cli" && build.Main.Version != "" && build.Main.Version != "(devel)" {
			version = build.Main.Version
		}
	}

	return Info{
		Version:   version,
		Commit:    Commit,
		BuildDate: BuildDate,
		GoVersion: runtime.Version(),
	}
}
