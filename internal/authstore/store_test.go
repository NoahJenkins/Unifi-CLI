package authstore

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/noahjenkins/unifi-cli/internal/privatefile/privatefiletest"
	gokeyring "github.com/zalando/go-keyring"
)

type memoryKeyring struct {
	entries     map[string]string
	getErr      error
	setErr      error
	deleteErr   error
	getCalls    int
	setCalls    int
	deleteCalls int
}

func (k *memoryKeyring) Get(service, account string) (string, error) {
	k.getCalls++
	if k.getErr != nil {
		return "", k.getErr
	}
	v, ok := k.entries[service+":"+account]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

func (k *memoryKeyring) Set(service, account, value string) error {
	k.setCalls++
	if k.setErr != nil {
		return k.setErr
	}
	k.entries[service+":"+account] = value
	return nil
}

func (k *memoryKeyring) Delete(service, account string) error {
	k.deleteCalls++
	if k.deleteErr != nil {
		return k.deleteErr
	}
	delete(k.entries, service+":"+account)
	return nil
}

func newMemoryKeyring() *memoryKeyring {
	return &memoryKeyring{entries: make(map[string]string)}
}

func TestNewStoreImplementsStoreInterface(t *testing.T) {
	var _ Store = NewStore(Options{Keyring: newMemoryKeyring()})
}

func TestSaveLoadNormalizesControllerAndStoresAPIKeyRecord(t *testing.T) {
	keyring := newMemoryKeyring()
	store := NewStore(Options{Keyring: keyring, StateHome: t.TempDir(), GOOS: "linux"})

	if err := store.Save("HTTPS://Controller.Example:443/", "api-key-secret", false); err != nil {
		t.Fatalf("Save: %v", err)
	}
	key, found, err := store.Load("https://controller.example:443")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !found || key != "api-key-secret" {
		t.Fatalf("Load = key %q, found %t", key, found)
	}

	encoded := keyring.entries[serviceName+":"+controllerAccount("https://controller.example:443")]
	if strings.Contains(encoded, "\"cookies\"") || !strings.Contains(encoded, "\"api_key\"") {
		t.Fatalf("keyring value was not an API-key record: %q", encoded)
	}
}

func TestKeysAreScopedToNormalizedController(t *testing.T) {
	keyring := newMemoryKeyring()
	store := NewStore(Options{Keyring: keyring, StateHome: t.TempDir(), GOOS: "linux"})
	if err := store.Save("https://controller-a.example:443", "key-a", false); err != nil {
		t.Fatalf("Save: %v", err)
	}
	key, found, err := store.Load("https://controller-b.example:443")
	if err != nil || found || key != "" {
		t.Fatalf("Load isolated controller = key %q, found %t, err %v", key, found, err)
	}
}

func TestSavePrefersKeyringAndCleansLegacyFallback(t *testing.T) {
	keyring := newMemoryKeyring()
	store := NewStore(Options{Keyring: keyring, StateHome: t.TempDir(), GOOS: "linux"})
	controller := "https://controller.example:443"
	if err := os.MkdirAll(filepath.Dir(store.legacyFallbackPath(controller)), 0o700); err != nil {
		t.Fatalf("MkdirAll legacy fallback: %v", err)
	}
	if err := os.WriteFile(store.legacyFallbackPath(controller), []byte(`{"controller":"https://controller.example:443","csrf":"legacy"}`), 0o600); err != nil {
		t.Fatalf("WriteFile legacy fallback: %v", err)
	}

	if err := store.Save(controller, "api-key-secret", true); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if keyring.setCalls != 1 {
		t.Fatalf("keyring Set calls = %d, want 1", keyring.setCalls)
	}
	if _, err := os.Stat(store.fallbackPath(controller)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("current fallback exists or stat failed: %v", err)
	}
	if _, err := os.Stat(store.legacyFallbackPath(controller)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy fallback exists or stat failed: %v", err)
	}
}

func TestSaveUsesFallbackOnlyWhenExplicitlyAllowed(t *testing.T) {
	keyring := newMemoryKeyring()
	keyring.setErr = ErrKeyringUnavailable
	store := NewStore(Options{Keyring: keyring, StateHome: t.TempDir(), GOOS: "linux"})
	controller := "https://controller.example:443"

	if err := store.Save(controller, "api-key-secret", false); !errors.Is(err, ErrKeyringUnavailable) {
		t.Fatalf("Save without fallback error = %v, want ErrKeyringUnavailable", err)
	}
	if _, err := os.Stat(store.fallbackPath(controller)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("fallback file exists or stat failed: %v", err)
	}
	if err := store.Save(controller, "api-key-secret", true); err != nil {
		t.Fatalf("Save with fallback: %v", err)
	}
	key, found, err := store.Load(controller)
	if err != nil || !found || key != "api-key-secret" {
		t.Fatalf("Load fallback = key %q, found %t, err %v", key, found, err)
	}
}

func TestLoadRejectsOversizedFallbackRecord(t *testing.T) {
	store := NewStore(Options{Keyring: &memoryKeyring{getErr: ErrKeyringUnavailable}, StateHome: t.TempDir(), GOOS: "linux"})
	controller := "https://controller.example:443"
	path := store.fallbackPath(controller)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	record, err := json.Marshal(apiKeyRecord{Controller: controller, APIKey: strings.Repeat("x", (1<<20)+1)})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, record, 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err = store.Load(controller)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("Load error = %v, want bounded-record failure", err)
	}
}

