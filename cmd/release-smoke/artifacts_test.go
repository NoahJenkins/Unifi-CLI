package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestVerifyArtifactsAcceptsExactHardenedRelease(t *testing.T) {
	fixture := newReleaseFixture(t)
	verifyReleaseFixture(t, fixture)
}

func TestVerifyArtifactsRejectsNonNativeBinaryThatDiffersFromTrustedBuild(t *testing.T) {
	fixture := newReleaseFixture(t)
	var selected target
	for _, candidate := range targets {
		if candidate.goos != runtime.GOOS || candidate.goarch != runtime.GOARCH {
			selected = candidate
			break
		}
	}
	fixture.binaries = cloneFixtureBinaries(fixture.binaries)
	fixture.binaries[selected] = append(slices.Clone(fixture.binaries[selected]), 0)
	fixture.replaceArchive(t, selected, fixture.validArchiveEntries(selected))
	fixture.writeSBOM(t, selected, fixture.sbom(selected, fixture.archiveName(selected), smokeVersion, true))
	fixture.writeMetadata(t)
	verifyReleaseFixtureFails(t, fixture)
}

func TestVerifyArtifactsRejectsSourceContentNotInTrustedCommitManifest(t *testing.T) {
	fixture := newReleaseFixture(t)
	entries := fixture.validSourceEntries()
	entries[1].body = []byte("backdoored readme")
	fixture.replaceSource(t, entries)
	fixture.writeMetadata(t)
	verifyReleaseFixtureFails(t, fixture)
}

func TestVerifyArtifactsRejectsAlteredPlatformArchiveSupportFiles(t *testing.T) {
	for _, selected := range []target{{goos: "linux", goarch: "amd64"}, {goos: "windows", goarch: "amd64"}} {
		t.Run(selected.String(), func(t *testing.T) {
			fixture := newReleaseFixture(t)
			entries := fixture.validArchiveEntries(selected)
			entries[2].body = []byte("attacker-controlled installation instructions")
			fixture.replaceArchive(t, selected, entries)
			fixture.writeMetadata(t)
			verifyReleaseFixtureFails(t, fixture)
		})
	}
}

func TestBuildTrustedSourceManifestUsesExactGitCommitObjects(t *testing.T) {
	root, err := repositoryRoot()
	if err != nil {
		t.Fatal(err)
	}
	headRaw, err := gitOutput(context.Background(), root, 128, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	head := strings.TrimSpace(string(headRaw))
	manifest, err := buildTrustedSourceManifest(context.Background(), root, head)
	if err != nil {
		t.Fatal(err)
	}
	readme, err := gitOutput(context.Background(), root, maxReleaseEntryBytes, "show", head+":README.md")
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(readme)
	want := hex.EncodeToString(digest[:])
	for _, entry := range manifest.Files {
		if entry.Path == "README.md" {
			if entry.Mode != "100644" || entry.SHA256 != want {
				t.Fatalf("README manifest entry = %+v, want mode 100644 and SHA-256 %s", entry, want)
			}
			return
		}
	}
	t.Fatal("trusted source manifest omitted README.md")
}

func cloneFixtureBinaries(in map[target][]byte) map[target][]byte {
	out := make(map[target][]byte, len(in))
	for key, value := range in {
		out[key] = slices.Clone(value)
	}
	return out
}

func TestWriteExtractedRejectsEntryLimitOverflow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unifi")
	err := writeExtracted(path, bytes.NewReader([]byte("123456789")), 0o755, 8)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("writeExtracted error = %v, want explicit size-limit failure", err)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("oversized extraction left file behind: %v", statErr)
	}
}

func TestReadReleaseFileRejectsOversizedAndNonRegularInput(t *testing.T) {
	t.Run("oversized", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "metadata.json")
		if err := os.WriteFile(path, []byte("123456789"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := readReleaseFile(path, 8)
		if err == nil || !strings.Contains(err.Error(), "exceeds") {
			t.Fatalf("readReleaseFile error = %v, want size-limit failure", err)
		}
	})

	t.Run("directory", func(t *testing.T) {
		_, err := readReleaseFile(t.TempDir(), 8)
		if err == nil || !strings.Contains(err.Error(), "regular file") {
			t.Fatalf("readReleaseFile error = %v, want regular-file failure", err)
		}
	})
}

func TestSHA256FileRejectsOversizedInput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "artifact")
	if err := os.WriteFile(path, []byte("123456789"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := sha256File(context.Background(), path, 8)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("sha256File error = %v, want size-limit failure", err)
	}
}

