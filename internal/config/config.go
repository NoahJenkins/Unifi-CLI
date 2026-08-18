package config

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/noahjenkins/unifi-cli/internal/fileutil"
)

const maxConfigBytes = 1 << 20

type Config struct {
	Host     string        `yaml:"host"`
	Port     int           `yaml:"port"`
	Insecure bool          `yaml:"insecure"`
	CACert   string        `yaml:"ca_cert"`
	Site     string        `yaml:"site"`
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
	data, err := fileutil.ReadRegularFile(path, maxConfigBytes)
	if err == nil {
		var fields map[string]yaml.Node
		if err := yaml.Unmarshal(data, &fields); err != nil {
			return Config{}, fmt.Errorf("parse config: %w", err)
		}
		for _, name := range []string{"username", "password", "api_key"} {
			if _, ok := fields[name]; ok {
				return Config{}, legacyCredentialError(name)
			}
		}
		decoder := yaml.NewDecoder(bytes.NewReader(data))
		decoder.KnownFields(true)
		if err := decoder.Decode(&cfg); err != nil {
			return Config{}, fmt.Errorf("parse config: %w", err)
		}
		var trailing any
		if err := decoder.Decode(&trailing); err == nil {
			return Config{}, fmt.Errorf("parse config: multiple YAML documents are not allowed")
		} else if err != io.EOF {
			return Config{}, fmt.Errorf("parse config: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return Config{}, err
	}
	// timeout may unmarshal as nanoseconds if int; support string via custom if needed later
	overrideString(&cfg.Host, "UNIFI_HOST")
	overrideString(&cfg.CACert, "UNIFI_CA_CERT")
	overrideString(&cfg.Site, "UNIFI_SITE")
	for _, name := range []string{"UNIFI_USERNAME", "UNIFI_PASSWORD"} {
		if os.Getenv(name) != "" {
			return Config{}, legacyCredentialError(name)
		}
	}
	if v := os.Getenv("UNIFI_PORT"); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("UNIFI_PORT: %w", err)
		}
		cfg.Port = p
	}
	if v := os.Getenv("UNIFI_INSECURE"); v != "" {
		parsed, err := strconv.ParseBool(v)
		if err != nil {
			return Config{}, fmt.Errorf("UNIFI_INSECURE: %w", err)
		}
		cfg.Insecure = parsed
	}
	if v := os.Getenv("UNIFI_SAFE_MODE"); v != "" {
		parsed, err := strconv.ParseBool(v)
		if err != nil {
			return Config{}, fmt.Errorf("UNIFI_SAFE_MODE: %w", err)
		}
		cfg.SafeMode = parsed
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
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	if err := validateHost(c.Host); err != nil {
		return err
	}
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535")
	}
	if c.Timeout <= 0 {
		return fmt.Errorf("timeout must be greater than zero")
	}
	if c.Insecure && c.CACert != "" {
		return fmt.Errorf("ca_cert cannot be used with insecure=true")
	}
	return nil
}

func validateHost(host string) error {
	if host == "" || host != strings.TrimSpace(host) {
		return fmt.Errorf("host must be a bare hostname or IP address")
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return nil
	}
	if len(host) > 253 || strings.ContainsAny(host, ":/?#@[]\\") {
		return fmt.Errorf("host must be a bare hostname or IP address")
	}

	name := strings.TrimSuffix(host, ".")
	if name == "" {
		return fmt.Errorf("host must be a bare hostname or IP address")
	}
	for _, label := range strings.Split(name, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return fmt.Errorf("host must be a bare hostname or IP address")
		}
		for _, ch := range label {
			if (ch < 'a' || ch > 'z') && (ch < 'A' || ch > 'Z') && (ch < '0' || ch > '9') && ch != '-' {
				return fmt.Errorf("host must be a bare hostname or IP address")
			}
		}
	}
	return nil
}

func legacyCredentialError(name string) error {
	return fmt.Errorf("config %q is no longer supported; remove it and run 'unifi login'", name)
}

func overrideString(dst *string, env string) {
	if v := os.Getenv(env); v != "" {
		*dst = v
	}
}

func (c Config) BaseURL() string {
	return (&url.URL{
		Scheme: "https",
		Host:   net.JoinHostPort(c.Host, strconv.Itoa(c.Port)),
	}).String()
}