func TestFallbackSaveRemovesLegacySessionFallback(t *testing.T) {
	keyring := newMemoryKeyring()
	keyring.setErr = ErrKeyringUnavailable
	store := NewStore(Options{Keyring: keyring, StateHome: t.TempDir(), GOOS: "linux"})
	controller := "https://controller.example:443"
	legacyPath := store.legacyFallbackPath(controller)
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o700); err != nil {
		t.Fatalf("MkdirAll legacy fallback: %v", err)
	}
	if err := os.WriteFile(legacyPath, []byte(`{"controller":"https://controller.example:443","csrf":"legacy"}`), 0o600); err != nil {
		t.Fatalf("WriteFile legacy fallback: %v", err)
	}

	if err := store.Save(controller, "api-key-secret", true); err != nil {
		t.Fatalf("Save fallback: %v", err)
	}
	key, found, err := store.Load(controller)
	if err != nil || !found || key != "api-key-secret" {
		t.Fatalf("Load current fallback = key %q, found %t, err %v", key, found, err)
	}
	if _, err := os.Stat(legacyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy fallback exists or stat failed: %v", err)
	}
}

func TestFallbackUsesProtectedPermissionsAndAtomicReplacement(t *testing.T) {
	keyring := newMemoryKeyring()
	keyring.setErr = ErrKeyringUnavailable
	store := NewStore(Options{Keyring: keyring, StateHome: t.TempDir(), GOOS: "linux"})
	controller := "https://controller.example:443"
	if err := store.Save(controller, "old-api-key", true); err != nil {
		t.Fatalf("initial Save: %v", err)
	}
	if err := store.Save(controller, "new-api-key", true); err != nil {
		t.Fatalf("replacement Save: %v", err)
	}

	path := store.fallbackPath(controller)
	privatefiletest.AssertFile(t, path)
	privatefiletest.AssertDir(t, filepath.Dir(path))
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("ReadDir fallback directory: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(path) {
		t.Fatalf("atomic replacement left unexpected files: %#v", entries)
	}
	key, found, err := store.Load(controller)
	if err != nil || !found || key != "new-api-key" {
		t.Fatalf("Load replacement = key %q, found %t, err %v", key, found, err)
	}
}

