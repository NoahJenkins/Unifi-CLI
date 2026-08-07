package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestVerifyArtifactsAcceptsExactHardenedRelease(t *testing.T) {
	fixture := newReleaseFixture(t)
	verifyReleaseFixture(t, fixture)
}

func TestVerifyArtifactsRejectsPathsOutsideDist(t *testing.T) {
	fixture := newReleaseFixture(t)
	archive := fixture.archiveArtifact(targets[0])
	absolute := filepath.Join(fixture.dist, archive.Name)
	for _, badPath := range []string{
		absolute,
		filepath.Join("..", filepath.Base(fixture.dist), archive.Name),
		"dist/../" + archive.Name,
	} {
		t.Run(strings.NewReplacer("/", "_", "\\", "_").Replace(badPath), func(t *testing.T) {
			clone := fixture.clone(t)
			clone.setArtifactPath(archive.Name, badPath)
			clone.writeMetadata(t)
			verifyReleaseFixtureFails(t, clone)
		})
	}

	t.Run("symlink outside dist", func(t *testing.T) {
		clone := fixture.clone(t)
		outside := filepath.Join(t.TempDir(), archive.Name)
		data, err := os.ReadFile(filepath.Join(clone.dist, archive.Name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(outside, data, 0o644); err != nil {
			t.Fatal(err)
		}
		linkName := "linked-" + archive.Name
		if err := os.Symlink(outside, filepath.Join(clone.dist, linkName)); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		clone.setArtifactPath(archive.Name, linkName)
		clone.writeMetadata(t)
		verifyReleaseFixtureFails(t, clone)
	})
}

func TestVerifyArtifactsRejectsWrongArchiveFilename(t *testing.T) {
	fixture := newReleaseFixture(t)
	clone := fixture.clone(t)
	clone.renameArchive(t, targets[0], "different_"+smokeVersion+"_darwin_amd64.tar.gz")
	clone.writeMetadata(t)
	verifyReleaseFixtureFails(t, clone)
}

func TestVerifyArtifactsRejectsWrongExpectedVersion(t *testing.T) {
	fixture := newReleaseFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := verifyArtifacts(ctx, fixture.dist, "9.9.9", smokeCommit); err == nil {
		t.Fatal("artifact verification accepted a release built for a different version")
	}
}

func TestVerifyArtifactsRejectsInvalidSourceArchive(t *testing.T) {
	fixture := newReleaseFixture(t)
	tests := map[string][]archiveEntry{
		"empty": nil,
		"absolute": {
			{name: "/" + fixture.sourceRoot() + "/LICENSE", mode: 0o644, body: []byte("license")},
		},
		"traversal": {
			{name: fixture.sourceRoot() + "/LICENSE", mode: 0o644, body: []byte("license")},
			{name: fixture.sourceRoot() + "/../escape", mode: 0o644, body: []byte("escape")},
		},
		"duplicate": {
			{name: fixture.sourceRoot() + "/LICENSE", mode: 0o644, body: []byte("license")},
			{name: fixture.sourceRoot() + "/LICENSE", mode: 0o644, body: []byte("duplicate")},
		},
		"missing core file": {
			{name: fixture.sourceRoot() + "/LICENSE", mode: 0o644, body: []byte("license")},
		},
	}
	for name, entries := range tests {
		t.Run(name, func(t *testing.T) {
			clone := fixture.clone(t)
			clone.replaceSource(t, entries)
			clone.writeMetadata(t)
			verifyReleaseFixtureFails(t, clone)
		})
	}

	t.Run("corrupt gzip", func(t *testing.T) {
		clone := fixture.clone(t)
		path := filepath.Join(clone.dist, clone.sourceName())
		if err := os.WriteFile(path, []byte("not a gzip stream"), 0o644); err != nil {
			t.Fatal(err)
		}
		clone.checksums[clone.sourceName()] = fileSHA256(t, path)
		clone.writeMetadata(t)
		verifyReleaseFixtureFails(t, clone)
	})
}

func TestVerifyArtifactsRejectsInvalidArchiveLayout(t *testing.T) {
	fixture := newReleaseFixture(t)
	target := nonNativeTarget()
	root := fixture.archiveRoot(target)
	binary := fixture.binaries[target]
	tests := map[string][]archiveEntry{
		"absolute": {
			{name: "/" + root + "/" + target.executableName(), mode: 0o755, body: binary},
			{name: root + "/LICENSE", mode: 0o644, body: []byte("license")},
			{name: root + "/README.md", mode: 0o644, body: []byte("readme")},
			{name: root + "/CHANGELOG.md", mode: 0o644, body: []byte("changelog")},
		},
		"traversal": {
			{name: root + "/../" + target.executableName(), mode: 0o755, body: binary},
			{name: root + "/LICENSE", mode: 0o644, body: []byte("license")},
			{name: root + "/README.md", mode: 0o644, body: []byte("readme")},
			{name: root + "/CHANGELOG.md", mode: 0o644, body: []byte("changelog")},
		},
		"duplicate path": {
			{name: root + "/" + target.executableName(), mode: 0o755, body: binary},
			{name: root + "/" + target.executableName(), mode: 0o755, body: binary},
			{name: root + "/LICENSE", mode: 0o644, body: []byte("license")},
			{name: root + "/README.md", mode: 0o644, body: []byte("readme")},
			{name: root + "/CHANGELOG.md", mode: 0o644, body: []byte("changelog")},
		},
		"duplicate basename": {
			{name: root + "/" + target.executableName(), mode: 0o755, body: binary},
			{name: root + "/LICENSE", mode: 0o644, body: []byte("license")},
			{name: root + "/nested/LICENSE", mode: 0o644, body: []byte("duplicate")},
			{name: root + "/README.md", mode: 0o644, body: []byte("readme")},
			{name: root + "/CHANGELOG.md", mode: 0o644, body: []byte("changelog")},
		},
		"missing executable bit": {
			{name: root + "/" + target.executableName(), mode: 0o644, body: binary},
			{name: root + "/LICENSE", mode: 0o644, body: []byte("license")},
			{name: root + "/README.md", mode: 0o644, body: []byte("readme")},
			{name: root + "/CHANGELOG.md", mode: 0o644, body: []byte("changelog")},
		},
		"wrong supporting file mode": {
			{name: root + "/" + target.executableName(), mode: 0o755, body: binary},
			{name: root + "/LICENSE", mode: 0o600, body: []byte("license")},
			{name: root + "/README.md", mode: 0o644, body: []byte("readme")},
			{name: root + "/CHANGELOG.md", mode: 0o644, body: []byte("changelog")},
		},
		"missing root": {
			{name: target.executableName(), mode: 0o755, body: binary},
			{name: "LICENSE", mode: 0o644, body: []byte("license")},
			{name: "README.md", mode: 0o644, body: []byte("readme")},
			{name: "CHANGELOG.md", mode: 0o644, body: []byte("changelog")},
		},
	}
	for name, entries := range tests {
		t.Run(name, func(t *testing.T) {
			clone := fixture.clone(t)
			clone.replaceArchive(t, target, entries)
			clone.writeMetadata(t)
			verifyReleaseFixtureFails(t, clone)
		})
	}
}

func TestVerifyArtifactsRejectsInvalidWindowsZipLayout(t *testing.T) {
	fixture := newReleaseFixture(t)
	target := target{goos: "windows", goarch: "amd64"}
	root := fixture.archiveRoot(target)
	binary := fixture.binaries[target]
	tests := map[string][]archiveEntry{
		"absolute": {
			{name: "/" + root + "/" + target.executableName(), mode: 0o644, body: binary},
			{name: root + "/LICENSE", mode: 0o644, body: []byte("license")},
			{name: root + "/README.md", mode: 0o644, body: []byte("readme")},
			{name: root + "/CHANGELOG.md", mode: 0o644, body: []byte("changelog")},
		},
		"traversal": {
			{name: root + "/../" + target.executableName(), mode: 0o644, body: binary},
			{name: root + "/LICENSE", mode: 0o644, body: []byte("license")},
			{name: root + "/README.md", mode: 0o644, body: []byte("readme")},
			{name: root + "/CHANGELOG.md", mode: 0o644, body: []byte("changelog")},
		},
		"missing root": {
			{name: target.executableName(), mode: 0o644, body: binary},
			{name: "LICENSE", mode: 0o644, body: []byte("license")},
			{name: "README.md", mode: 0o644, body: []byte("readme")},
			{name: "CHANGELOG.md", mode: 0o644, body: []byte("changelog")},
		},
		"wrong binary mode": {
			{name: root + "/" + target.executableName(), mode: 0o644, body: binary},
			{name: root + "/LICENSE", mode: 0o644, body: []byte("license")},
			{name: root + "/README.md", mode: 0o644, body: []byte("readme")},
			{name: root + "/CHANGELOG.md", mode: 0o644, body: []byte("changelog")},
		},
	}
	for name, entries := range tests {
		t.Run(name, func(t *testing.T) {
			clone := fixture.clone(t)
			clone.replaceArchive(t, target, entries)
			clone.writeMetadata(t)
			verifyReleaseFixtureFails(t, clone)
		})
	}
}

func TestVerifyArtifactsRejectsMalformedOrUnrelatedSBOM(t *testing.T) {
	fixture := newReleaseFixture(t)
	target := targets[0]
	tests := map[string]any{
		"malformed":        []byte(`{"bomFormat":`),
		"unrelated":        fixture.sbom(target, "unrelated.tar.gz", smokeVersion, true),
		"wrong version":    fixture.sbom(target, fixture.archiveName(target), "9.9.9", true),
		"empty components": fixture.sbom(target, fixture.archiveName(target), smokeVersion, false),
	}
	for name, value := range tests {
		t.Run(name, func(t *testing.T) {
			clone := fixture.clone(t)
			var data []byte
			switch value := value.(type) {
			case []byte:
				data = value
			default:
				var err error
				data, err = json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(clone.dist, clone.sbomName(target)), data, 0o644); err != nil {
				t.Fatal(err)
			}
			verifyReleaseFixtureFails(t, clone)
		})
	}
}

func TestVerifyArtifactsRejectsInexactChecksumManifest(t *testing.T) {
	fixture := newReleaseFixture(t)
	tests := map[string]func(*releaseFixture){
		"missing": func(clone *releaseFixture) {
			delete(clone.checksums, clone.archiveName(targets[0]))
		},
		"unexpected": func(clone *releaseFixture) {
			clone.checksums["unexpected.txt"] = strings.Repeat("0", sha256.Size*2)
		},
		"bad hash": func(clone *releaseFixture) {
			clone.checksums[clone.archiveName(targets[0])] = strings.Repeat("0", sha256.Size*2)
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			clone := fixture.clone(t)
			mutate(clone)
			clone.writeMetadata(t)
			verifyReleaseFixtureFails(t, clone)
		})
	}

	t.Run("duplicate", func(t *testing.T) {
		clone := fixture.clone(t)
		clone.writeMetadata(t)
		name := clone.archiveName(targets[0])
		line := fmt.Sprintf("%s  %s\n", clone.checksums[name], name)
		file, err := os.OpenFile(filepath.Join(clone.dist, "checksums.txt"), os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(file, line); err != nil {
			file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		verifyReleaseFixtureFails(t, clone)
	})
}

func TestVerifyArtifactsRejectsUnexpectedArtifactRecord(t *testing.T) {
	fixture := newReleaseFixture(t)
	clone := fixture.clone(t)
	clone.artifacts = append(clone.artifacts, artifact{Name: "unexpected.bin", Path: "unexpected.bin", Type: "File"})
	if err := os.WriteFile(filepath.Join(clone.dist, "unexpected.bin"), []byte("unexpected"), 0o644); err != nil {
		t.Fatal(err)
	}
	clone.writeMetadata(t)
	verifyReleaseFixtureFails(t, clone)
}

type archiveEntry struct {
	name string
	mode fs.FileMode
	body []byte
}

type releaseFixture struct {
	dist      string
	artifacts []artifact
	checksums map[string]string
	binaries  map[target][]byte
}

func newReleaseFixture(t *testing.T) *releaseFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	t.Cleanup(cancel)
	root, err := repositoryRoot()
	if err != nil {
		t.Fatal(err)
	}
	fixture := &releaseFixture{
		dist:      t.TempDir(),
		checksums: make(map[string]string),
		binaries:  make(map[target][]byte),
	}
	buildDir := t.TempDir()
	for _, target := range targets {
		binaryPath := filepath.Join(buildDir, target.goos+"_"+target.goarch, target.executableName())
		if err := os.MkdirAll(filepath.Dir(binaryPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := buildTarget(ctx, root, binaryPath, target); err != nil {
			t.Fatal(err)
		}
		binary, err := os.ReadFile(binaryPath)
		if err != nil {
			t.Fatal(err)
		}
		fixture.binaries[target] = binary
		fixture.replaceArchive(t, target, fixture.validArchiveEntries(target))
		sbomData, err := json.Marshal(fixture.sbom(target, fixture.archiveName(target), smokeVersion, true))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(fixture.dist, fixture.sbomName(target)), sbomData, 0o644); err != nil {
			t.Fatal(err)
		}
		fixture.artifacts = append(fixture.artifacts, artifact{
			Name: fixture.sbomName(target), Path: fixture.sbomName(target), Goos: target.goos, Goarch: target.goarch, Type: "SBOM",
		})
	}
	fixture.replaceSource(t, []archiveEntry{
		{name: fixture.sourceRoot() + "/LICENSE", mode: 0o644, body: []byte("license")},
		{name: fixture.sourceRoot() + "/README.md", mode: 0o644, body: []byte("readme")},
		{name: fixture.sourceRoot() + "/CHANGELOG.md", mode: 0o644, body: []byte("changelog")},
		{name: fixture.sourceRoot() + "/go.mod", mode: 0o644, body: []byte("module github.com/noahjenkins/unifi-cli")},
	})
	fixture.artifacts = append(fixture.artifacts, artifact{Name: fixture.sourceName(), Path: fixture.sourceName(), Type: "Source"})
	fixture.artifacts = append(fixture.artifacts, artifact{Name: "checksums.txt", Path: "checksums.txt", Type: "Checksum"})
	fixture.writeMetadata(t)
	return fixture
}

func (f *releaseFixture) clone(t *testing.T) *releaseFixture {
	t.Helper()
	clone := &releaseFixture{
		dist:      t.TempDir(),
		artifacts: slices.Clone(f.artifacts),
		checksums: make(map[string]string, len(f.checksums)),
		binaries:  f.binaries,
	}
	for name, checksum := range f.checksums {
		clone.checksums[name] = checksum
	}
	entries, err := os.ReadDir(f.dist)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(f.dist, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(clone.dist, entry.Name()), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return clone
}

func (f *releaseFixture) archiveName(target target) string {
	extension := ".tar.gz"
	if target.goos == "windows" {
		extension = ".zip"
	}
	return fmt.Sprintf("unifi-cli_%s_%s_%s%s", smokeVersion, target.goos, target.goarch, extension)
}

func (f *releaseFixture) archiveRoot(target target) string {
	name := f.archiveName(target)
	if strings.HasSuffix(name, ".tar.gz") {
		return strings.TrimSuffix(name, ".tar.gz")
	}
	return strings.TrimSuffix(name, ".zip")
}

func (f *releaseFixture) sourceName() string            { return "unifi-cli_" + smokeVersion + "_source.tar.gz" }
func (f *releaseFixture) sourceRoot() string            { return "unifi-cli_" + smokeVersion }
func (f *releaseFixture) sbomName(target target) string { return f.archiveName(target) + ".sbom.json" }

func (f *releaseFixture) validArchiveEntries(target target) []archiveEntry {
	root := f.archiveRoot(target)
	return []archiveEntry{
		{name: root + "/" + target.executableName(), mode: 0o755, body: f.binaries[target]},
		{name: root + "/LICENSE", mode: 0o644, body: []byte("license")},
		{name: root + "/README.md", mode: 0o644, body: []byte("readme")},
		{name: root + "/CHANGELOG.md", mode: 0o644, body: []byte("changelog")},
	}
}

func (f *releaseFixture) replaceArchive(t *testing.T, target target, entries []archiveEntry) {
	t.Helper()
	name := f.archiveName(target)
	path := filepath.Join(f.dist, name)
	if target.goos == "windows" {
		writeZip(t, path, entries)
	} else {
		writeTarGz(t, path, entries)
	}
	f.checksums[name] = fileSHA256(t, path)
	for i := range f.artifacts {
		if f.artifacts[i].Type == "Archive" && f.artifacts[i].Goos == target.goos && f.artifacts[i].Goarch == target.goarch {
			return
		}
	}
	entry := artifact{Name: name, Path: name, Goos: target.goos, Goarch: target.goarch, Type: "Archive"}
	if target.goos == "windows" {
		entry.Extra.Format = "zip"
	} else {
		entry.Extra.Format = "tar.gz"
	}
	f.artifacts = append(f.artifacts, entry)
}

func (f *releaseFixture) replaceSource(t *testing.T, entries []archiveEntry) {
	t.Helper()
	path := filepath.Join(f.dist, f.sourceName())
	writeTarGz(t, path, entries)
	f.checksums[f.sourceName()] = fileSHA256(t, path)
}

func (f *releaseFixture) renameArchive(t *testing.T, target target, newName string) {
	t.Helper()
	oldName := f.archiveName(target)
	if err := os.Rename(filepath.Join(f.dist, oldName), filepath.Join(f.dist, newName)); err != nil {
		t.Fatal(err)
	}
	oldSBOM := oldName + ".sbom.json"
	newSBOM := newName + ".sbom.json"
	if err := os.Rename(filepath.Join(f.dist, oldSBOM), filepath.Join(f.dist, newSBOM)); err != nil {
		t.Fatal(err)
	}
	delete(f.checksums, oldName)
	f.checksums[newName] = fileSHA256(t, filepath.Join(f.dist, newName))
	for i := range f.artifacts {
		switch f.artifacts[i].Name {
		case oldName:
			f.artifacts[i].Name = newName
			f.artifacts[i].Path = newName
		case oldSBOM:
			f.artifacts[i].Name = newSBOM
			f.artifacts[i].Path = newSBOM
		}
	}
}

func (f *releaseFixture) setArtifactPath(name, path string) {
	for i := range f.artifacts {
		if f.artifacts[i].Name == name {
			f.artifacts[i].Path = path
		}
	}
}

func (f *releaseFixture) archiveArtifact(target target) artifact {
	for _, artifact := range f.artifacts {
		if artifact.Type == "Archive" && artifact.Goos == target.goos && artifact.Goarch == target.goarch {
			return artifact
		}
	}
	panic("archive fixture not found")
}

func (f *releaseFixture) sbom(target target, name, version string, components bool) map[string]any {
	bom := map[string]any{
		"bomFormat":   "CycloneDX",
		"specVersion": "1.6",
		"version":     1,
		"metadata": map[string]any{
			"component": map[string]any{"type": "application", "name": name, "version": version},
		},
		"components": []any{},
	}
	if components {
		bom["components"] = []any{map[string]any{"type": "library", "name": "github.com/spf13/cobra", "version": "v1.10.2"}}
	}
	return bom
}

func (f *releaseFixture) writeMetadata(t *testing.T) {
	t.Helper()
	artifacts, err := json.MarshalIndent(f.artifacts, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.dist, "artifacts.json"), artifacts, 0o644); err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(f.checksums))
	for name := range f.checksums {
		names = append(names, name)
	}
	sort.Strings(names)
	var manifest strings.Builder
	for _, name := range names {
		fmt.Fprintf(&manifest, "%s  %s\n", f.checksums[name], name)
	}
	if err := os.WriteFile(filepath.Join(f.dist, "checksums.txt"), []byte(manifest.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func verifyReleaseFixture(t *testing.T, fixture *releaseFixture) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := verifyArtifacts(ctx, fixture.dist, smokeVersion, smokeCommit); err != nil {
		t.Fatal(err)
	}
}

func verifyReleaseFixtureFails(t *testing.T, fixture *releaseFixture) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := verifyArtifacts(ctx, fixture.dist, smokeVersion, smokeCommit); err == nil {
		t.Fatal("artifact verification unexpectedly accepted invalid release")
	}
}

func writeTarGz(t *testing.T, path string, entries []archiveEntry) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gzipWriter := gzip.NewWriter(file)
	tarWriter := tar.NewWriter(gzipWriter)
	for _, entry := range entries {
		header := &tar.Header{Name: entry.name, Mode: int64(entry.mode.Perm()), Size: int64(len(entry.body)), Typeflag: tar.TypeReg}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := tarWriter.Write(entry.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func writeZip(t *testing.T, path string, entries []archiveEntry) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Deflate}
		header.SetMode(entry.mode)
		entryWriter, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entryWriter.Write(entry.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func fileSHA256(t *testing.T, path string) string {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func nonNativeTarget() target {
	for _, target := range targets {
		if target.goos != runtime.GOOS || target.goarch != runtime.GOARCH {
			return target
		}
	}
	panic("no non-native release target")
}
