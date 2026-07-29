package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noahjenkins/unifi-cli/internal/config"
)

func TestLoadFromFileAndEnv(t *testing.T) {
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
	t.Setenv("UNIFI_API_KEY", "legacy-secret")

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.APIKey != "" {
		t.Fatalf("API key environment changed config: %+v", cfg)
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
