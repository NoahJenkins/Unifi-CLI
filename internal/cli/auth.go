package cli

import (
	"context"
	"net/http"

	"github.com/noahjenkins/unifi-cli/internal/client"
	"github.com/spf13/cobra"
)

var loadAuthRuntime = loadRuntime

func newAuthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Credential validation and saved-session management",
	}
	cmd.AddCommand(newAuthStatusCmd(), newAuthLoginCmd(), newAuthLogoutCmd())
	return cmd
}

func newAuthStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Validate credentials or a saved session and show auth status",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAuth("status", false)
		},
	}
}

func newAuthLoginCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Create and save an authenticated session",
		RunE: func(cmd *cobra.Command, args []string) error {
			allowFileFallback, err := cmd.Flags().GetBool("file-fallback")
			if err != nil {
				return err
			}
			return runAuth("login", allowFileFallback)
		},
	}
	cmd.Flags().Bool("file-fallback", false, "allow protected local session storage when the native keyring is unavailable")
	return cmd
}

func newAuthLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Remove the locally saved session for this controller",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAuth("logout", false)
		},
	}
}

func runAuth(action string, allowFileFallback bool) error {
	rt, err := loadAuthRuntime(true)
	if err != nil {
		return emitErr("auth", action, err)
	}

	ctx := context.Background()
	switch action {
	case "login":
		if allowFileFallback {
			rt.Client.SetAllowFileFallback(true)
		}
		err = rt.Client.Login(ctx)
	case "status":
		err = rt.Client.Do(ctx, http.MethodGet, client.PathSelfSites, nil, nil)
	case "logout":
		err = rt.Client.LogoutLocalSession()
	}
	if err != nil {
		code := rt.Emit("auth", action, nil, nil, err)
		if code != 0 {
			return err
		}
		return nil
	}

	method := rt.Client.AuthMethod()
	if action == "logout" {
		method = "logged_out"
	}
	code := rt.Emit("auth", action, authMetadata(rt.Cfg.Host, rt.Client.Site(), method), nil, nil)
	if code != 0 {
		return err
	}
	return nil
}

func authMetadata(host, site, method string) map[string]any {
	return map[string]any{
		"host":        host,
		"site":        site,
		"auth_method": method,
	}
}
