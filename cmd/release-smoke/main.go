package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
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

	"github.com/noahjenkins/unifi-cli/internal/fileutil"
)

const (
	smokeVersion   = "1.0.0-rc.1-smoke"
	smokeCommit    = "0123456789abcdef0123456789abcdef01234567"
	smokeBuildDate = "2026-08-07T00:00:00Z"

	maxReleaseEntryBytes     = 128 << 20
	maxReleaseManifestBytes  = 4 << 20
	maxReleaseSBOMBytes      = 32 << 20
	maxReleaseArchiveBytes   = 128 << 20
	maxReleaseExpandedBytes  = 512 << 20
	maxReleaseArchiveEntries = 64
	maxReleaseSourceEntries  = 10000
	maxBundleBytes           = 1 << 30
	maxBundleEntryBytes      = 256 << 20
	maxBundleEntries         = 2048
)

type sourceManifest struct {
	SchemaVersion string                `json:"schema_version"`
	Commit        string                `json:"commit"`
	Files         []sourceManifestEntry `json:"files"`
}

type sourceManifestEntry struct {
	Path   string `json:"path"`
	Mode   string `json:"mode"`
	SHA256 string `json:"sha256"`
}

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
	var binary string
	var expectedVersion string
	var expectedCommit string
	var trustedBinaries string
	var trustedSourceManifest string
	var writeSourceManifest string
	var extractBundlePath string
	var bundleKind string
	var destination string
	flag.BoolVar(&describe, "describe", false, "print the target and smoke-command contract")
	flag.BoolVar(&all, "all", false, "cross-build and structurally verify all release targets")
	flag.BoolVar(&native, "native", false, "build and execute the current native release target")
	flag.StringVar(&artifacts, "artifacts", "", "verify an existing GoReleaser dist directory")
	flag.StringVar(&binary, "binary", "", "execute the four-command smoke contract against a native release binary")
	flag.StringVar(&expectedVersion, "expected-version", "", "exact release version without a leading v")
	flag.StringVar(&expectedCommit, "expected-commit", "", "exact release commit")
	flag.StringVar(&trustedBinaries, "trusted-binaries", "", "directory of trusted binaries cross-built from the exact checkout before artifact generation")
	flag.StringVar(&trustedSourceManifest, "trusted-source-manifest", "", "trusted source manifest generated from the exact release commit")
	flag.StringVar(&writeSourceManifest, "write-source-manifest", "", "write a trusted source manifest for --expected-commit")
	flag.StringVar(&extractBundlePath, "extract-bundle", "", "safely extract a transferred release bundle")
	flag.StringVar(&bundleKind, "bundle-kind", "", "bundle namespace policy: trusted, generated, verified, or publication")
	flag.StringVar(&destination, "destination", "", "new destination directory for --extract-bundle")
	flag.Parse()

	selected := 0
	for _, enabled := range []bool{describe, all, native, artifacts != "", binary != "", writeSourceManifest != "", extractBundlePath != ""} {
		if enabled {
			selected++
		}
	}
	if selected != 1 || flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "choose exactly one of --describe, --all, --native, --binary FILE, --artifacts DIR, --write-source-manifest FILE, or --extract-bundle FILE")
		os.Exit(2)
	}
	if artifacts != "" && (expectedVersion == "" || expectedCommit == "" || trustedBinaries == "" || trustedSourceManifest == "") {
		fmt.Fprintln(os.Stderr, "--artifacts requires --expected-version, --expected-commit, --trusted-binaries, and --trusted-source-manifest")
		os.Exit(2)
	}
	if writeSourceManifest != "" && expectedCommit == "" {
		fmt.Fprintln(os.Stderr, "--write-source-manifest requires --expected-commit")
		os.Exit(2)
	}
	if binary != "" && (expectedVersion == "" || expectedCommit == "") {
		fmt.Fprintln(os.Stderr, "--binary requires --expected-version and --expected-commit")
		os.Exit(2)
	}
	if extractBundlePath != "" && (bundleKind == "" || destination == "") {
		fmt.Fprintln(os.Stderr, "--extract-bundle requires --bundle-kind and --destination")
		os.Exit(2)
	}
	if extractBundlePath == "" && (bundleKind != "" || destination != "") {
		fmt.Fprintln(os.Stderr, "--bundle-kind and --destination require --extract-bundle")
		os.Exit(2)
	}

	if describe {
		describeContract(os.Stdout)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	var err error
	if extractBundlePath != "" {
		err = extractBundle(extractBundlePath, destination, bundleKind)
	} else if writeSourceManifest != "" {
		root, rootErr := repositoryRoot()
		if rootErr != nil {
			err = rootErr
		} else {
			err = writeTrustedSourceManifest(ctx, root, expectedCommit, writeSourceManifest)
		}
	} else if artifacts != "" {
		err = verifyArtifacts(ctx, artifacts, expectedVersion, expectedCommit, trustedBinaries, trustedSourceManifest)
	} else if binary != "" {
		root, rootErr := repositoryRoot()
		if rootErr != nil {
			err = rootErr
		} else {
			nativeTarget := target{goos: runtime.GOOS, goarch: runtime.GOARCH}
			if err = verifyStructure(binary, nativeTarget); err == nil {
				err = verifyNativeCommands(ctx, root, binary, expectedVersion, expectedCommit, "")
			}
		}
	} else {
		err = buildAndVerify(ctx, all)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "release smoke:", err)
		os.Exit(1)
	}
}

