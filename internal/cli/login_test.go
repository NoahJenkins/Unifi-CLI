package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
	"github.com/noahjenkins/unifi-cli/internal/authstore"
	"github.com/noahjenkins/unifi-cli/internal/client"
	"github.com/noahjenkins/unifi-cli/internal/config"
)

func TestLoginPromptsValidatesThenSavesWithoutLeakingKey(t *testing.T) {
	const apiKey = "api-key-not-for-output"
	store := &authCommandStore{}
	var requests int
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if store.saveCalls != 0 {
			t.Fatal("login saved the key before validation completed")
		}
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
	useAuthStore(t, store)
	promptCalls := useAPIPrompt(t, apiKey, nil)
	var constructorCalls int
	previousClient := newClientWithAPIKey
	newClientWithAPIKey = func(gotCfg config.Config, gotKey, gotMethod string) (authClient, error) {
		constructorCalls++
		if gotCfg.BaseURL() != cfg.BaseURL() {
			t.Fatalf("client BaseURL = %q, want %q", gotCfg.BaseURL(), cfg.BaseURL())
		}
		if gotKey != apiKey {
			t.Fatalf("client API key = %q", gotKey)
		}
		if gotMethod != "interactive_api_key" {
			t.Fatalf("client auth method = %q", gotMethod)
		}
		return client.NewWithAPIKey(gotCfg, gotKey, gotMethod)
	}
	t.Cleanup(func() { newClientWithAPIKey = previousClient })

	output := new(bytes.Buffer)
	cmd := newLoginCmd()
	cmd.SetOut(output)
	cmd.SetErr(output)
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("login: %v", err)
	}

	if *promptCalls != 1 {
		t.Fatalf("prompt calls = %d, want 1", *promptCalls)
	}
	if constructorCalls != 1 {
		t.Fatalf("client constructor calls = %d, want 1", constructorCalls)
	}
	if requests != 1 {
		t.Fatalf("validation requests = %d, want 1", requests)
	}
	if store.saveCalls != 1 {
		t.Fatalf("save calls = %d, want 1", store.saveCalls)
	}
	if store.savedBaseURL != cfg.BaseURL() || store.savedKey != apiKey {
		t.Fatalf("saved (%q, %q), want (%q, redacted key)", store.savedBaseURL, store.savedKey, cfg.BaseURL())
	}
	if store.saveFallback {
		t.Fatal("login enabled file fallback without the flag")
	}
	if got := output.String(); !strings.Contains(got, "auth_method: saved_api_key") {
		t.Fatalf("login output missing saved auth method: %q", got)
	}
	if strings.Contains(output.String(), apiKey) {
		t.Fatalf("login output leaked API key: %q", output.String())
	}
}

func TestLoginValidationFailurePreservesExistingSavedKey(t *testing.T) {
	const (
		existingKey = "existing-saved-key"
		rejectedKey = "rejected-interactive-key"
	)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"invalid API key `+rejectedKey+`"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()

	cfg := useAuthCommandConfig(t, srv)
	store := &authCommandStore{keys: map[string]string{cfg.BaseURL(): existingKey}}
	useAuthStore(t, store)
	useAPIPrompt(t, rejectedKey, nil)

	output := new(bytes.Buffer)
	cmd := newLoginCmd()
	cmd.SetOut(output)
	cmd.SetErr(output)
	if err := cmd.ExecuteContext(context.Background()); err == nil {
		t.Fatal("login accepted a rejected API key")
	}
	if store.saveCalls != 0 {
		t.Fatalf("failed validation saved key %d times", store.saveCalls)
	}
	if got := store.keys[cfg.BaseURL()]; got != existingKey {
		t.Fatalf("saved key changed after failed validation: %q", got)
	}
	if strings.Contains(output.String(), rejectedKey) || strings.Contains(output.String(), existingKey) {
		t.Fatalf("failed login output leaked an API key: %q", output.String())
	}
}

