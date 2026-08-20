package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/noahjenkins/unifi-cli/internal/fileutil"
	"github.com/noahjenkins/unifi-cli/internal/privatefile"
)

const maxProfileMarkerBytes = 1024

type Selection struct {
	Profile string
	Path    string
}

type ProfileInfo struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Selected bool   `json:"selected"`
	Valid    bool   `json:"valid"`
	Error    string `json:"error,omitempty"`
}

type ProfileOptions struct {
	ConfigHome string
	HomeDir    string
}

type ProfileStore struct {
	configHome string
}

func NewProfileStore(options ProfileOptions) *ProfileStore {
	configHome := options.ConfigHome
	if configHome == "" {
		home := options.HomeDir
		if home == "" {
			home, _ = os.UserHomeDir()
		}
		configHome = filepath.Join(home, ".config", "unifi-cli")
	}
	return &ProfileStore{configHome: configHome}
}

func ValidateProfileName(name string) error {
	if len(name) < 1 || len(name) > 64 || name == "." || name == ".." {
		return fmt.Errorf("profile name must contain 1 to 64 safe characters")
	}
	for index, char := range []byte(name) {
		alphanumeric := (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9')
		if index == 0 && !alphanumeric {
			return fmt.Errorf("profile name must start with a letter or number")
		}
		if !alphanumeric && char != '.' && char != '_' && char != '-' {
			return fmt.Errorf("profile name may contain only letters, numbers, '.', '_', and '-'")
		}
	}
	return nil
}

func (s *ProfileStore) ProfilesDir() string {
	return filepath.Join(s.configHome, "profiles")
}

func (s *ProfileStore) MarkerPath() string {
	return filepath.Join(s.configHome, "current-profile")
}

func (s *ProfileStore) DefaultConfigPath() string {
	return filepath.Join(s.configHome, "config.yaml")
}

func (s *ProfileStore) ProfilePath(name string) (string, error) {
	if err := ValidateProfileName(name); err != nil {
		return "", err
	}
	return filepath.Join(s.ProfilesDir(), name+".yaml"), nil
}

func (s *ProfileStore) Selected() (string, bool, error) {
	path := s.MarkerPath()
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("inspect selected profile: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", false, fmt.Errorf("selected profile marker must not be a symbolic link")
	}
	data, err := fileutil.ReadRegularFile(path, maxProfileMarkerBytes)
	if err != nil {
		return "", false, fmt.Errorf("read selected profile: %w", err)
	}
	marker := string(data)
	name := strings.TrimSuffix(marker, "\n")
	if name == marker || name == "" || strings.ContainsAny(name, "\r\n") {
		return "", false, fmt.Errorf("selected profile marker must contain one profile name and a trailing newline")
	}
	if err := ValidateProfileName(name); err != nil {
		return "", false, fmt.Errorf("selected profile marker is invalid: %w", err)
	}
	return name, true, nil
}

func (s *ProfileStore) Show(name string) (ProfileInfo, Config, error) {
	path, err := s.ProfilePath(name)
	info := ProfileInfo{Name: name, Path: path}
	if err != nil {
		return info, Config{}, err
	}
	selected, ok, selectedErr := s.Selected()
	if selectedErr == nil {
		info.Selected = ok && selected == name
	}
	cfg, err := s.loadProfile(name, path)
	if err != nil {
		return info, Config{}, err
	}
	info.Valid = true
	return info, cfg, nil
}

func (s *ProfileStore) loadProfile(name, path string) (Config, error) {
	fileInfo, err := os.Lstat(path)
	if err != nil {
		return Config{}, fmt.Errorf("inspect profile %q: %w", name, err)
	}
	if fileInfo.Mode()&os.ModeSymlink != 0 {
		return Config{}, fmt.Errorf("profile %q must not be a symbolic link", name)
	}
	if !fileInfo.Mode().IsRegular() {
		return Config{}, fmt.Errorf("profile %q is not a regular file", name)
	}
	cfg, err := Load(path)
	if err != nil {
		return Config{}, fmt.Errorf("load profile %q: %w", name, err)
	}
	return cfg, nil
}

func (s *ProfileStore) List() ([]ProfileInfo, error) {
	selected, selectedExists, err := s.Selected()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(s.ProfilesDir())
	if errors.Is(err, os.ErrNotExist) {
		return []ProfileInfo{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list profiles: %w", err)
	}

	profiles := make([]ProfileInfo, 0, len(entries))
	for _, entry := range entries {
		filename := entry.Name()
		if !strings.HasSuffix(filename, ".yaml") {
			continue
		}
		name := strings.TrimSuffix(filename, ".yaml")
		info := ProfileInfo{
			Name:     name,
			Path:     filepath.Join(s.ProfilesDir(), filename),
			Selected: selectedExists && selected == name,
		}
		shown, _, showErr := s.Show(name)
		if showErr != nil {
			info.Error = showErr.Error()
		} else {
			info = shown
		}
		profiles = append(profiles, info)
	}
	sort.Slice(profiles, func(i, j int) bool { return profiles[i].Name < profiles[j].Name })
	return profiles, nil
}

func (s *ProfileStore) Select(name string) error {
	path, err := s.ProfilePath(name)
	if err != nil {
		return err
	}
	if err := privatefile.EnsureDir(s.ProfilesDir()); err != nil {
		return err
	}
	if _, err := s.loadProfile(name, path); err != nil {
		return err
	}
	if err := privatefile.EnsureDir(s.configHome); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(s.configHome, ".current-profile-*.tmp")
	if err != nil {
		return fmt.Errorf("create selected profile marker: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := privatefile.ProtectFile(temporaryPath); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.WriteString(name + "\n"); err != nil {
		temporary.Close()
		return fmt.Errorf("write selected profile marker: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync selected profile marker: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close selected profile marker: %w", err)
	}
	if err := os.Rename(temporaryPath, s.MarkerPath()); err != nil {
		return fmt.Errorf("replace selected profile marker: %w", err)
	}
	return nil
}

func ResolveSelection(explicitConfig, explicitProfile string, store *ProfileStore) (Selection, error) {
	configPath := explicitConfig
	if configPath == "" {
		configPath = os.Getenv("UNIFI_CONFIG")
	}
	profile := explicitProfile
	if profile == "" {
		profile = os.Getenv("UNIFI_PROFILE")
	}
	if configPath != "" && profile != "" {
		return Selection{}, fmt.Errorf("choose only one of config path and profile")
	}
	if configPath != "" {
		return Selection{Path: configPath}, nil
	}
	if profile != "" {
		path, err := store.ProfilePath(profile)
		if err != nil {
			return Selection{}, err
		}
		return Selection{Profile: profile, Path: path}, nil
	}
	selected, ok, err := store.Selected()
	if err != nil {
		return Selection{}, err
	}
	if ok {
		path, err := store.ProfilePath(selected)
		if err != nil {
			return Selection{}, err
		}
		return Selection{Profile: selected, Path: path}, nil
	}
	return Selection{Path: store.DefaultConfigPath()}, nil
}