type bundlePolicy struct {
	allowed  map[string]struct{}
	required []string
}

func extractBundle(bundlePath, destination, kind string) (err error) {
	policies := map[string]bundlePolicy{
		"trusted": {
			allowed:  map[string]struct{}{"unifi-trusted": {}, "unifi-source-manifest.json": {}},
			required: []string{"unifi-trusted", "unifi-source-manifest.json"},
		},
		"generated": {
			allowed:  map[string]struct{}{"dist": {}},
			required: []string{"dist"},
		},
		"verified": {
			allowed:  map[string]struct{}{"dist": {}, ".release-verification": {}},
			required: []string{"dist", ".release-verification"},
		},
		"publication": {
			allowed:  map[string]struct{}{"dist": {}},
			required: []string{"dist"},
		},
	}
	policy, ok := policies[kind]
	if !ok {
		return fmt.Errorf("unknown bundle kind %q", kind)
	}
	info, err := os.Lstat(bundlePath)
	if err != nil {
		return fmt.Errorf("stat %s bundle: %w", kind, err)
	}
	if !info.Mode().IsRegular() || info.Size() > maxBundleBytes {
		return fmt.Errorf("%s bundle is not a bounded regular file", kind)
	}
	if _, err := os.Lstat(destination); err == nil {
		return fmt.Errorf("bundle destination already exists")
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("inspect bundle destination: %w", err)
	}
	if err := os.Mkdir(destination, 0o700); err != nil {
		return fmt.Errorf("create bundle destination: %w", err)
	}
	destinationRoot, err := os.OpenRoot(destination)
	if err != nil {
		return fmt.Errorf("open bundle destination root: %w", err)
	}
	complete := false
	defer func() {
		_ = destinationRoot.Close()
		if !complete {
			_ = os.RemoveAll(destination)
		}
	}()

	f, err := os.Open(bundlePath)
	if err != nil {
		return fmt.Errorf("open %s bundle: %w", kind, err)
	}
	defer f.Close()
	reader := tar.NewReader(io.LimitReader(f, maxBundleBytes+1))
	seen := make(map[string]struct{})
	seenRoots := make(map[string]struct{})
	var total int64
	for entries := 0; ; entries++ {
		if entries >= maxBundleEntries {
			return fmt.Errorf("%s bundle exceeds %d entries", kind, maxBundleEntries)
		}
		header, nextErr := reader.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return fmt.Errorf("read %s bundle: %w", kind, nextErr)
		}
		name, pathErr := validateBundlePath(header.Name)
		if pathErr != nil {
			return pathErr
		}
		if _, duplicate := seen[name]; duplicate {
			return fmt.Errorf("duplicate bundle entry %q", name)
		}
		seen[name] = struct{}{}
		root, _, _ := strings.Cut(name, "/")
		if _, allowed := policy.allowed[root]; !allowed {
			return fmt.Errorf("entry %q is outside the %s bundle namespace", name, kind)
		}
		seenRoots[root] = struct{}{}
		if header.Size < 0 || header.Size > maxBundleEntryBytes || total > maxBundleBytes-header.Size {
			return fmt.Errorf("%s bundle entry %q exceeds size limits", kind, name)
		}
		total += header.Size
		if header.Mode&0o7000 != 0 {
			return fmt.Errorf("%s bundle entry %q has unsafe mode", kind, name)
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if header.Size != 0 {
				return fmt.Errorf("directory bundle entry %q has data", name)
			}
			if err := destinationRoot.MkdirAll(name, fs.FileMode(header.Mode)&0o777); err != nil {
				return fmt.Errorf("create bundle directory %q: %w", name, err)
			}
		case tar.TypeReg, tar.TypeRegA:
			out, openErr := createBundleFile(destinationRoot, name, fs.FileMode(header.Mode)&0o777)
			if openErr != nil {
				return fmt.Errorf("create bundle file %q: %w", name, openErr)
			}
			_, copyErr := io.CopyN(out, reader, header.Size)
			closeErr := out.Close()
			if copyErr != nil {
				return fmt.Errorf("extract bundle file %q: %w", name, copyErr)
			}
			if closeErr != nil {
				return fmt.Errorf("close bundle file %q: %w", name, closeErr)
			}
		default:
			return fmt.Errorf("unsupported bundle entry type %d for %q", header.Typeflag, name)
		}
	}
	for _, required := range policy.required {
		if _, ok := seenRoots[required]; !ok {
			return fmt.Errorf("%s bundle is missing required namespace %q", kind, required)
		}
	}
	complete = true
	return nil
}

func createBundleFile(root *os.Root, name string, mode fs.FileMode) (*os.File, error) {
	parent := archivepath.Dir(name)
	if parent != "." {
		if err := root.MkdirAll(parent, 0o755); err != nil {
			return nil, err
		}
	}
	return root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
}

