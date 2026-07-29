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

	"github.com/noahjenkins/unifi-cli/internal/authstore"
	"github.com/noahjenkins/unifi-cli/internal/client"
	"github.com/noahjenkins/unifi-cli/internal/config"
	"github.com/spf13/cobra"
)

type authCommandStore struct {
	keys           map[string]string
	loadCalls      int
	saveCalls      int
	deleteCalls    int
	saveFallback   bool
	savedKey       string
	savedBaseURL   string
	deletedBaseURL string
}

func (s *authCommandStore) Load(controller string) (string, bool, error) {
	s.loadCalls++
	key, found := s.keys[controller]
	return key, found, nil
}

func (s *authCommandStore) Save(controller, apiKey string, allowFileFallback bool) error {
	s.saveCalls++
	s.savedBaseURL = controller
	s.savedKey = apiKey
	s.saveFallback = allowFileFallback
	if s.keys == nil {
		s.keys = make(map[string]string)
	}
	s.keys[controller] = apiKey
	return nil
}

func (s *authCommandStore) Delete(controller string) error {
	s.deleteCalls++
	s.deletedBaseURL = controller
	delete(s.keys, controller)
	return nil
}

func TestAuthMetadataOmitsAPIKey(t *testing.T) {
	data := authMetadata("controller.example", "default", "saved_api_key")

	if len(data) != 3 {
		t.Fatalf("metadata field count = %d, want 3: %#v", len(data), data)
	}
	if data["host"] != "controller.example" {
		t.Fatalf("host = %v", data["host"])
	}
	if data["site"] != "default" {
		t.Fatalf("site = %v", data["site"])
	}
	if data["auth_method"] != "saved_api_key" {
		t.Fatalf("auth_method = %v", data["auth_method"])
	}
	for _, secretField := range []string{"password", "api_key", "cookie", "csrf"} {
		if _, found := data[secretField]; found {
			t.Fatalf("metadata exposed %q: %#v", secretField, data)
		}
	}
}

func TestAuthStatusValidatesSavedAPIKey(t *testing.T) {
	const apiKey = "saved-api-key-not-for-output"
	var requests int
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodGet || r.URL.Path != client.PathSelfSites {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("X-API-KEY"); got != apiKey {
			t.Fatalf("X-API-KEY = %q", got)
		}
		_, _ = io.WriteString(w, `{"data":[]}`)
	}))
	defer srv.Close()

	cfg := useAuthCommandConfig(t, srv)
	store := &authCommandStore{keys: map[string]string{cfg.BaseURL(): apiKey}}
	useAuthStore(t, store)

	output := new(bytes.Buffer)
	cmd := newAuthStatusCmd()
	cmd.SetOut(output)
	cmd.SetErr(output)
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("auth status: %v", err)
	}
	if requests != 1 {
		t.Fatalf("status requests = %d, want 1", requests)
	}
	if got := output.String(); !strings.Contains(got, "auth_method: saved_api_key") || strings.Contains(got, apiKey) {
		t.Fatalf("unsafe status output: %q", got)
	}
}

func TestAuthStatusPrefersEnvironmentAPIKeyWithoutLoadingStore(t *testing.T) {
	const apiKey = "environment-api-key-not-for-output"
	var requests int
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodGet || r.URL.Path != client.PathSelfSites {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("X-API-KEY"); got != apiKey {
			t.Fatalf("X-API-KEY = %q", got)
		}
		_, _ = io.WriteString(w, `{"data":[]}`)
	}))
	defer srv.Close()

	useAuthCommandConfig(t, srv)
	t.Setenv("UNIFI_API_KEY", apiKey)
	store := &authCommandStore{}
	useAuthStore(t, store)

	output := new(bytes.Buffer)
	cmd := newAuthStatusCmd()
	cmd.SetOut(output)
	cmd.SetErr(output)
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("auth status: %v", err)
	}
	if requests != 1 {
		t.Fatalf("status requests = %d, want 1", requests)
	}
	if store.loadCalls != 0 {
		t.Fatalf("environment auth loaded store %d times", store.loadCalls)
	}
	if got := output.String(); !strings.Contains(got, "auth_method: environment_api_key") || strings.Contains(got, apiKey) {
		t.Fatalf("unsafe status output: %q", got)
	}
}

func TestAuthContainsOnlyReadOnlyStatus(t *testing.T) {
	cmd := newAuthCmd()
	subcommands := cmd.Commands()
	if len(subcommands) != 1 || subcommands[0].Name() != "status" {
		t.Fatalf("auth subcommands = %v, want only status", commandNames(subcommands))
	}
}

func commandNames(commands []*cobra.Command) []string {
	names := make([]string, 0, len(commands))
	for _, command := range commands {
		names = append(names, command.Name())
	}
	return names
}

func useAuthCommandConfig(t *testing.T, srv *httptest.Server) config.Config {
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

	t.Setenv("UNIFI_CONFIG", t.TempDir()+"/missing.yaml")
	t.Setenv("UNIFI_HOST", host)
	t.Setenv("UNIFI_PORT", portString)
	t.Setenv("UNIFI_INSECURE", "true")
	t.Setenv("UNIFI_SITE", "default")
	t.Setenv("UNIFI_TIMEOUT", "1s")
	t.Setenv("UNIFI_API_KEY", "")
	t.Setenv("UNIFI_USERNAME", "")
	t.Setenv("UNIFI_PASSWORD", "")
	resetAuthCommandFlags(t)

	return config.Config{
		Host:     host,
		Port:     port,
		Insecure: true,
		Site:     "default",
		SafeMode: true,
		Timeout:  time.Second,
	}
}

func resetAuthCommandFlags(t *testing.T) {
	t.Helper()
	previousConfig := flagConfig
	previousJSON := flagJSON
	previousSite := flagSite
	previousTimeout := flagTimeout
	flagConfig = ""
	flagJSON = false
	flagSite = ""
	flagTimeout = ""
	t.Cleanup(func() {
		flagConfig = previousConfig
		flagJSON = previousJSON
		flagSite = previousSite
		flagTimeout = previousTimeout
	})
}

func useAuthStore(t *testing.T, store authstore.Store) {
	t.Helper()
	previous := newAuthStore
	newAuthStore = func() authstore.Store { return store }
	t.Cleanup(func() { newAuthStore = previous })
}