func TestLoginValidationFailureNeverEchoesReflectedAPIKey(t *testing.T) {
	const apiKey = "reflected-api-key-not-for-output"
	responses := []struct {
		name     string
		status   int
		wantCode string
	}{
		{name: "unauthorized", status: http.StatusUnauthorized, wantCode: "auth_failed"},
		{name: "forbidden", status: http.StatusForbidden, wantCode: "permission_denied"},
		{name: "unexpected", status: http.StatusBadGateway, wantCode: "validation_failed"},
	}
	for _, response := range responses {
		t.Run(response.name, func(t *testing.T) {
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, `{"message":"rejected `+apiKey+`"}`, response.status)
			}))
			defer srv.Close()

			for _, jsonOutput := range []bool{false, true} {
				name := "text"
				if jsonOutput {
					name = "json"
				}
				t.Run(name, func(t *testing.T) {
					useAuthCommandConfig(t, srv)
					flagJSON = jsonOutput
					useAuthStore(t, &authCommandStore{})
					useAPIPrompt(t, apiKey, nil)

					stdout := new(bytes.Buffer)
					stderr := new(bytes.Buffer)
					cmd := newLoginCmd()
					cmd.SetOut(stdout)
					cmd.SetErr(stderr)
					if err := cmd.ExecuteContext(context.Background()); err == nil {
						t.Fatal("login accepted a reflected rejected API key")
					}
					for stream, output := range map[string]string{
						"stdout": stdout.String(),
						"stderr": stderr.String(),
					} {
						if strings.Contains(output, apiKey) {
							t.Fatalf("%s leaked reflected API key: %q", stream, output)
						}
					}
					if jsonOutput && !strings.Contains(stdout.String(), `"code": "`+response.wantCode+`"`) {
						t.Fatalf("JSON output lacks generic %s result: %q", response.wantCode, stdout.String())
					}
				})
			}
		})
	}
}

func TestLoginFileFallbackAppliesOnlyToThatSave(t *testing.T) {
	const apiKey = "fallback-api-key-not-for-output"
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"data":[]}`)
	}))
	defer srv.Close()

	useAuthCommandConfig(t, srv)
	store := &authCommandStore{}
	useAuthStore(t, store)
	useAPIPrompt(t, apiKey, nil)

	cmd := newLoginCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--file-fallback"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("login --file-fallback: %v", err)
	}
	if store.saveCalls != 1 || !store.saveFallback {
		t.Fatalf("save calls = %d, allowFileFallback = %v", store.saveCalls, store.saveFallback)
	}
}

func TestJSONLoginKeepsInteractivePromptOffStdout(t *testing.T) {
	const apiKey = "json-login-api-key-not-for-output"
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"data":[]}`)
	}))
	defer srv.Close()

	useAuthCommandConfig(t, srv)
	flagJSON = true
	useAuthStore(t, &authCommandStore{})
	previousPrompt := promptAPIKey
	promptAPIKey = func(_ *os.File, promptOut io.Writer) (string, error) {
		_, _ = io.WriteString(promptOut, "API key: ")
		return apiKey, nil
	}
	t.Cleanup(func() { promptAPIKey = previousPrompt })

	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	cmd := newLoginCmd()
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("login: %v", err)
	}

	var envelope map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("JSON stdout is not parseable: %v; stdout=%q", err, stdout.String())
	}
	if strings.Contains(stdout.String(), "API key:") {
		t.Fatalf("JSON stdout contains interactive prompt: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "API key:") {
		t.Fatalf("stderr lacks interactive prompt: %q", stderr.String())
	}
}

