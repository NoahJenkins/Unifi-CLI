package releasepipeline_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestReleaseSmokeDescribesExactTargetAndCommandContract(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", "./cmd/release-smoke", "--describe")
	cmd.Env = append(os.Environ(), "GOCACHE="+t.TempDir())
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("describe release smoke contract: %v\n%s", err, output)
	}
	want := strings.Join([]string{
		"darwin/amd64",
		"darwin/arm64",
		"linux/amd64",
		"linux/arm64",
		"windows/amd64",
		"windows/arm64",
		"native commands: --version | version --json | --help | --config configs/config.example.yaml --json config show",
		"non-native policy: structural verification only; execution skipped",
		"",
	}, "\n")
	if string(output) != want {
		t.Errorf("release smoke contract:\n%s\nwant:\n%s", output, want)
	}
}

type goreleaserConfig struct {
	Version int `yaml:"version"`
	Builds  []struct {
		ID      string   `yaml:"id"`
		Main    string   `yaml:"main"`
		Binary  string   `yaml:"binary"`
		Env     []string `yaml:"env"`
		Flags   []string `yaml:"flags"`
		Goos    []string `yaml:"goos"`
		Goarch  []string `yaml:"goarch"`
		Ldflags []string `yaml:"ldflags"`
	} `yaml:"builds"`
	Archives []struct {
		IDs             []string `yaml:"ids"`
		Formats         []string `yaml:"formats"`
		NameTemplate    string   `yaml:"name_template"`
		FormatOverrides []struct {
			Goos    string   `yaml:"goos"`
			Formats []string `yaml:"formats"`
		} `yaml:"format_overrides"`
	} `yaml:"archives"`
	Checksum struct {
		NameTemplate string `yaml:"name_template"`
		Algorithm    string `yaml:"algorithm"`
	} `yaml:"checksum"`
	Source struct {
		Enabled        bool   `yaml:"enabled"`
		NameTemplate   string `yaml:"name_template"`
		Format         string `yaml:"format"`
		PrefixTemplate string `yaml:"prefix_template"`
	} `yaml:"source"`
	SBOMs []struct {
		Artifacts string   `yaml:"artifacts"`
		Cmd       string   `yaml:"cmd"`
		Documents []string `yaml:"documents"`
	} `yaml:"sboms"`
	Changelog struct {
		Use string `yaml:"use"`
	} `yaml:"changelog"`
	Brews  []any `yaml:"brews"`
	Scoops []any `yaml:"scoops"`
}

func TestGoReleaserConfigEnforcesReleaseContract(t *testing.T) {
	data, err := os.ReadFile(".goreleaser.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var cfg goreleaserConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse .goreleaser.yaml: %v", err)
	}

	if cfg.Version != 2 {
		t.Errorf("config version = %d, want 2", cfg.Version)
	}
	if len(cfg.Builds) != 1 {
		t.Fatalf("build count = %d, want 1", len(cfg.Builds))
	}
	build := cfg.Builds[0]
	if build.ID != "unifi" || build.Main != "./cmd/unifi" || build.Binary != "unifi" {
		t.Errorf("unexpected build identity: id=%q main=%q binary=%q", build.ID, build.Main, build.Binary)
	}
	assertStringSet(t, "GOOS", build.Goos, []string{"darwin", "linux", "windows"})
	assertStringSet(t, "GOARCH", build.Goarch, []string{"amd64", "arm64"})
	if !slices.Contains(build.Env, "CGO_ENABLED=0") {
		t.Error("build env must contain CGO_ENABLED=0")
	}
	if !slices.Contains(build.Flags, "-trimpath") {
		t.Error("build flags must contain -trimpath")
	}
	ldflags := strings.Join(build.Ldflags, " ")
	for _, field := range []string{
		"internal/buildinfo.Version={{ .Version }}",
		"internal/buildinfo.Commit={{ .Commit }}",
		"internal/buildinfo.BuildDate={{ .CommitDate }}",
	} {
		if !strings.Contains(ldflags, field) {
			t.Errorf("ldflags missing %q", field)
		}
	}

	if len(cfg.Archives) != 1 {
		t.Fatalf("archive count = %d, want 1", len(cfg.Archives))
	}
	archive := cfg.Archives[0]
	if !slices.Equal(archive.IDs, []string{"unifi"}) {
		t.Errorf("archive ids = %v, want [unifi]", archive.IDs)
	}
	if !slices.Equal(archive.Formats, []string{"tar.gz"}) {
		t.Errorf("default archive formats = %v, want [tar.gz]", archive.Formats)
	}
	if len(archive.FormatOverrides) != 1 || archive.FormatOverrides[0].Goos != "windows" ||
		!slices.Equal(archive.FormatOverrides[0].Formats, []string{"zip"}) {
		t.Errorf("Windows archive override = %#v, want zip only", archive.FormatOverrides)
	}
	if cfg.Checksum.Algorithm != "sha256" || cfg.Checksum.NameTemplate != "checksums.txt" {
		t.Errorf("checksum config = %#v, want explicit SHA-256 checksums.txt", cfg.Checksum)
	}
	if !cfg.Source.Enabled || cfg.Source.Format != "tar.gz" || cfg.Source.NameTemplate == "" || cfg.Source.PrefixTemplate == "" {
		t.Errorf("source archive is not fully configured: %#v", cfg.Source)
	}
	if len(cfg.SBOMs) != 1 || cfg.SBOMs[0].Artifacts != "archive" || cfg.SBOMs[0].Cmd != "syft" || len(cfg.SBOMs[0].Documents) != 1 {
		t.Errorf("archive SBOM config = %#v, want one Syft archive SBOM document", cfg.SBOMs)
	}
	if cfg.Changelog.Use != "git" {
		t.Errorf("changelog source = %q, want git", cfg.Changelog.Use)
	}
	if len(cfg.Brews) != 0 || len(cfg.Scoops) != 0 {
		t.Errorf("Homebrew/Scoop publication is out of scope: brews=%d scoops=%d", len(cfg.Brews), len(cfg.Scoops))
	}
}