func validateBundlePath(raw string) (string, error) {
	name := strings.TrimSuffix(raw, "/")
	if name == "" || name == "." || archivepath.IsAbs(name) || filepath.IsAbs(name) || !filepath.IsLocal(filepath.FromSlash(name)) || strings.ContainsAny(name, "\\:") || archivepath.Clean(name) != name || name == ".." || strings.HasPrefix(name, "../") {
		return "", fmt.Errorf("unsafe bundle path %q", raw)
	}
	return name, nil
}

func describeContract(w io.Writer) {
	for _, target := range targets {
		fmt.Fprintln(w, target)
	}
	fmt.Fprintln(w, "native commands: --version | version --json | --help | --config configs/config.example.yaml --json config show")
	fmt.Fprintln(w, "all-target policy: every archived executable must equal its independently cross-built trusted binary; only the native trusted binary is executed")
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

func verifyArtifacts(ctx context.Context, dist, expectedVersion, expectedCommit, trustedBinariesDir, trustedSourceManifestPath string) error {
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
	trustedBinaries, err := canonicalTrustedBinaries(dist, trustedBinariesDir)
	if err != nil {
		return err
	}
	manifest, err := loadTrustedSourceManifest(dist, trustedSourceManifestPath, expectedCommit)
	if err != nil {
		return err
	}
	nativeTarget := target{goos: runtime.GOOS, goarch: runtime.GOARCH}
	trustedNative, ok := trustedBinaries[nativeTarget]
	if !ok {
		return fmt.Errorf("release verifier host %s has no trusted binary target", nativeTarget)
	}
	if err := verifyStructure(trustedNative, nativeTarget); err != nil {
		return fmt.Errorf("trusted native binary: %w", err)
	}
	if err := verifyNativeCommands(ctx, root, trustedNative, expectedVersion, expectedCommit, ""); err != nil {
		return fmt.Errorf("trusted native binary: %w", err)
	}
	data, err := readReleaseFile(filepath.Join(dist, "artifacts.json"), maxReleaseManifestBytes)
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
	if err := inspectSourceArchive(ctx, sourcePath, fmt.Sprintf("unifi-cli_%s", expectedVersion), expectedCommit, manifest); err != nil {
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
	if err := verifyChecksum(ctx, sourcePath, checksums[sources[0].Name]); err != nil {
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
		if err := verifyChecksum(ctx, archivePath, checksums[current.Name]); err != nil {
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
		if err := verifyChecksum(ctx, sbomPath, checksums[sbom.Name]); err != nil {
			return fmt.Errorf("%s SBOM: %w", target, err)
		}
		expectedRoot := strings.TrimSuffix(strings.TrimSuffix(wantName, ".tar.gz"), ".zip")
		executable, cleanup, err := inspectReleaseArchive(ctx, archivePath, target, expectedRoot, manifest)
		if err != nil {
			return fmt.Errorf("%s: %w", target, err)
		}
		if err := verifyStructure(executable.extractedPath, target); err != nil {
			cleanup()
			return err
		}
		if err := verifyTrustedArtifact(ctx, trustedBinaries[target], executable, target); err != nil {
			cleanup()
			return err
		}
		inventory, err := trustedSBOMInventory(trustedBinaries[target], target)
		if err != nil {
			cleanup()
			return fmt.Errorf("%s trusted SBOM inventory: %w", target, err)
		}
		if err := inspectCycloneDXSBOM(sbomPath, wantName, expectedVersion, executable, inventory); err != nil {
			cleanup()
			return fmt.Errorf("%s SBOM: %w", target, err)
		}
		fmt.Printf("%s archive: checksum, SBOM, structure, and trusted-binary equality verified\n", target)
		cleanup()
	}
	return nil
}

func canonicalTrustedBinaries(dist, path string) (map[target]string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("resolve trusted binaries directory: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return nil, fmt.Errorf("stat trusted binaries directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("trusted binaries path is not a directory")
	}
	rel, err := filepath.Rel(dist, resolved)
	if err != nil {
		return nil, fmt.Errorf("compare trusted binaries and artifact directory: %w", err)
	}
	if rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
		return nil, fmt.Errorf("trusted binaries must be outside the untrusted artifact directory")
	}
	result := make(map[target]string, len(targets))
	for _, target := range targets {
		candidate := filepath.Join(resolved, target.goos+"_"+target.goarch, target.executableName())
		trustedPath, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			return nil, fmt.Errorf("resolve trusted binary for %s: %w", target, err)
		}
		entryInfo, err := os.Stat(trustedPath)
		if err != nil || !entryInfo.Mode().IsRegular() {
			return nil, fmt.Errorf("trusted binary for %s is not a regular file", target)
		}
		inside, err := filepath.Rel(resolved, trustedPath)
		if err != nil || inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) || filepath.IsAbs(inside) {
			return nil, fmt.Errorf("trusted binary for %s resolves outside trusted directory", target)
		}
		result[target] = trustedPath
	}
	return result, nil
}

