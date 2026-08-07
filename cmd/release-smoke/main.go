package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	archivepath "path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"
)

const (
	smokeVersion   = "1.0.0-rc.1-smoke"
	smokeCommit    = "0123456789abcdef0123456789abcdef01234567"
	smokeBuildDate = "2026-08-07T00:00:00Z"
)

var targets = []target{
	{goos: "darwin", goarch: "amd64"},
	{goos: "darwin", goarch: "arm64"},
	{goos: "linux", goarch: "amd64"},
	{goos: "linux", goarch: "arm64"},
	{goos: "windows", goarch: "amd64"},
	{goos: "windows", goarch: "arm64"},
}

var releaseVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)

type target struct {
	goos   string
	goarch string
}

func (t target) String() string { return t.goos + "/" + t.goarch }

func (t target) executableName() string {
	if t.goos == "windows" {
		return "unifi.exe"
	}
	return "unifi"
}

func main() {
	var describe bool
	var all bool
	var native bool
	var artifacts string
	var expectedVersion string
	var expectedCommit string
	flag.BoolVar(&describe, "describe", false, "print the target and smoke-command contract")
	flag.BoolVar(&all, "all", false, "cross-build and structurally verify all release targets")
	flag.BoolVar(&native, "native", false, "build and execute the current native release target")
	flag.StringVar(&artifacts, "artifacts", "", "verify an existing GoReleaser dist directory")
	flag.StringVar(&expectedVersion, "expected-version", "", "exact release version without a leading v")
	flag.StringVar(&expectedCommit, "expected-commit", "", "exact release commit")
	flag.Parse()

	selected := 0
	for _, enabled := range []bool{describe, all, native, artifacts != ""} {
		if enabled {
			selected++
		}
	}
	if selected != 1 || flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "choose exactly one of --describe, --all, --native, or --artifacts DIR")
		os.Exit(2)
	}
	if artifacts != "" && (expectedVersion == "" || expectedCommit == "") {
		fmt.Fprintln(os.Stderr, "--artifacts requires --expected-version and --expected-commit")
		os.Exit(2)
	}

	if describe {
		describeContract(os.Stdout)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	var err error
	if artifacts != "" {
		err = verifyArtifacts(ctx, artifacts, expectedVersion, expectedCommit)
	} else {
		err = buildAndVerify(ctx, all)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "release smoke:", err)
		os.Exit(1)
	}
}

func describeContract(w io.Writer) {
	for _, target := range targets {
		fmt.Fprintln(w, target)
	}
	fmt.Fprintln(w, "native commands: --version | version --json | --help | --config configs/config.example.yaml --json config show")
	fmt.Fprintln(w, "non-native policy: structural verification only; execution skipped")
}

func buildAndVerify(ctx context.Context, buildAll bool) error {
	root, err := repositoryRoot()
	if err != nil {
		return err
	}
	out, err := os.MkdirTemp("", "unifi-release-smoke-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(out)

	wanted := targets
	if !buildAll {
		native := target{goos: runtime.GOOS, goarch: runtime.GOARCH}
		if !slices.Contains(targets, native) {
			return fmt.Errorf("native target %s is outside the release matrix", native)
		}
		wanted = []target{native}
	}

	for _, target := range wanted {
		path := filepath.Join(out, target.goos+"_"+target.goarch, target.executableName())
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := buildTarget(ctx, root, path, target); err != nil {
			return err
		}
		if err := verifyStructure(path, target); err != nil {
			return err
		}
		if target.goos == runtime.GOOS && target.goarch == runtime.GOARCH {
			if err := verifyNativeCommands(ctx, root, path, smokeVersion, smokeCommit, smokeBuildDate); err != nil {
				return err
			}
			fmt.Printf("%s: structure and native commands verified\n", target)
		} else {
			fmt.Printf("%s: structure verified; execution skipped (non-native on %s/%s)\n", target, runtime.GOOS, runtime.GOARCH)
		}
	}
	return nil
}

func buildTarget(ctx context.Context, root, output string, target target) error {
	ldflags := strings.Join([]string{
		"-s", "-w",
		"-X", "github.com/noahjenkins/unifi-cli/internal/buildinfo.Version=" + smokeVersion,
		"-X", "github.com/noahjenkins/unifi-cli/internal/buildinfo.Commit=" + smokeCommit,
		"-X", "github.com/noahjenkins/unifi-cli/internal/buildinfo.BuildDate=" + smokeBuildDate,
	}, " ")
	cmd := exec.CommandContext(ctx, "go", "build", "-trimpath", "-ldflags", ldflags, "-o", output, "./cmd/unifi")
	cmd.Dir = root
	cmd.Env = replaceEnv(os.Environ(), map[string]string{
		"CGO_ENABLED": "0",
		"GOARCH":      target.goarch,
		"GOOS":        target.goos,
	})
	outputText, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("build %s: %w\n%s", target, err, outputText)
	}
	return nil
}

