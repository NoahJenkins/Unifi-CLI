package cli

import (
	"fmt"

	"github.com/noahjenkins/unifi-cli/internal/config"
	"github.com/spf13/cobra"
)

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Show configuration",
	}
	cmd.AddCommand(newConfigPathCmd(), newConfigShowCmd())
	return cmd
}

func newConfigPathCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "path",
		Short: "Print the default config file path",
		RunE: func(cmd *cobra.Command, args []string) error {
			rt := baseRuntime()
			path := config.DefaultPath()
			if flagJSON {
				code := rt.Emit("config", "path", map[string]any{"path": path}, nil, nil)
				if code != 0 {
					return fmt.Errorf("exit %d", code)
				}
				return nil
			}
			fmt.Fprintln(rt.Out, path)
			return nil
		},
	}
}

func newConfigShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Print effective non-secret config",
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := loadRuntime(false)
			if err != nil {
				return emitErr("config", "show", err)
			}
			data := publicConfig(rt)
			code := rt.Emit("config", "show", data, nil, nil)
			if code != 0 {
				return fmt.Errorf("exit %d", code)
			}
			return nil
		},
	}
}

func publicConfig(rt *Runtime) map[string]any {
	cfg := rt.Cfg
	return map[string]any{
		"host":      cfg.Host,
		"port":      cfg.Port,
		"insecure":  cfg.Insecure,
		"site":      cfg.Site,
		"safe_mode": cfg.SafeMode,
		"timeout":   cfg.Timeout.String(),
	}
}