func TestLoadFallbackRepairsPrivatePermissions(t *testing.T) {
	keyring := newMemoryKeyring()
	keyring.getErr = ErrKeyringUnavailable
	store := NewStore(Options{Keyring: keyring, StateHome: t.TempDir(), GOOS: "linux"})
	controller := "https://controller.example:443"
	path := store.fallbackPath(controller)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	record := `{"controller":"https://controller.example:443","api_key":"api-key-secret"}`
	if err := os.WriteFile(path, []byte(record), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}

	key, found, err := store.Load(controller)
	if err != nil || !found || key != "api-key-secret" {
		t.Fatalf("Load fallback = key %q, found %t, err %v", key, found, err)
	}
	privatefiletest.AssertDir(t, filepath.Dir(path))
	privatefiletest.AssertFile(t, path)
}

func TestFallbackProtectionFailureLeavesNoAPIKeyFile(t *testing.T) {
	keyring := newMemoryKeyring()
	keyring.setErr = ErrKeyringUnavailable
	store := NewStore(Options{Keyring: keyring, StateHome: t.TempDir(), GOOS: "linux"})
	store.protectFile = func(string) error { return errors.New("protect denied") }

	err := store.Save("https://controller.example:443", "api-key-secret", true)
	if err == nil || !strings.Contains(err.Error(), "protect fallback API-key file") {
		t.Fatalf("Save error = %v, want protected-file failure", err)
	}
	entries, readErr := os.ReadDir(store.fallbackDir())
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("fallback directory contains files after protection failure: %#v", entries)
	}
}

func TestLoadRejectsLegacyOrMalformedSessionRecords(t *testing.T) {
	keyring := newMemoryKeyring()
	store := NewStore(Options{Keyring: keyring, StateHome: t.TempDir(), GOOS: "linux"})
	controller := "https://controller.example:443"
	legacy := `{"controller":"https://controller.example:443","cookies":[{"name":"TOKEN","value":"session-secret"}],"csrf":"csrf-secret"}`
	keyring.entries[serviceName+":"+controllerAccount(controller)] = legacy
	if err := os.MkdirAll(filepath.Dir(store.legacyFallbackPath(controller)), 0o700); err != nil {
		t.Fatalf("MkdirAll legacy fallback: %v", err)
	}
	if err := os.WriteFile(store.legacyFallbackPath(controller), []byte(legacy), 0o600); err != nil {
		t.Fatalf("WriteFile legacy fallback: %v", err)
	}

	key, found, err := store.Load(controller)
	if err != nil || found || key != "" {
		t.Fatalf("legacy record was usable: key=%q found=%t err=%v", key, found, err)
	}
	keyring.entries[serviceName+":"+controllerAccount(controller)] = "not-json"
	key, found, err = store.Load(controller)
	if err != nil || found || key != "" {
		t.Fatalf("malformed record was usable: key=%q found=%t err=%v", key, found, err)
	}
}

func TestLoadUsesCurrentFallbackWhenKeyringContainsLegacySession(t *testing.T) {
	keyring := newMemoryKeyring()
	store := NewStore(Options{Keyring: keyring, StateHome: t.TempDir(), GOOS: "linux"})
	controller := "https://controller.example:443"
	keyring.entries[serviceName+":"+controllerAccount(controller)] = `{"controller":"https://controller.example:443","cookies":[{"name":"TOKEN"}],"csrf":"legacy"}`
	if err := store.writeFallback(apiKeyRecord{Controller: controller, APIKey: "fallback-api-key"}); err != nil {
		t.Fatalf("write current fallback: %v", err)
	}

	key, found, err := store.Load(controller)
	if err != nil || !found || key != "fallback-api-key" {
		t.Fatalf("Load current fallback = key %q, found %t, err %v", key, found, err)
	}
}