func verifyStructure(path string, target target) error {
	info, err := buildinfo.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read build info for %s: %w", target, err)
	}
	if info.Path != "github.com/noahjenkins/unifi-cli/cmd/unifi" {
		return fmt.Errorf("%s command path = %q", target, info.Path)
	}
	settings := make(map[string]string, len(info.Settings))
	for _, setting := range info.Settings {
		settings[setting.Key] = setting.Value
	}
	for key, want := range map[string]string{
		"CGO_ENABLED": "0",
		"GOARCH":      target.goarch,
		"GOOS":        target.goos,
	} {
		if settings[key] != want {
			return fmt.Errorf("%s build setting %s = %q, want %q", target, key, settings[key], want)
		}
	}
	if settings["-trimpath"] != "true" {
		return fmt.Errorf("%s is missing -trimpath build metadata", target)
	}
	return nil
}

func verifyNativeCommands(ctx context.Context, root, path, version, commit, buildDate string) error {
	versionText, err := run(ctx, root, path, "--version")
	if err != nil {
		return err
	}
	if versionText != "unifi version "+version+"\n" {
		return fmt.Errorf("--version output = %q", versionText)
	}

	versionJSON, err := run(ctx, root, path, "version", "--json")
	if err != nil {
		return err
	}
	var versionEnvelope struct {
		SchemaVersion string `json:"schema_version"`
		OK            bool   `json:"ok"`
		Resource      string `json:"resource"`
		Action        string `json:"action"`
		Data          struct {
			Version   string `json:"version"`
			Commit    string `json:"commit"`
			BuildDate string `json:"build_date"`
			GoVersion string `json:"go_version"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(versionJSON), &versionEnvelope); err != nil {
		return fmt.Errorf("decode version --json: %w", err)
	}
	if versionEnvelope.SchemaVersion != "1" || !versionEnvelope.OK || versionEnvelope.Resource != "version" ||
		versionEnvelope.Action != "show" || versionEnvelope.Data.Version != version ||
		!populatedOrEqual(versionEnvelope.Data.Commit, commit) ||
		!populatedOrEqual(versionEnvelope.Data.BuildDate, buildDate) ||
		!strings.HasPrefix(versionEnvelope.Data.GoVersion, "go") {
		return fmt.Errorf("unexpected version --json envelope: %+v", versionEnvelope)
	}

	help, err := run(ctx, root, path, "--help")
	if err != nil {
		return err
	}
	if !strings.Contains(help, "Usage:") || !strings.Contains(help, "Unofficial project.") {
		return errors.New("--help is missing usage or unofficial-project disclaimer")
	}

	configJSON, err := run(ctx, root, path, "--config", filepath.Join(root, "configs", "config.example.yaml"), "--json", "config", "show")
	if err != nil {
		return err
	}
	var configEnvelope struct {
		SchemaVersion string `json:"schema_version"`
		OK            bool   `json:"ok"`
		Resource      string `json:"resource"`
		Action        string `json:"action"`
		Data          struct {
			Host string `json:"host"`
			Port int    `json:"port"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(configJSON), &configEnvelope); err != nil {
		return fmt.Errorf("decode config show: %w", err)
	}
	if configEnvelope.SchemaVersion != "1" || !configEnvelope.OK || configEnvelope.Resource != "config" ||
		configEnvelope.Action != "show" || configEnvelope.Data.Host != "192.168.1.1" || configEnvelope.Data.Port != 443 {
		return fmt.Errorf("unexpected config-only envelope: %+v", configEnvelope)
	}
	return nil
}

func run(ctx context.Context, dir, path string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = dir
	cmd.Env = sanitizedCommandEnv(os.Environ())
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("run %s %s: %w\n%s", path, strings.Join(args, " "), err, output)
	}
	return string(output), nil
}

type artifact struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Goos   string `json:"goos"`
	Goarch string `json:"goarch"`
	Type   string `json:"type"`
	Extra  struct {
		Format string `json:"Format"`
	} `json:"extra"`
}

