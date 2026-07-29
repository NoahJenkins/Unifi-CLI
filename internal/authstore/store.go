// Package authstore persists controller-scoped UniFi API keys without
// retaining the legacy cookie-session data they replace.
package authstore

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/zalando/go-keyring"
)

const serviceName = "unifi-cli"

var (
	// ErrNotFound indicates that no stored API key exists for a controller.
	ErrNotFound = errors.New("stored API key not found")
	// ErrKeyringUnavailable indicates that the native OS credential store could
	// not be used. Callers may opt into protected-file persistence on Save.
	ErrKeyringUnavailable = errors.New("native keyring unavailable")
)

// Keyring is a small, injectable abstraction over the OS credential vault.
type Keyring interface {
	Get(service, account string) (string, error)
	Set(service, account, value string) error
	Delete(service, account string) error
}

type systemKeyring struct{}

func (systemKeyring) Get(service, account string) (string, error) {
	value, err := keyring.Get(service, account)
	if err != nil {
		return "", mapKeyringError(err)
	}
	return value, nil
}

func (systemKeyring) Set(service, account, value string) error {
	return mapKeyringError(keyring.Set(service, account, value))
}

func (systemKeyring) Delete(service, account string) error {
	return mapKeyringError(keyring.Delete(service, account))
}

func mapKeyringError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, keyring.ErrNotFound) {
		return ErrNotFound
	}
	return fmt.Errorf("%w: %v", ErrKeyringUnavailable, err)
}

// Options configures a Store. Empty fields use the current OS defaults. The
// explicit fields make the state location and native keyring injectable in
// tests without accessing a real user credential store.
type Options struct {
	Keyring   Keyring
	StateHome string
	HomeDir   string
	GOOS      string
}

// Store persists controller-scoped API keys. Client code depends on this
// interface so tests can use an in-memory store without an OS credential vault.
type Store interface {
	Load(controller string) (apiKey string, found bool, err error)
	Save(controller, apiKey string, allowFileFallback bool) error
	Delete(controller string) error
}

// KeyringStore implements Store using the native keyring and an opt-in
// protected-file fallback.
type KeyringStore struct {
	keyring   Keyring
	stateHome string
	homeDir   string
	goos      string
}

type apiKeyRecord struct {
	Controller string `json:"controller"`
	APIKey     string `json:"api_key"`
}

// NewStore creates an API-key store using supplied dependencies or OS defaults.
func NewStore(options Options) *KeyringStore {
	kr := options.Keyring
	if kr == nil {
		kr = systemKeyring{}
	}
	goos := options.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	home := options.HomeDir
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	return &KeyringStore{
		keyring:   kr,
		stateHome: options.StateHome,
		homeDir:   home,
		goos:      goos,
	}
}

// Load reads only a valid API-key record from the new storage location.
// A legacy or malformed record is not an API key and returns found == false.
func (s *KeyringStore) Load(controller string) (string, bool, error) {
	normalized, err := NormalizeController(controller)
	if err != nil {
		return "", false, err
	}
	encoded, err := s.keyring.Get(serviceName, controllerAccount(normalized))
	switch {
	case err == nil:
		apiKey, found, decodeErr := decodeRecord(encoded, normalized)
		if decodeErr != nil || found {
			return apiKey, found, decodeErr
		}
		// A legacy session can still occupy the shared keyring account after a
		// prior fallback-only save. It is never usable as a key, but a valid
		// current fallback remains usable until a native save replaces it.
		return s.readFallback(normalized)
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrKeyringUnavailable):
		apiKey, found, fallbackErr := s.readFallback(normalized)
		if fallbackErr != nil || found {
			return apiKey, found, fallbackErr
		}
		if errors.Is(err, ErrKeyringUnavailable) {
			return "", false, fmt.Errorf("load stored API key: %w", ErrKeyringUnavailable)
		}
		return "", false, nil
	default:
		return "", false, errors.New("load stored API key from keyring failed")
	}
}

// Save writes an API key to the native keyring. If that keyring is
// unavailable, it writes the protected file fallback only when explicitly
// allowed.
func (s *KeyringStore) Save(controller, apiKey string, allowFileFallback bool) error {
	normalized, err := NormalizeController(controller)
	if err != nil {
		return err
	}
	if apiKey == "" {
		return errors.New("API key is required")
	}
	record := apiKeyRecord{Controller: normalized, APIKey: apiKey}
	encoded, err := json.Marshal(record)
	if err != nil {
		return errors.New("serialize API key record failed")
	}
	if err := s.keyring.Set(serviceName, controllerAccount(normalized), string(encoded)); err == nil {
		if cleanupErr := s.removeFallbacks(normalized); cleanupErr != nil {
			return cleanupErr
		}
		return nil
	} else if !errors.Is(err, ErrKeyringUnavailable) {
		return errors.New("save API key in keyring failed")
	}
	if !allowFileFallback {
		return fmt.Errorf("save API key: %w", ErrKeyringUnavailable)
	}
	return s.writeFallback(record)
}

