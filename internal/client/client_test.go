package client_test

import (
	"context"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
	"github.com/noahjenkins/unifi-cli/internal/authstore"
	"github.com/noahjenkins/unifi-cli/internal/client"
	"github.com/noahjenkins/unifi-cli/internal/config"
)

type memoryAPIKeyStore struct {
	mu                 sync.Mutex
	keys               map[string]string
	loadErr            error
	deleteErr          error
	loadCalls          int
	saveCalls          int
	deleteCalls        int
	deletedControllers []string
}

func (s *memoryAPIKeyStore) Load(controller string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadCalls++
	if s.loadErr != nil {
		return "", false, s.loadErr
	}
	apiKey, found := s.keys[controller]
	return apiKey, found, nil
}

func (s *memoryAPIKeyStore) Save(controller, apiKey string, _ bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saveCalls++
	if s.keys == nil {
		s.keys = make(map[string]string)
	}
	s.keys[controller] = apiKey
	return nil
}

func (s *memoryAPIKeyStore) Delete(controller string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleteCalls++
	s.deletedControllers = append(s.deletedControllers, controller)
	if s.deleteErr != nil {
		return s.deleteErr
	}
	delete(s.keys, controller)
	return nil
}

func TestConcurrentSavedAPIKey401InvalidationDoesNotDeletePersistedKey(t *testing.T) {
	t.Setenv("UNIFI_API_KEY", "")
	const workers = 4
	arrived := make(chan string, workers)
	release := make(chan struct{})
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		arrived <- r.Header.Get("X-API-KEY")
		<-release
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	cfg := testConfig(t, srv)
	store := &memoryAPIKeyStore{keys: map[string]string{cfg.BaseURL(): "saved-key"}}
	c, err := client.NewWithStore(cfg, store)
	if err != nil {
		t.Fatal(err)
	}

	errs := make(chan error, workers)
	for range workers {
		go func() {
			errs <- c.DoOfficial(context.Background(), http.MethodGet, client.PathSelfSites, nil, nil)
		}()
	}
	for range workers {
		select {
		case key := <-arrived:
			if key != "saved-key" {
				t.Fatalf("concurrent request key = %q, want saved-key", key)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent authenticated requests did not reach the controller")
		}
	}
	close(release)
	for range workers {
		if err := <-errs; !apperr.Is(err, apperr.AuthFailed) {
			t.Fatalf("error = %v, want auth_failed", err)
		}
	}
	store.mu.Lock()
	deletes := store.deleteCalls
	store.mu.Unlock()
	if deletes != 0 {
		t.Fatalf("saved key delete calls = %d, want 0", deletes)
	}
}

var _ authstore.Store = (*memoryAPIKeyStore)(nil)

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

func loadTestConfig(t *testing.T, srv *httptest.Server, extra string) config.Config {
	t.Helper()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse server url: %v", err)
	}
	host, portStr, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatalf("split host port: %v", err)
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	contents := fmt.Sprintf("host: %q\nport: %s\ntimeout: 5s\n%s", host, portStr, extra)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return cfg
}

func TestEnvironmentAPIKeyWinsOverSavedKey(t *testing.T) {
	t.Setenv("UNIFI_API_KEY", "environment-key")
	var gotAPIKey string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAPIKey = r.Header.Get("X-API-KEY")
		_, _ = io.WriteString(w, `{"data":[]}`)
	}))
	defer srv.Close()

	cfg := testConfig(t, srv)
	store := &memoryAPIKeyStore{keys: map[string]string{cfg.BaseURL(): "saved-key"}}
	c, err := client.NewWithStore(cfg, store)
	if err != nil {
		t.Fatalf("NewWithStore: %v", err)
	}
	if c.AuthMethod() != "environment_api_key" {
		t.Fatalf("AuthMethod() = %q, want environment_api_key", c.AuthMethod())
	}
	if err := c.Do(context.Background(), http.MethodGet, client.PathSelfSites, nil, nil); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if gotAPIKey != "environment-key" {
		t.Fatalf("X-API-KEY = %q, want environment-key", gotAPIKey)
	}
	if store.loadCalls != 0 {
		t.Fatalf("store Load calls = %d, want 0", store.loadCalls)
	}
}