func verifyArtifacts(ctx context.Context, dist, expectedVersion, expectedCommit string) error {
	root, err := repositoryRoot()
	if err != nil {
		return err
	}
	dist, err = canonicalDist(dist)
	if err != nil {
		return err
	}
	if err := validateExpectedMetadata(expectedVersion, expectedCommit); err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(dist, "artifacts.json"))
	if err != nil {
		return fmt.Errorf("read artifacts.json: %w", err)
	}
	var artifacts []artifact
	if err := json.Unmarshal(data, &artifacts); err != nil {
		return fmt.Errorf("decode artifacts.json: %w", err)
	}

	archives := make(map[target]artifact)
	sboms := make(map[string]artifact)
	var sources []artifact
	var checksumArtifacts []artifact
	var metadataArtifacts []artifact
	for _, current := range artifacts {
		resolved, err := resolveArtifactPath(dist, current.Path)
		if err != nil {
			return fmt.Errorf("artifact %q: %w", current.Name, err)
		}
		if _, err := os.Stat(resolved); err != nil {
			return fmt.Errorf("artifact %q: %w", current.Name, err)
		}
		switch current.Type {
		case "Archive":
			t := target{goos: current.Goos, goarch: current.Goarch}
			if _, exists := archives[t]; exists {
				return fmt.Errorf("duplicate archive for %s", t)
			}
			archives[t] = current
		case "Source":
			sources = append(sources, current)
		case "SBOM":
			if _, exists := sboms[current.Name]; exists {
				return fmt.Errorf("duplicate SBOM %q", current.Name)
			}
			sboms[current.Name] = current
		case "Checksum":
			checksumArtifacts = append(checksumArtifacts, current)
		case "Metadata":
			metadataArtifacts = append(metadataArtifacts, current)
		case "Binary":
			// GoReleaser records intermediate binaries. Their target structure is
			// verified through the corresponding release archive below.
		default:
			return fmt.Errorf("unexpected artifact record type %q for %q", current.Type, current.Name)
		}
	}
	if len(archives) != len(targets) {
		return fmt.Errorf("archive target count = %d, want %d", len(archives), len(targets))
	}
	wantSourceName := fmt.Sprintf("unifi-cli_%s_source.tar.gz", expectedVersion)
	if len(sources) != 1 || sources[0].Name != wantSourceName {
		return fmt.Errorf("source artifacts = %#v, want exactly %q", sources, wantSourceName)
	}
	if len(sboms) != len(targets) {
		return fmt.Errorf("SBOM target count = %d, want %d", len(sboms), len(targets))
	}
	if len(checksumArtifacts) != 1 || checksumArtifacts[0].Name != "checksums.txt" {
		return fmt.Errorf("checksum artifacts = %#v, want checksums.txt", checksumArtifacts)
	}
	if len(metadataArtifacts) != 1 || metadataArtifacts[0].Name != "metadata.json" {
		return fmt.Errorf("metadata artifacts = %#v, want metadata.json", metadataArtifacts)
	}
	metadataPath, err := resolveNamedArtifact(dist, metadataArtifacts[0])
	if err != nil {
		return fmt.Errorf("metadata: %w", err)
	}
	if err := inspectGoReleaserMetadata(metadataPath, expectedVersion, expectedCommit); err != nil {
		return fmt.Errorf("metadata: %w", err)
	}
	sourcePath, err := resolveNamedArtifact(dist, sources[0])
	if err != nil {
		return fmt.Errorf("source archive: %w", err)
	}
	if err := inspectSourceArchive(sourcePath, fmt.Sprintf("unifi-cli_%s", expectedVersion), expectedCommit); err != nil {
		return fmt.Errorf("source archive: %w", err)
	}

	checksumsPath, err := resolveNamedArtifact(dist, checksumArtifacts[0])
	if err != nil {
		return fmt.Errorf("checksums: %w", err)
	}
	checksums, err := readChecksums(checksumsPath)
	if err != nil {
		return err
	}
	wantChecksums := map[string]struct{}{wantSourceName: {}}
	for _, target := range targets {
		archiveName := expectedArchiveName(expectedVersion, target)
		wantChecksums[archiveName] = struct{}{}
		wantChecksums[archiveName+".sbom.json"] = struct{}{}
	}
	if err := requireExactNames(checksums, wantChecksums, "checksum manifest"); err != nil {
		return err
	}
	if err := verifyChecksum(sourcePath, checksums[sources[0].Name]); err != nil {
		return fmt.Errorf("source archive: %w", err)
	}
	for _, target := range targets {
		current, ok := archives[target]
		if !ok {
			return fmt.Errorf("missing archive for %s", target)
		}
		wantFormat := "tar.gz"
		if target.goos == "windows" {
			wantFormat = "zip"
		}
		wantName := expectedArchiveName(expectedVersion, target)
		if current.Extra.Format != wantFormat || current.Name != wantName {
			return fmt.Errorf("%s archive format/name = %q/%q, want %q/%q", target, current.Extra.Format, current.Name, wantFormat, wantName)
		}
		archivePath, err := resolveNamedArtifact(dist, current)
		if err != nil {
			return fmt.Errorf("%s: %w", target, err)
		}
		if err := verifyChecksum(archivePath, checksums[current.Name]); err != nil {
			return fmt.Errorf("%s: %w", target, err)
		}
		wantSBOMName := wantName + ".sbom.json"
		sbom, ok := sboms[wantSBOMName]
		if !ok {
			return fmt.Errorf("missing archive SBOM for %s", target)
		}
		if sbom.Name != wantSBOMName {
			return fmt.Errorf("%s SBOM name = %q, want %q", target, sbom.Name, wantSBOMName)
		}
		sbomPath, err := resolveNamedArtifact(dist, sbom)
		if err != nil {
			return fmt.Errorf("%s SBOM: %w", target, err)
		}
		if err := verifyChecksum(sbomPath, checksums[sbom.Name]); err != nil {
			return fmt.Errorf("%s SBOM: %w", target, err)
		}
		expectedRoot := strings.TrimSuffix(strings.TrimSuffix(wantName, ".tar.gz"), ".zip")
		executable, cleanup, err := inspectReleaseArchive(archivePath, target, expectedRoot)
		if err != nil {
			return fmt.Errorf("%s: %w", target, err)
		}
		if err := inspectCycloneDXSBOM(sbomPath, wantName, expectedVersion, executable); err != nil {
			cleanup()
			return fmt.Errorf("%s SBOM: %w", target, err)
		}
		if err := verifyStructure(executable.extractedPath, target); err != nil {
			cleanup()
			return err
		}
		if target.goos == runtime.GOOS && target.goarch == runtime.GOARCH {
			if err := verifyNativeCommands(ctx, root, executable.extractedPath, expectedVersion, expectedCommit, ""); err != nil {
				cleanup()
				return err
			}
			fmt.Printf("%s archive: checksum, SBOM, structure, and native commands verified\n", target)
		} else {
			fmt.Printf("%s archive: checksum, SBOM, and structure verified; execution skipped (non-native on %s/%s)\n", target, runtime.GOOS, runtime.GOARCH)
		}
		cleanup()
	}
	return nil
}

