package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noahjenkins/unifi-cli/internal/config"
)

func TestValidateProfileName(t *testing.T) {
	for _, name := range []string{"home", "lab-1", "office_main", "site.example", "A1"} {
		if err := config.ValidateProfileName(name); err != nil {
			t.Errorf("ValidateProfileName(%q): %v", name, err)
		}
	}
	for _, name := range []string{"", ".", "..", "-home", "_home", ".home", "home lab", "home/lab", `home\\lab`, "höme", strings.Repeat("a", 65)} {
		if err := config.ValidateProfileName(name); err == nil {
			t.Errorf("ValidateProfileName(%q) unexpectedly succeeded", name)
		}
	}
}

func TestProfileStoreListsShowsAndSelectsSafely(t *testing.T) {
	configHome := t.TempDir()
	profilesDir := filepath.Join(configHome, "profiles")
	if err := os.MkdirAll(profilesDir, 0o700); err != nil {
		t.Fatal(err)
	}
	homePath := filepath.Join(profilesDir, "home.yaml")
	if err := os.WriteFile(homePath, []byte("host: 192.0.2.10\nsite: default\nsafe_mode: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profilesDir, "broken.yaml"), []byte("host: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(homePath, filepath.Join(profilesDir, "link.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configHome, "current-profile"), []byte("home\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	store := config.NewProfileStore(config.ProfileOptions{ConfigHome: configHome})
	profiles, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(profiles) != 3 {
		t.Fatalf("List returned %d profiles, want 3: %+v", len(profiles), profiles)
	}
	if profiles[0].Name != "broken" || profiles[0].Valid || profiles[1].Name != "home" || !profiles[1].Valid || !profiles[1].Selected || profiles[2].Name != "link" || profiles[2].Valid {
		t.Fatalf("unexpected profiles: %+v", profiles)
	}

	info, cfg, err := store.Show("home")
	if err != nil {
		t.Fatalf("Show: %v", err)
	}
	if !info.Valid || !info.Selected || cfg.Host != "192.0.2.10" {
		t.Fatalf("unexpected Show result: info=%+v cfg=%+v", info, cfg)
	}
	if _, _, err := store.Show("link"); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("Show(link) error = %v, want symbolic-link rejection", err)
	}

	marker := filepath.Join(configHome, "current-profile")
	before, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Select("broken"); err == nil {
		t.Fatal("Select(broken) unexpectedly succeeded")
	}
	after, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("failed selection changed marker: before=%q after=%q", before, after)
	}
	if err := store.Select("home"); err != nil {
		t.Fatalf("Select(home): %v", err)
	}
	markerInfo, err := os.Stat(marker)
	if err != nil {
		t.Fatal(err)
	}
	if markerInfo.Mode().Perm() != 0o600 {
		t.Fatalf("marker mode = %o, want 600", markerInfo.Mode().Perm())
	}
}

func TestProfileStoreSelectRepairsMalformedMarker(t *testing.T) {
	configHome := t.TempDir()
	profilesDir := filepath.Join(configHome, "profiles")
	if err := os.MkdirAll(profilesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profilesDir, "home.yaml"), []byte("host: 192.0.2.10\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(configHome, "current-profile")
	if err := os.WriteFile(marker, []byte("../invalid\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	store := config.NewProfileStore(config.ProfileOptions{ConfigHome: configHome})
	shown, _, err := store.Show("home")
	if err != nil || !shown.Valid || shown.Selected {
		t.Fatalf("Show(home) with malformed marker = %+v, %v", shown, err)
	}
	if err := store.Select("home"); err != nil {
		t.Fatalf("Select(home): %v", err)
	}
	selected, ok, err := store.Selected()
	if err != nil || !ok || selected != "home" {
		t.Fatalf("Selected = %q, %t, %v", selected, ok, err)
	}
	info, err := os.Stat(profilesDir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("profiles directory mode = %o, want 700", info.Mode().Perm())
	}
}

func TestProfileStoreRejectsMarkerWhitespaceAndExtraLines(t *testing.T) {
	for _, contents := range []string{" home\n", "home \n", "home\nextra\n", "home\n\n"} {
		t.Run(strings.ReplaceAll(contents, "\n", "newline"), func(t *testing.T) {
			configHome := t.TempDir()
			if err := os.WriteFile(filepath.Join(configHome, "current-profile"), []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
			store := config.NewProfileStore(config.ProfileOptions{ConfigHome: configHome})
			if _, _, err := store.Selected(); err == nil {
				t.Fatalf("Selected unexpectedly accepted %q", contents)
			}
		})
	}
}

func TestResolveSelectionPrecedenceAndConflicts(t *testing.T) {
	configHome := t.TempDir()
	profilesDir := filepath.Join(configHome, "profiles")
	if err := os.MkdirAll(profilesDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"flag", "environment", "selected"} {
		if err := os.WriteFile(filepath.Join(profilesDir, name+".yaml"), []byte("host: 192.0.2.10\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(configHome, "current-profile"), []byte("selected\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := config.NewProfileStore(config.ProfileOptions{ConfigHome: configHome})
	flagConfig := filepath.Join(configHome, "flag.yaml")
	envConfig := filepath.Join(configHome, "environment.yaml")

	tests := []struct {
		name        string
		flagConfig  string
		envConfig   string
		flagProfile string
		envProfile  string
		wantPath    string
		wantProfile string
		wantErr     bool
	}{
		{name: "config flag beats config environment", flagConfig: flagConfig, envConfig: envConfig, wantPath: flagConfig},
		{name: "config environment", envConfig: envConfig, wantPath: envConfig},
		{name: "profile flag beats profile environment", flagProfile: "flag", envProfile: "environment", wantPath: filepath.Join(profilesDir, "flag.yaml"), wantProfile: "flag"},
		{name: "profile environment", envProfile: "environment", wantPath: filepath.Join(profilesDir, "environment.yaml"), wantProfile: "environment"},
		{name: "selected marker", wantPath: filepath.Join(profilesDir, "selected.yaml"), wantProfile: "selected"},
		{name: "config and profile flags conflict", flagConfig: flagConfig, flagProfile: "flag", wantErr: true},
		{name: "config environment and profile flag conflict", envConfig: envConfig, flagProfile: "flag", wantErr: true},
		{name: "config flag and profile environment conflict", flagConfig: flagConfig, envProfile: "environment", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("UNIFI_CONFIG", tt.envConfig)
			t.Setenv("UNIFI_PROFILE", tt.envProfile)
			got, err := config.ResolveSelection(tt.flagConfig, tt.flagProfile, store)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ResolveSelection unexpectedly succeeded: %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveSelection: %v", err)
			}
			if got.Path != tt.wantPath || got.Profile != tt.wantProfile {
				t.Fatalf("ResolveSelection = %+v, want path=%q profile=%q", got, tt.wantPath, tt.wantProfile)
			}
		})
	}
}

func TestResolveSelectionUsesDefaultWithoutMarker(t *testing.T) {
	t.Setenv("UNIFI_CONFIG", "")
	t.Setenv("UNIFI_PROFILE", "")
	configHome := t.TempDir()
	store := config.NewProfileStore(config.ProfileOptions{ConfigHome: configHome})
	got, err := config.ResolveSelection("", "", store)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(configHome, "config.yaml")
	if got.Path != want || got.Profile != "" {
		t.Fatalf("ResolveSelection = %+v, want path=%q", got, want)
	}
}