func validateExpectedMetadata(version, commit string) error {
	if !releaseVersionPattern.MatchString(version) || strings.HasPrefix(version, "v") || strings.ContainsAny(version, "/\\") || archivepath.Clean(version) != version {
		return fmt.Errorf("invalid expected version %q", version)
	}
	return validateExpectedCommit(commit)
}

func validateExpectedCommit(commit string) error {
	if len(commit) != 40 {
		return fmt.Errorf("invalid expected commit %q", commit)
	}
	if _, err := hex.DecodeString(commit); err != nil {
		return fmt.Errorf("invalid expected commit %q", commit)
	}
	return nil
}

func writeTrustedSourceManifest(ctx context.Context, root, commit, outputPath string) error {
	if err := validateExpectedCommit(commit); err != nil {
		return err
	}
	manifest, err := buildTrustedSourceManifest(ctx, root, commit)
	if err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode trusted source manifest: %w", err)
	}
	if len(encoded) > maxReleaseManifestBytes {
		return fmt.Errorf("trusted source manifest exceeds %d bytes", maxReleaseManifestBytes)
	}
	abs, err := filepath.Abs(outputPath)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(abs, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create trusted source manifest: %w", err)
	}
	wrote := false
	defer func() {
		file.Close()
		if !wrote {
			_ = os.Remove(abs)
		}
	}()
	if _, err := file.Write(append(encoded, '\n')); err != nil {
		return fmt.Errorf("write trusted source manifest: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync trusted source manifest: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close trusted source manifest: %w", err)
	}
	wrote = true
	return nil
}

func buildTrustedSourceManifest(ctx context.Context, root, commit string) (sourceManifest, error) {
	if err := validateExpectedCommit(commit); err != nil {
		return sourceManifest{}, err
	}
	resolved, err := gitOutput(ctx, root, maxReleaseManifestBytes, "rev-parse", "--verify", commit+"^{commit}")
	if err != nil {
		return sourceManifest{}, fmt.Errorf("resolve source commit: %w", err)
	}
	if strings.TrimSpace(string(resolved)) != commit {
		return sourceManifest{}, fmt.Errorf("resolved source commit does not match expected commit")
	}
	tree, err := gitOutput(ctx, root, maxReleaseManifestBytes, "ls-tree", "-r", "-z", "--full-tree", commit)
	if err != nil {
		return sourceManifest{}, fmt.Errorf("list source commit: %w", err)
	}
	manifest := sourceManifest{SchemaVersion: "1", Commit: commit}
	seen := make(map[string]struct{})
	var aggregate int64
	for _, raw := range bytes.Split(tree, []byte{0}) {
		if len(raw) == 0 {
			continue
		}
		metadata, pathBytes, ok := bytes.Cut(raw, []byte{'\t'})
		fields := strings.Fields(string(metadata))
		if !ok || len(fields) != 3 || fields[1] != "blob" {
			return sourceManifest{}, fmt.Errorf("malformed source tree entry")
		}
		mode, objectID, path := fields[0], fields[2], string(pathBytes)
		if mode != "100644" && mode != "100755" {
			return sourceManifest{}, fmt.Errorf("unsupported Git mode %q for %q", mode, path)
		}
		if err := validateManifestPath(path); err != nil {
			return sourceManifest{}, err
		}
		if _, exists := seen[path]; exists {
			return sourceManifest{}, fmt.Errorf("duplicate source manifest path %q", path)
		}
		seen[path] = struct{}{}
		if len(manifest.Files) >= maxReleaseSourceEntries {
			return sourceManifest{}, fmt.Errorf("source commit exceeds maximum of %d files", maxReleaseSourceEntries)
		}
		digest, size, err := hashGitBlob(ctx, root, objectID)
		if err != nil {
			return sourceManifest{}, fmt.Errorf("hash source file %q: %w", path, err)
		}
		if aggregate > maxReleaseExpandedBytes-size {
			return sourceManifest{}, fmt.Errorf("source commit exceeds expanded byte limit")
		}
		aggregate += size
		manifest.Files = append(manifest.Files, sourceManifestEntry{Path: path, Mode: mode, SHA256: digest})
	}
	if len(manifest.Files) == 0 {
		return sourceManifest{}, fmt.Errorf("source commit has no files")
	}
	slices.SortFunc(manifest.Files, func(a, b sourceManifestEntry) int { return strings.Compare(a.Path, b.Path) })
	return manifest, nil
}

func gitOutput(ctx context.Context, root string, limit int64, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(io.LimitReader(stdout, limit+1))
	if readErr != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, readErr
	}
	if int64(len(data)) > limit {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, fmt.Errorf("Git output exceeds %d bytes", limit)
	}
	waitErr := cmd.Wait()
	if waitErr != nil {
		return nil, waitErr
	}
	return data, nil
}

func hashGitBlob(ctx context.Context, root, objectID string) (string, int64, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", root, "cat-file", "blob", objectID)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", 0, err
	}
	if err := cmd.Start(); err != nil {
		return "", 0, err
	}
	hash := sha256.New()
	size, readErr := io.Copy(hash, io.LimitReader(contextReader{ctx: ctx, reader: stdout}, maxReleaseEntryBytes+1))
	if readErr != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return "", 0, readErr
	}
	if size > maxReleaseEntryBytes {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return "", 0, fmt.Errorf("Git blob exceeds %d bytes", maxReleaseEntryBytes)
	}
	waitErr := cmd.Wait()
	if waitErr != nil {
		return "", 0, waitErr
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}