func validateExpectedMetadata(version, commit string) error {
	if !releaseVersionPattern.MatchString(version) || strings.HasPrefix(version, "v") || strings.ContainsAny(version, "/\\") || archivepath.Clean(version) != version {
		return fmt.Errorf("invalid expected version %q", version)
	}
	if len(commit) != 40 {
		return fmt.Errorf("invalid expected commit %q", commit)
	}
	if _, err := hex.DecodeString(commit); err != nil {
		return fmt.Errorf("invalid expected commit %q", commit)
	}
	return nil
}

func canonicalDist(dist string) (string, error) {
	abs, err := filepath.Abs(dist)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolve dist: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("dist is not a directory")
	}
	return resolved, nil
}

func expectedArchiveName(version string, target target) string {
	extension := ".tar.gz"
	if target.goos == "windows" {
		extension = ".zip"
	}
	return fmt.Sprintf("unifi-cli_%s_%s_%s%s", version, target.goos, target.goarch, extension)
}

func resolveNamedArtifact(dist string, current artifact) (string, error) {
	resolved, err := resolveArtifactPath(dist, current.Path)
	if err != nil {
		return "", err
	}
	if filepath.Base(resolved) != current.Name {
		return "", fmt.Errorf("artifact name %q does not match path %q", current.Name, current.Path)
	}
	return resolved, nil
}

