package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noahjenkins/unifi-cli/internal/config"
	"github.com/noahjenkins/unifi-cli/internal/privatefile/privatefiletest"
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
	privatefiletest.AssertFile(t, marker)
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
	privatefiletest.AssertDir(t, profilesDir)
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

func TestNewProfileStoreDefaultsToHomeConfigDirectory(t *testing.T) {
	explicitHome := t.TempDir()
	store := config.NewProfileStore(config.ProfileOptions{HomeDir: explicitHome})
	if got, want := store.DefaultConfigPath(), filepath.Join(explicitHome, ".config", "unifi-cli", "config.yaml"); got != want {
		t.Fatalf("DefaultConfigPath() = %q, want %q", got, want)
	}

	userHome := t.TempDir()
	t.Setenv("HOME", userHome)
	t.Setenv("USERPROFILE", userHome)
	store = config.NewProfileStore(config.ProfileOptions{})
	if got, want := store.MarkerPath(), filepath.Join(userHome, ".config", "unifi-cli", "current-profile"); got != want {
		t.Fatalf("MarkerPath() = %q, want %q", got, want)
	}
}

func TestProfileStoreRejectsInvalidProfileNames(t *testing.T) {
	configHome := t.TempDir()
	store := config.NewProfileStore(config.ProfileOptions{ConfigHome: configHome})
	for _, name := range []string{"", "../escape", "home/lab"} {
		if _, err := store.ProfilePath(name); err == nil {
			t.Errorf("ProfilePath(%q) unexpectedly succeeded", name)
		}
		if _, _, err := store.Show(name); err == nil {
			t.Errorf("Show(%q) unexpectedly succeeded", name)
		}
		if err := store.Select(name); err == nil {
			t.Errorf("Select(%q) unexpectedly succeeded", name)
		}
	}
	if _, err := os.Stat(store.MarkerPath()); !os.IsNotExist(err) {
		t.Fatalf("invalid selection wrote a marker or stat failed: %v", err)
	}
}

func TestProfileStoreRejectsMissingAndNonRegularProfiles(t *testing.T) {
	configHome := t.TempDir()
	profilesDir := filepath.Join(configHome, "profiles")
	if err := os.MkdirAll(filepath.Join(profilesDir, "folder.yaml"), 0o700); err != nil {
		t.Fatal(err)
	}
	store := config.NewProfileStore(config.ProfileOptions{ConfigHome: configHome})

	if _, _, err := store.Show("missing"); err == nil || !strings.Contains(err.Error(), "inspect profile") {
		t.Fatalf("Show(missing) error = %v, want inspect failure", err)
	}
	if err := store.Select("missing"); err == nil {
		t.Fatal("Select(missing) unexpectedly succeeded")
	}
	if _, _, err := store.Show("folder"); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("Show(folder) error = %v, want regular-file failure", err)
	}
	if err := store.Select("folder"); err == nil {
		t.Fatal("Select(folder) unexpectedly succeeded")
	}
	if _, err := os.Stat(store.MarkerPath()); !os.IsNotExist(err) {
		t.Fatalf("failed selection wrote a marker or stat failed: %v", err)
	}
}

func TestProfileStoreListHandlesMissingDirectoryAndOtherFiles(t *testing.T) {
	configHome := t.TempDir()
	store := config.NewProfileStore(config.ProfileOptions{ConfigHome: configHome})
	profiles, err := store.List()
	if err != nil || profiles == nil || len(profiles) != 0 {
		t.Fatalf("List without profiles directory = %#v, %v; want empty list", profiles, err)
	}

	profilesDir := filepath.Join(configHome, "profiles")
	if err := os.MkdirAll(profilesDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, contents := range map[string]string{
		"home.yaml":    "host: 192.0.2.10\n",
		"notes.txt":    "not a profile\n",
		"backup.yml":   "host: 192.0.2.11\n",
		"invalid.yaml": "host: 192.0.2.12\nport: 0\n",
	} {
		if err := os.WriteFile(filepath.Join(profilesDir, name), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	profiles, err = store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(profiles) != 2 || profiles[0].Name != "home" || !profiles[0].Valid || profiles[1].Name != "invalid" || profiles[1].Valid || profiles[1].Error == "" {
		t.Fatalf("unexpected profiles: %+v", profiles)
	}
}

func TestProfileStoreListReportsMarkerAndDirectoryErrors(t *testing.T) {
	t.Run("malformed marker", func(t *testing.T) {
		configHome := t.TempDir()
		if err := os.WriteFile(filepath.Join(configHome, "current-profile"), []byte("home"), 0o600); err != nil {
			t.Fatal(err)
		}
		store := config.NewProfileStore(config.ProfileOptions{ConfigHome: configHome})
		if _, err := store.List(); err == nil || !strings.Contains(err.Error(), "trailing newline") {
			t.Fatalf("List error = %v, want marker failure", err)
		}
	})

	t.Run("profiles path is a file", func(t *testing.T) {
		configHome := t.TempDir()
		if err := os.WriteFile(filepath.Join(configHome, "profiles"), []byte("not a directory"), 0o600); err != nil {
			t.Fatal(err)
		}
		store := config.NewProfileStore(config.ProfileOptions{ConfigHome: configHome})
		if _, err := store.List(); err == nil || !strings.Contains(err.Error(), "list profiles") {
			t.Fatalf("List error = %v, want directory failure", err)
		}
	})
}

func TestProfileStoreSelectedRejectsUnsafeMarkers(t *testing.T) {
	t.Run("directory", func(t *testing.T) {
		configHome := t.TempDir()
		if err := os.MkdirAll(filepath.Join(configHome, "current-profile"), 0o700); err != nil {
			t.Fatal(err)
		}
		store := config.NewProfileStore(config.ProfileOptions{ConfigHome: configHome})
		if _, _, err := store.Selected(); err == nil || !strings.Contains(err.Error(), "read selected profile") {
			t.Fatalf("Selected error = %v, want read failure", err)
		}
	})

	t.Run("symbolic link", func(t *testing.T) {
		configHome := t.TempDir()
		target := filepath.Join(configHome, "target")
		if err := os.WriteFile(target, []byte("home\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(configHome, "current-profile")); err != nil {
			t.Skipf("symbolic links unavailable: %v", err)
		}
		store := config.NewProfileStore(config.ProfileOptions{ConfigHome: configHome})
		if _, _, err := store.Selected(); err == nil || !strings.Contains(err.Error(), "symbolic link") {
			t.Fatalf("Selected error = %v, want symbolic-link rejection", err)
		}
	})

	for _, contents := range []string{"home", "\n"} {
		t.Run("contents "+strings.ReplaceAll(contents, "\n", "newline"), func(t *testing.T) {
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

func TestResolveSelectionRejectsInvalidProfileAndMarker(t *testing.T) {
	t.Setenv("UNIFI_CONFIG", "")
	t.Setenv("UNIFI_PROFILE", "")

	configHome := t.TempDir()
	store := config.NewProfileStore(config.ProfileOptions{ConfigHome: configHome})
	if _, err := config.ResolveSelection("", "../escape", store); err == nil {
		t.Fatal("ResolveSelection accepted an invalid profile name")
	}

	if err := os.WriteFile(filepath.Join(configHome, "current-profile"), []byte("../escape\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.ResolveSelection("", "", store); err == nil || !strings.Contains(err.Error(), "marker is invalid") {
		t.Fatalf("ResolveSelection error = %v, want invalid-marker failure", err)
	}
}
