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
	if fmt.Sprint(concurrency["group"]) != "release-${{ github.repository }}-${{ inputs.release_tag || github.ref_name }}" {
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
		"subject-path: |",
		"${{ runner.temp }}/attest/*.tar.gz",
		"${{ runner.temp }}/attest/*.zip",
		"${{ runner.temp }}/attest/checksums.txt",
		"RELEASE_COMMIT_DATE=\"$(date -u -d \"@$RELEASE_COMMIT_TIMESTAMP\" +%Y-%m-%dT%H:%M:%SZ)\"",
		"for TARGET in darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64 windows/arm64; do",
		"GOOS=\"$GOOS\" GOARCH=\"$GOARCH\" go build -trimpath -ldflags \"$RELEASE_LDFLAGS\" -o \"$OUTPUT\" ./cmd/unifi",
		"go run ./cmd/release-smoke --write-source-manifest \"$RUNNER_TEMP/unifi-source-manifest.json\" --expected-commit \"$RELEASE_COMMIT\"",
		"go run ./cmd/release-smoke --extract-bundle \"$RUNNER_TEMP/release-downloads/generated/generated-release.tar\" --bundle-kind generated --destination \"$RUNNER_TEMP/release-inputs/generated\"",
		"go run ./cmd/release-smoke --artifacts \"$RUNNER_TEMP/release-inputs/generated/dist\" --expected-version \"${RELEASE_TAG#v}\" --expected-commit \"$RELEASE_COMMIT\" --trusted-binaries \"$RUNNER_TEMP/release-inputs/trusted/unifi-trusted\" --trusted-source-manifest \"$RUNNER_TEMP/release-inputs/trusted/unifi-source-manifest.json\"",
		"gh run download \"$GITHUB_RUN_ID\" --repo \"$GITHUB_REPOSITORY\" --name \"publication-release-$RELEASE_COMMIT\"",
		"bash \"$RUNNER_TEMP/source/scripts/publish-release.sh\"",
		"env -u GH_TOKEN go run ./cmd/release-smoke --artifacts \"$RUNNER_TEMP/publication/dist\"",
		"go run ./cmd/release-smoke --binary $binary --expected-version $releaseVersion --expected-commit $env:RELEASE_COMMIT",
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
	preflightJob := mapValue(t, jobs, "preflight")
	trustedJob := mapValue(t, jobs, "trusted_inputs")
	generateJob := mapValue(t, jobs, "generate")
	verifyJob := mapValue(t, jobs, "verify")
	smokeJob := mapValue(t, jobs, "smoke_artifacts")
	attestJob := mapValue(t, jobs, "attest")
	prepareJob := mapValue(t, jobs, "prepare_publication")
	publishJob := mapValue(t, jobs, "publish")
	assertPermissions := func(name string, job map[string]any, want map[string]any) {
		t.Helper()
		got := mapValue(t, job, "permissions")
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("%s permissions = %v, want %v", name, got, want)
		}
	}
	assertPermissions("preflight", preflightJob, map[string]any{"contents": "read"})
	assertPermissions("trusted", trustedJob, map[string]any{"contents": "read"})
	assertPermissions("generate", generateJob, map[string]any{"contents": "read"})
	assertPermissions("verify", verifyJob, map[string]any{"actions": "read", "contents": "read"})
	assertPermissions("smoke", smokeJob, map[string]any{"actions": "read", "contents": "read"})
	assertPermissions("attest", attestJob, map[string]any{"actions": "read", "attestations": "write", "contents": "read", "id-token": "write"})
	assertPermissions("prepare", prepareJob, map[string]any{"actions": "read", "contents": "read"})
	assertPermissions("publish", publishJob, map[string]any{"actions": "read", "contents": "write"})
	if got := stringSliceValue(t, trustedJob, "needs"); !slices.Equal(got, []string{"preflight"}) {
		t.Errorf("trusted needs = %v, want [preflight]", got)
	}
	if got := stringSliceValue(t, generateJob, "needs"); !slices.Equal(got, []string{"preflight"}) {
		t.Errorf("generate needs = %v, want [preflight]", got)
	}
	if got := stringSliceValue(t, verifyJob, "needs"); !slices.Equal(got, []string{"trusted_inputs", "generate"}) {
		t.Errorf("verify needs = %v, want [trusted_inputs generate]", got)
	}
	if got := stringSliceValue(t, smokeJob, "needs"); !slices.Equal(got, []string{"verify"}) {
		t.Errorf("smoke needs = %v, want [verify]", got)
	}
	if got := stringSliceValue(t, attestJob, "needs"); !slices.Equal(got, []string{"verify", "smoke_artifacts"}) {
		t.Errorf("attest needs = %v, want [verify smoke_artifacts]", got)
	}
	if got := stringSliceValue(t, prepareJob, "needs"); !slices.Equal(got, []string{"verify", "attest"}) {
		t.Errorf("prepare needs = %v, want [verify attest]", got)
	}
	if got := stringSliceValue(t, publishJob, "needs"); !slices.Equal(got, []string{"prepare_publication"}) {
		t.Errorf("publish needs = %v, want [prepare_publication]", got)
	}

	trustedSteps := jobSteps(t, trustedJob)
	trustedBuild := stepIndex(t, trustedSteps, "Build trusted all-target binaries and source manifest")
	trustedSeal := stepIndex(t, trustedSteps, "Seal trusted input bundle")
	trustedUpload := stepIndex(t, trustedSteps, "Transfer sealed trusted inputs")
	if !(trustedBuild < trustedSeal && trustedSeal < trustedUpload) {
		t.Errorf("unsafe trusted-input ordering: build=%d seal=%d upload=%d", trustedBuild, trustedSeal, trustedUpload)
	}
	for _, use := range allUses(trustedJob) {
		if strings.Contains(use, "goreleaser") || strings.Contains(use, "anchore") {
			t.Errorf("trusted-input job executes artifact generator %q", use)
		}
	}

	generateSteps := jobSteps(t, generateJob)
	preflight := stepIndex(t, generateSteps, "Refuse moved tags or published release replacement")
	syft := stepIndex(t, generateSteps, "Install pinned Syft for archive SBOMs")
	build := stepIndex(t, generateSteps, "Build release artifacts without publishing")
	generateSeal := stepIndex(t, generateSteps, "Seal generated artifact bundle")
	generateUpload := stepIndex(t, generateSteps, "Transfer generated artifact bundle")
	if !(preflight < syft && syft < build && build < generateSeal && generateSeal < generateUpload) {
		t.Errorf("unsafe generator ordering: preflight=%d syft=%d build=%d seal=%d upload=%d", preflight, syft, build, generateSeal, generateUpload)
	}
	preflightStep := generateSteps[preflight].(map[string]any)
	if fmt.Sprint(preflightStep["run"]) != "GITHUB_REF_NAME=\"$RELEASE_TAG\" GITHUB_SHA=\"$RELEASE_COMMIT\" bash ./scripts/release-preflight.sh" {
		t.Errorf("preflight command = %q", preflightStep["run"])
	}
	preflightEnv := mapValue(t, preflightStep, "env")
	if fmt.Sprint(preflightEnv["GH_TOKEN"]) != "${{ secrets.GITHUB_TOKEN }}" {
		t.Errorf("preflight GH_TOKEN = %q", preflightEnv["GH_TOKEN"])
	}
	verifySteps := jobSteps(t, verifyJob)
	downloadTrusted := stepIndex(t, verifySteps, "Download trusted input bundle")
	downloadGenerated := stepIndex(t, verifySteps, "Download generated artifact bundle")
	unseal := stepIndex(t, verifySteps, "Verify bundle seals and extract")
	compare := stepIndex(t, verifySteps, "Verify generated artifacts against isolated trusted inputs")
	verifiedSeal := stepIndex(t, verifySteps, "Seal verified release bundle")
	verifiedUpload := stepIndex(t, verifySteps, "Transfer sealed verified release bundle")
	if !(downloadTrusted < unseal && downloadGenerated < unseal && unseal < compare && compare < verifiedSeal && verifiedSeal < verifiedUpload) {
		t.Errorf("unsafe verification ordering: trusted=%d generated=%d unseal=%d compare=%d seal=%d upload=%d", downloadTrusted, downloadGenerated, unseal, compare, verifiedSeal, verifiedUpload)
	}
	for _, step := range verifySteps {
		run := fmt.Sprint(step.(map[string]any)["run"])
		if strings.Contains(run, "needs.trusted_inputs.outputs.bundle_digest") || strings.Contains(run, "needs.generate.outputs.bundle_digest") {
			t.Errorf("job output interpolated directly into verifier shell: %q", run)
		}
	}

	if uses := allUses(publishJob); len(uses) != 0 {
		t.Errorf("write-capable publish job must not run third-party actions: %v", uses)
	}
	publishSteps := jobSteps(t, publishJob)
	if len(publishSteps) != 1 {
		t.Fatalf("publish job steps = %d, want one sealed-shell publication step", len(publishSteps))
	}
	publishStep := publishSteps[0].(map[string]any)
	run := fmt.Sprint(publishStep["run"])
	for _, want := range []string{"gh run download", "shasum -a 256", "publish-release.sh", "git -C \"$RUNNER_TEMP/source\" fetch", "--extract-bundle", "--artifacts"} {
		if !strings.Contains(run, want) {
			t.Errorf("minimal publish step missing %q", want)
		}
	}
	env := mapValue(t, publishStep, "env")
	if fmt.Sprint(env["GH_TOKEN"]) != "${{ secrets.GITHUB_TOKEN }}" {
		t.Errorf("publish GH_TOKEN = %q", env["GH_TOKEN"])
	}
	if fmt.Sprint(env["EXPECTED_BUNDLE_DIGEST"]) != "${{ needs.prepare_publication.outputs.bundle_digest }}" {
		t.Errorf("publish expected digest = %q", env["EXPECTED_BUNDLE_DIGEST"])
	}
	if strings.Contains(run, "needs.prepare_publication.outputs.bundle_digest") {
		t.Error("publisher interpolates a preceding-job digest directly into shell source")
	}
	if strings.Contains(run, "$RUNNER_TEMP/publication/scripts/") {
		t.Error("publisher executes code supplied by the lower-privilege publication artifact")
	}
	prepareSeal := fmt.Sprint(jobSteps(t, prepareJob)[stepIndex(t, jobSteps(t, prepareJob), "Seal publication bundle")].(map[string]any)["run"])
	if strings.Contains(prepareSeal, "scripts") || strings.Contains(prepareSeal, "docs") {
		t.Errorf("publication artifact must be data-only: %q", prepareSeal)
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

func TestReleaseWorkflowCanResumeAnImmutableTagAndUsesNativeWindowsExtraction(t *testing.T) {
	data, err := os.ReadFile(".github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		"workflow_dispatch:",
		"release_tag:",
		"release_commit:",
		"RELEASE_TAG: ${{ inputs.release_tag || github.ref_name }}",
		"RELEASE_COMMIT: ${{ inputs.release_commit || github.sha }}",
		"ref: ${{ env.RELEASE_COMMIT }}",
		"GITHUB_REF_NAME=\"$RELEASE_TAG\" GITHUB_SHA=\"$RELEASE_COMMIT\"",
		"if: runner.os == 'Windows'",
		"shell: pwsh",
		"Expand-Archive -LiteralPath $archive -DestinationPath $smokeDir",
		"--expected-commit $env:RELEASE_COMMIT",
		"predicate-type: https://slsa.dev/provenance/v1",
		"predicate-path: ${{ runner.temp }}/resumed-provenance.json",
		"uri: (\"git+\" + $repository + \"@refs/tags/\" + $release_tag)",
		"digest: {gitCommit: $release_commit}",
		"digest: {gitCommit: $workflow_commit}",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("resumable release workflow missing %q", want)
		}
	}
	if strings.Contains(text, "if [ \"${{ matrix.goos }}\" = windows ]") {
		t.Error("Windows archive execution still shares the Unix tar extraction step")
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
