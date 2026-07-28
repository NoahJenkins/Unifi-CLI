// Package session persists authenticated UniFi controller sessions without
// persisting the credentials that created them.
package session

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/zalando/go-keyring"
)

const serviceName = "unifi-cli"

var (
	// ErrNotFound indicates that no stored session exists for a controller.
	ErrNotFound = errors.New("stored session not found")
	// ErrKeyringUnavailable indicates that the native OS credential store could
	// not be used. Callers may opt into protected-file persistence on Save.
	ErrKeyringUnavailable = errors.New("native keyring unavailable")
)

// RequestCookie is the subset of http.Cookie required to restore a request
// cookie to an http.CookieJar. It intentionally excludes response-only and
// diagnostic fields such as Raw and Unparsed.
type RequestCookie struct {
	Name        string        `json:"name"`
	Value       string        `json:"value"`
	Path        string        `json:"path,omitempty"`
	Domain      string        `json:"domain,omitempty"`
	Expires     time.Time     `json:"expires,omitempty"`
	MaxAge      int           `json:"max_age,omitempty"`
	Secure      bool          `json:"secure,omitempty"`
	HTTPOnly    bool          `json:"http_only,omitempty"`
	SameSite    http.SameSite `json:"same_site,omitempty"`
	Partitioned bool          `json:"partitioned,omitempty"`
}

// Session is a controller-scoped, serializable authenticated session. It
// contains only ephemeral session state and never a password or API key.
type Session struct {
	Controller string          `json:"controller"`
	Cookies    []RequestCookie `json:"cookies"`
	CSRF       string          `json:"csrf"`
	UpdatedAt  time.Time       `json:"updated_at"`
}

// CookiesFromHTTP converts request cookies into the persistent session form.
func CookiesFromHTTP(cookies []*http.Cookie) []RequestCookie {
	result := make([]RequestCookie, 0, len(cookies))
	for _, cookie := range cookies {
		if cookie == nil {
			continue
		}
		result = append(result, RequestCookie{
			Name:        cookie.Name,
			Value:       cookie.Value,
			Path:        cookie.Path,
			Domain:      cookie.Domain,
			Expires:     cookie.Expires,
			MaxAge:      cookie.MaxAge,
			Secure:      cookie.Secure,
			HTTPOnly:    cookie.HttpOnly,
			SameSite:    cookie.SameSite,
			Partitioned: cookie.Partitioned,
		})
	}
	return result
}

// NormalizeCookieLifetimes turns relative Max-Age values into fixed expiry
// deadlines based on the time the session was saved. This keeps a persisted
// cookie from receiving a fresh lifetime whenever a later process restores it.
func (s *Session) NormalizeCookieLifetimes() {
	for i := range s.Cookies {
		if s.Cookies[i].MaxAge > 0 {
			s.Cookies[i].Expires = s.UpdatedAt.Add(time.Duration(s.Cookies[i].MaxAge) * time.Second)
			s.Cookies[i].MaxAge = 0
		}
	}
}

// HTTPCookies converts persistent cookies back to cookies accepted by
// http.CookieJar.SetCookies.
func (s Session) HTTPCookies() []*http.Cookie {
	s.NormalizeCookieLifetimes()
	result := make([]*http.Cookie, 0, len(s.Cookies))
	for _, cookie := range s.Cookies {
		result = append(result, &http.Cookie{
			Name:        cookie.Name,
			Value:       cookie.Value,
			Path:        cookie.Path,
			Domain:      cookie.Domain,
			Expires:     cookie.Expires,
			MaxAge:      cookie.MaxAge,
			Secure:      cookie.Secure,
			HttpOnly:    cookie.HTTPOnly,
			SameSite:    cookie.SameSite,
			Partitioned: cookie.Partitioned,
		})
	}
	return result
}

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
	// go-keyring exposes non-not-found backend errors without a portable type.
	// They mean the native provider cannot service this request (for example a
	// missing Linux Secret Service session), so preserve that distinction for
	// callers without exposing backend details that can include local paths.
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

// Store persists controller-scoped sessions. Client code depends on this
// interface so it can use an in-memory test store without accessing a real OS
// credential vault.
type Store interface {
	Load(controller string) (Session, bool, error)
	Save(session Session, allowFileFallback bool) error
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

// NewStore creates a session store using the supplied dependencies or OS
// defaults when an option is omitted.
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

// Load returns the saved session for controller. A fallback file created by a
// previous explicit opt-in may be read even when the native keyring is now
// unavailable; loading never creates a fallback file.
func (s *KeyringStore) Load(controller string) (Session, bool, error) {
	normalized, err := NormalizeController(controller)
	if err != nil {
		return Session{}, false, err
	}
	encoded, err := s.keyring.Get(serviceName, controllerAccount(normalized))
	switch {
	case err == nil:
		session, err := decodeSession(encoded, normalized)
		return session, err == nil, err
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrKeyringUnavailable):
		fallback, found, fallbackErr := s.readFallback(normalized)
		if fallbackErr != nil || found {
			return fallback, found, fallbackErr
		}
		if errors.Is(err, ErrKeyringUnavailable) {
			return Session{}, false, ErrKeyringUnavailable
		}
		return Session{}, false, nil
	default:
		return Session{}, false, err
	}
}