func TestSavedAPIKeyIsLoadedOnceAndSentByFreshClient(t *testing.T) {
	t.Setenv("UNIFI_API_KEY", "")
	var gotAPIKeys []string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAPIKeys = append(gotAPIKeys, r.Header.Get("X-API-KEY"))
		_, _ = io.WriteString(w, `{"data":[]}`)
	}))
	defer srv.Close()

	cfg := testConfig(t, srv)
	store := &memoryAPIKeyStore{keys: map[string]string{cfg.BaseURL(): "saved-key"}}
	c, err := client.NewWithStore(cfg, store)
	if err != nil {
		t.Fatalf("NewWithStore: %v", err)
	}
	if c.AuthMethod() != "saved_api_key" {
		t.Fatalf("AuthMethod() = %q, want saved_api_key", c.AuthMethod())
	}
	for range 2 {
		if err := c.Do(context.Background(), http.MethodGet, client.PathSelfSites, nil, nil); err != nil {
			t.Fatalf("Do: %v", err)
		}
	}
	if store.loadCalls != 1 {
		t.Fatalf("store Load calls = %d, want 1", store.loadCalls)
	}
	if len(gotAPIKeys) != 2 || gotAPIKeys[0] != "saved-key" || gotAPIKeys[1] != "saved-key" {
		t.Fatalf("X-API-KEY values = %q, want saved-key twice", gotAPIKeys)
	}
}

func TestMissingAPIKeyReturnsNotAuthenticatedBeforeRequest(t *testing.T) {
	t.Setenv("UNIFI_API_KEY", "")
	requests := 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := testConfig(t, srv)
	c, err := client.NewWithStore(cfg, &memoryAPIKeyStore{})
	if err != nil {
		t.Fatalf("NewWithStore: %v", err)
	}
	err = c.Do(context.Background(), http.MethodGet, client.PathSelfSites, nil, nil)
	if !apperr.Is(err, apperr.NotAuthenticated) || requests != 0 {
		t.Fatalf("missing-key behavior: err=%v requests=%d", err, requests)
	}
	if got := apperr.As(err).Hint; got != "run 'unifi login' to save an API key" {
		t.Fatalf("hint = %q", got)
	}
}

