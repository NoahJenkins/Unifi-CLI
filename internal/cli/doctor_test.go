package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noahjenkins/unifi-cli/internal/authstore"
	"github.com/noahjenkins/unifi-cli/internal/buildinfo"
	"github.com/noahjenkins/unifi-cli/internal/client"
	"github.com/noahjenkins/unifi-cli/internal/config"
)

type doctorAuthStore struct {
	key   string
	found bool
	err   error
	loads int
}

func (s *doctorAuthStore) Load(string) (string, bool, error) {
	s.loads++
	return s.key, s.found, s.err
}
func (*doctorAuthStore) Save(string, string, bool) error { return errors.New("unexpected save") }
func (*doctorAuthStore) Delete(string) error             { return errors.New("unexpected delete") }

func TestDoctorCommandIsDiscoverable(t *testing.T) {
	root := newRoot(buildinfo.Info{Version: "v1.1.0", Commit: "abc123"})
	command, _, err := root.Find([]string{"doctor"})
	if err != nil || command.Name() != "doctor" {
		t.Fatalf("doctor unavailable: command=%v err=%v", command, err)
	}
}

func TestDoctorReportsLocalReadinessWithoutConstructingClient(t *testing.T) {
	tests := []struct {
		name              string
		config            string
		environmentKey    string
		storeKey          string
		wantTLS           string
		wantCredential    string
		wantCredentialUse int
	}{
		{name: "system roots and saved key", config: "host: 192.0.2.10\nsite: lab\n", storeKey: "saved-secret", wantTLS: "system_roots", wantCredential: "saved_api_key", wantCredentialUse: 1},
		{name: "custom CA and environment key", config: "host: 192.0.2.10\nca_cert: /tmp/controller-ca.pem\n", environmentKey: "environment-secret", wantTLS: "custom_ca", wantCredential: "environment_api_key"},
		{name: "insecure and saved key", config: "host: 192.0.2.10\ninsecure: true\n", storeKey: "saved-secret", wantTLS: "insecure", wantCredential: "saved_api_key", wantCredentialUse: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(tt.config), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("UNIFI_API_KEY", tt.environmentKey)
			isolateDoctorEnvironment(t)
			store := &doctorAuthStore{key: tt.storeKey, found: tt.storeKey != ""}
			withDoctorSeams(t, store)

			out, execErr := executeDoctor(t, buildinfo.Info{Version: "v1.1.0", Commit: "abc123"}, "--config", path, "--json", "doctor")
			if execErr != nil {
				t.Fatalf("doctor: %v\n%s", execErr, out)
			}
			var envelope struct {
				OK   bool         `json:"ok"`
				Data DoctorResult `json:"data"`
			}
			if err := json.Unmarshal([]byte(out), &envelope); err != nil {
				t.Fatalf("decode doctor output: %v\n%s", err, out)
			}
			got := envelope.Data
			if !envelope.OK || !got.Ready || got.Version != "v1.1.0" || got.Commit != "abc123" || got.ConfigPath != path || got.Profile != "" || got.Host != "192.0.2.10" || got.TLSMode != tt.wantTLS || got.CredentialSource != tt.wantCredential {
				t.Fatalf("unexpected doctor result: %+v", got)
			}
			if store.loads != tt.wantCredentialUse {
				t.Fatalf("credential store loads = %d, want %d", store.loads, tt.wantCredentialUse)
			}
			for _, secret := range []string{tt.environmentKey, tt.storeKey} {
				if secret != "" && strings.Contains(out, secret) {
					t.Fatalf("doctor output leaked credential %q", secret)
				}
			}
		})
	}
}

func TestDoctorReportsCredentialReadinessFailuresSafely(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("host: 192.0.2.10\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("UNIFI_API_KEY", "")
	isolateDoctorEnvironment(t)

	tests := []struct {
		name       string
		store      *doctorAuthStore
		wantSource string
		wantCode   string
		wantHint   string
	}{
		{name: "missing", store: &doctorAuthStore{}, wantSource: "missing", wantCode: "not_authenticated", wantHint: "unifi login"},
		{name: "keyring unavailable", store: &doctorAuthStore{err: authstore.ErrKeyringUnavailable}, wantSource: "keyring_unavailable", wantCode: "internal", wantHint: "credential store"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withDoctorSeams(t, tt.store)
			out, execErr := executeDoctor(t, buildinfo.Info{Version: "v1.1.0", Commit: "abc123"}, "--config", path, "--json", "doctor")
			if execErr == nil {
				t.Fatalf("doctor unexpectedly ready:\n%s", out)
			}
			if !strings.Contains(out, `"code": "`+tt.wantCode+`"`) || !strings.Contains(out, tt.wantHint) {
				t.Fatalf("unexpected doctor failure:\n%s", out)
			}
			if strings.Contains(out, "saved-secret") {
				t.Fatal("doctor failure leaked a credential")
			}
		})
	}
}

func TestDoctorRejectsInvalidConfigurationLocally(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("host: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("UNIFI_API_KEY", "local-secret")
	isolateDoctorEnvironment(t)
	store := &doctorAuthStore{}
	withDoctorSeams(t, store)

	out, execErr := executeDoctor(t, buildinfo.Info{Version: "v1.1.0", Commit: "abc123"}, "--config", path, "--json", "doctor")
	if execErr == nil || !strings.Contains(out, `"code": "validation_failed"`) {
		t.Fatalf("invalid config result: err=%v\n%s", execErr, out)
	}
	if store.loads != 0 || strings.Contains(out, "local-secret") {
		t.Fatalf("invalid config accessed credential store or leaked secret: loads=%d\n%s", store.loads, out)
	}
}

func withDoctorSeams(t *testing.T, store authstore.Store) {
	t.Helper()
	previousStore := newAuthStore
	previousClient := newRuntimeClient
	newAuthStore = func() authstore.Store { return store }
	newRuntimeClient = func(config.Config) (*client.Client, error) {
		panic("doctor constructed an HTTP client")
	}
	t.Cleanup(func() {
		newAuthStore = previousStore
		newRuntimeClient = previousClient
	})
}

func isolateDoctorEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{"UNIFI_CONFIG", "UNIFI_PROFILE", "UNIFI_HOST", "UNIFI_PORT", "UNIFI_INSECURE", "UNIFI_CA_CERT", "UNIFI_SITE", "UNIFI_SAFE_MODE", "UNIFI_TIMEOUT", "UNIFI_USERNAME", "UNIFI_PASSWORD"} {
		t.Setenv(name, "")
	}
}

func executeDoctor(t *testing.T, info buildinfo.Info, args ...string) (string, error) {
	t.Helper()
	root := newRoot(info)
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&output)
	root.SetArgs(args)
	err := root.Execute()
	return output.String(), err
}