func TestReleaseWorkflowUsesApprovedPinsAndLeastPermissions(t *testing.T) {
	workflow := readYAMLMap(t, ".github/workflows/release.yml")
	on := mapValue(t, workflow, "on")
	push := mapValue(t, on, "push")
	tags := stringSliceValue(t, push, "tags")
	if !slices.Equal(tags, []string{"v*"}) {
		t.Errorf("release tags = %v, want [v*]", tags)
	}

	permissions := mapValue(t, workflow, "permissions")
	wantPermissions := map[string]any{
		"attestations": "write",
		"contents":     "write",
		"id-token":     "write",
	}
	if fmt.Sprint(permissions) != fmt.Sprint(wantPermissions) {
		t.Errorf("release permissions = %v, want %v", permissions, wantPermissions)
	}

	uses := allUses(workflow)
	pin := regexp.MustCompile(`^[^@]+@[0-9a-f]{40}$`)
	for _, use := range uses {
		if !pin.MatchString(use) {
			t.Errorf("action is not pinned to a full immutable SHA: %q", use)
		}
	}
	for _, want := range []string{
		"goreleaser/goreleaser-action@f06c13b6b1a9625abc9e6e439d9c05a8f2190e94",
		"anchore/sbom-action/download-syft@e22c389904149dbc22b58101806040fa8d37a610",
		"actions/attest-build-provenance@4d101475d8b20a2381f78447822ac1eab6504dd8",
	} {
		if !slices.Contains(uses, want) {
			t.Errorf("release workflow missing approved action %q", want)
		}
	}

	data, err := os.ReadFile(".github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		"version: v2.17.1",
		"args: release --clean",
		"fetch-depth: 0",
		"persist-credentials: false",
		"subject-path: |",
		"dist/*.tar.gz",
		"dist/*.zip",
		"dist/checksums.txt",
		"go run ./cmd/release-smoke --artifacts dist",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("release workflow missing %q", want)
		}
	}
}

func TestRequiredCIIncludesLinuxRaceAndArtifactSmoke(t *testing.T) {
	data, err := os.ReadFile(".github/workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		"go test -race ./...",
		"if: runner.os == 'Linux'",
		"go run ./cmd/release-smoke --native",
		"go run ./cmd/release-smoke --all",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("CI workflow missing %q", want)
		}
	}
}

func readYAMLMap(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := yaml.Unmarshal(data, &value); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return value
}

func mapValue(t *testing.T, value map[string]any, key string) map[string]any {
	t.Helper()
	got, ok := value[key].(map[string]any)
	if !ok {
		t.Fatalf("%q = %#v, want mapping", key, value[key])
	}
	return got
}

func stringSliceValue(t *testing.T, value map[string]any, key string) []string {
	t.Helper()
	items, ok := value[key].([]any)
	if !ok {
		t.Fatalf("%q = %#v, want list", key, value[key])
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		result = append(result, fmt.Sprint(item))
	}
	return result
}

func allUses(value any) []string {
	var result []string
	switch value := value.(type) {
	case map[string]any:
		for key, child := range value {
			if key == "uses" {
				result = append(result, fmt.Sprint(child))
			}
			result = append(result, allUses(child)...)
		}
	case []any:
		for _, child := range value {
			result = append(result, allUses(child)...)
		}
	}
	return result
}

func assertStringSet(t *testing.T, label string, got, want []string) {
	t.Helper()
	got = slices.Clone(got)
	want = slices.Clone(want)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("%s = %v, want %v", label, got, want)
	}
}