func requireExactNames(got map[string]string, want map[string]struct{}, label string) error {
	if len(got) != len(want) {
		return fmt.Errorf("%s has %d entries, want %d", label, len(got), len(want))
	}
	for name := range got {
		if _, ok := want[name]; !ok {
			return fmt.Errorf("%s contains unexpected entry %q", label, name)
		}
	}
	for name := range want {
		if _, ok := got[name]; !ok {
			return fmt.Errorf("%s is missing entry %q", label, name)
		}
	}
	return nil
}

func populatedOrEqual(got, want string) bool {
	if want != "" {
		return got == want
	}
	return got != "" && got != "unknown" && got != "dev"
}

func sanitizedCommandEnv(environ []string) []string {
	result := make([]string, 0, len(environ))
	for _, entry := range environ {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(key, "UNIFI_") {
			result = append(result, entry)
		}
	}
	return result
}

func readChecksums(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read checksums: %w", err)
	}
	result := make(map[string]string)
	for line := range strings.Lines(string(data)) {
		fields := strings.Fields(line)
		if len(fields) != 2 || len(fields[0]) != sha256.Size*2 {
			return nil, fmt.Errorf("malformed SHA-256 checksum line %q", strings.TrimSpace(line))
		}
		name := strings.TrimPrefix(fields[1], "*")
		if name == "" || filepath.Base(name) != name || filepath.IsAbs(name) || strings.ContainsAny(name, "/\\") {
			return nil, fmt.Errorf("unsafe checksum artifact name %q", name)
		}
		if _, err := hex.DecodeString(fields[0]); err != nil {
			return nil, fmt.Errorf("malformed SHA-256 checksum %q", fields[0])
		}
		if _, exists := result[name]; exists {
			return nil, fmt.Errorf("duplicate checksum entry %q", name)
		}
		result[name] = strings.ToLower(fields[0])
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("checksum manifest is empty")
	}
	return result, nil
}

func verifyChecksum(path, want string) error {
	if want == "" {
		return fmt.Errorf("checksum entry missing for %s", filepath.Base(path))
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return err
	}
	got := hex.EncodeToString(hash.Sum(nil))
	if got != want {
		return fmt.Errorf("SHA-256 for %s = %s, want %s", filepath.Base(path), got, want)
	}
	return nil
}

type archiveExecutable struct {
	extractedPath string
	relativePath  string
	sha256        string
}

func inspectReleaseArchive(archivePath string, target target, expectedRoot string) (archiveExecutable, func(), error) {
	dir, err := os.MkdirTemp("", "unifi-release-artifact-")
	if err != nil {
		return archiveExecutable{}, func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	destination := filepath.Join(dir, target.executableName())
	if strings.HasSuffix(archivePath, ".zip") {
		err = inspectZipArchive(archivePath, target, expectedRoot, destination)
	} else {
		err = inspectTarArchive(archivePath, target, expectedRoot, destination)
	}
	if err != nil {
		cleanup()
		return archiveExecutable{}, func() {}, err
	}
	digest, err := sha256File(destination)
	if err != nil {
		cleanup()
		return archiveExecutable{}, func() {}, err
	}
	return archiveExecutable{
		extractedPath: destination,
		relativePath:  expectedRoot + "/" + target.executableName(),
		sha256:        digest,
	}, cleanup, nil
}

func inspectZipArchive(archivePath string, target target, expectedRoot, destination string) error {
	archive, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer archive.Close()
	want := expectedArchiveEntries(expectedRoot, target)
	seenPaths := make(map[string]struct{})
	seenNames := make(map[string]struct{})
	for _, file := range archive.File {
		clean, isDir, err := validateArchiveEntry(file.Name, expectedRoot, file.FileInfo().IsDir())
		if err != nil {
			return err
		}
		if !isDir && !file.Mode().IsRegular() {
			return fmt.Errorf("archive entry %q has unsupported mode %s", file.Name, file.Mode())
		}
		if err := recordArchiveEntry(clean, isDir, seenPaths, seenNames); err != nil {
			return err
		}
		if isDir {
			continue
		}
		if _, ok := want[clean]; !ok {
			return fmt.Errorf("unexpected archive entry %q", clean)
		}
		mode := file.Mode().Perm()
		wantMode := expectedArchiveMode(clean, expectedRoot, target)
		if mode != wantMode {
			return fmt.Errorf("archive entry %q mode = %04o, want %04o", clean, mode, wantMode)
		}
		if clean != expectedRoot+"/"+target.executableName() {
			continue
		}
		reader, err := file.Open()
		if err != nil {
			return err
		}
		err = writeExtracted(destination, reader, mode)
		closeErr := reader.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return requireArchiveEntries(seenPaths, want, destination, expectedRoot)
}

func inspectTarArchive(archivePath string, target target, expectedRoot, destination string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer file.Close()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gzipReader.Close()
	reader := tar.NewReader(gzipReader)
	want := expectedArchiveEntries(expectedRoot, target)
	seenPaths := make(map[string]struct{})
	seenNames := make(map[string]struct{})
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		isDir := header.Typeflag == tar.TypeDir
		if header.Typeflag != tar.TypeReg && !isDir {
			return fmt.Errorf("archive entry %q has unsupported type %d", header.Name, header.Typeflag)
		}
		clean, isDir, err := validateArchiveEntry(header.Name, expectedRoot, isDir)
		if err != nil {
			return err
		}
		if err := recordArchiveEntry(clean, isDir, seenPaths, seenNames); err != nil {
			return err
		}
		if isDir {
			continue
		}
		if _, ok := want[clean]; !ok {
			return fmt.Errorf("unexpected archive entry %q", clean)
		}
		mode := fs.FileMode(header.Mode).Perm()
		wantMode := expectedArchiveMode(clean, expectedRoot, target)
		if mode != wantMode {
			return fmt.Errorf("archive entry %q mode = %04o, want %04o", clean, mode, wantMode)
		}
		if clean == expectedRoot+"/"+target.executableName() {
			if err := writeExtracted(destination, reader, mode); err != nil {
				return err
			}
		}
	}
	return requireArchiveEntries(seenPaths, want, destination, expectedRoot)
}

func writeExtracted(path string, reader io.Reader, mode fs.FileMode) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(file, reader); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Chmod(path, mode)
}

