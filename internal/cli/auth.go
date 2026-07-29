package cli

import (
	"github.com/spf13/cobra"
)

func newAuthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Inspect the active API-key authentication source",
	}
	cmd.AddCommand(newAuthStatusCmd())
	return cmd
}

func newAuthStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Validate the active API key and show its source",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := loadAuthCommandRuntime(cmd)
			if err != nil {
				return emitAuthError(rt, "status", err)
			}

			authClient, err := newClientWithStore(rt.Cfg, newAuthStore())
			if err == nil {
				err = authClient.Validate(cmd.Context())
			}
			if err != nil {
				return emitAuthError(rt, "status", err)
			}

			return emittedExit(rt.Emit(
				"auth",
				"status",
				authMetadata(rt.Cfg.Host, authClient.Site(), authClient.AuthMethod()),
				nil,
				nil,
			))
		},
	}
}

func authMetadata(host, site, method string) map[string]any {
	return map[string]any{
		"host":        host,
		"site":        site,
		"auth_method": method,
	}
}