func TestSavedAPIKey401PreservesPersistedKeyAndHintsLogin(t *testing.T) {
	t.Setenv("UNIFI_API_KEY", "")
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"message":"expired API key"}`)
	}))
	defer srv.Close()

	cfg := testConfig(t, srv)
	otherController := "https://other-controller.example:443"
	store := &memoryAPIKeyStore{keys: map[string]string{
		cfg.BaseURL():   "expired-key",
		otherController: "other-key",
	}}
	c, err := client.NewWithStore(cfg, store)
	if err != nil {
		t.Fatalf("NewWithStore: %v", err)
	}
	err = c.Do(context.Background(), http.MethodGet, client.PathSelfSites, nil, nil)
	if !apperr.Is(err, apperr.AuthFailed) {
		t.Fatalf("err = %v, want auth_failed", err)
	}
	if got := apperr.As(err).Hint; got != "run 'unifi login' to save an API key" {
		t.Fatalf("hint = %q", got)
	}
	if store.deleteCalls != 0 || len(store.deletedControllers) != 0 {
		t.Fatalf("deleted controllers = %q, want none", store.deletedControllers)
	}
	if got := store.keys[cfg.BaseURL()]; got != "expired-key" {
		t.Fatalf("expired controller API key = %q, want persisted key unchanged", got)
	}
	if got := store.keys[otherController]; got != "other-key" {
		t.Fatalf("other controller API key = %q, want other-key", got)
	}
}

func TestDelayedSavedAPIKey401CannotDeleteRotatedPersistedKey(t *testing.T) {
	t.Setenv("UNIFI_API_KEY", "")
	arrived := make(chan struct{})
	release := make(chan struct{})
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(arrived)
		<-release
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	cfg := testConfig(t, srv)
	store := &memoryAPIKeyStore{keys: map[string]string{cfg.BaseURL(): "stale-key"}}
	c, err := client.NewWithStore(cfg, store)
	if err != nil {
		t.Fatalf("NewWithStore: %v", err)
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- c.Do(context.Background(), http.MethodGet, client.PathSelfSites, nil, nil)
	}()
	select {
	case <-arrived:
	case <-time.After(5 * time.Second):
		t.Fatal("stale-key request did not reach controller")
	}
	if err := store.Save(cfg.BaseURL(), "rotated-key", false); err != nil {
		t.Fatalf("rotate persisted key: %v", err)
	}
	close(release)
	err = <-errCh
	if !apperr.Is(err, apperr.AuthFailed) {
		t.Fatalf("err = %v, want auth_failed", err)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if got := store.keys[cfg.BaseURL()]; got != "rotated-key" {
		t.Fatalf("persisted key = %q, want rotated-key", got)
	}
	if store.deleteCalls != 0 {
		t.Fatalf("saved key delete calls = %d, want 0", store.deleteCalls)
	}
}

func TestEnvironmentAPIKey401LeavesStoreUntouched(t *testing.T) {
	t.Setenv("UNIFI_API_KEY", "environment-key")
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	cfg := testConfig(t, srv)
	store := &memoryAPIKeyStore{keys: map[string]string{cfg.BaseURL(): "saved-key"}}
	c, err := client.NewWithStore(cfg, store)
	if err != nil {
		t.Fatalf("NewWithStore: %v", err)
	}
	err = c.Do(context.Background(), http.MethodGet, client.PathSelfSites, nil, nil)
	if !apperr.Is(err, apperr.AuthFailed) {
		t.Fatalf("err = %v, want auth_failed", err)
	}
	if store.loadCalls != 0 || store.deleteCalls != 0 {
		t.Fatalf("environment key accessed store: loads=%d deletes=%d", store.loadCalls, store.deleteCalls)
	}
	if got := store.keys[cfg.BaseURL()]; got != "saved-key" {
		t.Fatalf("saved API key = %q, want saved-key", got)
	}
}

func TestNewWithAPIKeyValidatesEnteredKeyWithoutResolvingEnvironment(t *testing.T) {
	t.Setenv("UNIFI_API_KEY", "environment-key")
	var gotAPIKey, gotPath string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAPIKey = r.Header.Get("X-API-KEY")
		gotPath = r.URL.Path
		_, _ = io.WriteString(w, `{"data":[]}`)
	}))
	defer srv.Close()

	c, err := client.NewWithAPIKey(testConfig(t, srv), "entered-key", "interactive_api_key")
	if err != nil {
		t.Fatalf("NewWithAPIKey: %v", err)
	}
	if c.AuthMethod() != "interactive_api_key" {
		t.Fatalf("AuthMethod() = %q, want interactive_api_key", c.AuthMethod())
	}
	if err := c.Validate(context.Background()); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if gotAPIKey != "entered-key" {
		t.Fatalf("X-API-KEY = %q, want entered-key", gotAPIKey)
	}
	if gotPath != client.PathSelfSites {
		t.Fatalf("request path = %q, want %q", gotPath, client.PathSelfSites)
	}
}

func TestCustomCACertificateEstablishesVerifiedTLS(t *testing.T) {
	tests := []struct {
		name   string
		useEnv bool
	}{
		{name: "config file"},
		{name: "environment", useEnv: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.WriteString(w, `{"data":[]}`)
			}))
			defer srv.Close()

			caPath := filepath.Join(t.TempDir(), "controller-ca.pem")
			caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
			if err := os.WriteFile(caPath, caPEM, 0o600); err != nil {
				t.Fatal(err)
			}

			extra := fmt.Sprintf("ca_cert: %q\n", caPath)
			if tt.useEnv {
				t.Setenv("UNIFI_CA_CERT", caPath)
				extra = ""
			}
			cfg := loadTestConfig(t, srv, extra)
			c, err := client.NewWithAPIKey(cfg, "key", "interactive_api_key")
			if err != nil {
				t.Fatalf("NewWithAPIKey: %v", err)
			}
			if err := c.Do(context.Background(), http.MethodGet, client.PathSelfSites, nil, nil); err != nil {
				t.Fatalf("Do with custom CA: %v", err)
			}
		})
	}
}

func TestCustomCARejectsOversizedAndNonRegularFiles(t *testing.T) {
	tests := []struct {
		name string
		path func(*testing.T) string
		want string
	}{
		{
			name: "oversized regular file",
			path: func(t *testing.T) string {
				path := filepath.Join(t.TempDir(), "controller-ca.pem")
				if err := os.WriteFile(path, []byte(strings.Repeat("x", (1<<20)+1)), 0o600); err != nil {
					t.Fatal(err)
				}
				return path
			},
			want: "exceeds",
		},
		{name: "directory", path: func(t *testing.T) string { return t.TempDir() }, want: "regular file"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := client.NewWithAPIKey(config.Config{
				Host: "controller.example", Port: 443, Timeout: time.Second, CACert: tt.path(t),
			}, "key", "interactive_api_key")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("NewWithAPIKey error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestVerifiedTLSIsDefaultWithoutCustomCAOrInsecureMode(t *testing.T) {
	requests := 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		_, _ = io.WriteString(w, `{"data":[]}`)
	}))
	defer srv.Close()

	cfg := loadTestConfig(t, srv, "")
	c, err := client.NewWithAPIKey(cfg, "key", "interactive_api_key")
	if err != nil {
		t.Fatalf("NewWithAPIKey: %v", err)
	}
	err = c.Do(context.Background(), http.MethodGet, client.PathSelfSites, nil, nil)
	if !apperr.Is(err, apperr.ControllerUnreachable) {
		t.Fatalf("Do error = %v, want TLS verification failure", err)
	}
	if requests != 0 {
		t.Fatalf("controller handler requests = %d, want 0 after TLS verification failure", requests)
	}
}

func TestNewWithAPIKeyRejectsInvalidTransportConfig(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.Config
		want string
	}{
		{
			name: "non-origin host",
			cfg:  config.Config{Host: "https://controller.example/path", Port: 443, Timeout: time.Second},
			want: "host",
		},
		{
			name: "oversized port",
			cfg:  config.Config{Host: "controller.example", Port: 65536, Timeout: time.Second},
			want: "port",
		},
		{
			name: "negative timeout",
			cfg:  config.Config{Host: "controller.example", Port: 443, Timeout: -time.Second},
			want: "timeout",
		},
		{
			name: "conflicting TLS settings",
			cfg: config.Config{
				Host: "controller.example", Port: 443, Timeout: time.Second,
				Insecure: true, CACert: "does-not-need-to-exist.pem",
			},
			want: "insecure",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := client.NewWithAPIKey(tt.cfg, "key", "interactive_api_key")
			if err == nil {
				t.Fatal("NewWithAPIKey unexpectedly accepted invalid transport configuration")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %q, want %q", err, tt.want)
			}
		})
	}
}

func TestNewWithStoreRejectsInvalidDestinationBeforeCredentialLookup(t *testing.T) {
	store := &memoryAPIKeyStore{keys: map[string]string{"https://controller.example:443": "saved-key"}}
	_, err := client.NewWithStore(config.Config{
		Host:    "https://controller.example/path",
		Port:    443,
		Timeout: time.Second,
	}, store)
	if err == nil {
		t.Fatal("NewWithStore unexpectedly accepted an invalid destination")
	}
	if store.loadCalls != 0 {
		t.Fatalf("credential store Load calls = %d, want 0 before destination validation", store.loadCalls)
	}
}

func TestDoMapsConnectionError(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	cfg := testConfig(t, srv)
	srv.Close()

	c, err := client.NewWithAPIKey(cfg, "key", "interactive_api_key")
	if err != nil {
		t.Fatalf("NewWithAPIKey: %v", err)
	}
	err = c.Do(context.Background(), http.MethodGet, c.SitePath(client.PathStatDevice), nil, nil)
	if !apperr.Is(err, apperr.ControllerUnreachable) {
		t.Fatalf("err = %v, want controller_unreachable", err)
	}
}

func TestDoMapsOfficialBadRequestValidationError(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{
			"code":"api.validation.invalid-request",
			"message":"destination traffic filter is invalid",
			"requestId":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
			"requestPath":"/integration/v1/sites/bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb/firewall/policies",
			"statusCode":400,
			"statusName":"BAD_REQUEST",
			"untrusted":"must-not-render"
		}`)
	}))
	defer srv.Close()

	c, err := client.NewWithAPIKey(testConfig(t, srv), "key", "interactive_api_key")
	if err != nil {
		t.Fatalf("NewWithAPIKey: %v", err)
	}
	err = c.DoOfficial(context.Background(), http.MethodPost, "/firewall/policies", map[string]any{"name": "synthetic"}, nil)
	if !apperr.Is(err, apperr.ValidationFailed) {
		t.Fatalf("error = %v, want validation_failed", err)
	}
	want := "validation_failed: controller returned HTTP status 400: api.validation.invalid-request: destination traffic filter is invalid"
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err, want)
	}
	for _, protected := range []string{
		"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
		"/integration/v1/sites/",
		"BAD_REQUEST",
		"must-not-render",
	} {
		if strings.Contains(err.Error(), protected) {
			t.Fatalf("validation error rendered ignored controller field %q: %v", protected, err)
		}
	}
}

