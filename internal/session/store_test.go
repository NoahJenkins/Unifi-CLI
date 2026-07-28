package session

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func sampleSession(controller string) Session {
	return Session{
		Controller: controller,
		Cookies: []RequestCookie{{
			Name:     "TOKEN",
			Value:    "session-secret",
			Path:     "/",
			Domain:   "controller.example",
			Secure:   true,
			HTTPOnly: true,
			SameSite: http.SameSiteStrictMode,
		}},
		CSRF:      "csrf-secret",
		UpdatedAt: time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC),
	}
}

func TestSaveLoadRoundTripPreservesRequestCookieFields(t *testing.T) {
	keyring := newMemoryKeyring()
	store := NewStore(Options{Keyring: keyring, StateHome: t.TempDir(), GOOS: "linux"})
	want := sampleSession("HTTPS://Controller.Example:443/")

	if err := store.Save(want, false); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, found, err := store.Load("https://controller.example:443")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !found {
		t.Fatal("Load did not find the saved session")
	}
	if got.Controller != "https://controller.example:443" {
		t.Fatalf("controller = %q, want normalized URL", got.Controller)
	}
	if got.CSRF != want.CSRF || !got.UpdatedAt.Equal(want.UpdatedAt) {
		t.Fatalf("session metadata not preserved: %#v", got)
	}
	if len(got.Cookies) != 1 || got.Cookies[0] != want.Cookies[0] {
		t.Fatalf("request cookie not preserved: %#v", got.Cookies)
	}
}

func TestSaveLoadRoundTripPreservesPartitionedCookieAttribute(t *testing.T) {
	keyring := newMemoryKeyring()
	store := NewStore(Options{Keyring: keyring, StateHome: t.TempDir(), GOOS: "linux"})
	want := sampleSession("https://controller.example:443")
	want.Cookies = CookiesFromHTTP([]*http.Cookie{{
		Name:        "TOKEN",
		Value:       "session-secret",
		Partitioned: true,
	}})
	if !want.Cookies[0].Partitioned {
		t.Fatal("partitioned cookie attribute was not copied from http.Cookie")
	}

	if err := store.Save(want, false); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, found, err := store.Load(want.Controller)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !found {
		t.Fatal("Load did not find the saved session")
	}
	if !got.Cookies[0].Partitioned {
		t.Fatal("partitioned cookie attribute was not preserved")
	}
	if !got.HTTPCookies()[0].Partitioned {
		t.Fatal("partitioned cookie attribute was not restored to http.Cookie")
	}
}

func TestSessionsAreScopedToNormalizedController(t *testing.T) {
	keyring := newMemoryKeyring()
	store := NewStore(Options{Keyring: keyring, StateHome: t.TempDir(), GOOS: "linux"})

	if err := store.Save(sampleSession("https://controller-a.example:443"), false); err != nil {
		t.Fatalf("Save: %v", err)
	}
	_, found, err := store.Load("https://controller-b.example:443")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if found {
		t.Fatal("session for one controller was available to another controller")
	}
	if len(keyring.entries) != 1 {
		t.Fatalf("keyring entries = %d, want 1", len(keyring.entries))
	}
}

func TestSavePrefersKeyringAndDoesNotCreateFallbackFile(t *testing.T) {
	keyring := newMemoryKeyring()
	stateHome := t.TempDir()
	store := NewStore(Options{Keyring: keyring, StateHome: stateHome, GOOS: "linux"})

	if err := store.Save(sampleSession("https://controller.example:443"), true); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if keyring.setCalls != 1 {
		t.Fatalf("keyring Set calls = %d, want 1", keyring.setCalls)
	}
	if _, err := os.Stat(store.fallbackPath("https://controller.example:443")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("fallback file exists or unexpected error: %v", err)
	}
}