func TestArchiveInspectionRejectsDeclaredEntryExpansionBeforeReadingBody(t *testing.T) {
	t.Run("release archive", func(t *testing.T) {
		target := target{goos: "linux", goarch: "amd64"}
		root := "unifi-cli_1.0.0_linux_amd64"
		path := filepath.Join(t.TempDir(), "release.tar.gz")
		writeTruncatedTarGzHeader(t, path, &tar.Header{
			Name: root + "/unifi", Mode: 0o755, Typeflag: tar.TypeReg, Size: maxReleaseEntryBytes + 1,
		})
		err := inspectTarArchive(context.Background(), path, target, root, filepath.Join(t.TempDir(), "unifi"))
		if err == nil || !strings.Contains(err.Error(), "exceeds") {
			t.Fatalf("inspectTarArchive error = %v, want expansion-limit failure", err)
		}
	})

	t.Run("source archive", func(t *testing.T) {
		root := "unifi-cli_1.0.0"
		path := filepath.Join(t.TempDir(), "source.tar.gz")
		writeTruncatedTarGzHeader(t, path, &tar.Header{
			Name: root + "/huge", Mode: 0o644, Typeflag: tar.TypeReg, Size: maxReleaseEntryBytes + 1,
		})
		err := inspectSourceArchive(context.Background(), path, root, smokeCommit, sourceManifest{})
		if err == nil || !strings.Contains(err.Error(), "exceeds") {
			t.Fatalf("inspectSourceArchive error = %v, want expansion-limit failure", err)
		}
	})
}

func TestReleaseArchiveInspectionRejectsEntryCountAndCompressedSizeBudgets(t *testing.T) {
	releaseTarget := target{goos: "linux", goarch: "amd64"}
	root := "unifi-cli_1.0.0_linux_amd64"

	t.Run("entry count", func(t *testing.T) {
		entries := make([]archiveEntry, maxReleaseArchiveEntries+1)
		for i := range entries {
			entries[i] = archiveEntry{name: fmt.Sprintf("%s/file-%d", root, i), mode: 0o644, body: []byte("x")}
		}
		path := filepath.Join(t.TempDir(), "many-entries.zip")
		writeZip(t, path, entries)
		windowsTarget := target{goos: "windows", goarch: "amd64"}
		err := inspectZipArchive(context.Background(), path, windowsTarget, root, filepath.Join(t.TempDir(), "unifi.exe"))
		if err == nil || !strings.Contains(err.Error(), "entry count") {
			t.Fatalf("inspectTarArchive error = %v, want entry-count failure", err)
		}
	})

	t.Run("compressed bytes", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "oversized.tar.gz")
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := file.Truncate(maxReleaseArchiveBytes + 1); err != nil {
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		err = inspectTarArchive(context.Background(), path, releaseTarget, root, filepath.Join(t.TempDir(), "unifi"))
		if err == nil || !strings.Contains(err.Error(), "exceeds") {
			t.Fatalf("inspectTarArchive error = %v, want compressed-size failure", err)
		}
	})
}

