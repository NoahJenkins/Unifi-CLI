package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
)

func TestReadWlanPasswordFromStdinAcceptsExactlyOneNonEmptyLine(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "no terminator", input: " secret with spaces ", want: " secret with spaces "},
		{name: "line feed", input: "secret\n", want: "secret"},
		{name: "CRLF", input: "secret\r\n", want: "secret"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := readWlanPasswordFromStdin(strings.NewReader(tt.input))
			if err != nil {
				t.Fatalf("readWlanPasswordFromStdin: %v", err)
			}
			if got != tt.want {
				t.Fatalf("password = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestReadWlanPasswordFromStdinRejectsInvalidInputWithoutEchoingIt(t *testing.T) {
	const secret = "never-echo-this-secret"
	tests := []struct {
		name  string
		input string
	}{
		{name: "empty", input: ""},
		{name: "empty line", input: "\n"},
		{name: "multiple lines", input: secret + "\nsecond\n"},
		{name: "oversized", input: strings.Repeat("x", wlanPasswordMaxBytes+1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := readWlanPasswordFromStdin(strings.NewReader(tt.input))
			if err == nil {
				t.Fatal("invalid password input was accepted")
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("error leaked password input: %q", err.Error())
			}
		})
	}
}

func TestPromptWlanPasswordUsesHiddenTerminalInput(t *testing.T) {
	previousTerminal := isTerminal
	previousRead := readPassword
	isTerminal = func(int) bool { return true }
	readPassword = func(int) ([]byte, error) { return []byte(" hidden secret "), nil }
	t.Cleanup(func() {
		isTerminal = previousTerminal
		readPassword = previousRead
	})

	out := new(bytes.Buffer)
	got, err := promptWlanPasswordFromTerminal(os.Stdin, out)
	if err != nil {
		t.Fatalf("promptWlanPasswordFromTerminal: %v", err)
	}
	if got != " hidden secret " {
		t.Fatalf("password = %q", got)
	}
	if output := out.String(); !strings.Contains(output, "WLAN password:") || strings.Contains(output, got) {
		t.Fatalf("unsafe prompt output: %q", output)
	}
}

func TestPromptWlanPasswordRejectsNonTerminalWithAutomationHint(t *testing.T) {
	previousTerminal := isTerminal
	isTerminal = func(int) bool { return false }
	t.Cleanup(func() { isTerminal = previousTerminal })

	_, err := promptWlanPasswordFromTerminal(os.Stdin, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "--password-stdin") {
		t.Fatalf("error = %v", err)
	}
}

func TestResolveWlanPasswordRejectsMultipleSources(t *testing.T) {
	cmd := newWlanCreateCmd()
	_, err := resolveWlanPassword(cmd, true, true)
	if err == nil {
		t.Fatal("multiple password sources were accepted")
	}
}

func TestJSONWlanPromptWritesOnlyToStderr(t *testing.T) {
	const secret = "json-wlan-secret"
	resetAuthCommandFlags(t)
	flagJSON = true
	previousPrompt := promptWlanPassword
	promptWlanPassword = func(_ *os.File, out io.Writer) (string, error) {
		_, _ = io.WriteString(out, "WLAN password: ")
		return secret, apperr.New(apperr.ValidationFailed, "could not read WLAN password")
	}
	t.Cleanup(func() { promptWlanPassword = previousPrompt })

	cmd := newWlanCreateCmd()
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{"--name", "Test", "--password"})
	stdout, stderr, err := captureProcessOutput(t, func() error {
		return cmd.ExecuteContext(context.Background())
	})
	if err == nil {
		t.Fatal("expected injected prompt failure")
	}
	var envelope map[string]any
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatalf("JSON stdout is not parseable: %v; stdout=%q", err, stdout)
	}
	if strings.Contains(stdout, "WLAN password:") {
		t.Fatalf("stdout contains prompt data: %q", stdout)
	}
	if !strings.Contains(stderr, "WLAN password:") {
		t.Fatalf("stderr lacks prompt: %q", stderr)
	}
	if strings.Contains(stdout, secret) || strings.Contains(stderr, secret) {
		t.Fatal("prompt streams leaked the password")
	}
}

func TestWlanCreateRequiresPasswordForSecuredMode(t *testing.T) {
	if err := validateWlanCreatePassword("wpapsk", ""); err == nil {
		t.Fatal("secured WLAN creation accepted no password")
	}
	if err := validateWlanCreatePassword("open", ""); err != nil {
		t.Fatalf("open WLAN creation requires a password: %v", err)
	}
	if err := validateWlanCreatePassword("wpapsk", "secret"); err != nil {
		t.Fatalf("secured WLAN creation rejected a password: %v", err)
	}
}

func TestWlanCreatePasswordDistinguishesPersonalAndEnterprise(t *testing.T) {
	if err := validateWlanCreatePassword("wpa3-personal", ""); err == nil {
		t.Fatal("WPA3 personal accepted a missing passphrase")
	}
	for _, security := range []string{"wpa2-enterprise", "wpa2-wpa3-enterprise", "wpa3-enterprise"} {
		if err := validateWlanCreatePassword(security, ""); err != nil {
			t.Errorf("%s required a personal passphrase: %v", security, err)
		}
	}
}

func TestWlanCreateRejectsLegacyPasswordValueWithoutEchoingIt(t *testing.T) {
	const secret = "legacy-password-value-not-for-output"
	cmd := newWlanCreateCmd()
	cmd.SilenceUsage = true
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--name", "Test", "--password", secret})
	err := cmd.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("legacy string-form --password was accepted")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error leaked legacy password value: %q", err.Error())
	}
}
