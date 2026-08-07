package config_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noahjenkins/unifi-cli/internal/config"
)

func TestLoadFromFileAndEnv(t *testing.T) {
	t.Setenv("UNIFI_HOST", "")

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte("host: 10.0.0.1\nport: 443\nsite: default\ninsecure: true\nsafe_mode: true\n")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Host != "10.0.0.1" {
		t.Fatalf("unexpected cfg: %+v", cfg)
	}
	if !cfg.SafeMode || !cfg.Insecure {
		t.Fatalf("expected safe_mode and insecure true")
	}
}

func TestLoadRejectsOversizedAndNonRegularConfiguration(t *testing.T) {
	t.Run("oversized regular file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, []byte("host: 10.0.0.1\n#"+strings.Repeat("x", 1<<20)), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := config.Load(path)
		if err == nil || !strings.Contains(err.Error(), "exceeds") {
			t.Fatalf("Load error = %v, want size-limit failure", err)
		}
	})

	t.Run("directory", func(t *testing.T) {
		_, err := config.Load(t.TempDir())
		if err == nil || !strings.Contains(err.Error(), "regular file") {
			t.Fatalf("Load error = %v, want regular-file failure", err)
		}
	})
}

func TestLoadRejectsLegacyCredentials(t *testing.T) {
	tests := []struct {
		name     string
		contents string
		env      string
	}{
		{name: "username config", contents: "host: 10.0.0.1\nusername: legacy-secret\n"},
		{name: "password config", contents: "host: 10.0.0.1\npassword: legacy-secret\n"},
		{name: "api key config", contents: "host: 10.0.0.1\napi_key: legacy-secret\n"},
		{name: "merged username config", contents: "legacy: &legacy\n  username: legacy-secret\n<<: *legacy\nhost: 10.0.0.1\n"},
		{name: "merged password config", contents: "legacy: &legacy\n  password: legacy-secret\n<<: *legacy\nhost: 10.0.0.1\n"},
		{name: "merged api key config", contents: "legacy: &legacy\n  api_key: legacy-secret\n<<: *legacy\nhost: 10.0.0.1\n"},
		{name: "username environment", contents: "host: 10.0.0.1\n", env: "UNIFI_USERNAME"},
		{name: "password environment", contents: "host: 10.0.0.1\n", env: "UNIFI_PASSWORD"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(tt.contents), 0o600); err != nil {
				t.Fatal(err)
			}
			if tt.env != "" {
				t.Setenv(tt.env, "legacy-secret")
			}

			_, err := config.Load(path)
			if err == nil {
				t.Fatal("Load unexpectedly succeeded")
			}
			if !strings.Contains(err.Error(), "no longer supported") || !strings.Contains(err.Error(), "unifi login") {
				t.Fatalf("unexpected migration error: %v", err)
			}
			if strings.Contains(err.Error(), "legacy-secret") {
				t.Fatalf("migration error leaked secret: %v", err)
			}
		})
	}
}

func TestLoadIgnoresAPIKeyEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("host: 10.0.0.1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("UNIFI_API_KEY", "")
	want, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load without API-key environment: %v", err)
	}

	t.Setenv("UNIFI_API_KEY", "legacy-secret")
	got, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load with API-key environment: %v", err)
	}
	if got != want {
		t.Fatalf("UNIFI_API_KEY changed non-secret config: got %+v, want %+v", got, want)
	}
}

func TestEnvOverridesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("host: 10.0.0.1\nsite: default\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("UNIFI_HOST", "192.168.1.1")
	t.Setenv("UNIFI_SITE", "lab")

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Host != "192.168.1.1" || cfg.Site != "lab" {
		t.Fatalf("env override failed: %+v", cfg)
	}
}

func TestLoadRejectsInvalidBooleanEnvironmentOverrides(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("host: 10.0.0.1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, env := range []string{"UNIFI_INSECURE", "UNIFI_SAFE_MODE"} {
		t.Run(env, func(t *testing.T) {
			t.Setenv(env, "definitely-not-a-boolean")
			_, err := config.Load(path)
			if err == nil {
				t.Fatal("Load unexpectedly accepted an invalid boolean override")
			}
			if !strings.Contains(err.Error(), env) {
				t.Fatalf("error %q does not identify %s", err, env)
			}
		})
	}
}