func expectedArchiveEntries(root string, target target) map[string]struct{} {
	return map[string]struct{}{
		root + "/" + target.executableName(): {},
		root + "/LICENSE":                    {},
		root + "/README.md":                  {},
		root + "/CHANGELOG.md":               {},
	}
}

func expectedArchiveMode(name, root string, target target) fs.FileMode {
	if name == root+"/"+target.executableName() {
		return 0o755
	}
	return 0o644
}

func validateArchiveEntry(name, expectedRoot string, directory bool) (string, bool, error) {
	if name == "" || strings.Contains(name, "\\") || archivepath.IsAbs(name) {
		return "", false, fmt.Errorf("unsafe archive entry %q", name)
	}
	trimmed := strings.TrimSuffix(name, "/")
	clean := archivepath.Clean(trimmed)
	if clean != trimmed || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", false, fmt.Errorf("unsafe archive entry %q", name)
	}
	if clean != expectedRoot && !strings.HasPrefix(clean, expectedRoot+"/") {
		return "", false, fmt.Errorf("archive entry %q is outside root %q", name, expectedRoot)
	}
	if directory && clean != expectedRoot {
		return "", false, fmt.Errorf("unexpected archive directory %q", name)
	}
	return clean, directory, nil
}

func recordArchiveEntry(name string, directory bool, paths, basenames map[string]struct{}) error {
	if _, exists := paths[name]; exists {
		return fmt.Errorf("duplicate archive path %q", name)
	}
	paths[name] = struct{}{}
	if directory {
		return nil
	}
	base := archivepath.Base(name)
	if _, exists := basenames[base]; exists {
		return fmt.Errorf("duplicate archive basename %q", base)
	}
	basenames[base] = struct{}{}
	return nil
}

func requireArchiveEntries(got, want map[string]struct{}, executable, expectedRoot string) error {
	fileCount := len(got)
	if _, hasRootDirectory := got[expectedRoot]; hasRootDirectory {
		fileCount--
	}
	if fileCount != len(want) {
		return fmt.Errorf("archive has %d files, want %d", fileCount, len(want))
	}
	for name := range want {
		if _, ok := got[name]; !ok {
			return fmt.Errorf("archive is missing %q", name)
		}
	}
	info, err := os.Stat(executable)
	if err != nil {
		return fmt.Errorf("archive executable: %w", err)
	}
	if info.Size() == 0 {
		return fmt.Errorf("archive executable is empty")
	}
	return nil
}

