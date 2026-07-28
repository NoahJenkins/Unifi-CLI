package cli

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/noahjenkins/unifi-cli/internal/client"
	"github.com/noahjenkins/unifi-cli/internal/config"
	"github.com/noahjenkins/unifi-cli/internal/session"
)

func TestAuthMetadataOmitsCredentialsAndSessionMaterial(t *testing.T) {
	data := authMetadata("controller.example", "default", "saved_session")

	if len(data) != 3 {
		t.Fatalf("metadata field count = %d, want 3: %#v", len(data), data)
	}
	if data["host"] != "controller.example" {
		t.Fatalf("host = %v", data["host"])
	}
	if data["site"] != "default" {
		t.Fatalf("site = %v", data["site"])
	}
	if data["auth_method"] != "saved_session" {
		t.Fatalf("auth_method = %v", data["auth_method"])
	}
	for _, secretField := range []string{"password", "api_key", "cookie", "csrf"} {
		if _, found := data[secretField]; found {
			t.Fatalf("metadata exposed %q: %#v", secretField, data)
		}
	}
}

type authTestStore struct {
	sessions          map[string]session.Session
	allowFileFallback bool
	deletedController string
}

func (s *authTestStore) Load(controller string) (session.Session, bool, error) {
	record, found := s.sessions[controller]
	return record, found, nil
}

func (s *authTestStore) Save(record session.Session, allowFileFallback bool) error {
	s.allowFileFallback = allowFileFallback
	if s.sessions == nil {
		s.sessions = make(map[string]session.Session)
	}
	s.sessions[record.Controller] = record
	return nil
}

func (s *authTestStore) Delete(controller string) error {
	s.deletedController = controller
	delete(s.sessions, controller)
	return nil
}

func TestAuthLoginFileFallbackPermitsFallbackOnlyForThatLogin(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/auth/login" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		http.SetCookie(w, &http.Cookie{Name: "TOKEN", Value: "fresh", Path: "/"})
		w.Header().Set("X-CSRF-Token", "fresh-csrf")
		_, _ = io.WriteString(w, `{"meta":{"rc":"ok"}}`)
	}))
	defer srv.Close()

	cfg := authTestConfig(t, srv)
	cfg.Username = "admin"
	cfg.Password = "password-not-for-output"
	store := &authTestStore{}
	c, err := client.NewWithSessionStore(cfg, store)
	if err != nil {
		t.Fatalf("NewWithSessionStore: %v", err)
	}
	output := new(bytes.Buffer)
	useAuthRuntime(t, &Runtime{Cfg: cfg, Client: c, Site: cfg.Site, Out: output, Err: new(bytes.Buffer)})

	cmd := newAuthLoginCmd()
	cmd.SetArgs([]string{"--file-fallback"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("auth login: %v", err)
	}
	if !store.allowFileFallback {
		t.Fatal("auth login --file-fallback did not permit the explicit fallback")
	}
	if got := output.String(); !strings.Contains(got, "auth_method: password") || strings.Contains(got, cfg.Password) {
		t.Fatalf("unsafe login output: %q", got)
	}
}

func TestAuthStatusValidatesSavedSessionAndReportsItsMethod(t *testing.T) {
	var requests int
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodGet || r.URL.Path != client.PathSelfSites {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"data":[]}`)
	}))
	defer srv.Close()

	cfg := authTestConfig(t, srv)
	store := &authTestStore{sessions: map[string]session.Session{
		cfg.BaseURL(): {Controller: cfg.BaseURL(), Cookies: []session.RequestCookie{{Name: "TOKEN", Value: "saved", Path: "/"}}, CSRF: "saved-csrf"},
	}}
	c, err := client.NewWithSessionStore(cfg, store)
	if err != nil {
		t.Fatalf("NewWithSessionStore: %v", err)
	}
	output := new(bytes.Buffer)
	useAuthRuntime(t, &Runtime{Cfg: cfg, Client: c, Site: cfg.Site, Out: output, Err: new(bytes.Buffer)})

	if err := newAuthStatusCmd().ExecuteContext(context.Background()); err != nil {
		t.Fatalf("auth status: %v", err)
	}
	if requests != 1 {
		t.Fatalf("status requests = %d, want 1", requests)
	}
	if got := output.String(); !strings.Contains(got, "auth_method: saved_session") || strings.Contains(got, "saved-csrf") {
		t.Fatalf("unsafe status output: %q", got)
	}
}

func TestAuthLogoutRemovesLocalStateWithoutContactingController(t *testing.T) {
	cfg := config.Config{Host: "controller.example", Port: 8443, Site: "default", APIKey: "api-key-not-for-output", Timeout: time.Second}
	store := &authTestStore{sessions: map[string]session.Session{
		cfg.BaseURL(): {Controller: cfg.BaseURL()},
	}}
	c, err := client.NewWithSessionStore(cfg, store)
	if err != nil {
		t.Fatalf("NewWithSessionStore: %v", err)
	}
	output := new(bytes.Buffer)
	useAuthRuntime(t, &Runtime{Cfg: cfg, Client: c, Site: cfg.Site, Out: output, Err: new(bytes.Buffer)})

	if err := newAuthLogoutCmd().ExecuteContext(context.Background()); err != nil {
		t.Fatalf("auth logout: %v", err)
	}
	if store.deletedController != cfg.BaseURL() {
		t.Fatalf("deleted controller = %q, want %q", store.deletedController, cfg.BaseURL())
	}
	if _, found := store.sessions[cfg.BaseURL()]; found {
		t.Fatal("logout left the saved session behind")
	}
	if got := output.String(); !strings.Contains(got, "auth_method: logged_out") || strings.Contains(got, cfg.APIKey) {
		t.Fatalf("unsafe logout output: %q", got)
	}
}

func useAuthRuntime(t *testing.T, runtime *Runtime) {
	t.Helper()
	previous := loadAuthRuntime
	loadAuthRuntime = func(bool) (*Runtime, error) { return runtime, nil }
	t.Cleanup(func() { loadAuthRuntime = previous })
}

func authTestConfig(t *testing.T, srv *httptest.Server) config.Config {
	t.Helper()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	host, portString, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatalf("split server address: %v", err)
	}
	port, err := strconv.Atoi(portString)
	if err != nil {
		t.Fatalf("parse server port: %v", err)
	}
	return config.Config{Host: host, Port: port, Site: "default", Insecure: true, Timeout: time.Second}
}
