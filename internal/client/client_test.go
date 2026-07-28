package client_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
	"github.com/noahjenkins/unifi-cli/internal/client"
	"github.com/noahjenkins/unifi-cli/internal/config"
	"github.com/noahjenkins/unifi-cli/internal/session"
)

type memorySessionStore struct {
	sessions  map[string]session.Session
	loadErr   error
	saveErr   error
	deleteErr error
}

type failAfterFirstSaveStore struct {
	memorySessionStore
	saves int
	err   error
}

func (s *failAfterFirstSaveStore) Save(record session.Session, allowFileFallback bool) error {
	s.saves++
	if s.saves > 1 {
		return s.err
	}
	return s.memorySessionStore.Save(record, allowFileFallback)
}

func (s *memorySessionStore) Load(controller string) (session.Session, bool, error) {
	if s.loadErr != nil {
		return session.Session{}, false, s.loadErr
	}
	record, found := s.sessions[controller]
	return record, found, nil
}

func (s *memorySessionStore) Save(record session.Session, _ bool) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	if s.sessions == nil {
		s.sessions = make(map[string]session.Session)
	}
	s.sessions[record.Controller] = record
	return nil
}

func (s *memorySessionStore) Delete(controller string) error {
	if s.deleteErr != nil {
		return s.deleteErr
	}
	delete(s.sessions, controller)
	return nil
}

func testConfig(t *testing.T, srv *httptest.Server) config.Config {
	t.Helper()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse server url: %v", err)
	}
	host, portStr, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatalf("split host port: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}
	return config.Config{
		Host:     host,
		Port:     port,
		Insecure: true,
		Site:     "default",
		Timeout:  5 * time.Second,
	}
}

func TestLoginWithPassword(t *testing.T) {
	var gotBody map[string]string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/auth/login" && r.Method == http.MethodPost {
			defer r.Body.Close()
			if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
				t.Errorf("decode login body: %v", err)
			}
			http.SetCookie(w, &http.Cookie{Name: "TOKEN", Value: "abc"})
			w.Header().Set("X-CSRF-Token", "csrf123")
			w.WriteHeader(http.StatusOK)
			fixture, err := os.ReadFile(filepath.Join("fixtures", "login_ok.json"))
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			_, _ = w.Write(fixture)
			return
		}
		t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()

	cfg := testConfig(t, srv)
	cfg.Username = "admin"
	cfg.Password = "secret"

	c, err := client.NewWithSessionStore(cfg, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := c.Login(context.Background()); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if gotBody["username"] != "admin" || gotBody["password"] != "secret" {
		t.Fatalf("login body = %#v", gotBody)
	}
}

func TestAPIKeySkipsLoginBody(t *testing.T) {
	var sawLogin bool
	var gotAPIKey string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/auth/login" {
			sawLogin = true
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/proxy/network/api/s/default/stat/device" {
			gotAPIKey = r.Header.Get("X-API-KEY")
			w.Header().Set("Content-Type", "application/json")
			fixture, err := os.ReadFile(filepath.Join("fixtures", "devices.json"))
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			_, _ = w.Write(fixture)
			return
		}
		t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()

	cfg := testConfig(t, srv)
	cfg.APIKey = "test-api-key"

	c, err := client.NewWithSessionStore(cfg, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := c.Login(context.Background()); err != nil {
		t.Fatalf("Login with API key: %v", err)
	}
	if sawLogin {
		t.Fatal("Login should not POST when API key is set")
	}

	var devices []map[string]any
	path := c.SitePath(client.PathStatDevice)
	if err := c.Do(context.Background(), http.MethodGet, path, nil, &devices); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if gotAPIKey != "test-api-key" {
		t.Fatalf("X-API-KEY = %q", gotAPIKey)
	}
	if len(devices) != 2 {
		t.Fatalf("decoded devices len = %d", len(devices))
	}
	if devices[0]["name"] != "Gateway" {
		t.Fatalf("first device name = %v", devices[0]["name"])
	}
}