func TestDoSanitizesOfficialBadRequestValidationError(t *testing.T) {
	const body = `{
		"code":"api.validation.rejected",
		"message":"address 192.0.2.44, id aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa, mac 02:00:5e:10:00:00, url https://controller.example/private, api-key=synthetic-secret-value, value 'private policy name', token abcdefghijklmnopqrstuvwxyz012345"
	}`
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, body)
	}))
	defer srv.Close()

	c, err := client.NewWithAPIKey(testConfig(t, srv), "key", "interactive_api_key")
	if err != nil {
		t.Fatalf("NewWithAPIKey: %v", err)
	}
	err = c.DoOfficial(context.Background(), http.MethodPost, "/firewall/policies", nil, nil)
	if !apperr.Is(err, apperr.ValidationFailed) {
		t.Fatalf("error = %v, want validation_failed", err)
	}
	if !strings.Contains(err.Error(), "api.validation.rejected") || !strings.Contains(err.Error(), "[redacted]") {
		t.Fatalf("validation error lacks safe diagnostic fields: %v", err)
	}
	for _, protected := range []string{
		"192.0.2.44",
		"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		"02:00:5e:10:00:00",
		"controller.example",
		"synthetic-secret-value",
		"private policy name",
		"abcdefghijklmnopqrstuvwxyz012345",
	} {
		if strings.Contains(err.Error(), protected) {
			t.Fatalf("validation error rendered protected value %q: %v", protected, err)
		}
	}
}

