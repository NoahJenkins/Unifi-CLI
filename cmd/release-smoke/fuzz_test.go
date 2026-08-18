package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func FuzzReleaseBundleExtraction(f *testing.F) {
	f.Add([]byte("not a tar archive"), "generated")
	f.Add([]byte{}, "publication")
	f.Fuzz(func(t *testing.T, data []byte, kind string) {
		if len(data) > 1<<20 {
			t.Skip()
		}
		bundle := filepath.Join(t.TempDir(), "bundle.tar")
		if err := os.WriteFile(bundle, data, 0o600); err != nil {
			t.Fatal(err)
		}
		_ = extractBundle(bundle, filepath.Join(t.TempDir(), "out"), kind)
	})
}

func FuzzChecksumManifest(f *testing.F) {
	valid := hex.EncodeToString(make([]byte, sha256.Size)) + "  unifi.tar.gz\n"
	f.Add([]byte(valid))
	f.Add([]byte("../../unsafe  file"))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > maxReleaseManifestBytes {
			t.Skip()
		}
		path := filepath.Join(t.TempDir(), "checksums.txt")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		_, _ = readChecksums(path)
	})
}

func FuzzSBOMSnapshotParsing(f *testing.F) {
	f.Add([]byte(`{"bomFormat":"CycloneDX","specVersion":"1.7","version":1,"metadata":{"component":{"type":"file","name":"archive.tar.gz","version":"1.0.0"}},"components":[]}`))
	f.Add([]byte(`null`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			t.Skip()
		}
		path := filepath.Join(t.TempDir(), "snapshot.cdx.json")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		_ = inspectCycloneDXSBOM(path, "archive.tar.gz", "1.0.0", archiveExecutable{relativePath: "unifi"}, map[sbomComponentIdentity]struct{}{})
	})
}