func TestDeleteRemovesKeyringCurrentAndLegacyFallbacks(t *testing.T) {
	keyring := newMemoryKeyring()
	store := NewStore(Options{Keyring: keyring, StateHome: t.TempDir(), GOOS: "linux"})
	controller := "https://controller.example:443"
	if err := store.Save(controller, "api-key-secret", false); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := store.writeFallback(apiKeyRecord{Controller: controller, APIKey: "fallback-key"}); err != nil {
		t.Fatalf("write current fallback: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(store.legacyFallbackPath(controller)), 0o700); err != nil {
		t.Fatalf("MkdirAll legacy fallback: %v", err)
	}
	if err := os.WriteFile(store.legacyFallbackPath(controller), []byte(`{"controller":"https://controller.example:443"}`), 0o600); err != nil {
		t.Fatalf("WriteFile legacy fallback: %v", err)
	}

	if err := store.Delete(controller); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, found, err := store.Load(controller); err != nil || found {
		t.Fatalf("Load after Delete = found %t, err %v", found, err)
	}
	for _, path := range []string{store.fallbackPath(controller), store.legacyFallbackPath(controller)} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("fallback still exists or stat failed at %s: %v", path, err)
		}
	}
	if err := store.Delete(controller); err != nil {
		t.Fatalf("Delete missing data: %v", err)
	}
}

func TestDeleteReportsUnavailableAfterFallbackCleanupWhenNativeKeyMayRemain(t *testing.T) {
	keyring := newMemoryKeyring()
	store := NewStore(Options{Keyring: keyring, StateHome: t.TempDir(), GOOS: "linux"})
	controller := "https://controller.example:443"
	account := serviceName + ":" + controllerAccount(controller)
	keyring.entries[account] = `{"controller":"https://controller.example:443","api_key":"retained-native-key"}`
	if err := store.writeFallback(apiKeyRecord{Controller: controller, APIKey: "fallback-key"}); err != nil {
		t.Fatalf("write current fallback: %v", err)
	}
	keyring.deleteErr = ErrKeyringUnavailable

	err := store.Delete(controller)
	if !errors.Is(err, ErrKeyringUnavailable) {
		t.Fatalf("Delete error = %v, want ErrKeyringUnavailable", err)
	}
	if _, found := keyring.entries[account]; !found {
		t.Fatal("test keyring did not retain the native key after deletion failure")
	}
	if _, statErr := os.Stat(store.fallbackPath(controller)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("fallback was not cleaned up: %v", statErr)
	}
}

