package cli

import (
	"context"

	"github.com/spf13/cobra"
)

func newAuthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Credential validation",
	}
	cmd.AddCommand(newAuthStatusCmd(), newAuthLoginCmd())
	return cmd
}

func newAuthStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Validate credentials and show auth status",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAuth("status")
		},
	}
}

func newAuthLoginCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "login",
		Short: "Perform an explicit connectivity and auth check",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAuth("login")
		},
	}
}

func runAuth(action string) error {
	rt, err := loadRuntime(true)
	if err != nil {
		return emitErr("auth", action, err)
	}

	ctx := context.Background()
	err = rt.Client.Login(ctx)
	if err != nil {
		code := rt.Emit("auth", action, nil, nil, err)
		if code != 0 {
			return err
		}
		return nil
	}

	data := map[string]any{
		"host":        rt.Cfg.Host,
		"site":        rt.Client.Site(),
		"auth_method": authMethod(rt),
	}
	code := rt.Emit("auth", action, data, nil, nil)
	if code != 0 {
		return err
	}
	return nil
}

func authMethod(rt *Runtime) string {
	if rt.Cfg.APIKey != "" {
		return "api_key"
	}
	return "password"
}