func resolveArtifactPath(dist, artifactPath string) (string, error) {
	if artifactPath == "" || filepath.IsAbs(artifactPath) || archivepath.IsAbs(artifactPath) || strings.Contains(artifactPath, "\\") {
		return "", fmt.Errorf("unsafe artifact path %q", artifactPath)
	}
	clean := archivepath.Clean(artifactPath)
	if clean != artifactPath || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("unsafe artifact path %q", artifactPath)
	}
	parts := strings.Split(clean, "/")
	if len(parts) > 1 && parts[0] == "dist" {
		parts = parts[1:]
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("unsafe artifact path %q", artifactPath)
	}
	candidate := filepath.Join(append([]string{dist}, parts...)...)
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(dist, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("artifact path %q resolves outside dist", artifactPath)
	}
	return resolved, nil
}

func inspectSourceArchive(archivePath, expectedRoot, expectedCommit string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer file.Close()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("open gzip: %w", err)
	}
	defer gzipReader.Close()
	reader := tar.NewReader(gzipReader)
	seen := make(map[string]struct{})
	core := map[string]bool{
		expectedRoot + "/LICENSE":      false,
		expectedRoot + "/README.md":    false,
		expectedRoot + "/CHANGELOG.md": false,
		expectedRoot + "/go.mod":       false,
	}
	regularFiles := 0
	globalHeaders := 0
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read tar: %w", err)
		}
		if header.Typeflag == tar.TypeXGlobalHeader {
			globalHeaders++
			if globalHeaders > 1 {
				return fmt.Errorf("source archive has multiple PAX global headers")
			}
			if header.Name != "pax_global_header" {
				return fmt.Errorf("source PAX global header name = %q, want pax_global_header", header.Name)
			}
			if len(header.PAXRecords) != 1 || header.PAXRecords["comment"] != expectedCommit {
				return fmt.Errorf("source PAX records = %#v, want exact commit comment", header.PAXRecords)
			}
			continue
		}
		isDir := header.Typeflag == tar.TypeDir
		if header.Typeflag != tar.TypeReg && !isDir {
			return fmt.Errorf("source entry %q has unsupported type %d", header.Name, header.Typeflag)
		}
		clean, _, err := validateSourceEntry(header.Name, expectedRoot, isDir)
		if err != nil {
			return err
		}
		if _, exists := seen[clean]; exists {
			return fmt.Errorf("duplicate source path %q", clean)
		}
		seen[clean] = struct{}{}
		if isDir {
			continue
		}
		regularFiles++
		if _, ok := core[clean]; ok {
			if header.Size == 0 {
				return fmt.Errorf("source core file %q is empty", clean)
			}
			core[clean] = true
		}
	}
	if regularFiles == 0 {
		return fmt.Errorf("source archive has no files")
	}
	if globalHeaders != 1 {
		return fmt.Errorf("source archive PAX global header count = %d, want 1", globalHeaders)
	}
	for name, present := range core {
		if !present {
			return fmt.Errorf("source archive is missing %q", name)
		}
	}
	return nil
}

func validateSourceEntry(name, expectedRoot string, directory bool) (string, bool, error) {
	if name == "" || strings.Contains(name, "\\") || archivepath.IsAbs(name) {
		return "", false, fmt.Errorf("unsafe source entry %q", name)
	}
	trimmed := strings.TrimSuffix(name, "/")
	clean := archivepath.Clean(trimmed)
	if clean != trimmed || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", false, fmt.Errorf("unsafe source entry %q", name)
	}
	if clean != expectedRoot && !strings.HasPrefix(clean, expectedRoot+"/") {
		return "", false, fmt.Errorf("source entry %q is outside root %q", name, expectedRoot)
	}
	return clean, directory, nil
}

