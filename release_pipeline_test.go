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
		"all-target policy: every archived executable must equal its independently cross-built trusted binary; only the native trusted binary is executed",
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
		WrapInDirectory bool     `yaml:"wrap_in_directory"`
		BuildsInfo      fileInfo `yaml:"builds_info"`
		Files           []struct {
			Src  string   `yaml:"src"`
			Info fileInfo `yaml:"info"`
		} `yaml:"files"`
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
		Args      []string `yaml:"args"`
	} `yaml:"sboms"`
	Changelog struct {
		Use string `yaml:"use"`
	} `yaml:"changelog"`
	Brews   []any `yaml:"brews"`
	Scoops  []any `yaml:"scoops"`
	Release struct {
		Draft                bool `yaml:"draft"`
		ReplaceExistingDraft bool `yaml:"replace_existing_draft"`
	} `yaml:"release"`
}

type fileInfo struct {
	Mtime string `yaml:"mtime"`
	Mode  uint32 `yaml:"mode"`
	Owner string `yaml:"owner"`
	Group string `yaml:"group"`
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
	if !archive.WrapInDirectory {
		t.Error("release archives must have one deterministic root directory")
	}
	wantBinaryInfo := fileInfo{Mtime: "{{ .CommitDate }}", Mode: 0o755, Owner: "root", Group: "root"}
	if archive.BuildsInfo != wantBinaryInfo {
		t.Errorf("binary archive metadata = %#v, want %#v", archive.BuildsInfo, wantBinaryInfo)
	}
	wantFiles := []string{"LICENSE", "README.md", "CHANGELOG.md"}
	if len(archive.Files) != len(wantFiles) {
		t.Fatalf("archive files = %#v, want %v", archive.Files, wantFiles)
	}
	wantFileInfo := fileInfo{Mtime: "{{ .CommitDate }}", Mode: 0o644, Owner: "root", Group: "root"}
	for i, want := range wantFiles {
		if archive.Files[i].Src != want || archive.Files[i].Info != wantFileInfo {
			t.Errorf("archive file %d = %#v, want src=%q info=%#v", i, archive.Files[i], want, wantFileInfo)
		}
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
	if len(cfg.SBOMs) == 1 {
		args := strings.Join(cfg.SBOMs[0].Args, " ")
		for _, want := range []string{"$artifact", "--source-name", "{{ .ArtifactName }}", "--source-version", "{{ .Version }}", "cyclonedx-json=$document"} {
			if !strings.Contains(args, want) {
				t.Errorf("SBOM args missing %q: %v", want, cfg.SBOMs[0].Args)
			}
		}
	}
	if cfg.Changelog.Use != "git" {
		t.Errorf("changelog source = %q, want git", cfg.Changelog.Use)
	}
	if len(cfg.Brews) != 0 || len(cfg.Scoops) != 0 {
		t.Errorf("Homebrew/Scoop publication is out of scope: brews=%d scoops=%d", len(cfg.Brews), len(cfg.Scoops))
	}
	if !cfg.Release.Draft {
		t.Error("GoReleaser must upload a draft release so failed gates cannot publish it")
	}
	if !cfg.Release.ReplaceExistingDraft {
		t.Error("GoReleaser must replace the exact existing tag draft so reruns are idempotent")
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

	if permissions, exists := workflow["permissions"]; exists && fmt.Sprint(permissions) != "map[contents:read]" {
		t.Errorf("workflow-wide permissions = %v, want absent or contents: read", permissions)
	}
	concurrency := mapValue(t, workflow, "concurrency")
	if fmt.Sprint(concurrency["group"]) != "release-${{ github.repository }}-${{ github.ref }}" {
		t.Errorf("release concurrency group = %q", concurrency["group"])
	}
	if cancel, ok := concurrency["cancel-in-progress"].(bool); !ok || cancel {
		t.Errorf("release cancel-in-progress = %#v, want false", concurrency["cancel-in-progress"])
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
		"actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02",
		"actions/download-artifact@d3f86a106a0bac45b974a628896c90dbdf5c8093",
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
		"args: release --clean --skip=publish --release-notes docs/releases/v1.0.0-rc.1.md",
		"fetch-depth: 0",
		"persist-credentials: false",
		"include-hidden-files: true",
		"subject-path: |",
		"dist/*.tar.gz",
		"dist/*.zip",
		"dist/checksums.txt",
		"RELEASE_COMMIT_DATE=\"$(date -u -d \"@$RELEASE_COMMIT_TIMESTAMP\" +%Y-%m-%dT%H:%M:%SZ)\"",
		"for TARGET in darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64 windows/arm64; do",
		"GOOS=\"$GOOS\" GOARCH=\"$GOARCH\" go build -trimpath -ldflags \"$RELEASE_LDFLAGS\" -o \"$OUTPUT\" ./cmd/unifi",
		"go run ./cmd/release-smoke --write-source-manifest \"$RUNNER_TEMP/unifi-source-manifest.json\" --expected-commit \"$GITHUB_SHA\"",
		"go run ./cmd/release-smoke --artifacts dist --expected-version \"${GITHUB_REF_NAME#v}\" --expected-commit \"$GITHUB_SHA\" --trusted-binaries \"$RUNNER_TEMP/unifi-trusted\" --trusted-source-manifest \"$RUNNER_TEMP/unifi-source-manifest.json\"",
		"bash ./scripts/publish-release.sh dist docs/releases/v1.0.0-rc.1.md",
		"go run ./cmd/release-smoke --binary \"$BINARY\" --expected-version \"$RELEASE_VERSION\" --expected-commit \"$GITHUB_SHA\"",
		"ubuntu-24.04-arm",
		"macos-15-intel",
		"macos-15",
		"windows-11-arm",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("release workflow missing %q", want)
		}
	}

	jobs := mapValue(t, workflow, "jobs")
	buildJob := mapValue(t, jobs, "build_verify")
	smokeJob := mapValue(t, jobs, "smoke_artifacts")
	attestJob := mapValue(t, jobs, "attest")
	publishJob := mapValue(t, jobs, "publish")
	assertPermissions := func(name string, job map[string]any, want map[string]any) {
		t.Helper()
		got := mapValue(t, job, "permissions")
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("%s permissions = %v, want %v", name, got, want)
		}
	}
	assertPermissions("build", buildJob, map[string]any{"contents": "read"})
	assertPermissions("smoke", smokeJob, map[string]any{"actions": "read", "contents": "read"})
	assertPermissions("attest", attestJob, map[string]any{"actions": "read", "attestations": "write", "contents": "read", "id-token": "write"})
	assertPermissions("publish", publishJob, map[string]any{"actions": "read", "contents": "write"})
	if got := stringSliceValue(t, attestJob, "needs"); !slices.Equal(got, []string{"build_verify", "smoke_artifacts"}) {
		t.Errorf("attest needs = %v, want [build_verify smoke_artifacts]", got)
	}
	if got := stringSliceValue(t, publishJob, "needs"); !slices.Equal(got, []string{"build_verify", "attest"}) {
		t.Errorf("publish needs = %v, want [build_verify attest]", got)
	}

	buildSteps := jobSteps(t, buildJob)
	preflight := stepIndex(t, buildSteps, "Refuse published release replacement")
	trusted := stepIndex(t, buildSteps, "Build trusted all-target binaries and source manifest")
	syft := stepIndex(t, buildSteps, "Install pinned Syft for archive SBOMs")
	build := stepIndex(t, buildSteps, "Build release artifacts without publishing")
	verify := stepIndex(t, buildSteps, "Verify local artifact set")
	upload := stepIndex(t, buildSteps, "Transfer verified release bundle")
	if !(preflight < trusted && trusted < syft && syft < build && build < verify && verify < upload) {
		t.Errorf("unsafe build ordering: preflight=%d trusted=%d syft=%d build=%d verify=%d upload=%d", preflight, trusted, syft, build, verify, upload)
	}
	preflightStep := buildSteps[preflight].(map[string]any)
	if fmt.Sprint(preflightStep["run"]) != "bash ./scripts/release-preflight.sh" {
		t.Errorf("preflight command = %q", preflightStep["run"])
	}
	preflightEnv := mapValue(t, preflightStep, "env")
	if fmt.Sprint(preflightEnv["GH_TOKEN"]) != "${{ secrets.GITHUB_TOKEN }}" {
		t.Errorf("preflight GH_TOKEN = %q", preflightEnv["GH_TOKEN"])
	}
	publishSteps := jobSteps(t, publishJob)
	reverify := stepIndex(t, publishSteps, "Reverify transferred release bundle")
	publish := stepIndex(t, publishSteps, "Upload, read back, and publish exact assets")
	if publish != len(publishSteps)-1 || reverify >= publish {
		t.Errorf("publish job ordering is unsafe: reverify=%d publish=%d steps=%d", reverify, publish, len(publishSteps))
	}
	publishStep := publishSteps[publish].(map[string]any)
	if run := fmt.Sprint(publishStep["run"]); run != "bash ./scripts/publish-release.sh dist docs/releases/v1.0.0-rc.1.md" {
		t.Errorf("publish command = %q", run)
	}
	env := mapValue(t, publishStep, "env")
	if fmt.Sprint(env["GH_TOKEN"]) != "${{ secrets.GITHUB_TOKEN }}" {
		t.Errorf("publish GH_TOKEN = %q", env["GH_TOKEN"])
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

func jobSteps(t *testing.T, job map[string]any) []any {
	t.Helper()
	steps, ok := job["steps"].([]any)
	if !ok {
		t.Fatalf("job steps = %#v, want list", job["steps"])
	}
	return steps
}

func stepIndex(t *testing.T, steps []any, name string) int {
	t.Helper()
	for i, raw := range steps {
		step, ok := raw.(map[string]any)
		if ok && fmt.Sprint(step["name"]) == name {
			return i
		}
	}
	t.Fatalf("release workflow missing step %q", name)
	return -1
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
