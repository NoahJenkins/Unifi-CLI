package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
	"golang.org/x/term"
)

var (
	isTerminal   = term.IsTerminal
	readPassword = term.ReadPassword
	promptAPIKey = promptAPIKeyFromTerminal
)

func promptAPIKeyFromTerminal(in *os.File, out io.Writer) (string, error) {
	if !isTerminal(int(in.Fd())) {
		return "", apperr.WithHint(
			apperr.New(apperr.ValidationFailed, "interactive login requires a terminal"),
			"set UNIFI_API_KEY for non-interactive authentication",
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
		return "", apperr.New(apperr.ValidationFailed, "API key is required")
	}
	return apiKey, nil
}