func TestDoMaps401ToAuthFailed(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"meta":{"rc":"error","msg":"api.err.LoginRequired"}}`)
	}))
	defer srv.Close()

	cfg := testConfig(t, srv)
	cfg.APIKey = "bad-key"

	c, err := client.NewWithSessionStore(cfg, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	err = c.Do(context.Background(), http.MethodGet, c.SitePath(client.PathStatDevice), nil, nil)
	if !apperr.Is(err, apperr.AuthFailed) {
		t.Fatalf("err = %v, want auth_failed", err)
	}
}

func TestDoMapsConnectionError(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	cfg := testConfig(t, srv)
	cfg.APIKey = "key"
	srv.Close()

	c, err := client.NewWithSessionStore(cfg, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	err = c.Do(context.Background(), http.MethodGet, c.SitePath(client.PathStatDevice), nil, nil)
	if !apperr.Is(err, apperr.ControllerUnreachable) {
		t.Fatalf("err = %v, want controller_unreachable", err)
	}
}

func TestSitePath(t *testing.T) {
	c, err := client.NewWithSessionStore(config.Config{
		Host: "127.0.0.1",
		Port: 443,
		Site: "default",
	}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.Site() != "default" {
		t.Fatalf("Site() = %q", c.Site())
	}
	got := c.SitePath(client.PathStatDevice)
	want := "/proxy/network/api/s/default/stat/device"
	if got != want {
		t.Fatalf("SitePath = %q, want %q", got, want)
	}
	got = c.SitePath("rest/networkconf", "abc/")
	want = "/proxy/network/api/s/default/rest/networkconf/abc"
	if got != want {
		t.Fatalf("SitePath multi = %q, want %q", got, want)
	}
}

func TestDoSendsCSRFAfterLogin(t *testing.T) {
	var gotCSRF string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/auth/login" && r.Method == http.MethodPost:
			http.SetCookie(w, &http.Cookie{Name: "TOKEN", Value: "sess"})
			w.Header().Set("X-CSRF-Token", "csrf-from-login")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"unique_id":"u1"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/proxy/network/api/s/default/rest/wlanconf":
			gotCSRF = r.Header.Get("X-CSRF-Token")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"meta":{"rc":"ok"},"data":[]}`))
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	cfg := testConfig(t, srv)
	cfg.Username = "admin"
	cfg.Password = "secret"

	c, err := client.NewWithSessionStore(cfg, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := c.Login(context.Background()); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if err := c.Do(context.Background(), http.MethodPost, c.SitePath(client.PathRestWlan), map[string]string{"name": "Guest"}, nil); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if gotCSRF != "csrf-from-login" {
		t.Fatalf("X-CSRF-Token = %q", gotCSRF)
	}
}

func TestLoginRequiresCredentials(t *testing.T) {
	c, err := client.NewWithSessionStore(config.Config{Host: "127.0.0.1", Port: 443, Site: "default"}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	err = c.Login(context.Background())
	if !apperr.Is(err, apperr.ValidationFailed) {
		t.Fatalf("err = %v, want validation_failed", err)
	}
}

func TestSavedSessionRestoresCookieAndCSRFWithoutLogin(t *testing.T) {
	var gotCookie, gotCSRF string
	var loginRequests int
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/auth/login" {
			loginRequests++
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/proxy/network/api/s/default/stat/device" {
			gotCookie = r.Header.Get("Cookie")
			gotCSRF = r.Header.Get("X-CSRF-Token")
			_, _ = io.WriteString(w, `{"data":[]}`)
			return
		}
		t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()

	cfg := testConfig(t, srv)
	cfg.Username = "admin"
	cfg.Password = "secret"
	store := &memorySessionStore{sessions: map[string]session.Session{
		cfg.BaseURL(): {
			Controller: cfg.BaseURL(),
			Cookies:    []session.RequestCookie{{Name: "TOKEN", Value: "saved", Path: "/"}},
			CSRF:       "saved-csrf",
		},
	}}

	c, err := client.NewWithSessionStore(cfg, store)
	if err != nil {
		t.Fatalf("NewWithSessionStore: %v", err)
	}
	if c.AuthMethod() != "saved_session" {
		t.Fatalf("AuthMethod() = %q, want saved_session", c.AuthMethod())
	}
	if err := c.Do(context.Background(), http.MethodGet, c.SitePath(client.PathStatDevice), nil, nil); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if gotCookie != "TOKEN=saved" {
		t.Fatalf("Cookie = %q, want TOKEN=saved", gotCookie)
	}
	if gotCSRF != "saved-csrf" {
		t.Fatalf("X-CSRF-Token = %q, want saved-csrf", gotCSRF)
	}
	if loginRequests != 0 {
		t.Fatalf("login requests = %d, want 0", loginRequests)
	}
}