func TestLoginGuidesFileFallbackWhenNativeKeyringIsUnavailable(t *testing.T) {
	const apiKey = "fallback-guidance-api-key-not-for-output"
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"data":[]}`)
	}))
	defer srv.Close()

	useAuthCommandConfig(t, srv)
	store := &authCommandStore{saveErr: authstore.ErrKeyringUnavailable}
	useAuthStore(t, store)
	useAPIPrompt(t, apiKey, nil)

	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	cmd := newLoginCmd()
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	if err := cmd.ExecuteContext(context.Background()); err == nil {
		t.Fatal("login succeeded without native keyring or fallback permission")
	}
	output := stdout.String() + stderr.String()
	if !strings.Contains(output, "unifi login --file-fallback") {
		t.Fatalf("login error lacks file-fallback guidance: %q", output)
	}
	if strings.Contains(output, apiKey) {
		t.Fatalf("login error leaked API key: %q", output)
	}
}

func TestPromptAPIKeyRejectsNonTerminalWithEnvironmentGuidance(t *testing.T) {
	in, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()

	_, err = promptAPIKey(in, io.Discard)
	if !apperr.Is(err, apperr.ValidationFailed) {
		t.Fatalf("prompt error = %v, want validation_failed", err)
	}
	if !strings.Contains(err.Error(), "UNIFI_API_KEY") {
		t.Fatalf("prompt error lacks UNIFI_API_KEY guidance: %v", err)
	}
}

func TestLoginNonInteractiveReportsAPIKeyGuidanceBeforeMissingHost(t *testing.T) {
	t.Setenv("UNIFI_CONFIG", t.TempDir()+"/missing.yaml")
	t.Setenv("UNIFI_HOST", "")
	t.Setenv("UNIFI_API_KEY", "")
	t.Setenv("UNIFI_USERNAME", "")
	t.Setenv("UNIFI_PASSWORD", "")
	resetAuthCommandFlags(t)

	useAPIPrompt(t, "", apperr.WithHint(
		apperr.New(apperr.ValidationFailed, "interactive login requires a terminal"),
		apiKeyAutomationHint,
	))
	output := new(bytes.Buffer)
	cmd := newLoginCmd()
	cmd.SetOut(output)
	cmd.SetErr(output)

	if err := cmd.ExecuteContext(context.Background()); err == nil {
		t.Fatal("login accepted non-interactive input")
	}
	if got := output.String(); !strings.Contains(got, "UNIFI_API_KEY") {
		t.Fatalf("login output lacks API-key guidance: %q", got)
	}
	if strings.Contains(output.String(), "host is required") {
		t.Fatalf("login checked the host before rejecting non-interactive input: %q", output.String())
	}
}

func TestPromptAPIKeyHidesInputAndTrimsWhitespace(t *testing.T) {
	previousTerminal := isTerminal
	previousReadPassword := readPassword
	isTerminal = func(int) bool { return true }
	readPassword = func(int) ([]byte, error) {
		return []byte("  hidden-key  \n"), nil
	}
	t.Cleanup(func() {
		isTerminal = previousTerminal
		readPassword = previousReadPassword
	})

	in, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	output := new(bytes.Buffer)
	key, err := promptAPIKey(in, output)
	if err != nil {
		t.Fatalf("prompt API key: %v", err)
	}
	if key != "hidden-key" {
		t.Fatalf("prompt key = %q", key)
	}
	if got := output.String(); got != "API key: \n" {
		t.Fatalf("prompt output = %q", got)
	}
	if strings.Contains(output.String(), key) {
		t.Fatalf("prompt output leaked API key: %q", output.String())
	}
}

func TestPromptAPIKeyRejectsEmptyInputWithoutEchoingIt(t *testing.T) {
	previousTerminal := isTerminal
	previousReadPassword := readPassword
	isTerminal = func(int) bool { return true }
	readPassword = func(int) ([]byte, error) {
		return []byte(" \t\n"), nil
	}
	t.Cleanup(func() {
		isTerminal = previousTerminal
		readPassword = previousReadPassword
	})

	in, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	output := new(bytes.Buffer)
	_, err = promptAPIKey(in, output)
	if !apperr.Is(err, apperr.ValidationFailed) {
		t.Fatalf("prompt error = %v, want validation_failed", err)
	}
	if !strings.Contains(err.Error(), "UNIFI_API_KEY") {
		t.Fatalf("empty prompt error lacks UNIFI_API_KEY guidance: %v", err)
	}
	if strings.Contains(err.Error(), " \t\n") {
		t.Fatalf("prompt error echoed input: %q", err.Error())
	}
}

func TestLogoutDeletesLocalEntriesWithoutConstructingClient(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("logout contacted controller: %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()

	cfg := useAuthCommandConfig(t, srv)
	const apiKey = "saved-key-not-for-output"
	store := &authCommandStore{keys: map[string]string{cfg.BaseURL(): apiKey}}
	useAuthStore(t, store)
	var clientConstructions int
	previousClient := newClientWithStore
	newClientWithStore = func(config.Config, authstore.Store) (authClient, error) {
		clientConstructions++
		return nil, errors.New("logout must not construct a client")
	}
	t.Cleanup(func() { newClientWithStore = previousClient })

	output := new(bytes.Buffer)
	cmd := newLogoutCmd()
	cmd.SetOut(output)
	cmd.SetErr(output)
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if clientConstructions != 0 {
		t.Fatalf("logout constructed %d clients", clientConstructions)
	}
	if store.deleteCalls != 1 || store.deletedBaseURL != cfg.BaseURL() {
		t.Fatalf("delete calls = %d, base URL = %q", store.deleteCalls, store.deletedBaseURL)
	}
	if _, found := store.keys[cfg.BaseURL()]; found {
		t.Fatal("logout left the saved API key behind")
	}
	if got := output.String(); !strings.Contains(got, "auth_method: logged_out") || strings.Contains(got, apiKey) {
		t.Fatalf("unsafe logout output: %q", got)
	}
}

func useAPIPrompt(t *testing.T, key string, err error) *int {
	t.Helper()
	previous := promptAPIKey
	calls := new(int)
	promptAPIKey = func(*os.File, io.Writer) (string, error) {
		*calls++
		return key, err
	}
	t.Cleanup(func() { promptAPIKey = previous })
	return calls
}
