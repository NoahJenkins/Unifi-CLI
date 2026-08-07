package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
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
	"path/filepath"
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
	flag.BoolVar(&describe, "describe", false, "print the target and smoke-command contract")
	flag.BoolVar(&all, "all", false, "cross-build and structurally verify all release targets")
	flag.BoolVar(&native, "native", false, "build and execute the current native release target")
	flag.StringVar(&artifacts, "artifacts", "", "verify an existing GoReleaser dist directory")
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

	if describe {
		describeContract(os.Stdout)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	var err error
	if artifacts != "" {
		err = verifyArtifacts(ctx, artifacts)
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
	cmd.Env = replaceEnv(os.Environ(), map[string]string{"UNIFI_API_KEY": ""})
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

func verifyArtifacts(ctx context.Context, dist string) error {
	root, err := repositoryRoot()
	if err != nil {
		return err
	}
	dist, err = filepath.Abs(dist)
	if err != nil {
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
	var sources []artifact
	for _, artifact := range artifacts {
		switch artifact.Type {
		case "Archive":
			t := target{goos: artifact.Goos, goarch: artifact.Goarch}
			if _, exists := archives[t]; exists {
				return fmt.Errorf("duplicate archive for %s", t)
			}
			archives[t] = artifact
		case "Source":
			sources = append(sources, artifact)
		}
	}
	if len(archives) != len(targets) {
		return fmt.Errorf("archive target count = %d, want %d", len(archives), len(targets))
	}
	if len(sources) != 1 || !strings.HasSuffix(sources[0].Name, ".tar.gz") {
		return fmt.Errorf("source artifacts = %#v, want one tar.gz", sources)
	}
	if _, err := os.Stat(resolveArtifactPath(root, dist, sources[0].Path)); err != nil {
		return fmt.Errorf("source archive: %w", err)
	}

	checksums, err := readChecksums(filepath.Join(dist, "checksums.txt"))
	if err != nil {
		return err
	}
	sourcePath := resolveArtifactPath(root, dist, sources[0].Path)
	if err := verifyChecksum(sourcePath, checksums[sources[0].Name]); err != nil {
		return fmt.Errorf("source archive: %w", err)
	}
	for _, target := range targets {
		artifact, ok := archives[target]
		if !ok {
			return fmt.Errorf("missing archive for %s", target)
		}
		wantFormat := "tar.gz"
		wantSuffix := ".tar.gz"
		if target.goos == "windows" {
			wantFormat = "zip"
			wantSuffix = ".zip"
		}
		if artifact.Extra.Format != wantFormat || !strings.HasSuffix(artifact.Name, wantSuffix) {
			return fmt.Errorf("%s archive format/name = %q/%q", target, artifact.Extra.Format, artifact.Name)
		}
		archivePath := resolveArtifactPath(root, dist, artifact.Path)
		if err := verifyChecksum(archivePath, checksums[artifact.Name]); err != nil {
			return fmt.Errorf("%s: %w", target, err)
		}
		sbomPath := archivePath + ".sbom.json"
		if info, err := os.Stat(sbomPath); err != nil || info.Size() == 0 {
			return fmt.Errorf("%s archive SBOM missing or empty: %s", target, sbomPath)
		}
		extracted, cleanup, err := extractExecutable(archivePath, target.executableName())
		if err != nil {
			return fmt.Errorf("%s: %w", target, err)
		}
		if err := verifyStructure(extracted, target); err != nil {
			cleanup()
			return err
		}
		if target.goos == runtime.GOOS && target.goarch == runtime.GOARCH {
			if err := verifyNativeCommands(ctx, root, extracted, versionFromTag(), os.Getenv("GITHUB_SHA"), ""); err != nil {
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

func versionFromTag() string {
	tag := strings.TrimPrefix(os.Getenv("GITHUB_REF_NAME"), "v")
	if tag == "" {
		return smokeVersion
	}
	return tag
}

func populatedOrEqual(got, want string) bool {
	if want != "" {
		return got == want
	}
	return got != "" && got != "unknown" && got != "dev"
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
		result[strings.TrimPrefix(fields[1], "*")] = fields[0]
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

func extractExecutable(archivePath, executable string) (string, func(), error) {
	dir, err := os.MkdirTemp("", "unifi-release-artifact-")
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	destination := filepath.Join(dir, executable)
	if strings.HasSuffix(archivePath, ".zip") {
		err = extractZipFile(archivePath, executable, destination)
	} else {
		err = extractTarFile(archivePath, executable, destination)
	}
	if err != nil {
		cleanup()
		return "", func() {}, err
	}
	return destination, cleanup, nil
}

func extractZipFile(archivePath, executable, destination string) error {
	archive, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer archive.Close()
	for _, file := range archive.File {
		if filepath.Base(file.Name) != executable {
			continue
		}
		reader, err := file.Open()
		if err != nil {
			return err
		}
		err = writeExtracted(destination, reader, file.Mode())
		reader.Close()
		return err
	}
	return fs.ErrNotExist
}

func extractTarFile(archivePath, executable, destination string) error {
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
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return fs.ErrNotExist
		}
		if err != nil {
			return err
		}
		if filepath.Base(header.Name) == executable && header.Typeflag == tar.TypeReg {
			return writeExtracted(destination, reader, fs.FileMode(header.Mode))
		}
	}
}

func writeExtracted(path string, reader io.Reader, mode fs.FileMode) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, mode|0o700)
	if err != nil {
		return err
	}
	if _, err := io.Copy(file, reader); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

func resolveArtifactPath(root, dist, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	if strings.HasPrefix(filepath.Clean(path), "dist"+string(filepath.Separator)) {
		return filepath.Join(root, path)
	}
	return filepath.Join(dist, path)
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
