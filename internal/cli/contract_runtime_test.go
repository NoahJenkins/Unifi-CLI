package cli

import (
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"

	"github.com/noahjenkins/unifi-cli/internal/client"
	"github.com/noahjenkins/unifi-cli/internal/config"
	"github.com/noahjenkins/unifi-cli/internal/testutil/contractapi"
)

func contractRuntime(t *testing.T, version string) *contractapi.Server {
	t.Helper()
	profile, err := contractapi.ProfileFor(version)
	if err != nil {
		t.Fatal(err)
	}
	validator, err := contractapi.NewValidator(profile, os.Getenv("UNIFI_CONTRACT_CACHE"))
	if err != nil {
		t.Fatal(err)
	}
	server := contractapi.NewServer(t, profile, validator)
	for _, key := range []string{"UNIFI_CONFIG", "UNIFI_PROFILE", "UNIFI_HOST", "UNIFI_PORT", "UNIFI_SITE", "UNIFI_TIMEOUT", "UNIFI_INSECURE", "UNIFI_CA_CERT", "UNIFI_SAFE_MODE", "UNIFI_API_KEY", "UNIFI_USERNAME", "UNIFI_PASSWORD"} {
		t.Setenv(key, "")
	}
	savedConfig, savedProfile, savedSite, savedTimeout := flagConfig, flagProfile, flagSite, flagTimeout
	savedJSON, savedYes, savedDry, savedForce, savedQuiet, savedExperimental := flagJSON, flagYes, flagDryRun, flagForce, flagQuiet, flagExperimental
	originalClient, originalProfiles := newRuntimeClient, newProfileStore
	t.Cleanup(func() {
		flagConfig, flagProfile, flagSite, flagTimeout = savedConfig, savedProfile, savedSite, savedTimeout
		flagJSON, flagYes, flagDryRun, flagForce, flagQuiet, flagExperimental = savedJSON, savedYes, savedDry, savedForce, savedQuiet, savedExperimental
		newRuntimeClient, newProfileStore = originalClient, originalProfiles
	})
	dir := t.TempDir()
	caPath := filepath.Join(dir, "test-ca.pem")
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.HTTP.Certificate().Raw})
	if err := os.WriteFile(caPath, ca, 0600); err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(server.HTTP.URL)
	host, portText, _ := net.SplitHostPort(u.Host)
	port, _ := strconv.Atoi(portText)
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(fmt.Sprintf("host: %s\nport: %d\nsite: default\ninsecure: false\nca_cert: %q\nsafe_mode: true\ntimeout: 5s\n", host, port, caPath)), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("UNIFI_CONFIG", cfgPath)
	newProfileStore = func() *config.ProfileStore {
		return config.NewProfileStore(config.ProfileOptions{ConfigHome: filepath.Join(dir, "profiles")})
	}
	newRuntimeClient = func(cfg config.Config) (*client.Client, error) {
		if cfg.Host != host || cfg.Port != port || cfg.CACert != caPath || cfg.Insecure || !cfg.SafeMode {
			return nil, fmt.Errorf("contract runtime isolation violated")
		}
		// Production transport has no proxy and refuses all redirects. The sole
		// configured origin is this server; no saved credential store is opened.
		return client.NewWithAPIKey(cfg, "synthetic-test-key", "contract_test")
	}
	return server
}
func contractCommand(t *testing.T, wantOK bool, args ...string) map[string]any {
	t.Helper()
	root := NewRoot()
	root.SetArgs(append([]string{"--json", "--quiet"}, args...))
	stdout, _, err := captureProcessOutput(t, root.Execute)
	var envelope map[string]any
	if json.Unmarshal([]byte(stdout), &envelope) != nil {
		t.Fatalf("command did not return JSON; error=%v", err)
	}
	if (err == nil) != wantOK || envelope["ok"] != wantOK {
		t.Fatalf("command success=%t, want %t; error=%v; code=%v", err == nil, wantOK, err, envelope["error"])
	}
	assertSchemaV1Document(t, envelope)
	if envelope["schema_version"] != "1" {
		t.Fatal("wrong envelope version")
	}
	if _, ok := envelope["data"]; !ok {
		t.Fatal("missing data field")
	}
	return envelope
}
func applied(args ...string) []string { return append(args, "--yes", "--experimental", "--force") }
func preserved(t *testing.T, s *contractapi.Server, resource, id string, want map[string]any) {
	t.Helper()
	if !reflect.DeepEqual(s.Snapshot(resource, id), want) {
		t.Fatalf("%s final state differs from independent seed plus intended changes", resource)
	}
}