func TestPasswordLoginSavesCookiesAndCSRF(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/auth/login" || r.Method != http.MethodPost {
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
		http.SetCookie(w, &http.Cookie{Name: "TOKEN", Value: "fresh", Path: "/", HttpOnly: true})
		w.Header().Set("X-CSRF-Token", "fresh-csrf")
		_, _ = io.WriteString(w, `{"meta":{"rc":"ok"}}`)
	}))
	defer srv.Close()

	cfg := testConfig(t, srv)
	cfg.Username = "admin"
	cfg.Password = "secret"
	store := &memorySessionStore{}
	c, err := client.NewWithSessionStore(cfg, store)
	if err != nil {
		t.Fatalf("NewWithSessionStore: %v", err)
	}
	if err := c.Login(context.Background()); err != nil {
		t.Fatalf("Login: %v", err)
	}

	record, found := store.sessions[cfg.BaseURL()]
	if !found {
		t.Fatal("saved session missing")
	}
	if record.Controller != cfg.BaseURL() {
		t.Fatalf("Controller = %q, want %q", record.Controller, cfg.BaseURL())
	}
	if record.CSRF != "fresh-csrf" {
		t.Fatalf("CSRF = %q, want fresh-csrf", record.CSRF)
	}
	if len(record.Cookies) != 1 || record.Cookies[0].Name != "TOKEN" || record.Cookies[0].Value != "fresh" {
		t.Fatalf("Cookies = %#v, want fresh TOKEN cookie", record.Cookies)
	}
}

func TestPasswordLoginPersistsFullResponseCookieForNewClient(t *testing.T) {
	var loginRequests int
	var authenticatedRequests int
	const (
		cookieValue = "full-response-cookie-secret"
		csrfValue   = "full-response-csrf-secret"
	)
	expires := time.Date(2030, time.January, 2, 3, 4, 5, 0, time.UTC)

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/auth/login":
			loginRequests++
			http.SetCookie(w, &http.Cookie{
				Name:        "TOKEN",
				Value:       cookieValue,
				Path:        "/",
				Domain:      "127.0.0.1",
				Expires:     expires,
				MaxAge:      3600,
				Secure:      true,
				HttpOnly:    true,
				SameSite:    http.SameSiteNoneMode,
				Partitioned: true,
			})
			w.Header().Set("X-CSRF-Token", csrfValue)
			_, _ = io.WriteString(w, `{"meta":{"rc":"ok"}}`)
		case r.Method == http.MethodGet && r.URL.Path == "/proxy/network/api/s/default/stat/device":
			if cookie, err := r.Cookie("TOKEN"); err != nil || cookie.Value != cookieValue {
				t.Fatalf("restored request cookie unavailable")
			}
			if got := r.Header.Get("X-CSRF-Token"); got != csrfValue {
				t.Fatalf("restored CSRF token unavailable")
			}
			authenticatedRequests++
			_, _ = io.WriteString(w, `{"data":[]}`)
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	cfg := testConfig(t, srv)
	cfg.Username = "admin"
	cfg.Password = "secret"
	store := &memorySessionStore{}

	first, err := client.NewWithSessionStore(cfg, store)
	if err != nil {
		t.Fatalf("NewWithSessionStore first client: %v", err)
	}
	if err := first.Login(context.Background()); err != nil {
		t.Fatalf("Login: %v", err)
	}

	record, found := store.sessions[cfg.BaseURL()]
	if !found || len(record.Cookies) != 1 {
		t.Fatalf("saved cookie count = %d, found %t, want 1", len(record.Cookies), found)
	}
	saved := record.Cookies[0]
	if saved.Name != "TOKEN" ||
		saved.Path != "/" ||
		saved.Domain != "127.0.0.1" ||
		!saved.Expires.Equal(record.UpdatedAt.Add(time.Hour)) ||
		saved.MaxAge != 0 ||
		!saved.Secure ||
		!saved.HTTPOnly ||
		saved.SameSite != http.SameSiteNoneMode ||
		!saved.Partitioned {
		t.Fatal("saved cookie attributes were not preserved")
	}

	second, err := client.NewWithSessionStore(cfg, store)
	if err != nil {
		t.Fatalf("NewWithSessionStore second client: %v", err)
	}
	if err := second.Do(context.Background(), http.MethodGet, second.SitePath(client.PathStatDevice), nil, nil); err != nil {
		t.Fatalf("restored authenticated request: %v", err)
	}
	if loginRequests != 1 {
		t.Fatalf("login requests = %d, want 1", loginRequests)
	}
	if authenticatedRequests != 1 {
		t.Fatalf("authenticated requests = %d, want 1", authenticatedRequests)
	}
}

