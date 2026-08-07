package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"runtime"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

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