func TestDeleteAttemptsLegacyCleanupWhenCurrentFallbackRemovalFails(t *testing.T) {
	store := NewStore(Options{Keyring: newMemoryKeyring(), StateHome: t.TempDir(), GOOS: "linux"})
	controller := "https://controller.example:443"
	if err := os.MkdirAll(store.fallbackPath(controller), 0o700); err != nil {
		t.Fatalf("MkdirAll current fallback blocker: %v", err)
	}
	if err := os.WriteFile(filepath.Join(store.fallbackPath(controller), "blocks-removal"), []byte("not key material"), 0o600); err != nil {
		t.Fatalf("WriteFile current fallback blocker: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(store.legacyFallbackPath(controller)), 0o700); err != nil {
		t.Fatalf("MkdirAll legacy fallback: %v", err)
	}
	if err := os.WriteFile(store.legacyFallbackPath(controller), []byte(`{"controller":"https://controller.example:443"}`), 0o600); err != nil {
		t.Fatalf("WriteFile legacy fallback: %v", err)
	}

	if err := store.Delete(controller); err == nil {
		t.Fatal("Delete succeeded despite current fallback cleanup failure")
	}
	if _, err := os.Stat(store.legacyFallbackPath(controller)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy fallback was not removed after current cleanup failure: %v", err)
	}
}

func TestLoadRejectsControllerMismatch(t *testing.T) {
	keyring := newMemoryKeyring()
	store := NewStore(Options{Keyring: keyring, StateHome: t.TempDir(), GOOS: "linux"})
	controller := "https://controller.example:443"
	encoded, err := json.Marshal(apiKeyRecord{Controller: "https://other.example:443", APIKey: "api-key-secret"})
	if err != nil {
		t.Fatalf("Marshal record: %v", err)
	}
	keyring.entries[serviceName+":"+controllerAccount(controller)] = string(encoded)

	key, found, err := store.Load(controller)
	if err != nil || found || key != "" {
		t.Fatalf("mismatched record was usable: key=%q found=%t err=%v", key, found, err)
	}
}

func TestMapKeyringErrorClassifiesNativeErrors(t *testing.T) {
	if err := mapKeyringError(nil); err != nil {
		t.Fatalf("mapKeyringError(nil) = %v, want nil", err)
	}
	if err := mapKeyringError(gokeyring.ErrNotFound); !errors.Is(err, ErrNotFound) {
		t.Fatalf("mapKeyringError(not found) = %v, want ErrNotFound", err)
	}
	err := mapKeyringError(errors.New("dbus session unavailable"))
	if !errors.Is(err, ErrKeyringUnavailable) || errors.Is(err, ErrNotFound) {
		t.Fatalf("mapKeyringError(backend failure) = %v, want only ErrKeyringUnavailable", err)
	}
}

func TestNewStoreAppliesOSDefaults(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	store := NewStore(Options{})
	if _, ok := store.keyring.(systemKeyring); !ok {
		t.Fatalf("default keyring = %T, want systemKeyring", store.keyring)
	}
	if store.goos != runtime.GOOS {
		t.Fatalf("default GOOS = %q, want %q", store.goos, runtime.GOOS)
	}
	if store.homeDir != home {
		t.Fatalf("default home = %q, want %q", store.homeDir, home)
	}
}

func TestStateDirFollowsPlatformConventions(t *testing.T) {
	home := filepath.Join("synthetic", "home")
	stateHome := filepath.Join("synthetic", "state")
	envHome := filepath.Join("synthetic", "env")
	tests := []struct {
		name      string
		goos      string
		stateHome string
		env       map[string]string
		want      string
	}{
		{name: "darwin ignores state home", goos: "darwin", stateHome: stateHome, want: filepath.Join(home, "Library", "Application Support", serviceName, "keys")},
		{name: "windows explicit state home", goos: "windows", stateHome: stateHome, env: map[string]string{"LOCALAPPDATA": envHome}, want: filepath.Join(stateHome, serviceName, "keys")},
		{name: "windows LOCALAPPDATA", goos: "windows", env: map[string]string{"LOCALAPPDATA": envHome}, want: filepath.Join(envHome, serviceName, "keys")},
		{name: "windows home default", goos: "windows", env: map[string]string{"LOCALAPPDATA": ""}, want: filepath.Join(home, "AppData", "Local", serviceName, "keys")},
		{name: "linux explicit state home", goos: "linux", stateHome: stateHome, env: map[string]string{"XDG_STATE_HOME": envHome}, want: filepath.Join(stateHome, serviceName, "keys")},
		{name: "linux XDG_STATE_HOME", goos: "linux", env: map[string]string{"XDG_STATE_HOME": envHome}, want: filepath.Join(envHome, serviceName, "keys")},
		{name: "linux home default", goos: "linux", env: map[string]string{"XDG_STATE_HOME": ""}, want: filepath.Join(home, ".local", "state", serviceName, "keys")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for name, value := range tt.env {
				t.Setenv(name, value)
			}
			store := NewStore(Options{Keyring: newMemoryKeyring(), StateHome: tt.stateHome, HomeDir: home, GOOS: tt.goos})
			if got := store.fallbackDir(); got != tt.want {
				t.Fatalf("fallbackDir() = %q, want %q", got, tt.want)
			}
			wantLegacy := filepath.Join(filepath.Dir(tt.want), "sessions")
			if got := store.legacyFallbackDir(); got != wantLegacy {
				t.Fatalf("legacyFallbackDir() = %q, want %q", got, wantLegacy)
			}
		})
	}
}