func TestLoadRejectsMalformedHosts(t *testing.T) {
	tests := []string{
		"https://controller.example",
		"controller.example/network",
		"controller.example:8443",
		"admin@controller.example",
		"controller.example?source=cli",
		" controller.example",
		"[2001:db8::1]",
	}

	for _, host := range tests {
		t.Run(host, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			contents := fmt.Sprintf("host: %q\n", host)
			if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}

			_, err := config.Load(path)
			if err == nil {
				t.Fatalf("Load unexpectedly accepted host %q", host)
			}
			if !strings.Contains(err.Error(), "host") {
				t.Fatalf("error %q does not identify host", err)
			}
		})
	}
}

func TestBaseURLConstructsIPv4AndIPv6Origins(t *testing.T) {
	tests := []struct {
		name string
		host string
		want string
	}{
		{name: "IPv4", host: "192.0.2.10", want: "https://192.0.2.10:8443"},
		{name: "IPv6", host: "2001:db8::10", want: "https://[2001:db8::10]:8443"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Config{Host: tt.host, Port: 8443}
			if got := cfg.BaseURL(); got != tt.want {
				t.Fatalf("BaseURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLoadRejectsInvalidPortAndTimeout(t *testing.T) {
	tests := []struct {
		name     string
		contents string
		env      string
		value    string
		field    string
	}{
		{name: "zero port in file", contents: "host: controller.example\nport: 0\n", field: "port"},
		{name: "negative port in file", contents: "host: controller.example\nport: -1\n", field: "port"},
		{name: "oversized port in file", contents: "host: controller.example\nport: 65536\n", field: "port"},
		{name: "zero timeout in file", contents: "host: controller.example\ntimeout: 0s\n", field: "timeout"},
		{name: "negative timeout in file", contents: "host: controller.example\ntimeout: -1s\n", field: "timeout"},
		{name: "zero port in environment", contents: "host: controller.example\n", env: "UNIFI_PORT", value: "0", field: "port"},
		{name: "oversized port in environment", contents: "host: controller.example\n", env: "UNIFI_PORT", value: "65536", field: "port"},
		{name: "zero timeout in environment", contents: "host: controller.example\n", env: "UNIFI_TIMEOUT", value: "0s", field: "timeout"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(tt.contents), 0o600); err != nil {
				t.Fatal(err)
			}
			if tt.env != "" {
				t.Setenv(tt.env, tt.value)
			}

			_, err := config.Load(path)
			if err == nil {
				t.Fatal("Load unexpectedly accepted invalid configuration")
			}
			if !strings.Contains(strings.ToLower(err.Error()), tt.field) {
				t.Fatalf("error %q does not identify %s", err, tt.field)
			}
		})
	}
}

func TestLoadRejectsConflictingTLSSettings(t *testing.T) {
	tests := []struct {
		name     string
		contents string
		env      map[string]string
	}{
		{
			name:     "file settings",
			contents: "host: controller.example\ninsecure: true\nca_cert: /tmp/controller-ca.pem\n",
		},
		{
			name:     "environment settings",
			contents: "host: controller.example\n",
			env: map[string]string{
				"UNIFI_INSECURE": "true",
				"UNIFI_CA_CERT":  "/tmp/controller-ca.pem",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(tt.contents), 0o600); err != nil {
				t.Fatal(err)
			}
			for name, value := range tt.env {
				t.Setenv(name, value)
			}

			_, err := config.Load(path)
			if err == nil {
				t.Fatal("Load unexpectedly accepted insecure=true with a custom CA")
			}
			if !strings.Contains(err.Error(), "ca_cert") || !strings.Contains(err.Error(), "insecure") {
				t.Fatalf("conflict error = %q, want ca_cert and insecure", err)
			}
		})
	}
}
