package cli

import (
	"context"
	"os"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
	"github.com/noahjenkins/unifi-cli/internal/authstore"
	"github.com/noahjenkins/unifi-cli/internal/client"
	"github.com/noahjenkins/unifi-cli/internal/config"
	"github.com/spf13/cobra"
)

type authClient interface {
	Validate(context.Context) error
	AuthMethod() string
	Site() string
}

var (
	newAuthStore = func() authstore.Store {
		return authstore.NewStore(authstore.Options{})
	}
	newClientWithAPIKey = func(cfg config.Config, apiKey, method string) (authClient, error) {
		return client.NewWithAPIKey(cfg, apiKey, method)
	}
	newClientWithStore = func(cfg config.Config, store authstore.Store) (authClient, error) {
		return client.NewWithStore(cfg, store)
	}
)

func newLoginCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Validate and save a controller API key",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := loadAuthCommandRuntime(cmd)
			if err != nil {
				return emitAuthError(rt, "login", err)
			}

			apiKey, err := promptAPIKey(os.Stdin, rt.Out)
			if err != nil {
				return emitAuthError(rt, "login", err)
			}
			validationClient, err := newClientWithAPIKey(rt.Cfg, apiKey, "interactive_api_key")
			if err != nil {
				return emitAuthError(rt, "login", safeAPIKeyValidationError(err))
			}
			if err := validationClient.Validate(cmd.Context()); err != nil {
				return emitAuthError(rt, "login", safeAPIKeyValidationError(err))
			}

			allowFileFallback, err := cmd.Flags().GetBool("file-fallback")
			if err != nil {
				return emitAuthError(rt, "login", err)
			}
			if err := newAuthStore().Save(rt.Cfg.BaseURL(), apiKey, allowFileFallback); err != nil {
				return emitAuthError(rt, "login", err)
			}

			return emittedExit(rt.Emit(
				"auth",
				"login",
				authMetadata(rt.Cfg.Host, validationClient.Site(), "saved_api_key"),
				nil,
				nil,
			))
		},
	}
	cmd.Flags().Bool(
		"file-fallback",
		false,
		"allow protected local API-key storage when the native keyring is unavailable",
	)
	return cmd
}

func newLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Remove the locally saved API key for this controller",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := loadAuthCommandRuntime(cmd)
			if err != nil {
				return emitAuthError(rt, "logout", err)
			}
			if err := newAuthStore().Delete(rt.Cfg.BaseURL()); err != nil {
				return emitAuthError(rt, "logout", err)
			}
			return emittedExit(rt.Emit(
				"auth",
				"logout",
				authMetadata(rt.Cfg.Host, rt.Cfg.Site, "logged_out"),
				nil,
				nil,
			))
		},
	}
}

func loadAuthCommandRuntime(cmd *cobra.Command) (*Runtime, error) {
	rt, err := loadRuntime(false)
	rt.Out = cmd.OutOrStdout()
	rt.Err = cmd.ErrOrStderr()
	return rt, err
}

func emitAuthError(rt *Runtime, action string, err error) error {
	return emittedExit(rt.Emit("auth", action, nil, nil, err))
}

func safeAPIKeyValidationError(err error) error {
	if appError := apperr.As(err); appError != nil {
		switch appError.Code {
		case apperr.AuthFailed, apperr.NotAuthenticated:
			return apperr.WithHint(
				apperr.New(apperr.AuthFailed, "API key validation failed"),
				"verify the API key and controller access",
			)
		case apperr.PermissionDenied:
			return apperr.WithHint(
				apperr.New(apperr.PermissionDenied, "API key is not authorized"),
				"verify the API key's controller permissions",
			)
		case apperr.ControllerUnreachable:
			return apperr.WithHint(
				apperr.New(apperr.ControllerUnreachable, "cannot reach controller to validate API key"),
				"check host, port, TLS settings, and that the controller is online",
			)
		}
	}
	return apperr.WithHint(
		apperr.New(apperr.ValidationFailed, "API key validation failed"),
		"verify the controller address and API key, then try again",
	)
}