func TestNormalizeController(t *testing.T) {
	valid := []struct {
		input string
		want  string
	}{
		{input: "https://controller.example", want: "https://controller.example:443"},
		{input: "http://controller.example", want: "http://controller.example:80"},
		{input: "HTTP://Controller.Example:8080/", want: "http://controller.example:8080"},
		{input: "https://[2001:DB8::1]", want: "https://[2001:db8::1]:443"},
	}
	for _, tt := range valid {
		t.Run(tt.input, func(t *testing.T) {
			got, err := NormalizeController(tt.input)
			if err != nil || got != tt.want {
				t.Fatalf("NormalizeController(%q) = %q, %v; want %q", tt.input, got, err, tt.want)
			}
		})
	}

	invalid := []struct {
		input string
		want  string
	}{
		{input: "https://[2001:db8::1", want: "parse controller URL"},
		{input: "controller.example", want: "only scheme, host"},
		{input: "https://user@controller.example", want: "only scheme, host"},
		{input: "https://controller.example/network", want: "only scheme, host"},
		{input: "https://controller.example/?site=default", want: "only scheme, host"},
		{input: "https://controller.example/#fragment", want: "only scheme, host"},
		{input: "ftp://controller.example", want: "unsupported controller URL scheme"},
		{input: "https://:8443", want: "host is required"},
	}
	for _, tt := range invalid {
		t.Run(tt.input, func(t *testing.T) {
			got, err := NormalizeController(tt.input)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("NormalizeController(%q) = %q, %v; want error containing %q", tt.input, got, err, tt.want)
			}
		})
	}
}

func TestStoreRejectsInvalidControllerBeforeKeyringAccess(t *testing.T) {
	keyring := newMemoryKeyring()
	store := NewStore(Options{Keyring: keyring, StateHome: t.TempDir(), GOOS: "linux"})
	controller := "https://controller.example/network"

	if _, _, err := store.Load(controller); err == nil {
		t.Fatal("Load accepted a controller URL with a path")
	}
	if err := store.Save(controller, "api-key-secret", true); err == nil {
		t.Fatal("Save accepted a controller URL with a path")
	}
	if err := store.Delete(controller); err == nil {
		t.Fatal("Delete accepted a controller URL with a path")
	}
	if keyring.getCalls+keyring.setCalls+keyring.deleteCalls != 0 {
		t.Fatalf("keyring was called for an invalid controller: get=%d set=%d delete=%d", keyring.getCalls, keyring.setCalls, keyring.deleteCalls)
	}
	if store.fallbackPath(controller) != "" || store.legacyFallbackPath(controller) != "" {
		t.Fatal("fallback paths were derived for an invalid controller")
	}
}

func TestSaveRejectsEmptyAPIKey(t *testing.T) {
	keyring := newMemoryKeyring()
	store := NewStore(Options{Keyring: keyring, StateHome: t.TempDir(), GOOS: "linux"})
	if err := store.Save("https://controller.example:443", "", true); err == nil || !strings.Contains(err.Error(), "API key is required") {
		t.Fatalf("Save empty key error = %v, want required-key failure", err)
	}
	if keyring.setCalls != 0 {
		t.Fatalf("keyring Set calls = %d, want 0", keyring.setCalls)
	}
}

func TestLoadReportsUnavailableKeyringWithoutFallback(t *testing.T) {
	store := NewStore(Options{Keyring: &memoryKeyring{getErr: ErrKeyringUnavailable}, StateHome: t.TempDir(), GOOS: "linux"})
	key, found, err := store.Load("https://controller.example:443")
	if !errors.Is(err, ErrKeyringUnavailable) || found || key != "" {
		t.Fatalf("Load = key %q, found %t, err %v; want ErrKeyringUnavailable", key, found, err)
	}
}