func TestAuthenticatedResponsePersistsRotatedSessionForNewClient(t *testing.T) {
	var loginRequests int
	var authenticatedRequests int
	const (
		initialCookie = "initial-session-secret"
		rotatedCookie = "rotated-session-secret"
		initialCSRF   = "initial-csrf-secret"
		rotatedCSRF   = "rotated-csrf-secret"
	)

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/auth/login":
			loginRequests++
			http.SetCookie(w, &http.Cookie{Name: "TOKEN", Value: initialCookie, Path: "/", Secure: true, HttpOnly: true})
			w.Header().Set("X-CSRF-Token", initialCSRF)
			_, _ = io.WriteString(w, `{"meta":{"rc":"ok"}}`)
		case r.Method == http.MethodGet && r.URL.Path == "/proxy/network/api/s/default/stat/device":
			authenticatedRequests++
			cookie, err := r.Cookie("TOKEN")
			if err != nil {
				t.Fatal("authenticated request did not include TOKEN cookie")
			}
			switch authenticatedRequests {
			case 1:
				if cookie.Value != initialCookie || r.Header.Get("X-CSRF-Token") != initialCSRF {
					t.Fatal("first authenticated request did not use initial session")
				}
				http.SetCookie(w, &http.Cookie{
					Name:        "TOKEN",
					Value:       rotatedCookie,
					Path:        "/",
					Secure:      true,
					HttpOnly:    true,
					SameSite:    http.SameSiteStrictMode,
					Partitioned: true,
				})
				w.Header().Set("X-CSRF-Token", rotatedCSRF)
			case 2:
				if cookie.Value != rotatedCookie || r.Header.Get("X-CSRF-Token") != rotatedCSRF {
					t.Fatal("new client did not use rotated session")
				}
			default:
				t.Fatalf("authenticated requests = %d, want at most 2", authenticatedRequests)
			}
			_, _ = io.WriteString(w, `{"data":[]}`)
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	cfg := testConfig(t, srv)
	cfg.Username = "admin"
	cfg.Password = "secret"
	store := &memorySessionStore{}

	first, err := client.NewWithSessionStore(cfg, store)
	if err != nil {
		t.Fatalf("NewWithSessionStore first client: %v", err)
	}
	if err := first.Login(context.Background()); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if err := first.Do(context.Background(), http.MethodGet, first.SitePath(client.PathStatDevice), nil, nil); err != nil {
		t.Fatalf("rotating authenticated request: %v", err)
	}

	rotated := store.sessions[cfg.BaseURL()]
	if rotated.CSRF != rotatedCSRF {
		t.Fatal("rotated CSRF token was not persisted")
	}
	if len(rotated.Cookies) != 1 ||
		rotated.Cookies[0].Value != rotatedCookie ||
		rotated.Cookies[0].SameSite != http.SameSiteStrictMode ||
		!rotated.Cookies[0].Partitioned {
		t.Fatal("rotated response cookie was not persisted with its attributes")
	}

	second, err := client.NewWithSessionStore(cfg, store)
	if err != nil {
		t.Fatalf("NewWithSessionStore second client: %v", err)
	}
	if err := second.Do(context.Background(), http.MethodGet, second.SitePath(client.PathStatDevice), nil, nil); err != nil {
		t.Fatalf("restored rotated session request: %v", err)
	}
	if loginRequests != 1 {
		t.Fatalf("login requests = %d, want 1", loginRequests)
	}
	if authenticatedRequests != 2 {
		t.Fatalf("authenticated requests = %d, want 2", authenticatedRequests)
	}
}

