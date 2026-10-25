package main

import (
	"os"
	"strings"
	"testing"
)

func TestLatestPatchStaysOnTheCurrentMinor(t *testing.T) {
	releases := []Release{
		{Version: "go1.27.1", Stable: true},
		{Version: "go1.26.12rc1", Stable: true},
		{Version: "go1.26.11", Stable: true},
		{Version: "go1.26.10", Stable: true},
		{Version: "go1.26.9", Stable: true},
		{Version: "go1.25.4", Stable: true},
		{Version: "go1.26.12", Stable: false},
	}
	next, ok, err := LatestPatch("1.26.10", releases)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || next != "1.26.11" {
		t.Fatalf("LatestPatch = %q, %v; want 1.26.11, true", next, ok)
	}
	next, ok, err = LatestPatch("1.26.11", releases)
	if err != nil {
		t.Fatal(err)
	}
	if ok || next != "" {
		t.Fatalf("already-latest patch = %q, %v; want no bump", next, ok)
	}
}

func TestLatestPatchFailsClosedWhenTheMinorIsMissing(t *testing.T) {
	_, ok, err := LatestPatch("1.26.10", []Release{{Version: "go1.27.0", Stable: true}})
	if err == nil || ok {
		t.Fatalf("missing minor err = %v, ok = %v; want an error", err, ok)
	}
}

func TestLatestPatchIgnoresExtraReleaseFields(t *testing.T) {
	const feed = `[{"version":"go1.26.11","stable":true,"files":[{"filename":"ignored"}]},{"version":"go1.26.10","stable":true}]`
	releases, err := parseReleaseFeed([]byte(feed))
	if err != nil {
		t.Fatal(err)
	}
	next, ok, err := LatestPatch("1.26.10", releases)
	if err != nil || !ok || next != "1.26.11" {
		t.Fatalf("LatestPatch = %q, %v, %v; want 1.26.11", next, ok, err)
	}
}

func TestApplyRewritesDirectiveAndMatchingToolchain(t *testing.T) {
	input := "module example.com/cli\n\ngo 1.26.10\n\nrequire example.com/dep v1.2.3\n\ntoolchain go1.26.10\n"
	got, err := Apply(input, "1.26.10", "1.26.11")
	if err != nil {
		t.Fatal(err)
	}
	want := "module example.com/cli\n\ngo 1.26.11\n\nrequire example.com/dep v1.2.3\n\ntoolchain go1.26.11\n"
	if got != want {
		t.Fatalf("Apply =\n%s\nwant:\n%s", got, want)
	}
	directive, err := GoDirective(got)
	if err != nil || directive != "1.26.11" {
		t.Fatalf("directive = %q, %v", directive, err)
	}
}

func TestApplyLeavesAMissingToolchainUnset(t *testing.T) {
	input := "module example.com/cli\n\ngo 1.26.10\n"
	got, err := Apply(input, "1.26.10", "1.26.11")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "toolchain") || !strings.Contains(got, "go 1.26.11\n") {
		t.Fatalf("Apply = %q", got)
	}
}

func TestApplyRejectsADifferentToolchain(t *testing.T) {
	input := "module example.com/cli\n\ngo 1.26.10\n\ntoolchain go1.26.9\n"
	if _, err := Apply(input, "1.26.10", "1.26.11"); err == nil {
		t.Fatal("expected mismatched toolchain to fail")
	}
}

func TestRepoGoModDeclaresAPatchVersion(t *testing.T) {
	data, err := os.ReadFile("../../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	directive, err := GoDirective(string(data))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseDotted(directive); err != nil {
		t.Fatal(err)
	}
}