func TestSaveUsesFallbackOnlyWhenExplicitlyAllowedAndKeyringUnavailable(t *testing.T) {
	keyring := newMemoryKeyring()
	keyring.setErr = ErrKeyringUnavailable
	stateHome := t.TempDir()
	store := NewStore(Options{Keyring: keyring, StateHome: stateHome, GOOS: "linux"})
	want := sampleSession("https://controller.example:443")

	if err := store.Save(want, false); !errors.Is(err, ErrKeyringUnavailable) {
		t.Fatalf("Save without fallback error = %v, want ErrKeyringUnavailable", err)
	}
	if _, err := os.Stat(store.fallbackPath(want.Controller)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("fallback file exists or unexpected error: %v", err)
	}

	if err := store.Save(want, true); err != nil {
		t.Fatalf("Save with fallback: %v", err)
	}
	got, found, err := store.Load(want.Controller)
	if err != nil {
		t.Fatalf("Load fallback: %v", err)
	}
	if !found || got.CSRF != want.CSRF {
		t.Fatalf("Load fallback = %#v, found %t", got, found)
	}
}

func TestLoadReadsExistingFallbackWhenKeyringUnavailable(t *testing.T) {
	stateHome := t.TempDir()
	writerKeyring := newMemoryKeyring()
	writerKeyring.setErr = ErrKeyringUnavailable
	writer := NewStore(Options{Keyring: writerKeyring, StateHome: stateHome, GOOS: "linux"})
	want := sampleSession("https://controller.example:443")
	if err := writer.Save(want, true); err != nil {
		t.Fatalf("Save fallback: %v", err)
	}

	readerKeyring := newMemoryKeyring()
	readerKeyring.getErr = ErrKeyringUnavailable
	reader := NewStore(Options{Keyring: readerKeyring, StateHome: stateHome, GOOS: "linux"})
	got, found, err := reader.Load(want.Controller)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !found || got.CSRF != want.CSRF {
		t.Fatalf("Load = %#v, found %t", got, found)
	}
}

func TestDeleteRemovesKeyringAndFallbackAndIgnoresMissingData(t *testing.T) {
	stateHome := t.TempDir()
	keyring := newMemoryKeyring()
	store := NewStore(Options{Keyring: keyring, StateHome: stateHome, GOOS: "linux"})
	session := sampleSession("https://controller.example:443")
	if err := store.Save(session, false); err != nil {
		t.Fatalf("Save keyring: %v", err)
	}
	if err := store.writeFallback(session); err != nil {
		t.Fatalf("writeFallback: %v", err)
	}

	if err := store.Delete(session.Controller); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, found, err := store.Load(session.Controller); err != nil || found {
		t.Fatalf("Load after Delete = found %t, err %v", found, err)
	}
	if err := store.Delete(session.Controller); err != nil {
		t.Fatalf("Delete missing data: %v", err)
	}
}

func TestDeleteSucceedsWhenKeyringIsUnavailableAndFallbackIsRemovedOrAbsent(t *testing.T) {
	stateHome := t.TempDir()
	keyring := newMemoryKeyring()
	keyring.deleteErr = ErrKeyringUnavailable
	store := NewStore(Options{Keyring: keyring, StateHome: stateHome, GOOS: "linux"})
	session := sampleSession("https://controller.example:443")
	if err := store.writeFallback(session); err != nil {
		t.Fatalf("writeFallback: %v", err)
	}

	if err := store.Delete(session.Controller); err != nil {
		t.Fatalf("Delete fallback session: %v", err)
	}
	if _, err := os.Stat(store.fallbackPath(session.Controller)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("fallback file exists or unexpected error: %v", err)
	}
	if err := store.Delete(session.Controller); err != nil {
		t.Fatalf("Delete absent fallback session: %v", err)
	}
}

func TestFallbackUsesUserOnlyDirectoryAndFilePermissions(t *testing.T) {
	keyring := newMemoryKeyring()
	keyring.setErr = ErrKeyringUnavailable
	stateHome := t.TempDir()
	store := NewStore(Options{Keyring: keyring, StateHome: stateHome, GOOS: "linux"})
	session := sampleSession("https://controller.example:443")

	if err := store.Save(session, true); err != nil {
		t.Fatalf("Save: %v", err)
	}
	path := store.fallbackPath(session.Controller)
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
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "csrf-secret") || !strings.Contains(string(data), "session-secret") {
		t.Fatalf("fallback did not contain persisted session record")
	}
}
