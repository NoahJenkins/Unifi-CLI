package client_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
	"github.com/noahjenkins/unifi-cli/internal/authstore"
	"github.com/noahjenkins/unifi-cli/internal/client"
	"github.com/noahjenkins/unifi-cli/internal/config"
)

type memoryAPIKeyStore struct {
	keys               map[string]string
	loadErr            error
	deleteErr          error
	loadCalls          int
	saveCalls          int
	deleteCalls        int
	deletedControllers []string
}

func (s *memoryAPIKeyStore) Load(controller string) (string, bool, error) {
	s.loadCalls++
	if s.loadErr != nil {
		return "", false, s.loadErr
	}
	apiKey, found := s.keys[controller]
	return apiKey, found, nil
}

func (s *memoryAPIKeyStore) Save(controller, apiKey string, _ bool) error {
	s.saveCalls++
	if s.keys == nil {
		s.keys = make(map[string]string)
	}
	s.keys[controller] = apiKey
	return nil
}

func (s *memoryAPIKeyStore) Delete(controller string) error {
	s.deleteCalls++
	s.deletedControllers = append(s.deletedControllers, controller)
	if s.deleteErr != nil {
		return s.deleteErr
	}
	delete(s.keys, controller)
	return nil
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

func TestSavedAPIKey401DeletesOnlyThatControllerAndHintsLogin(t *testing.T) {
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
	if store.deleteCalls != 1 || len(store.deletedControllers) != 1 || store.deletedControllers[0] != cfg.BaseURL() {
		t.Fatalf("deleted controllers = %q, want only %q", store.deletedControllers, cfg.BaseURL())
	}
	if _, found := store.keys[cfg.BaseURL()]; found {
		t.Fatal("expired controller API key was not deleted")
	}
	if got := store.keys[otherController]; got != "other-key" {
		t.Fatalf("other controller API key = %q, want other-key", got)
	}
}

func TestSavedAPIKey401PreservesSafeDeleteFailureCause(t *testing.T) {
	t.Setenv("UNIFI_API_KEY", "")
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	cfg := testConfig(t, srv)
	deleteErr := errors.New("keyring path contains secret=do-not-render")
	store := &memoryAPIKeyStore{
		keys:      map[string]string{cfg.BaseURL(): "expired-key"},
		deleteErr: deleteErr,
	}
	c, err := client.NewWithStore(cfg, store)
	if err != nil {
		t.Fatalf("NewWithStore: %v", err)
	}
	err = c.Do(context.Background(), http.MethodGet, client.PathSelfSites, nil, nil)
	if !apperr.Is(err, apperr.AuthFailed) {
		t.Fatalf("err = %v, want auth_failed", err)
	}
	if !errors.Is(err, deleteErr) {
		t.Fatalf("delete failure was not preserved as cause: %v", err)
	}
	if strings.Contains(err.Error(), "secret=do-not-render") {
		t.Fatalf("delete failure rendered sensitive detail: %v", err)
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
