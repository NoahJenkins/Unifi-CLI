package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Host     string        `yaml:"host"`
	Port     int           `yaml:"port"`
	Insecure bool          `yaml:"insecure"`
	Site     string        `yaml:"site"`
	Username string        `yaml:"username"`
	Password string        `yaml:"password"`
	APIKey   string        `yaml:"api_key"`
	SafeMode bool          `yaml:"safe_mode"`
	Timeout  time.Duration `yaml:"timeout"`
}

func DefaultPath() string {
	if v := os.Getenv("UNIFI_CONFIG"); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "unifi-cli", "config.yaml")
}

func Load(path string) (Config, error) {
	if path == "" {
		path = DefaultPath()
	}
	cfg := Config{
		Port:     443,
		Site:     "default",
		SafeMode: true,
		Timeout:  30 * time.Second,
	}
	data, err := os.ReadFile(path)
	if err == nil {
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return Config{}, fmt.Errorf("parse config: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return Config{}, err
	}
	// timeout may unmarshal as nanoseconds if int; support string via custom if needed later
	overrideString(&cfg.Host, "UNIFI_HOST")
	overrideString(&cfg.Site, "UNIFI_SITE")
	overrideString(&cfg.Username, "UNIFI_USERNAME")
	overrideString(&cfg.Password, "UNIFI_PASSWORD")
	overrideString(&cfg.APIKey, "UNIFI_API_KEY")
	if v := os.Getenv("UNIFI_PORT"); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("UNIFI_PORT: %w", err)
		}
		cfg.Port = p
	}
	if v := os.Getenv("UNIFI_INSECURE"); v != "" {
		cfg.Insecure = v == "true" || v == "1"
	}
	if v := os.Getenv("UNIFI_SAFE_MODE"); v != "" {
		cfg.SafeMode = v == "true" || v == "1"
	}
	if v := os.Getenv("UNIFI_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, fmt.Errorf("UNIFI_TIMEOUT: %w", err)
		}
		cfg.Timeout = d
	}
	if cfg.Host == "" {
		return Config{}, fmt.Errorf("host is required (config host or UNIFI_HOST)")
	}
	if cfg.Port == 0 {
		cfg.Port = 443
	}
	return cfg, nil
}

func overrideString(dst *string, env string) {
	if v := os.Getenv(env); v != "" {
		*dst = v
	}
}

func (c Config) BaseURL() string {
	return fmt.Sprintf("https://%s:%d", c.Host, c.Port)
}