func TestSourceArchiveInspectionHonorsCanceledContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source.tar.gz")
	writeTarGz(t, path, []archiveEntry{{
		name: "pax_global_header", typeflag: tar.TypeXGlobalHeader, paxRecords: map[string]string{"comment": smokeCommit},
	}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := inspectSourceArchive(ctx, path, "unifi-cli_1.0.0", smokeCommit, sourceManifest{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("inspectSourceArchive error = %v, want context.Canceled", err)
	}
}

func writeTruncatedTarGzHeader(t *testing.T, path string, header *tar.Header) {
	t.Helper()
	var raw bytes.Buffer
	writer := tar.NewWriter(&raw)
	if err := writer.WriteHeader(header); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gzipWriter := gzip.NewWriter(file)
	if _, err := gzipWriter.Write(raw.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestNativeArtifactMustMatchTrustedBinaryWithoutExecution(t *testing.T) {
	dir := t.TempDir()
	trusted := filepath.Join(dir, "trusted")
	if err := os.WriteFile(trusted, []byte("trusted-release-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "artifact-executed")
	artifactPath := filepath.Join(dir, "artifact")
	script := fmt.Sprintf("#!/bin/sh\nprintf executed > %q\n", marker)
	if err := os.WriteFile(artifactPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(script))
	artifact := archiveExecutable{extractedPath: artifactPath, sha256: hex.EncodeToString(digest[:])}

	err := verifyTrustedArtifact(context.Background(), trusted, artifact, target{goos: runtime.GOOS, goarch: runtime.GOARCH})
	if err == nil || !strings.Contains(err.Error(), "trusted binary") {
		t.Fatalf("verifyTrustedArtifact error = %v, want hash mismatch", err)
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatalf("untrusted artifact was executed: %v", statErr)
	}
}

func TestVerifyArtifactsRejectsNativeArchiveThatDiffersFromTrustedBuild(t *testing.T) {
	fixture := newReleaseFixture(t)
	native := target{goos: runtime.GOOS, goarch: runtime.GOARCH}
	trustedPath := fixture.trustedBinaryPath(native)
	trusted, err := os.ReadFile(trustedPath)
	if err != nil {
		t.Fatal(err)
	}
	trusted = append(trusted, []byte("different-build")...)
	if err := os.WriteFile(trustedPath, trusted, 0o755); err != nil {
		t.Fatal(err)
	}
	verifyReleaseFixtureFails(t, fixture)
}

func TestVerifyArtifactsAcceptsPinnedToolOutputSnapshot(t *testing.T) {
	fixture := newReleaseFixture(t)
	snapshot := materializePinnedOutputSnapshot(t, fixture)

	gotChecksums := make([]string, 0, len(fixture.checksums))
	for name := range fixture.checksums {
		gotChecksums = append(gotChecksums, name)
	}
	sort.Strings(gotChecksums)
	wantChecksums := slices.Clone(snapshot.ChecksumNames)
	sort.Strings(wantChecksums)
	if !slices.Equal(gotChecksums, wantChecksums) {
		t.Fatalf("fixture checksum scope = %v, pinned GoReleaser scope = %v", gotChecksums, wantChecksums)
	}

	verifyReleaseFixture(t, fixture)
}

func TestInspectCycloneDXSBOMAcceptsPinnedSyftFileComponent(t *testing.T) {
	snapshot := loadPinnedOutputSnapshot(t)
	archiveName := "unifi-cli_0.0.0-SNAPSHOT-1aa4eee_windows_arm64.zip"
	bom := snapshot.CycloneDXByArchive[archiveName]
	data, err := json.Marshal(bom)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "pinned-syft.cdx.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	components := bom["components"].([]any)
	fileComponent := components[1].(map[string]any)
	hashes := fileComponent["hashes"].([]any)
	sha256Hash := hashes[0].(map[string]any)["content"].(string)
	executable := archiveExecutable{
		relativePath: "unifi-cli_0.0.0-SNAPSHOT-1aa4eee_windows_arm64/unifi.exe",
		sha256:       sha256Hash,
	}
	expected := map[sbomComponentIdentity]struct{}{
		{Type: "library", Name: "github.com/spf13/cobra", Version: "v1.10.2"}: {},
	}
	if err := inspectCycloneDXSBOM(path, archiveName, "0.0.0-SNAPSHOT-1aa4eee", executable, expected); err != nil {
		t.Fatalf("pinned Syft 1.48.0 CycloneDX shape rejected: %v", err)
	}
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
	if err := verifyArtifacts(ctx, fixture.dist, "9.9.9", smokeCommit, fixture.trustedBinaries, fixture.trustedSourceManifest); err == nil {
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
		"setuid source file": append([]archiveEntry{{
			name: fixture.sourceRoot() + "/LICENSE", mode: fs.ModeSetuid | 0o664, body: []byte("license"),
		}}, fixture.validSourceEntries()[1:]...),
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

func TestInspectSourceArchiveAcceptsPinnedGoReleaserPAXHeader(t *testing.T) {
	root := "unifi-cli_0.0.0-SNAPSHOT-1aa4eee"
	path := filepath.Join(t.TempDir(), "source.tar.gz")
	writeTarGz(t, path, []archiveEntry{
		{name: "pax_global_header", typeflag: tar.TypeXGlobalHeader, paxRecords: map[string]string{"comment": smokeCommit}},
		{name: root + "/LICENSE", mode: 0o664, body: []byte("license")},
		{name: root + "/README.md", mode: 0o664, body: []byte("readme")},
		{name: root + "/CHANGELOG.md", mode: 0o664, body: []byte("changelog")},
		{name: root + "/go.mod", mode: 0o664, body: []byte("module github.com/noahjenkins/unifi-cli")},
	})
	manifest := manifestForSourceEntries(t, root, []archiveEntry{
		{name: root + "/LICENSE", mode: 0o664, body: []byte("license")},
		{name: root + "/README.md", mode: 0o664, body: []byte("readme")},
		{name: root + "/CHANGELOG.md", mode: 0o664, body: []byte("changelog")},
		{name: root + "/go.mod", mode: 0o664, body: []byte("module github.com/noahjenkins/unifi-cli")},
	})
	if err := inspectSourceArchive(context.Background(), path, root, smokeCommit, manifest); err != nil {
		t.Fatalf("pinned GoReleaser source PAX header rejected: %v", err)
	}
}

func TestVerifyArtifactsRejectsSourceArchiveWithoutExactCommitBinding(t *testing.T) {
	fixture := newReleaseFixture(t)
	core := fixture.validSourceEntries()
	tests := map[string][]archiveEntry{
		"missing global header": core,
		"wrong global header name": append([]archiveEntry{{
			name: "GlobalHead.0.0", typeflag: tar.TypeXGlobalHeader, paxRecords: map[string]string{"comment": smokeCommit},
		}}, core...),
		"wrong commit": append([]archiveEntry{{
			name: "pax_global_header", typeflag: tar.TypeXGlobalHeader, paxRecords: map[string]string{"comment": strings.Repeat("f", 40)},
		}}, core...),
		"truncated commit": append([]archiveEntry{{
			name: "pax_global_header", typeflag: tar.TypeXGlobalHeader, paxRecords: map[string]string{"comment": smokeCommit[:12]},
		}}, core...),
		"extra PAX key": append([]archiveEntry{{
			name: "pax_global_header", typeflag: tar.TypeXGlobalHeader,
			paxRecords: map[string]string{"comment": smokeCommit, "path": "unexpected"},
		}}, core...),
		"multiple global headers": append([]archiveEntry{
			{name: "pax_global_header", typeflag: tar.TypeXGlobalHeader, paxRecords: map[string]string{"comment": smokeCommit}},
			{name: "pax_global_header", typeflag: tar.TypeXGlobalHeader, paxRecords: map[string]string{"comment": smokeCommit}},
		}, core...),
		"other header type": append([]archiveEntry{{
			name: fixture.sourceRoot() + "/link", typeflag: tar.TypeSymlink, linkname: "LICENSE",
		}}, core...),
	}
	for name, entries := range tests {
		t.Run(name, func(t *testing.T) {
			clone := fixture.clone(t)
			clone.replaceSourceRaw(t, entries)
			clone.writeMetadata(t)
			verifyReleaseFixtureFails(t, clone)
		})
	}
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
		"setuid binary": {
			{name: root + "/" + target.executableName(), mode: fs.ModeSetuid | 0o755, body: binary},
			{name: root + "/LICENSE", mode: 0o644, body: []byte("license")},
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
		"sticky binary": {
			{name: root + "/" + target.executableName(), mode: fs.ModeSticky | 0o755, body: binary},
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
			clone.checksums[clone.sbomName(target)] = fileSHA256(t, filepath.Join(clone.dist, clone.sbomName(target)))
			clone.writeMetadata(t)
			verifyReleaseFixtureFails(t, clone)
		})
	}

	for name, mutate := range map[string]func(map[string]any){
		"unsupported spec": func(bom map[string]any) {
			bom["specVersion"] = "1.6"
		},
		"wrong source type": func(bom map[string]any) {
			metadata := bom["metadata"].(map[string]any)
			component := metadata["component"].(map[string]any)
			component["type"] = "application"
		},
		"unversioned library": func(bom map[string]any) {
			bom["components"] = []any{map[string]any{"type": "library", "name": "github.com/spf13/cobra"}}
		},
		"unhashed file": func(bom map[string]any) {
			bom["components"] = []any{map[string]any{"type": "file", "name": "/tmp/unifi"}}
		},
		"invalid file hash": func(bom map[string]any) {
			bom["components"] = []any{map[string]any{
				"type": "file", "name": "/tmp/unifi",
				"hashes": []any{map[string]any{"alg": "SHA-256", "content": "not-a-digest"}},
			}}
		},
		"extra versioned library": func(bom map[string]any) {
			bom["components"] = append(bom["components"].([]any), map[string]any{
				"type": "library", "name": "example.invalid/injected", "version": "v9.9.9",
			})
		},
		"changed trusted version": func(bom map[string]any) {
			components := bom["components"].([]any)
			components[0].(map[string]any)["version"] = "v9.9.9"
		},
		"missing trusted library": func(bom map[string]any) {
			components := bom["components"].([]any)
			bom["components"] = components[1:]
		},
	} {
		t.Run(name, func(t *testing.T) {
			clone := fixture.clone(t)
			bom := clone.sbom(target, clone.archiveName(target), smokeVersion, true)
			mutate(bom)
			data, err := json.Marshal(bom)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(clone.dist, clone.sbomName(target)), data, 0o644); err != nil {
				t.Fatal(err)
			}
			clone.checksums[clone.sbomName(target)] = fileSHA256(t, filepath.Join(clone.dist, clone.sbomName(target)))
			clone.writeMetadata(t)
			verifyReleaseFixtureFails(t, clone)
		})
	}
}

func TestVerifyArtifactsRejectsSBOMExecutableMismatch(t *testing.T) {
	fixture := newReleaseFixture(t)
	currentTarget := target{goos: "windows", goarch: "amd64"}
	other := target{goos: "linux", goarch: "arm64"}
	tests := map[string]func(map[string]any){
		"wrong target root": func(file map[string]any) {
			file["name"] = fixture.syftExecutablePath(other)
		},
		"wrong executable name": func(file map[string]any) {
			file["name"] = "/private/tmp/syft-archive-contents-test/" + fixture.archiveRoot(currentTarget) + "/other.exe"
		},
		"missing file component": func(bom map[string]any) {
			bom["components"] = []any{map[string]any{"type": "library", "name": "github.com/spf13/cobra", "version": "v1.10.2"}}
		},
		"duplicate matching file component": func(bom map[string]any) {
			components := bom["components"].([]any)
			file := components[1].(map[string]any)
			duplicate := map[string]any{"type": file["type"], "name": file["name"], "hashes": file["hashes"]}
			bom["components"] = append(components, duplicate)
		},
		"all-zero digest": func(file map[string]any) {
			file["hashes"] = []any{map[string]any{"alg": "SHA-256", "content": strings.Repeat("0", 64)}}
		},
		"fabricated digest": func(file map[string]any) {
			file["hashes"] = []any{map[string]any{"alg": "SHA-256", "content": strings.Repeat("f", 64)}}
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			clone := fixture.clone(t)
			bom := clone.sbom(currentTarget, clone.archiveName(currentTarget), smokeVersion, true)
			if strings.Contains(name, "file component") {
				mutate(bom)
			} else {
				components := bom["components"].([]any)
				mutate(components[1].(map[string]any))
			}
			clone.writeSBOM(t, currentTarget, bom)
			clone.writeMetadata(t)
			verifyReleaseFixtureFails(t, clone)
		})
	}
}

func TestVerifyArtifactsRejectsInvalidMetadataRecord(t *testing.T) {
	fixture := newReleaseFixture(t)

	t.Run("duplicate", func(t *testing.T) {
		clone := fixture.clone(t)
		clone.artifacts = append(clone.artifacts, artifact{Name: "metadata.json", Path: "metadata.json", Type: "Metadata"})
		clone.writeMetadata(t)
		verifyReleaseFixtureFails(t, clone)
	})

	for name, metadata := range map[string]string{
		"wrong project": fmt.Sprintf(`{"project_name":"other","version":%q,"commit":%q}`, smokeVersion, smokeCommit),
		"wrong version": fmt.Sprintf(`{"project_name":"unifi-cli","version":"9.9.9","commit":%q}`, smokeCommit),
		"wrong commit":  fmt.Sprintf(`{"project_name":"unifi-cli","version":%q,"commit":"%s"}`, smokeVersion, strings.Repeat("0", 40)),
		"malformed":     `{"project_name":`,
	} {
		t.Run(name, func(t *testing.T) {
			clone := fixture.clone(t)
			if err := os.WriteFile(filepath.Join(clone.dist, "metadata.json"), []byte(metadata), 0o644); err != nil {
				t.Fatal(err)
			}
			verifyReleaseFixtureFails(t, clone)
		})
	}
}

func TestVerifyArtifactsRejectsAmbiguousTargetlessSBOMRecords(t *testing.T) {
	fixture := newReleaseFixture(t)

	t.Run("duplicate exact archive SBOM", func(t *testing.T) {
		clone := fixture.clone(t)
		name := clone.sbomName(targets[0])
		clone.artifacts = append(clone.artifacts, artifact{Name: name, Path: name, Type: "SBOM"})
		clone.writeMetadata(t)
		verifyReleaseFixtureFails(t, clone)
	})

	t.Run("unrelated targetless SBOM name", func(t *testing.T) {
		clone := fixture.clone(t)
		name := clone.sbomName(targets[0])
		for i := range clone.artifacts {
			if clone.artifacts[i].Type == "SBOM" && clone.artifacts[i].Name == name {
				clone.artifacts[i].Name = "unrelated.sbom.json"
				clone.artifacts[i].Path = "unrelated.sbom.json"
			}
		}
		data, err := os.ReadFile(filepath.Join(clone.dist, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(clone.dist, "unrelated.sbom.json"), data, 0o644); err != nil {
			t.Fatal(err)
		}
		clone.writeMetadata(t)
		verifyReleaseFixtureFails(t, clone)
	})
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
		"bad SBOM hash": func(clone *releaseFixture) {
			clone.checksums[clone.sbomName(targets[0])] = strings.Repeat("0", sha256.Size*2)
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

	t.Run("unsafe name", func(t *testing.T) {
		clone := fixture.clone(t)
		clone.writeMetadata(t)
		line := fmt.Sprintf("%s  ../escape\n", strings.Repeat("0", sha256.Size*2))
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
	name       string
	mode       fs.FileMode
	body       []byte
	typeflag   byte
	linkname   string
	paxRecords map[string]string
}

type releaseFixture struct {
	dist                  string
	artifacts             []artifact
	checksums             map[string]string
	binaries              map[target][]byte
	trustedBinaries       string
	trustedSourceManifest string
	sbomInventories       map[target]map[sbomComponentIdentity]struct{}
}

type pinnedOutputSnapshot struct {
	Version            string                    `json:"version"`
	Commit             string                    `json:"commit"`
	ArtifactRecords    []artifact                `json:"artifact_records"`
	ChecksumNames      []string                  `json:"checksum_names"`
	CycloneDXByArchive map[string]map[string]any `json:"cyclonedx_by_archive"`
}

func loadPinnedOutputSnapshot(t *testing.T) pinnedOutputSnapshot {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "goreleaser-v2.17.1-syft-v1.48.0-snapshot.json"))
	if err != nil {
		t.Fatal(err)
	}
	var snapshot pinnedOutputSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func materializePinnedOutputSnapshot(t *testing.T, fixture *releaseFixture) pinnedOutputSnapshot {
	t.Helper()
	snapshot := loadPinnedOutputSnapshot(t)
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	rewritten := strings.ReplaceAll(string(raw), snapshot.Version, smokeVersion)
	rewritten = strings.ReplaceAll(rewritten, snapshot.Commit, smokeCommit)
	if err := json.Unmarshal([]byte(rewritten), &snapshot); err != nil {
		t.Fatal(err)
	}
	fixture.artifacts = snapshot.ArtifactRecords
	for _, current := range fixture.artifacts {
		path := filepath.Join(fixture.dist, strings.TrimPrefix(current.Path, "dist/"))
		switch current.Type {
		case "Metadata":
			metadata := fmt.Sprintf(`{"project_name":"unifi-cli","version":%q,"commit":%q}`, smokeVersion, smokeCommit)
			if err := os.WriteFile(path, []byte(metadata), 0o644); err != nil {
				t.Fatal(err)
			}
		case "Binary":
			target := target{goos: current.Goos, goarch: current.Goarch}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, fixture.binaries[target], 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, target := range targets {
		archiveName := fixture.archiveName(target)
		bomData, err := json.Marshal(snapshot.CycloneDXByArchive[archiveName])
		if err != nil {
			t.Fatal(err)
		}
		var bom map[string]any
		if err := json.Unmarshal(bomData, &bom); err != nil {
			t.Fatal(err)
		}
		bom["components"] = fixture.sbomComponents(target)
		fixture.writeSBOM(t, target, bom)
	}
	fixture.writeMetadata(t)
	return snapshot
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
		dist:            t.TempDir(),
		checksums:       make(map[string]string),
		binaries:        make(map[target][]byte),
		sbomInventories: make(map[target]map[sbomComponentIdentity]struct{}),
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
		inventory, err := trustedSBOMInventory(binaryPath)
		if err != nil {
			t.Fatal(err)
		}
		fixture.sbomInventories[target] = inventory
		fixture.replaceArchive(t, target, fixture.validArchiveEntries(target))
		sbomData, err := json.Marshal(fixture.sbom(target, fixture.archiveName(target), smokeVersion, true))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(fixture.dist, fixture.sbomName(target)), sbomData, 0o644); err != nil {
			t.Fatal(err)
		}
		fixture.checksums[fixture.sbomName(target)] = fileSHA256(t, filepath.Join(fixture.dist, fixture.sbomName(target)))
		fixture.artifacts = append(fixture.artifacts, artifact{
			Name: fixture.sbomName(target), Path: fixture.sbomName(target), Type: "SBOM",
		})
	}
	fixture.trustedBinaries = t.TempDir()
	for _, target := range targets {
		path := fixture.trustedBinaryPath(target)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, fixture.binaries[target], 0o755); err != nil {
			t.Fatal(err)
		}
	}
	sourceEntries := fixture.validSourceEntries()
	fixture.replaceSource(t, sourceEntries)
	fixture.trustedSourceManifest = filepath.Join(t.TempDir(), "source-manifest.json")
	writeFixtureSourceManifest(t, fixture.trustedSourceManifest, manifestForSourceEntries(t, fixture.sourceRoot(), sourceEntries))
	fixture.artifacts = append(fixture.artifacts, artifact{Name: fixture.sourceName(), Path: fixture.sourceName(), Type: "Source"})
	fixture.artifacts = append(fixture.artifacts, artifact{Name: "checksums.txt", Path: "checksums.txt", Type: "Checksum"})
	metadata := fmt.Sprintf(`{"project_name":"unifi-cli","version":%q,"commit":%q}`, smokeVersion, smokeCommit)
	if err := os.WriteFile(filepath.Join(fixture.dist, "metadata.json"), []byte(metadata), 0o644); err != nil {
		t.Fatal(err)
	}
	fixture.artifacts = append(fixture.artifacts, artifact{Name: "metadata.json", Path: "metadata.json", Type: "Metadata"})
	fixture.writeMetadata(t)
	return fixture
}

func (f *releaseFixture) clone(t *testing.T) *releaseFixture {
	t.Helper()
	clone := &releaseFixture{
		dist:                  t.TempDir(),
		artifacts:             slices.Clone(f.artifacts),
		checksums:             make(map[string]string, len(f.checksums)),
		binaries:              f.binaries,
		trustedBinaries:       f.trustedBinaries,
		trustedSourceManifest: f.trustedSourceManifest,
		sbomInventories:       f.sbomInventories,
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

func (f *releaseFixture) trustedBinaryPath(target target) string {
	return filepath.Join(f.trustedBinaries, target.goos+"_"+target.goarch, target.executableName())
}

func (f *releaseFixture) validArchiveEntries(target target) []archiveEntry {
	root := f.archiveRoot(target)
	return []archiveEntry{
		{name: root + "/" + target.executableName(), mode: 0o755, body: f.binaries[target]},
		{name: root + "/LICENSE", mode: 0o644, body: []byte("license")},
		{name: root + "/README.md", mode: 0o644, body: []byte("readme")},
		{name: root + "/CHANGELOG.md", mode: 0o644, body: []byte("changelog")},
	}
}

func (f *releaseFixture) validSourceEntries() []archiveEntry {
	return []archiveEntry{
		{name: f.sourceRoot() + "/LICENSE", mode: 0o664, body: []byte("license")},
		{name: f.sourceRoot() + "/README.md", mode: 0o664, body: []byte("readme")},
		{name: f.sourceRoot() + "/CHANGELOG.md", mode: 0o664, body: []byte("changelog")},
		{name: f.sourceRoot() + "/go.mod", mode: 0o664, body: []byte("module github.com/noahjenkins/unifi-cli")},
	}
}

func manifestForSourceEntries(t *testing.T, root string, entries []archiveEntry) sourceManifest {
	t.Helper()
	manifest := sourceManifest{SchemaVersion: "1", Commit: smokeCommit}
	for _, entry := range entries {
		if entry.typeflag == tar.TypeXGlobalHeader || entry.typeflag == tar.TypeDir {
			continue
		}
		path := strings.TrimPrefix(entry.name, root+"/")
		mode := "100644"
		if entry.mode&0o111 != 0 {
			mode = "100755"
		}
		digest := sha256.Sum256(entry.body)
		manifest.Files = append(manifest.Files, sourceManifestEntry{
			Path: path, Mode: mode, SHA256: hex.EncodeToString(digest[:]),
		})
	}
	slices.SortFunc(manifest.Files, func(a, b sourceManifestEntry) int { return strings.Compare(a.Path, b.Path) })
	return manifest
}

func writeFixtureSourceManifest(t *testing.T, path string, manifest sourceManifest) {
	t.Helper()
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
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
	entries = append([]archiveEntry{{
		name:       "pax_global_header",
		typeflag:   tar.TypeXGlobalHeader,
		paxRecords: map[string]string{"comment": smokeCommit},
	}}, entries...)
	f.replaceSourceRaw(t, entries)
}

func (f *releaseFixture) replaceSourceRaw(t *testing.T, entries []archiveEntry) {
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
		"specVersion": "1.7",
		"version":     1,
		"metadata": map[string]any{
			"component": map[string]any{"type": "file", "name": name, "version": version},
		},
		"components": []any{},
	}
	if components {
		bom["components"] = f.sbomComponents(target)
	}
	return bom
}

func (f *releaseFixture) sbomComponents(target target) []any {
	identities := make([]sbomComponentIdentity, 0, len(f.sbomInventories[target]))
	for identity := range f.sbomInventories[target] {
		identities = append(identities, identity)
	}
	slices.SortFunc(identities, func(a, b sbomComponentIdentity) int {
		if value := strings.Compare(a.Name, b.Name); value != 0 {
			return value
		}
		return strings.Compare(a.Version, b.Version)
	})
	components := make([]any, 0, len(identities)+1)
	for _, identity := range identities {
		components = append(components, map[string]any{"type": identity.Type, "name": identity.Name, "version": identity.Version})
	}
	return append(components, f.sbomFileComponent(target))
}

func (f *releaseFixture) sbomFileComponent(target target) map[string]any {
	digest := sha256.Sum256(f.binaries[target])
	return map[string]any{
		"type": "file",
		"name": f.syftExecutablePath(target),
		"hashes": []any{
			map[string]any{"alg": "SHA-256", "content": hex.EncodeToString(digest[:])},
		},
	}
}

func (f *releaseFixture) syftExecutablePath(target target) string {
	return "/private/tmp/syft-archive-contents-test/" + f.archiveRoot(target) + "/" + target.executableName()
}

func (f *releaseFixture) writeSBOM(t *testing.T, target target, bom map[string]any) {
	t.Helper()
	data, err := json.Marshal(bom)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(f.dist, f.sbomName(target))
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	f.checksums[f.sbomName(target)] = fileSHA256(t, path)
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
	if err := verifyArtifacts(ctx, fixture.dist, smokeVersion, smokeCommit, fixture.trustedBinaries, fixture.trustedSourceManifest); err != nil {
		t.Fatal(err)
	}
}

func verifyReleaseFixtureFails(t *testing.T, fixture *releaseFixture) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := verifyArtifacts(ctx, fixture.dist, smokeVersion, smokeCommit, fixture.trustedBinaries, fixture.trustedSourceManifest); err == nil {
		t.Fatal("artifact verification unexpectedly accepted invalid release")
	}
}

func writeTarGz(t *testing.T, path string, entries []archiveEntry) {
	t.Helper()
	var raw bytes.Buffer
	tarWriter := tar.NewWriter(&raw)
	for _, entry := range entries {
		typeflag := entry.typeflag
		if typeflag == 0 {
			typeflag = tar.TypeReg
		}
		if typeflag == tar.TypeXGlobalHeader {
			if err := tarWriter.WriteHeader(&tar.Header{Typeflag: typeflag, PAXRecords: entry.paxRecords}); err != nil {
				t.Fatal(err)
			}
			continue
		}
		mode := int64(entry.mode.Perm())
		if entry.mode&fs.ModeSetuid != 0 {
			mode |= 0o4000
		}
		if entry.mode&fs.ModeSetgid != 0 {
			mode |= 0o2000
		}
		if entry.mode&fs.ModeSticky != 0 {
			mode |= 0o1000
		}
		header := &tar.Header{Name: entry.name, Linkname: entry.linkname, Mode: mode, Size: int64(len(entry.body)), Typeflag: typeflag}
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
	patchPAXHeaderNames(t, raw.Bytes(), entries)
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gzipWriter := gzip.NewWriter(file)
	if _, err := gzipWriter.Write(raw.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func patchPAXHeaderNames(t *testing.T, data []byte, entries []archiveEntry) {
	t.Helper()
	names := make([]string, 0)
	for _, entry := range entries {
		if entry.typeflag == tar.TypeXGlobalHeader {
			names = append(names, entry.name)
		}
	}
	nameIndex := 0
	for offset := 0; offset+tarBlockSize <= len(data); {
		header := data[offset : offset+tarBlockSize]
		if bytes.Equal(header, make([]byte, tarBlockSize)) {
			break
		}
		sizeText := strings.Trim(string(header[124:136]), " \x00")
		size := int64(0)
		if sizeText != "" {
			var err error
			size, err = strconv.ParseInt(sizeText, 8, 64)
			if err != nil {
				t.Fatal(err)
			}
		}
		if header[156] == tar.TypeXGlobalHeader {
			if nameIndex >= len(names) {
				t.Fatal("generated an unexpected PAX global header")
			}
			name := names[nameIndex]
			nameIndex++
			if len(name) == 0 || len(name) > 100 {
				t.Fatalf("invalid test PAX header name %q", name)
			}
			clear(header[:100])
			copy(header[:100], name)
			for i := 148; i < 156; i++ {
				header[i] = ' '
			}
			checksum := 0
			for _, value := range header {
				checksum += int(value)
			}
			copy(header[148:156], fmt.Sprintf("%06o\x00 ", checksum))
		}
		offset += tarBlockSize + int((size+tarBlockSize-1)/tarBlockSize)*tarBlockSize
	}
	if nameIndex != len(names) {
		t.Fatalf("patched %d PAX global headers, want %d", nameIndex, len(names))
	}
}

const tarBlockSize = 512

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