func loadTrustedSourceManifest(dist, path, expectedCommit string) (sourceManifest, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return sourceManifest{}, err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return sourceManifest{}, fmt.Errorf("resolve trusted source manifest: %w", err)
	}
	rel, err := filepath.Rel(dist, resolved)
	if err != nil || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)) {
		return sourceManifest{}, fmt.Errorf("trusted source manifest must be outside the untrusted artifact directory")
	}
	data, err := fileutil.ReadRegularFile(resolved, maxReleaseManifestBytes)
	if err != nil {
		return sourceManifest{}, fmt.Errorf("read trusted source manifest: %w", err)
	}
	var manifest sourceManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return sourceManifest{}, fmt.Errorf("decode trusted source manifest: %w", err)
	}
	if manifest.SchemaVersion != "1" || manifest.Commit != expectedCommit || len(manifest.Files) == 0 || len(manifest.Files) > maxReleaseSourceEntries {
		return sourceManifest{}, fmt.Errorf("trusted source manifest metadata does not match release")
	}
	seen := make(map[string]struct{}, len(manifest.Files))
	for _, entry := range manifest.Files {
		if err := validateManifestPath(entry.Path); err != nil {
			return sourceManifest{}, err
		}
		if entry.Mode != "100644" && entry.Mode != "100755" {
			return sourceManifest{}, fmt.Errorf("trusted source manifest has unsupported mode %q", entry.Mode)
		}
		if len(entry.SHA256) != sha256.Size*2 {
			return sourceManifest{}, fmt.Errorf("trusted source manifest has invalid digest for %q", entry.Path)
		}
		if _, err := hex.DecodeString(entry.SHA256); err != nil {
			return sourceManifest{}, fmt.Errorf("trusted source manifest has invalid digest for %q", entry.Path)
		}
		if _, exists := seen[entry.Path]; exists {
			return sourceManifest{}, fmt.Errorf("trusted source manifest has duplicate path %q", entry.Path)
		}
		seen[entry.Path] = struct{}{}
	}
	return manifest, nil
}

func validateManifestPath(path string) error {
	if path == "" || filepath.IsAbs(path) || archivepath.IsAbs(path) || strings.Contains(path, "\\") || archivepath.Clean(path) != path || path == "." || path == ".." || strings.HasPrefix(path, "../") {
		return fmt.Errorf("unsafe source manifest path %q", path)
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
	data, err := readReleaseFile(path, maxReleaseManifestBytes)
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

func readReleaseFile(path string, maxBytes int64) ([]byte, error) {
	return fileutil.ReadRegularFile(path, maxBytes)
}

func verifyChecksum(ctx context.Context, path, want string) error {
	if want == "" {
		return fmt.Errorf("checksum entry missing for %s", filepath.Base(path))
	}
	got, err := sha256File(ctx, path, maxReleaseArchiveBytes)
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("SHA-256 for %s = %s, want %s", filepath.Base(path), got, want)
	}
	return nil
}

type archiveExecutable struct {
	extractedPath string
	relativePath  string
	sha1          string
	sha256        string
}

type sbomComponentIdentity struct {
	Type    string
	Name    string
	Version string
}

func trustedSBOMInventory(path string, target target) (map[sbomComponentIdentity]struct{}, error) {
	info, err := buildinfo.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read trusted binary build info: %w", err)
	}
	inventory := make(map[sbomComponentIdentity]struct{}, len(info.Deps)+2)
	add := func(name, version string) error {
		if name == "" || version == "" || version == "(devel)" {
			return fmt.Errorf("trusted binary has unversioned module %q", name)
		}
		identity := sbomComponentIdentity{Type: "library", Name: name, Version: version}
		if _, duplicate := inventory[identity]; duplicate {
			return fmt.Errorf("trusted binary has duplicate module %s@%s", name, version)
		}
		inventory[identity] = struct{}{}
		return nil
	}
	main := info.Main
	if main.Replace != nil {
		main = *main.Replace
	}
	if err := add(main.Path, main.Version); err != nil {
		return nil, err
	}
	for _, dependency := range info.Deps {
		module := *dependency
		if module.Replace != nil {
			module = *module.Replace
		}
		if err := add(module.Path, module.Version); err != nil {
			return nil, err
		}
	}
	if err := add("stdlib", info.GoVersion); err != nil {
		return nil, err
	}
	if target.goos == "windows" {
		inventory[sbomComponentIdentity{
			Type: "application", Name: strings.TrimSuffix(target.executableName(), ".exe"), Version: "UNKNOWN",
		}] = struct{}{}
	}
	return inventory, nil
}

func verifyTrustedArtifact(ctx context.Context, trustedPath string, artifact archiveExecutable, target target) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	trustedSHA256, err := sha256File(ctx, trustedPath, maxReleaseEntryBytes)
	if err != nil {
		return fmt.Errorf("hash trusted binary for %s: %w", target, err)
	}
	if !strings.EqualFold(artifact.sha256, trustedSHA256) {
		return fmt.Errorf("%s artifact SHA-256 does not match trusted binary", target)
	}
	return nil
}