func TestDoRejectsUnsafeOrMalformedOfficialBadRequestDetails(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "malformed", body: `{"code":`, want: "validation failed"},
		{name: "invalid code", body: `{"code":"invalid code","message":"safe words"}`, want: "validation failed"},
		{name: "identifier code", body: `{"code":"api.aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","message":"safe words"}`, want: "validation failed"},
		{name: "embedded raw body", body: `{"code":"api.validation.failed","message":"request contained {raw json}"}`, want: "api.validation.failed"},
		{name: "terminal control", body: `{"code":"api.validation.failed","message":"unsafe\u001b[31mtext"}`, want: "api.validation.failed"},
		{name: "oversized message", body: `{"code":"api.validation.failed","message":"` + strings.Repeat("x", 513) + `"}`, want: "api.validation.failed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, tt.body)
			}))
			defer srv.Close()

			c, err := client.NewWithAPIKey(testConfig(t, srv), "key", "interactive_api_key")
			if err != nil {
				t.Fatalf("NewWithAPIKey: %v", err)
			}
			err = c.DoOfficial(context.Background(), http.MethodPost, "/firewall/policies", nil, nil)
			if !apperr.Is(err, apperr.ValidationFailed) {
				t.Fatalf("error = %v, want validation_failed", err)
			}
			want := "validation_failed: controller returned HTTP status 400: " + tt.want
			if err.Error() != want {
				t.Fatalf("error = %q, want %q", err, want)
			}
		})
	}
}

