package main

import (
	"archive/tar"
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestExtractBundleUsesAnIsolatedValidatedNamespace(t *testing.T) {
	bundle := filepath.Join(t.TempDir(), "generated.tar")
	writeTestBundle(t, bundle, []tar.Header{
		{Name: "dist", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "dist/artifacts.json", Typeflag: tar.TypeReg, Mode: 0o644, Size: 2},
	}, [][]byte{nil, []byte("{}")})
	destination := filepath.Join(t.TempDir(), "generated")
	if err := extractBundle(bundle, destination, "generated"); err != nil {
		t.Fatalf("extract valid generated bundle: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(destination, "dist", "artifacts.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "{}" {
		t.Fatalf("extracted data = %q", data)
	}
}

func TestExtractBundleRejectsGeneratedTrustAnchorOverwrite(t *testing.T) {
	bundle := filepath.Join(t.TempDir(), "generated.tar")
	writeTestBundle(t, bundle, []tar.Header{
		{Name: "dist", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "dist/artifacts.json", Typeflag: tar.TypeReg, Mode: 0o644, Size: 2},
		{Name: "unifi-trusted/linux_amd64/unifi", Typeflag: tar.TypeReg, Mode: 0o755, Size: 7},
	}, [][]byte{nil, []byte("{}"), []byte("hostile")})
	err := extractBundle(bundle, filepath.Join(t.TempDir(), "generated"), "generated")
	if err == nil || !strings.Contains(err.Error(), "outside the generated bundle namespace") {
		t.Fatalf("trust-anchor overwrite error = %v", err)
	}
}

func TestExtractBundleRejectsLinksTraversalAndDuplicateEntries(t *testing.T) {
	tests := []struct {
		name    string
		headers []tar.Header
		bodies  [][]byte
		want    string
	}{
		{
			name: "traversal",
			headers: []tar.Header{
				{Name: "dist", Typeflag: tar.TypeDir, Mode: 0o755},
				{Name: "dist/../unifi-trusted", Typeflag: tar.TypeReg, Mode: 0o644, Size: 1},
			},
			bodies: [][]byte{nil, []byte("x")},
			want:   "unsafe bundle path",
		},
		{
			name: "symlink",
			headers: []tar.Header{
				{Name: "dist", Typeflag: tar.TypeDir, Mode: 0o755},
				{Name: "dist/link", Typeflag: tar.TypeSymlink, Mode: 0o777, Linkname: "../../trusted"},
			},
			bodies: [][]byte{nil, nil},
			want:   "unsupported bundle entry type",
		},
		{
			name: "duplicate",
			headers: []tar.Header{
				{Name: "dist", Typeflag: tar.TypeDir, Mode: 0o755},
				{Name: "dist/value", Typeflag: tar.TypeReg, Mode: 0o644, Size: 1},
				{Name: "dist/value", Typeflag: tar.TypeReg, Mode: 0o644, Size: 1},
			},
			bodies: [][]byte{nil, []byte("a"), []byte("b")},
			want:   "duplicate bundle entry",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bundle := filepath.Join(t.TempDir(), "bundle.tar")
			writeTestBundle(t, bundle, tt.headers, tt.bodies)
			err := extractBundle(bundle, filepath.Join(t.TempDir(), "generated"), "generated")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("extract error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestExtractBundleRejectsSymlinkedBundleFile(t *testing.T) {
	dir := t.TempDir()
	realBundle := filepath.Join(dir, "real.tar")
	writeTestBundle(t, realBundle, []tar.Header{
		{Name: "dist", Typeflag: tar.TypeDir, Mode: 0o755},
	}, [][]byte{nil})
	linkedBundle := filepath.Join(dir, "linked.tar")
	if err := os.Symlink(realBundle, linkedBundle); err != nil {
		t.Fatal(err)
	}
	err := extractBundle(linkedBundle, filepath.Join(t.TempDir(), "generated"), "generated")
	if err == nil || !strings.Contains(err.Error(), "not a bounded regular file") {
		t.Fatalf("symlinked bundle error = %v", err)
	}
}

func TestCreateBundleFileCannotEscapeThroughDestinationSymlink(t *testing.T) {
	destination := t.TempDir()
	outside := t.TempDir()
	if err := os.Mkdir(filepath.Join(destination, "dist"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(destination, "dist", "escape")); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(destination)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	file, err := createBundleFile(root, "dist/escape/hostile", 0o600)
	if err == nil {
		_ = file.Close()
		t.Fatal("root-scoped extraction followed a symlink outside the destination")
	}
	if _, err := os.Stat(filepath.Join(outside, "hostile")); !os.IsNotExist(err) {
		t.Fatalf("outside file status = %v", err)
	}
}

func writeTestBundle(t *testing.T, path string, headers []tar.Header, bodies [][]byte) {
	t.Helper()
	var buffer bytes.Buffer
	w := tar.NewWriter(&buffer)
	for i := range headers {
		header := headers[i]
		if err := w.WriteHeader(&header); err != nil {
			t.Fatal(err)
		}
		if len(bodies[i]) > 0 {
			if _, err := w.Write(bodies[i]); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buffer.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestNativeCommandVerificationAcceptsPopulatedReleaseMetadata(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	root, err := repositoryRoot()
	if err != nil {
		t.Fatal(err)
	}
	native := target{goos: runtime.GOOS, goarch: runtime.GOARCH}
	binary := filepath.Join(t.TempDir(), native.executableName())
	if err := buildTarget(ctx, root, binary, native); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int64
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	defer server.Close()
	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(serverURL.Port())
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{
		"UNIFI_API_KEY":   "hostile-key",
		"UNIFI_CA_CERT":   filepath.Join(t.TempDir(), "hostile-ca.pem"),
		"UNIFI_CONFIG":    filepath.Join(t.TempDir(), "hostile-config.yaml"),
		"UNIFI_HOST":      serverURL.Hostname(),
		"UNIFI_INSECURE":  "true",
		"UNIFI_PORT":      strconv.Itoa(port),
		"UNIFI_SAFE_MODE": "false",
		"UNIFI_SITE":      "hostile-site",
		"UNIFI_TIMEOUT":   "1ns",
	} {
		t.Setenv(key, value)
	}
	if err := verifyNativeCommands(ctx, root, binary, smokeVersion, smokeCommit, ""); err != nil {
		t.Fatalf("verify native command contract with populated release build date: %v", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("config-only native smoke contacted hostile controller %d times", requests.Load())
	}
}