func inspectCycloneDXSBOM(sbomPath, archiveName, expectedVersion string, executable archiveExecutable) error {
	file, err := os.Open(sbomPath)
	if err != nil {
		return err
	}
	defer file.Close()
	var bom struct {
		BOMFormat   string `json:"bomFormat"`
		SpecVersion string `json:"specVersion"`
		Version     int    `json:"version"`
		Metadata    struct {
			Component struct {
				Type    string `json:"type"`
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"component"`
		} `json:"metadata"`
		Components []struct {
			Type    string `json:"type"`
			Name    string `json:"name"`
			Version string `json:"version"`
			Hashes  []struct {
				Algorithm string `json:"alg"`
				Content   string `json:"content"`
			} `json:"hashes"`
		} `json:"components"`
	}
	decoder := json.NewDecoder(io.LimitReader(file, 32<<20))
	if err := decoder.Decode(&bom); err != nil {
		return fmt.Errorf("decode CycloneDX JSON: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return fmt.Errorf("CycloneDX JSON has trailing content")
	}
	if bom.BOMFormat != "CycloneDX" || bom.SpecVersion != "1.7" || bom.Version < 1 {
		return fmt.Errorf("invalid CycloneDX identity: format=%q spec=%q version=%d", bom.BOMFormat, bom.SpecVersion, bom.Version)
	}
	if bom.Metadata.Component.Type != "file" || bom.Metadata.Component.Name != archiveName || bom.Metadata.Component.Version != expectedVersion {
		return fmt.Errorf("SBOM source = %q@%q, want %q@%q", bom.Metadata.Component.Name, bom.Metadata.Component.Version, archiveName, expectedVersion)
	}
	if len(bom.Components) == 0 {
		return fmt.Errorf("SBOM components are empty")
	}
	executableComponents := 0
	for i, component := range bom.Components {
		if component.Name == "" {
			return fmt.Errorf("SBOM component %d has empty name", i)
		}
		switch component.Type {
		case "library", "application":
			if component.Version == "" {
				return fmt.Errorf("SBOM %s component %d has empty version", component.Type, i)
			}
		case "file":
			if len(component.Hashes) == 0 {
				return fmt.Errorf("SBOM file component %d has no hashes", i)
			}
			sha256Hashes := 0
			for _, hash := range component.Hashes {
				wantBytes := 0
				switch hash.Algorithm {
				case "SHA-1":
					wantBytes = sha1.Size
				case "SHA-256":
					wantBytes = sha256.Size
				default:
					return fmt.Errorf("SBOM file component %d has unsupported hash algorithm %q", i, hash.Algorithm)
				}
				decoded, err := hex.DecodeString(hash.Content)
				if err != nil || len(decoded) != wantBytes {
					return fmt.Errorf("SBOM file component %d has invalid %s hash", i, hash.Algorithm)
				}
				if hash.Algorithm == "SHA-256" {
					sha256Hashes++
					if matchesSyftExecutablePath(component.Name, executable.relativePath) && !strings.EqualFold(hash.Content, executable.sha256) {
						return fmt.Errorf("SBOM executable SHA-256 = %s, want %s", hash.Content, executable.sha256)
					}
				}
			}
			if matchesSyftExecutablePath(component.Name, executable.relativePath) {
				executableComponents++
				if sha256Hashes != 1 {
					return fmt.Errorf("SBOM executable component has %d SHA-256 hashes, want 1", sha256Hashes)
				}
			}
		default:
			return fmt.Errorf("SBOM component %d has unsupported type %q", i, component.Type)
		}
	}
	if executableComponents != 1 {
		return fmt.Errorf("SBOM matching executable component count = %d, want 1", executableComponents)
	}
	return nil
}

func matchesSyftExecutablePath(name, relativePath string) bool {
	if name == "" || !archivepath.IsAbs(name) || strings.Contains(name, "\\") || archivepath.Clean(name) != name {
		return false
	}
	suffix := "/" + relativePath
	if !strings.HasSuffix(name, suffix) {
		return false
	}
	prefix := strings.TrimSuffix(name, suffix)
	tempDir := archivepath.Base(prefix)
	return strings.HasPrefix(tempDir, "syft-archive-contents-") && len(tempDir) > len("syft-archive-contents-")
}

func sha256File(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func inspectGoReleaserMetadata(path, expectedVersion, expectedCommit string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var metadata struct {
		ProjectName string `json:"project_name"`
		Version     string `json:"version"`
		Commit      string `json:"commit"`
	}
	if err := json.Unmarshal(data, &metadata); err != nil {
		return fmt.Errorf("decode metadata.json: %w", err)
	}
	if metadata.ProjectName != "unifi-cli" || metadata.Version != expectedVersion || metadata.Commit != expectedCommit {
		return fmt.Errorf("identity = %q %q %q, want unifi-cli %q %q", metadata.ProjectName, metadata.Version, metadata.Commit, expectedVersion, expectedCommit)
	}
	return nil
}

func repositoryRoot() (string, error) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("find repository root: %w", err)
	}
	return strings.TrimSpace(string(output)), nil
}

func replaceEnv(environ []string, replacements map[string]string) []string {
	result := make([]string, 0, len(environ)+len(replacements))
	for _, entry := range environ {
		key, _, _ := strings.Cut(entry, "=")
		if _, replace := replacements[key]; !replace {
			result = append(result, entry)
		}
	}
	for key, value := range replacements {
		result = append(result, key+"="+value)
	}
	return result
}
