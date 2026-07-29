package authstore

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat fallback: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("fallback file mode = %o, want 600", got)
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("Stat fallback directory: %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("fallback directory mode = %o, want 700", got)
	}
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