func TestSuccessfulRequestDoesNotBecomeAuthFailureWhenRotationPersistenceFails(t *testing.T) {
	const (
		initialCookie = "initial-session-secret"
		rotatedCookie = "rotated-session-secret"
		initialCSRF   = "initial-csrf-secret"
		rotatedCSRF   = "rotated-csrf-secret"
	)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/login":
			http.SetCookie(w, &http.Cookie{Name: "TOKEN", Value: initialCookie, Path: "/", Secure: true})
			w.Header().Set("X-CSRF-Token", initialCSRF)
			_, _ = io.WriteString(w, `{"meta":{"rc":"ok"}}`)
		case "/proxy/network/api/s/default/rest/wlanconf":
			if r.Header.Get("X-CSRF-Token") != initialCSRF {
				t.Fatal("mutation did not receive the authenticated CSRF token")
			}
			http.SetCookie(w, &http.Cookie{Name: "TOKEN", Value: rotatedCookie, Path: "/", Secure: true})
			w.Header().Set("X-CSRF-Token", rotatedCSRF)
			_, _ = io.WriteString(w, `{"data":{"id":"applied"}}`)
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	cfg := testConfig(t, srv)
	cfg.Username = "admin"
	cfg.Password = "secret"
	store := &failAfterFirstSaveStore{err: errors.New("keyring write failed")}
	c, err := client.NewWithSessionStore(cfg, store)
	if err != nil {
		t.Fatalf("NewWithSessionStore: %v", err)
	}
	if err := c.Login(context.Background()); err != nil {
		t.Fatalf("Login: %v", err)
	}

	var result map[string]string
	err = c.Do(context.Background(), http.MethodPost, c.SitePath(client.PathRestWlan), map[string]string{"name": "Guest"}, &result)
	if err != nil {
		t.Fatalf("successful controller mutation returned an error after persistence failed: %v", err)
	}
	if result["id"] != "applied" {
		t.Fatalf("mutation result = %#v, want applied result", result)
	}
}

func TestCSRFOnlySessionUpdateDoesNotRenewPersistedCookieLifetime(t *testing.T) {
	issuedAt := time.Now().UTC().Add(-10 * time.Minute).Truncate(time.Second)
	originalExpiry := issuedAt.Add(time.Hour)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/proxy/network/api/s/default/stat/device" {
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("X-CSRF-Token", "rotated-csrf")
		_, _ = io.WriteString(w, `{"data":[]}`)
	}))
	defer srv.Close()

	cfg := testConfig(t, srv)
	store := &memorySessionStore{sessions: map[string]session.Session{
		cfg.BaseURL(): {
			Controller: cfg.BaseURL(),
			UpdatedAt:  issuedAt,
			Cookies: []session.RequestCookie{{
				Name:    "TOKEN",
				Value:   "session-secret",
				Path:    "/",
				Secure:  true,
				MaxAge:  3600,
				Expires: issuedAt.Add(24 * time.Hour),
			}},
			CSRF: "original-csrf",
		},
	}}
	c, err := client.NewWithSessionStore(cfg, store)
	if err != nil {
		t.Fatalf("NewWithSessionStore: %v", err)
	}
	if err := c.Do(context.Background(), http.MethodGet, c.SitePath(client.PathStatDevice), nil, nil); err != nil {
		t.Fatalf("CSRF-only update: %v", err)
	}

	persisted := store.sessions[cfg.BaseURL()].Cookies[0]
	if persisted.MaxAge != 0 {
		t.Fatalf("persisted MaxAge = %d, want 0 after converting to original deadline", persisted.MaxAge)
	}
	if !persisted.Expires.Equal(originalExpiry) {
		t.Fatalf("persisted Expires = %s, want original deadline %s", persisted.Expires, originalExpiry)
	}
}