func TestDoKeepsLegacyBadRequestBodyOpaque(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"code":"legacy.validation","message":"must-not-render"}`)
	}))
	defer srv.Close()

	c, err := client.NewWithAPIKey(testConfig(t, srv), "key", "interactive_api_key")
	if err != nil {
		t.Fatalf("NewWithAPIKey: %v", err)
	}
	err = c.Do(context.Background(), http.MethodPost, "/legacy", nil, nil)
	if !apperr.Is(err, apperr.Internal) {
		t.Fatalf("error = %v, want internal", err)
	}
	want := "internal: controller returned unexpected HTTP status 400"
	if err.Error() != want || strings.Contains(err.Error(), "must-not-render") {
		t.Fatalf("legacy bad-request error = %q, want %q", err, want)
	}
}

func TestDoRejectsOversizedControllerResponse(t *testing.T) {
	const responseLimit = 16 << 20
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.CopyN(w, strings.NewReader(strings.Repeat("x", responseLimit+1)), responseLimit+1)
	}))
	defer srv.Close()

	c, err := client.NewWithAPIKey(testConfig(t, srv), "key", "interactive_api_key")
	if err != nil {
		t.Fatalf("NewWithAPIKey: %v", err)
	}
	err = c.Do(context.Background(), http.MethodGet, client.PathSelfSites, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "response is too large") {
		t.Fatalf("Do error = %v, want bounded-response failure", err)
	}
}

func TestDoRejectsRedirectsBeforeCredentialsOrMutationBodiesAreForwarded(t *testing.T) {
	statuses := []int{
		http.StatusMovedPermanently,
		http.StatusFound,
		http.StatusTemporaryRedirect,
		http.StatusPermanentRedirect,
	}

	for _, status := range statuses {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			var destinationRequests int
			var destinationAPIKey, destinationBody string
			destination := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				destinationRequests++
				destinationAPIKey = r.Header.Get("X-API-KEY")
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Errorf("read redirected body: %v", err)
				}
				destinationBody = string(body)
				_, _ = io.WriteString(w, `{"data":[]}`)
			}))
			defer destination.Close()

			origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, destination.URL+"/redirected", status)
			}))
			defer origin.Close()

			c, err := client.NewWithAPIKey(testConfig(t, origin), "redirect-secret", "interactive_api_key")
			if err != nil {
				t.Fatalf("NewWithAPIKey: %v", err)
			}
			err = c.Do(
				context.Background(),
				http.MethodPost,
				"/mutate",
				map[string]string{"name": "redirect-body-sentinel"},
				nil,
			)

			if destinationRequests != 0 || destinationAPIKey != "" || destinationBody != "" {
				t.Fatalf(
					"redirect destination received requests=%d API-key=%q body=%q",
					destinationRequests,
					destinationAPIKey,
					destinationBody,
				)
			}
			if err == nil {
				t.Fatal("Do unexpectedly accepted a redirect response")
			}
		})
	}
}

func TestSitePath(t *testing.T) {
	c, err := client.NewWithAPIKey(config.Config{
		Host: "127.0.0.1",
		Port: 443,
		Site: "default",
	}, "key", "interactive_api_key")
	if err != nil {
		t.Fatalf("NewWithAPIKey: %v", err)
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

func TestSitePathEscapesEveryLegacyDynamicSegment(t *testing.T) {
	c, err := client.NewWithAPIKey(config.Config{
		Host: "127.0.0.1", Port: 443, Site: "site ?#/%2e%2e",
	}, "key", "interactive_api_key")
	if err != nil {
		t.Fatalf("NewWithAPIKey: %v", err)
	}

	got := c.SitePath("rest/networkconf", "record ?#/../value")
	want := "/proxy/network/api/s/site%20%3F%23%2F%252e%252e/rest/networkconf/record%20%3F%23%2F..%2Fvalue"
	if got != want {
		t.Fatalf("SitePath = %q, want %q", got, want)
	}
}

func TestSitePathEncodesLegacyDotSegments(t *testing.T) {
	c, err := client.NewWithAPIKey(config.Config{
		Host: "127.0.0.1", Port: 443, Site: "..",
	}, "key", "interactive_api_key")
	if err != nil {
		t.Fatalf("NewWithAPIKey: %v", err)
	}

	got := c.SitePath(client.PathRestDevice, ".")
	want := "/proxy/network/api/s/%2E%2E/rest/device/%2E"
	if got != want {
		t.Fatalf("SitePath = %q, want %q", got, want)
	}
}

func TestIntegrationSitePathResolvesConfiguredInternalReference(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/proxy/network/integration/v1/sites" {
			t.Fatalf("request path = %q", r.URL.Path)
		}
		if got := r.URL.Query().Get("limit"); got != "100" {
			t.Fatalf("limit = %q, want 100", got)
		}
		_, _ = io.WriteString(w, `{"offset":0,"limit":100,"count":1,"totalCount":1,"data":[{"id":"site-uuid","internalReference":"default","name":"Default"}]}`)
	}))
	defer srv.Close()

	c, err := client.NewWithAPIKey(testConfig(t, srv), "key", "interactive_api_key")
	if err != nil {
		t.Fatalf("NewWithAPIKey: %v", err)
	}
	got, err := c.IntegrationSitePath(context.Background(), "dns", "policies")
	if err != nil {
		t.Fatalf("IntegrationSitePath: %v", err)
	}
	want := "/proxy/network/integration/v1/sites/site-uuid/dns/policies"
	if got != want {
		t.Fatalf("IntegrationSitePath = %q, want %q", got, want)
	}
}