// Save writes a session to the native keyring. If that keyring is unavailable,
// it writes the protected file fallback only when allowFileFallback is true.
func (s *KeyringStore) Save(session Session, allowFileFallback bool) error {
	normalized, err := NormalizeController(session.Controller)
	if err != nil {
		return err
	}
	session.Controller = normalized
	if session.UpdatedAt.IsZero() {
		session.UpdatedAt = time.Now().UTC()
	}
	encoded, err := json.Marshal(session)
	if err != nil {
		return fmt.Errorf("serialize session: %w", err)
	}
	if err := s.keyring.Set(serviceName, controllerAccount(normalized), string(encoded)); err == nil {
		if cleanupErr := os.Remove(s.fallbackPath(normalized)); cleanupErr != nil && !errors.Is(cleanupErr, os.ErrNotExist) {
			return fmt.Errorf("remove obsolete fallback session: %w", cleanupErr)
		}
		return nil
	} else if !errors.Is(err, ErrKeyringUnavailable) {
		return err
	}
	if !allowFileFallback {
		if _, err := os.Stat(s.fallbackPath(normalized)); errors.Is(err, os.ErrNotExist) {
			return ErrKeyringUnavailable
		} else if err != nil {
			return fmt.Errorf("inspect existing fallback session: %w", err)
		}
	}
	return s.writeFallback(session)
}

// Delete removes both the keyring record and any fallback file. Missing
// records are successful when the keyring can prove absence. If the keyring is
// unavailable, deletion succeeds only when an explicit fallback file existed
// and was removed.
func (s *KeyringStore) Delete(controller string) error {
	normalized, err := NormalizeController(controller)
	if err != nil {
		return err
	}
	keyringErr := s.keyring.Delete(serviceName, controllerAccount(normalized))
	if errors.Is(keyringErr, ErrNotFound) {
		keyringErr = nil
	}
	fileErr := os.Remove(s.fallbackPath(normalized))
	fallbackRemoved := fileErr == nil
	if errors.Is(fileErr, os.ErrNotExist) {
		fileErr = nil
	}
	if fileErr != nil {
		return fmt.Errorf("remove fallback session: %w", fileErr)
	}
	if errors.Is(keyringErr, ErrKeyringUnavailable) {
		if fallbackRemoved {
			return nil
		}
		return keyringErr
	}
	if keyringErr != nil {
		return keyringErr
	}
	return nil
}

func (s *KeyringStore) readFallback(controller string) (Session, bool, error) {
	encoded, err := os.ReadFile(s.fallbackPath(controller))
	if errors.Is(err, os.ErrNotExist) {
		return Session{}, false, nil
	}
	if err != nil {
		return Session{}, false, fmt.Errorf("read fallback session: %w", err)
	}
	session, err := decodeSession(string(encoded), controller)
	if err != nil {
		return Session{}, false, err
	}
	return session, true, nil
}

func (s *KeyringStore) writeFallback(session Session) error {
	directory := s.fallbackDir()
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create fallback session directory: %w", err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return fmt.Errorf("protect fallback session directory: %w", err)
	}
	encoded, err := json.Marshal(session)
	if err != nil {
		return fmt.Errorf("serialize fallback session: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".session-*.tmp")
	if err != nil {
		return fmt.Errorf("create fallback session file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("protect fallback session file: %w", err)
	}
	if _, err := temporary.Write(encoded); err != nil {
		temporary.Close()
		return fmt.Errorf("write fallback session: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync fallback session: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close fallback session: %w", err)
	}
	if err := os.Rename(temporaryPath, s.fallbackPath(session.Controller)); err != nil {
		return fmt.Errorf("replace fallback session: %w", err)
	}
	return nil
}

func (s *KeyringStore) fallbackPath(controller string) string {
	normalized, err := NormalizeController(controller)
	if err != nil {
		return ""
	}
	return filepath.Join(s.fallbackDir(), controllerAccount(normalized)+".json")
}

func (s *KeyringStore) fallbackDir() string {
	switch s.goos {
	case "darwin":
		return filepath.Join(s.homeDir, "Library", "Application Support", serviceName, "sessions")
	case "windows":
		base := s.stateHome
		if base == "" {
			base = os.Getenv("LOCALAPPDATA")
		}
		if base == "" {
			base = filepath.Join(s.homeDir, "AppData", "Local")
		}
		return filepath.Join(base, serviceName, "sessions")
	default:
		base := s.stateHome
		if base == "" {
			base = os.Getenv("XDG_STATE_HOME")
		}
		if base == "" {
			base = filepath.Join(s.homeDir, ".local", "state")
		}
		return filepath.Join(base, serviceName, "sessions")
	}
}

func decodeSession(encoded, expectedController string) (Session, error) {
	var session Session
	if err := json.Unmarshal([]byte(encoded), &session); err != nil {
		return Session{}, fmt.Errorf("decode stored session: %w", err)
	}
	normalized, err := NormalizeController(session.Controller)
	if err != nil {
		return Session{}, fmt.Errorf("stored session controller: %w", err)
	}
	if normalized != expectedController {
		return Session{}, fmt.Errorf("stored session controller does not match requested controller")
	}
	session.Controller = normalized
	return session, nil
}

// NormalizeController canonicalizes the scheme, host, and port used to scope
// a session. Paths, credentials, queries, and fragments are not accepted.
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
	return scheme + "://" + netJoinHostPort(host, port), nil
}

func netJoinHostPort(host, port string) string {
	if strings.Contains(host, ":") {
		return "[" + host + "]:" + port
	}
	return host + ":" + port
}

func controllerAccount(controller string) string {
	sum := sha256.Sum256([]byte(controller))
	return hex.EncodeToString(sum[:])
}