func TestSavedSession401DeletesSessionAndHintsLogin(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"meta":{"rc":"error","msg":"api.err.LoginRequired"}}`)
	}))
	defer srv.Close()

	cfg := testConfig(t, srv)
	cfg.Username = "admin"
	cfg.Password = "secret"
	store := &memorySessionStore{sessions: map[string]session.Session{
		cfg.BaseURL(): {
			Controller: cfg.BaseURL(),
			Cookies:    []session.RequestCookie{{Name: "TOKEN", Value: "stale", Path: "/"}},
		},
	}}
	c, err := client.NewWithSessionStore(cfg, store)
	if err != nil {
		t.Fatalf("NewWithSessionStore: %v", err)
	}
	err = c.Do(context.Background(), http.MethodGet, c.SitePath(client.PathStatDevice), nil, nil)
	if !apperr.Is(err, apperr.AuthFailed) {
		t.Fatalf("err = %v, want auth_failed", err)
	}
	if got := apperr.As(err).Hint; got != "run 'unifi auth login' to create a new saved session" {
		t.Fatalf("hint = %q", got)
	}
	if _, found := store.sessions[cfg.BaseURL()]; found {
		t.Fatal("stale saved session was not deleted")
	}
}

func TestSavedSession401PreservesSafeCleanupFailure(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"meta":{"rc":"error","msg":"api.err.LoginRequired"}}`)
	}))
	defer srv.Close()

	cfg := testConfig(t, srv)
	cfg.Username = "admin"
	cfg.Password = "secret"
	cleanupErr := errors.New("delete failed for session token=secret")
	store := &memorySessionStore{
		sessions: map[string]session.Session{
			cfg.BaseURL(): {
				Controller: cfg.BaseURL(),
				Cookies:    []session.RequestCookie{{Name: "TOKEN", Value: "stale", Path: "/"}},
			},
		},
		deleteErr: cleanupErr,
	}
	c, err := client.NewWithSessionStore(cfg, store)
	if err != nil {
		t.Fatalf("NewWithSessionStore: %v", err)
	}
	err = c.Do(context.Background(), http.MethodGet, c.SitePath(client.PathStatDevice), nil, nil)
	if !apperr.Is(err, apperr.AuthFailed) {
		t.Fatalf("err = %v, want auth_failed", err)
	}
	if got := apperr.As(err).Hint; got != "run 'unifi auth login' to create a new saved session" {
		t.Fatalf("hint = %q", got)
	}
	if !errors.Is(err, cleanupErr) {
		t.Fatalf("cleanup error context was not preserved: %v", err)
	}
	if strings.Contains(err.Error(), "token=secret") {
		t.Fatalf("error exposed cleanup details: %v", err)
	}
}

func TestAPIKeyDoesNotUseSessionStore(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-API-KEY"); got != "api-key" {
			t.Fatalf("X-API-KEY = %q, want api-key", got)
		}
		_, _ = io.WriteString(w, `{"data":[]}`)
	}))
	defer srv.Close()

	cfg := testConfig(t, srv)
	cfg.APIKey = "api-key"
	store := &memorySessionStore{loadErr: os.ErrPermission, saveErr: os.ErrPermission, deleteErr: os.ErrPermission}
	c, err := client.NewWithSessionStore(cfg, store)
	if err != nil {
		t.Fatalf("NewWithSessionStore: %v", err)
	}
	if c.AuthMethod() != "api_key" {
		t.Fatalf("AuthMethod() = %q, want api_key", c.AuthMethod())
	}
	if err := c.Do(context.Background(), http.MethodGet, c.SitePath(client.PathStatDevice), nil, nil); err != nil {
		t.Fatalf("Do: %v", err)
	}
}

func TestLogoutLocalSessionDeletesSavedStateWithoutControllerRequest(t *testing.T) {
	store := &memorySessionStore{sessions: map[string]session.Session{
		"https://controller.example:8443": {
			Controller: "https://controller.example:8443",
			Cookies:    []session.RequestCookie{{Name: "TOKEN", Value: "saved", Path: "/"}},
			CSRF:       "saved-csrf",
		},
	}}
	c, err := client.NewWithSessionStore(config.Config{
		Host:   "controller.example",
		Port:   8443,
		Site:   "default",
		APIKey: "configured-api-key",
	}, store)
	if err != nil {
		t.Fatalf("NewWithSessionStore: %v", err)
	}

	if err := c.LogoutLocalSession(); err != nil {
		t.Fatalf("LogoutLocalSession: %v", err)
	}
	if _, found := store.sessions["https://controller.example:8443"]; found {
		t.Fatal("saved session was not deleted")
	}
}
