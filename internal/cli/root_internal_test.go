package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/noahjenkins/unifi-cli/internal/buildinfo"
)

func TestRootVersionFlagUsesEffectiveBuildVersion(t *testing.T) {
	root := newRoot(buildinfo.Info{Version: "v1.0.0-rc.3"})
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"--version"})

	if err := root.Execute(); err != nil {
		t.Fatalf("--version: %v", err)
	}
	if got := strings.TrimSpace(buf.String()); got != "unifi version v1.0.0-rc.3" {
		t.Fatalf("--version output = %q, want effective build version", got)
	}
}