func TestUnexpectedKeyringErrorsFailClosedWithoutDetail(t *testing.T) {
	backendErr := errors.New("backend detail api-key-secret")
	controller := "https://controller.example:443"

	t.Run("load ignores fallback", func(t *testing.T) {
		store := NewStore(Options{Keyring: &memoryKeyring{getErr: backendErr}, StateHome: t.TempDir(), GOOS: "linux"})
		if err := store.writeFallback(apiKeyRecord{Controller: controller, APIKey: "fallback-api-key"}); err != nil {
			t.Fatalf("write current fallback: %v", err)
		}
		key, found, err := store.Load(controller)
		if err == nil || found || key != "" {
			t.Fatalf("Load = key %q, found %t, err %v; want failure", key, found, err)
		}
		if errors.Is(err, ErrKeyringUnavailable) || strings.Contains(err.Error(), "api-key-secret") {
			t.Fatalf("Load error = %v, want generic failure without backend detail", err)
		}
	})

	t.Run("save does not write fallback", func(t *testing.T) {
		keyring := newMemoryKeyring()
		keyring.setErr = backendErr
		store := NewStore(Options{Keyring: keyring, StateHome: t.TempDir(), GOOS: "linux"})
		err := store.Save(controller, "api-key-secret", true)
		if err == nil || errors.Is(err, ErrKeyringUnavailable) || strings.Contains(err.Error(), "api-key-secret") {
			t.Fatalf("Save error = %v, want generic failure without backend detail", err)
		}
		if _, statErr := os.Stat(store.fallbackPath(controller)); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("fallback was written after an unexpected keyring error: %v", statErr)
		}
	})

	t.Run("delete still cleans fallback", func(t *testing.T) {
		keyring := newMemoryKeyring()
		keyring.deleteErr = backendErr
		store := NewStore(Options{Keyring: keyring, StateHome: t.TempDir(), GOOS: "linux"})
		if err := store.writeFallback(apiKeyRecord{Controller: controller, APIKey: "fallback-api-key"}); err != nil {
			t.Fatalf("write current fallback: %v", err)
		}
		err := store.Delete(controller)
		if err == nil || errors.Is(err, ErrKeyringUnavailable) || strings.Contains(err.Error(), "api-key-secret") {
			t.Fatalf("Delete error = %v, want generic failure without backend detail", err)
		}
		if _, statErr := os.Stat(store.fallbackPath(controller)); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("fallback was not cleaned up: %v", statErr)
		}
	})
}

func TestLoadFallbackProtectionFailuresReturnErrors(t *testing.T) {
	controller := "https://controller.example:443"
	tests := []struct {
		name      string
		configure func(*KeyringStore)
		want      string
	}{
		{
			name:      "directory",
			configure: func(s *KeyringStore) { s.ensureDir = func(string) error { return errors.New("directory denied") } },
			want:      "protect fallback API-key directory",
		},
		{
			name:      "file",
			configure: func(s *KeyringStore) { s.protectFile = func(string) error { return errors.New("file denied") } },
			want:      "protect fallback API-key file",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewStore(Options{Keyring: &memoryKeyring{getErr: ErrKeyringUnavailable}, StateHome: t.TempDir(), GOOS: "linux"})
			if err := store.writeFallback(apiKeyRecord{Controller: controller, APIKey: "fallback-api-key"}); err != nil {
				t.Fatalf("write current fallback: %v", err)
			}
			tt.configure(store)
			key, found, err := store.Load(controller)
			if err == nil || !strings.Contains(err.Error(), tt.want) || found || key != "" {
				t.Fatalf("Load = key %q, found %t, err %v; want %q", key, found, err, tt.want)
			}
		})
	}
}

func TestLoadReportsFallbackStatFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows reports a file path component as not found")
	}
	stateHome := t.TempDir()
	store := NewStore(Options{Keyring: &memoryKeyring{getErr: ErrKeyringUnavailable}, StateHome: stateHome, GOOS: "linux"})
	if err := os.WriteFile(filepath.Join(stateHome, serviceName), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, found, err := store.Load("https://controller.example:443")
	if err == nil || !strings.Contains(err.Error(), "stat fallback API key") || found {
		t.Fatalf("Load = found %t, err %v; want stat failure", found, err)
	}
}