func inspectReleaseArchive(ctx context.Context, archivePath string, target target, expectedRoot string, manifest sourceManifest) (archiveExecutable, func(), error) {
	dir, err := os.MkdirTemp("", "unifi-release-artifact-")
	if err != nil {
		return archiveExecutable{}, func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	destination := filepath.Join(dir, target.executableName())
	supportFiles, err := trustedArchiveSupportFiles(manifest)
	if err != nil {
		cleanup()
		return archiveExecutable{}, func() {}, err
	}
	if strings.HasSuffix(archivePath, ".zip") {
		err = inspectZipArchiveTrusted(ctx, archivePath, target, expectedRoot, destination, supportFiles)
	} else {
		err = inspectTarArchiveTrusted(ctx, archivePath, target, expectedRoot, destination, supportFiles)
	}
	if err != nil {
		cleanup()
		return archiveExecutable{}, func() {}, err
	}
	digest, err := sha256File(ctx, destination, maxReleaseEntryBytes)
	if err != nil {
		cleanup()
		return archiveExecutable{}, func() {}, err
	}
	digestSHA1, err := sha1File(ctx, destination, maxReleaseEntryBytes)
	if err != nil {
		cleanup()
		return archiveExecutable{}, func() {}, err
	}
	return archiveExecutable{
		extractedPath: destination,
		relativePath:  expectedRoot + "/" + target.executableName(),
		sha1:          digestSHA1,
		sha256:        digest,
	}, cleanup, nil
}

func inspectZipArchive(ctx context.Context, archivePath string, target target, expectedRoot, destination string) error {
	return inspectZipArchiveTrusted(ctx, archivePath, target, expectedRoot, destination, nil)
}

func inspectZipArchiveTrusted(ctx context.Context, archivePath string, target target, expectedRoot, destination string, supportFiles map[string]string) error {
	file, size, err := openReleaseFile(archivePath, maxReleaseArchiveBytes)
	if err != nil {
		return err
	}
	defer file.Close()
	archive, err := zip.NewReader(file, size)
	if err != nil {
		return err
	}
	if len(archive.File) > maxReleaseArchiveEntries {
		return fmt.Errorf("archive entry count %d exceeds maximum of %d", len(archive.File), maxReleaseArchiveEntries)
	}
	want := expectedArchiveEntries(expectedRoot, target)
	seenPaths := make(map[string]struct{})
	seenNames := make(map[string]struct{})
	var expanded int64
	for _, entry := range archive.File {
		if err := contextErr(ctx); err != nil {
			return err
		}
		if entry.UncompressedSize64 > uint64(maxReleaseEntryBytes) || entry.UncompressedSize64 > uint64(maxReleaseExpandedBytes-expanded) {
			return fmt.Errorf("archive entry %q exceeds expanded byte limit", entry.Name)
		}
		expanded += int64(entry.UncompressedSize64)
		clean, isDir, err := validateArchiveEntry(entry.Name, expectedRoot, entry.FileInfo().IsDir())
		if err != nil {
			return err
		}
		if !isDir && !entry.Mode().IsRegular() {
			return fmt.Errorf("archive entry %q has unsupported mode %s", entry.Name, entry.Mode())
		}
		if entry.Mode()&(fs.ModeSetuid|fs.ModeSetgid|fs.ModeSticky) != 0 {
			return fmt.Errorf("archive entry %q has forbidden special mode bits", entry.Name)
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
		mode := entry.Mode().Perm()
		wantMode := expectedArchiveMode(clean, expectedRoot, target)
		if mode != wantMode {
			return fmt.Errorf("archive entry %q mode = %04o, want %04o", clean, mode, wantMode)
		}
		reader, err := entry.Open()
		if err != nil {
			return err
		}
		if clean == expectedRoot+"/"+target.executableName() {
			err = writeExtracted(destination, contextReader{ctx: ctx, reader: reader}, mode, maxReleaseEntryBytes)
		} else {
			err = verifyArchiveSupportReader(ctx, reader, clean, expectedRoot, supportFiles)
		}
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

func inspectTarArchive(ctx context.Context, archivePath string, target target, expectedRoot, destination string) error {
	return inspectTarArchiveTrusted(ctx, archivePath, target, expectedRoot, destination, nil)
}

func inspectTarArchiveTrusted(ctx context.Context, archivePath string, target target, expectedRoot, destination string, supportFiles map[string]string) error {
	file, _, err := openReleaseFile(archivePath, maxReleaseArchiveBytes)
	if err != nil {
		return err
	}
	defer file.Close()
	gzipReader, err := gzip.NewReader(contextReader{ctx: ctx, reader: file})
	if err != nil {
		return err
	}
	defer gzipReader.Close()
	reader := tar.NewReader(gzipReader)
	want := expectedArchiveEntries(expectedRoot, target)
	seenPaths := make(map[string]struct{})
	seenNames := make(map[string]struct{})
	entries := 0
	var expanded int64
	for {
		if err := contextErr(ctx); err != nil {
			return err
		}
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		entries++
		if entries > maxReleaseArchiveEntries {
			return fmt.Errorf("archive entry count exceeds maximum of %d", maxReleaseArchiveEntries)
		}
		if header.Size < 0 || header.Size > maxReleaseEntryBytes || header.Size > maxReleaseExpandedBytes-expanded {
			return fmt.Errorf("archive entry %q exceeds expanded byte limit", header.Name)
		}
		expanded += header.Size
		isDir := header.Typeflag == tar.TypeDir
		if header.Typeflag != tar.TypeReg && !isDir {
			return fmt.Errorf("archive entry %q has unsupported type %d", header.Name, header.Typeflag)
		}
		if header.Mode&0o7000 != 0 {
			return fmt.Errorf("archive entry %q has forbidden special mode bits", header.Name)
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
			if err := writeExtracted(destination, contextReader{ctx: ctx, reader: reader}, mode, maxReleaseEntryBytes); err != nil {
				return err
			}
		} else if err := verifyArchiveSupportReader(ctx, io.LimitReader(reader, header.Size), clean, expectedRoot, supportFiles); err != nil {
			return err
		}
	}
	return requireArchiveEntries(seenPaths, want, destination, expectedRoot)
}

func trustedArchiveSupportFiles(manifest sourceManifest) (map[string]string, error) {
	want := map[string]string{"LICENSE": "", "README.md": "", "CHANGELOG.md": ""}
	for _, entry := range manifest.Files {
		if _, ok := want[entry.Path]; !ok {
			continue
		}
		if entry.Mode != "100644" {
			return nil, fmt.Errorf("trusted support file %q has Git mode %s, want 100644", entry.Path, entry.Mode)
		}
		want[entry.Path] = entry.SHA256
	}
	for path, digest := range want {
		if digest == "" {
			return nil, fmt.Errorf("trusted source manifest is missing archive support file %q", path)
		}
	}
	return want, nil
}

func verifyArchiveSupportReader(ctx context.Context, reader io.Reader, clean, expectedRoot string, supportFiles map[string]string) error {
	relative := strings.TrimPrefix(clean, expectedRoot+"/")
	want, ok := supportFiles[relative]
	if !ok {
		return fmt.Errorf("archive support file %q has no trusted source digest", relative)
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, contextReader{ctx: ctx, reader: reader}); err != nil {
		return fmt.Errorf("read archive support file %q: %w", relative, err)
	}
	if got := hex.EncodeToString(hash.Sum(nil)); !strings.EqualFold(got, want) {
		return fmt.Errorf("archive support file %q SHA-256 does not match trusted source manifest", relative)
	}
	return nil
}

func writeExtracted(path string, reader io.Reader, mode fs.FileMode, maxBytes int64) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	remove := true
	defer func() {
		_ = file.Close()
		if remove {
			_ = os.Remove(path)
		}
	}()
	written, err := io.Copy(file, io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return err
	}
	if written > maxBytes {
		return fmt.Errorf("archive entry exceeds %d bytes", maxBytes)
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Chmod(path, mode); err != nil {
		return err
	}
	remove = false
	return nil
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

func inspectSourceArchive(ctx context.Context, archivePath, expectedRoot, expectedCommit string, manifest sourceManifest) error {
	file, _, err := openReleaseFile(archivePath, maxReleaseArchiveBytes)
	if err != nil {
		return err
	}
	defer file.Close()
	gzipReader, err := gzip.NewReader(contextReader{ctx: ctx, reader: file})
	if err != nil {
		return fmt.Errorf("open gzip: %w", err)
	}
	defer gzipReader.Close()
	reader := tar.NewReader(gzipReader)
	seen := make(map[string]struct{})
	want := make(map[string]sourceManifestEntry, len(manifest.Files))
	for _, entry := range manifest.Files {
		want[entry.Path] = entry
	}
	core := map[string]bool{
		expectedRoot + "/LICENSE":      false,
		expectedRoot + "/README.md":    false,
		expectedRoot + "/CHANGELOG.md": false,
		expectedRoot + "/go.mod":       false,
	}
	regularFiles := 0
	globalHeaders := 0
	entries := 0
	var expanded int64
	for {
		if err := contextErr(ctx); err != nil {
			return err
		}
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read tar: %w", err)
		}
		entries++
		if entries > maxReleaseSourceEntries {
			return fmt.Errorf("source archive entry count exceeds maximum of %d", maxReleaseSourceEntries)
		}
		if header.Size < 0 || header.Size > maxReleaseEntryBytes || header.Size > maxReleaseExpandedBytes-expanded {
			return fmt.Errorf("source archive entry %q exceeds expanded byte limit", header.Name)
		}
		expanded += header.Size
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
		if header.Mode&0o7000 != 0 {
			return fmt.Errorf("source entry %q has forbidden special mode bits", header.Name)
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
		relative := strings.TrimPrefix(clean, expectedRoot+"/")
		expected, ok := want[relative]
		if !ok {
			return fmt.Errorf("source archive contains untracked file %q", relative)
		}
		if gotMode, wantMode := fs.FileMode(header.Mode).Perm(), sourceArchiveMode(expected.Mode); gotMode != wantMode {
			return fmt.Errorf("source entry %q mode = %04o, want %04o", clean, gotMode, wantMode)
		}
		if _, ok := core[clean]; ok {
			if header.Size == 0 {
				return fmt.Errorf("source core file %q is empty", clean)
			}
			core[clean] = true
		}
		hash := sha256.New()
		if _, err := io.CopyN(hash, contextReader{ctx: ctx, reader: reader}, header.Size); err != nil {
			return fmt.Errorf("read source entry %q: %w", header.Name, err)
		}
		if got := hex.EncodeToString(hash.Sum(nil)); !strings.EqualFold(got, expected.SHA256) {
			return fmt.Errorf("source entry %q SHA-256 does not match trusted commit manifest", clean)
		}
		delete(want, relative)
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
	if len(want) != 0 {
		missing := make([]string, 0, len(want))
		for path := range want {
			missing = append(missing, path)
		}
		slices.Sort(missing)
		return fmt.Errorf("source archive is missing tracked file %q", missing[0])
	}
	return nil
}

func sourceArchiveMode(gitMode string) fs.FileMode {
	if gitMode == "100755" {
		return 0o775
	}
	return 0o664
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := contextErr(r.ctx); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func contextErr(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func openReleaseFile(path string, maxBytes int64) (*os.File, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, 0, err
	}
	if !info.Mode().IsRegular() {
		file.Close()
		return nil, 0, fmt.Errorf("%s is not a regular file", path)
	}
	if info.Size() > maxBytes {
		file.Close()
		return nil, 0, fmt.Errorf("%s exceeds %d bytes", path, maxBytes)
	}
	return file, info.Size(), nil
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

func inspectCycloneDXSBOM(sbomPath, archiveName, expectedVersion string, executable archiveExecutable, expectedInventory map[sbomComponentIdentity]struct{}) error {
	data, err := readReleaseFile(sbomPath, maxReleaseSBOMBytes)
	if err != nil {
		return err
	}
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
	decoder := json.NewDecoder(bytes.NewReader(data))
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
	fileComponents := 0
	observedInventory := make(map[sbomComponentIdentity]struct{}, len(expectedInventory))
	for i, component := range bom.Components {
		if component.Name == "" {
			return fmt.Errorf("SBOM component %d has empty name", i)
		}
		switch component.Type {
		case "library", "application":
			if component.Version == "" {
				return fmt.Errorf("SBOM %s component %d has empty version", component.Type, i)
			}
			identity := sbomComponentIdentity{Type: component.Type, Name: component.Name, Version: component.Version}
			if _, duplicate := observedInventory[identity]; duplicate {
				return fmt.Errorf("SBOM has duplicate component %s@%s", component.Name, component.Version)
			}
			observedInventory[identity] = struct{}{}
		case "file":
			fileComponents++
			if !matchesSyftExecutablePath(component.Name, executable.relativePath) {
				return fmt.Errorf("SBOM contains unrelated file component %q", component.Name)
			}
			if len(component.Hashes) == 0 {
				return fmt.Errorf("SBOM file component %d has no hashes", i)
			}
			sha1Hashes := 0
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
					if !strings.EqualFold(hash.Content, executable.sha256) {
						return fmt.Errorf("SBOM executable SHA-256 = %s, want %s", hash.Content, executable.sha256)
					}
				} else if hash.Algorithm == "SHA-1" {
					sha1Hashes++
					if executable.sha1 == "" || !strings.EqualFold(hash.Content, executable.sha1) {
						return fmt.Errorf("SBOM executable SHA-1 does not match trusted bytes")
					}
				}
			}
			if sha256Hashes != 1 || sha1Hashes > 1 {
				return fmt.Errorf("SBOM executable component has %d SHA-256 and %d SHA-1 hashes, want exactly one SHA-256 and at most one SHA-1", sha256Hashes, sha1Hashes)
			}
		default:
			return fmt.Errorf("SBOM component %d has unsupported type %q", i, component.Type)
		}
	}
	if fileComponents != 1 {
		return fmt.Errorf("SBOM file component count = %d, want exactly 1", fileComponents)
	}
	if len(observedInventory) != len(expectedInventory) {
		return fmt.Errorf("SBOM dependency component count = %d, want %d from trusted binary", len(observedInventory), len(expectedInventory))
	}
	for identity := range expectedInventory {
		if _, ok := observedInventory[identity]; !ok {
			return fmt.Errorf("SBOM is missing trusted component %s@%s", identity.Name, identity.Version)
		}
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

func sha256File(ctx context.Context, path string, maxBytes int64) (string, error) {
	file, _, err := openReleaseFile(path, maxBytes)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, contextReader{ctx: ctx, reader: file}); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func sha1File(ctx context.Context, path string, maxBytes int64) (string, error) {
	file, _, err := openReleaseFile(path, maxBytes)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha1.New()
	if _, err := io.Copy(hash, contextReader{ctx: ctx, reader: file}); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func inspectGoReleaserMetadata(path, expectedVersion, expectedCommit string) error {
	data, err := readReleaseFile(path, maxReleaseManifestBytes)
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