// Delete removes the keyring account, new fallback, and legacy fallback;
// missing entries are successful.
func (s *KeyringStore) Delete(controller string) error {
	normalized, err := NormalizeController(controller)
	if err != nil {
		return err
	}
	keyringErr := s.keyring.Delete(serviceName, controllerAccount(normalized))
	if errors.Is(keyringErr, ErrNotFound) {
		keyringErr = nil
	}
	removedFallback, cleanupErr := s.removeFallbacksWithStatus(normalized)
	if cleanupErr != nil {
		return cleanupErr
	}
	if errors.Is(keyringErr, ErrKeyringUnavailable) {
		if removedFallback {
			return nil
		}
		return fmt.Errorf("delete stored API key: %w", ErrKeyringUnavailable)
	}
	if keyringErr != nil {
		return errors.New("delete stored API key from keyring failed")
	}
	return nil
}

func (s *KeyringStore) readFallback(controller string) (string, bool, error) {
	encoded, err := os.ReadFile(s.fallbackPath(controller))
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read fallback API key: %w", err)
	}
	return decodeRecord(string(encoded), controller)
}

func (s *KeyringStore) writeFallback(record apiKeyRecord) error {
	directory := s.fallbackDir()
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create fallback API-key directory: %w", err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return fmt.Errorf("protect fallback API-key directory: %w", err)
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return errors.New("serialize fallback API-key record failed")
	}
	temporary, err := os.CreateTemp(directory, ".key-*.tmp")
	if err != nil {
		return fmt.Errorf("create fallback API-key file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("protect fallback API-key file: %w", err)
	}
	if _, err := temporary.Write(encoded); err != nil {
		temporary.Close()
		return fmt.Errorf("write fallback API key: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync fallback API key: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close fallback API-key file: %w", err)
	}
	if err := os.Rename(temporaryPath, s.fallbackPath(record.Controller)); err != nil {
		return fmt.Errorf("replace fallback API key: %w", err)
	}
	return nil
}

func (s *KeyringStore) removeFallbacks(controller string) error {
	_, err := s.removeFallbacksWithStatus(controller)
	return err
}

func (s *KeyringStore) removeFallbacksWithStatus(controller string) (bool, error) {
	removed := false
	var firstErr error
	for _, path := range []string{s.fallbackPath(controller), s.legacyFallbackPath(controller)} {
		err := os.Remove(path)
		if err == nil {
			removed = true
			continue
		}
		if !errors.Is(err, os.ErrNotExist) {
			if firstErr == nil {
				firstErr = fmt.Errorf("remove fallback API key: %w", err)
			}
		}
	}
	return removed, firstErr
}

func (s *KeyringStore) fallbackPath(controller string) string {
	normalized, err := NormalizeController(controller)
	if err != nil {
		return ""
	}
	return filepath.Join(s.fallbackDir(), controllerAccount(normalized)+".json")
}

func (s *KeyringStore) legacyFallbackPath(controller string) string {
	normalized, err := NormalizeController(controller)
	if err != nil {
		return ""
	}
	return filepath.Join(s.legacyFallbackDir(), controllerAccount(normalized)+".json")
}

func (s *KeyringStore) fallbackDir() string {
	return s.stateDir("keys")
}

func (s *KeyringStore) legacyFallbackDir() string {
	return s.stateDir("sessions")
}

func (s *KeyringStore) stateDir(kind string) string {
	switch s.goos {
	case "darwin":
		return filepath.Join(s.homeDir, "Library", "Application Support", serviceName, kind)
	case "windows":
		base := s.stateHome
		if base == "" {
			base = os.Getenv("LOCALAPPDATA")
		}
		if base == "" {
			base = filepath.Join(s.homeDir, "AppData", "Local")
		}
		return filepath.Join(base, serviceName, kind)
	default:
		base := s.stateHome
		if base == "" {
			base = os.Getenv("XDG_STATE_HOME")
		}
		if base == "" {
			base = filepath.Join(s.homeDir, ".local", "state")
		}
		return filepath.Join(base, serviceName, kind)
	}
}

func decodeRecord(encoded, expectedController string) (string, bool, error) {
	var record apiKeyRecord
	if err := json.Unmarshal([]byte(encoded), &record); err != nil {
		return "", false, nil
	}
	normalized, err := NormalizeController(record.Controller)
	if err != nil || normalized != expectedController || record.APIKey == "" {
		return "", false, nil
	}
	return record.APIKey, true, nil
}

// NormalizeController canonicalizes the scheme, host, and port used to scope
// an API key. Paths, credentials, queries, and fragments are not accepted.
func NormalizeController(controller string) (string, error) {
	u, err := url.Parse(controller)
	if err != nil {
		return "", fmt.Errorf("parse controller URL: %w", err)
	}
	if u.Scheme == "" || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("controller URL must contain only scheme, host, and optional port")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "https" && scheme != "http" {
		return "", fmt.Errorf("unsupported controller URL scheme %q", u.Scheme)
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return "", errors.New("controller URL host is required")
	}
	port := u.Port()
	if port == "" {
		if scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return scheme + "://" + net.JoinHostPort(host, port), nil
}

func controllerAccount(controller string) string {
	sum := sha256.Sum256([]byte(controller))
	return hex.EncodeToString(sum[:])
}