func TestLoadIgnoresFallbackRecordForAnotherController(t *testing.T) {
	store := NewStore(Options{Keyring: &memoryKeyring{getErr: ErrNotFound}, StateHome: t.TempDir(), GOOS: "linux"})
	controller := "https://controller.example:443"
	path := store.fallbackPath(controller)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	record := `{"controller":"https://other.example:443","api_key":"api-key-secret"}`
	if err := os.WriteFile(path, []byte(record), 0o600); err != nil {
		t.Fatal(err)
	}

	key, found, err := store.Load(controller)
	if err != nil || found || key != "" {
		t.Fatalf("mismatched fallback was usable: key=%q found=%t err=%v", key, found, err)
	}
}

func TestSaveNativeReportsFallbackCleanupFailure(t *testing.T) {
	store := NewStore(Options{Keyring: newMemoryKeyring(), StateHome: t.TempDir(), GOOS: "linux"})
	controller := "https://controller.example:443"
	blocker := store.fallbackPath(controller)
	if err := os.MkdirAll(blocker, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blocker, "blocks-removal"), []byte("not key material"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := store.Save(controller, "api-key-secret", false)
	if err == nil || !strings.Contains(err.Error(), "remove fallback API key") {
		t.Fatalf("Save error = %v, want fallback cleanup failure", err)
	}
}

func TestFallbackSaveFailures(t *testing.T) {
	controller := "https://controller.example:443"

	t.Run("directory protection", func(t *testing.T) {
		keyring := newMemoryKeyring()
		keyring.setErr = ErrKeyringUnavailable
		store := NewStore(Options{Keyring: keyring, StateHome: t.TempDir(), GOOS: "linux"})
		store.ensureDir = func(string) error { return errors.New("directory denied") }
		err := store.Save(controller, "api-key-secret", true)
		if err == nil || !strings.Contains(err.Error(), "protect fallback API-key directory") {
			t.Fatalf("Save error = %v, want directory-protection failure", err)
		}
	})

	t.Run("temporary file creation", func(t *testing.T) {
		keyring := newMemoryKeyring()
		keyring.setErr = ErrKeyringUnavailable
		store := NewStore(Options{Keyring: keyring, StateHome: t.TempDir(), GOOS: "linux"})
		store.ensureDir = func(string) error { return nil }
		err := store.Save(controller, "api-key-secret", true)
		if err == nil || !strings.Contains(err.Error(), "create fallback API-key file") {
			t.Fatalf("Save error = %v, want temporary-file failure", err)
		}
	})

	t.Run("replacement", func(t *testing.T) {
		keyring := newMemoryKeyring()
		keyring.setErr = ErrKeyringUnavailable
		store := NewStore(Options{Keyring: keyring, StateHome: t.TempDir(), GOOS: "linux"})
		blocker := store.fallbackPath(controller)
		if err := os.MkdirAll(blocker, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(blocker, "blocks-replacement"), []byte("not key material"), 0o600); err != nil {
			t.Fatal(err)
		}
		err := store.Save(controller, "api-key-secret", true)
		if err == nil || !strings.Contains(err.Error(), "replace fallback API key") {
			t.Fatalf("Save error = %v, want replacement failure", err)
		}
		entries, readErr := os.ReadDir(store.fallbackDir())
		if readErr != nil {
			t.Fatal(readErr)
		}
		if len(entries) != 1 || entries[0].Name() != filepath.Base(blocker) {
			t.Fatalf("failed replacement left temporary files: %#v", entries)
		}
	})

	t.Run("legacy cleanup", func(t *testing.T) {
		keyring := newMemoryKeyring()
		keyring.setErr = ErrKeyringUnavailable
		store := NewStore(Options{Keyring: keyring, StateHome: t.TempDir(), GOOS: "linux"})
		blocker := store.legacyFallbackPath(controller)
		if err := os.MkdirAll(blocker, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(blocker, "blocks-removal"), []byte("not key material"), 0o600); err != nil {
			t.Fatal(err)
		}
		err := store.Save(controller, "api-key-secret", true)
		if err == nil || !strings.Contains(err.Error(), "remove legacy fallback API key") {
			t.Fatalf("Save error = %v, want legacy cleanup failure", err)
		}
	})
}
