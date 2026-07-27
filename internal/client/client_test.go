package client_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
	"github.com/noahjenkins/unifi-cli/internal/client"
	"github.com/noahjenkins/unifi-cli/internal/config"
)

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

	c, err := client.New(cfg)
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

	c, err := client.New(cfg)
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

	c, err := client.New(cfg)
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

	c, err := client.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	err = c.Do(context.Background(), http.MethodGet, c.SitePath(client.PathStatDevice), nil, nil)
	if !apperr.Is(err, apperr.ControllerUnreachable) {
		t.Fatalf("err = %v, want controller_unreachable", err)
	}
}

func TestSitePath(t *testing.T) {
	c, err := client.New(config.Config{
		Host: "127.0.0.1",
		Port: 443,
		Site: "default",
	})
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

	c, err := client.New(cfg)
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
	c, err := client.New(config.Config{Host: "127.0.0.1", Port: 443, Site: "default"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	err = c.Login(context.Background())
	if !apperr.Is(err, apperr.ValidationFailed) {
		t.Fatalf("err = %v, want validation_failed", err)
	}
}
