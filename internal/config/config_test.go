package config_test

import (
	"os"
	"path/filepath"
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
	t.Setenv("UNIFI_USERNAME", "admin")
	t.Setenv("UNIFI_PASSWORD", "secret")
	t.Setenv("UNIFI_API_KEY", "")

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Host != "10.0.0.1" || cfg.Username != "admin" || cfg.Password != "secret" {
		t.Fatalf("unexpected cfg: %+v", cfg)
	}
	if !cfg.SafeMode || !cfg.Insecure {
		t.Fatalf("expected safe_mode and insecure true")
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
