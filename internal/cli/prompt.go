package cli

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
	"golang.org/x/term"
)

const apiKeyAutomationHint = "set UNIFI_API_KEY for non-interactive authentication"

const (
	wlanPasswordAutomationHint = "pass --password-stdin for non-interactive WLAN password input"
	wlanPasswordMaxBytes       = 4 * 1024
)

var (
	isTerminal         = term.IsTerminal
	readPassword       = term.ReadPassword
	promptAPIKey       = promptAPIKeyFromTerminal
	promptWlanPassword = promptWlanPasswordFromTerminal
)

func promptAPIKeyFromTerminal(in *os.File, out io.Writer) (string, error) {
	if !isTerminal(int(in.Fd())) {
		return "", apperr.WithHint(
			apperr.New(apperr.ValidationFailed, "interactive login requires a terminal"),
			apiKeyAutomationHint,
		)
	}
	if _, err := fmt.Fprint(out, "API key: "); err != nil {
		return "", apperr.WithCause(
			apperr.New(apperr.Internal, "write API-key prompt"),
			err,
		)
	}
	input, readErr := readPassword(int(in.Fd()))
	_, newlineErr := fmt.Fprintln(out)
	if readErr != nil {
		return "", apperr.WithCause(
			apperr.New(apperr.Internal, "read API key"),
			readErr,
		)
	}
	if newlineErr != nil {
		return "", apperr.WithCause(
			apperr.New(apperr.Internal, "write API-key prompt"),
			newlineErr,
		)
	}

	apiKey := strings.TrimSpace(string(input))
	if apiKey == "" {
		return "", apperr.WithHint(
			apperr.New(apperr.ValidationFailed, "API key is required"),
			apiKeyAutomationHint,
		)
	}
	return apiKey, nil
}

func promptWlanPasswordFromTerminal(in *os.File, out io.Writer) (string, error) {
	if !isTerminal(int(in.Fd())) {
		return "", apperr.WithHint(
			apperr.New(apperr.ValidationFailed, "WLAN password prompt requires a terminal"),
			wlanPasswordAutomationHint,
		)
	}
	if _, err := fmt.Fprint(out, "WLAN password: "); err != nil {
		return "", apperr.WithCause(apperr.New(apperr.Internal, "write WLAN password prompt"), err)
	}
	input, readErr := readPassword(int(in.Fd()))
	_, newlineErr := fmt.Fprintln(out)
	if readErr != nil {
		return "", apperr.WithCause(apperr.New(apperr.Internal, "read WLAN password"), readErr)
	}
	if newlineErr != nil {
		return "", apperr.WithCause(apperr.New(apperr.Internal, "write WLAN password prompt"), newlineErr)
	}
	if len(input) == 0 {
		return "", apperr.WithHint(
			apperr.New(apperr.ValidationFailed, "WLAN password is required"),
			wlanPasswordAutomationHint,
		)
	}
	if len(input) > wlanPasswordMaxBytes {
		return "", apperr.New(apperr.ValidationFailed, "WLAN password exceeds the 4 KiB limit")
	}
	return string(input), nil
}

func readWlanPasswordFromStdin(in io.Reader) (string, error) {
	input, err := io.ReadAll(io.LimitReader(in, wlanPasswordMaxBytes+3))
	if err != nil {
		return "", apperr.WithCause(apperr.New(apperr.Internal, "read WLAN password from stdin"), err)
	}
	if len(input) > wlanPasswordMaxBytes+2 {
		return "", apperr.New(apperr.ValidationFailed, "WLAN password exceeds the 4 KiB limit")
	}

	line := input
	if bytes.HasSuffix(line, []byte("\r\n")) {
		line = line[:len(line)-2]
	} else if bytes.HasSuffix(line, []byte("\n")) {
		line = line[:len(line)-1]
	}
	if bytes.ContainsRune(line, '\n') {
		return "", apperr.New(apperr.ValidationFailed, "WLAN password stdin must contain exactly one line")
	}
	if len(line) == 0 {
		return "", apperr.New(apperr.ValidationFailed, "WLAN password is required")
	}
	if len(line) > wlanPasswordMaxBytes {
		return "", apperr.New(apperr.ValidationFailed, "WLAN password exceeds the 4 KiB limit")
	}
	return string(line), nil
}
