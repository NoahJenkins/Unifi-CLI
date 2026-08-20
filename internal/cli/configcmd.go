package cli

import (
	"fmt"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
	"github.com/noahjenkins/unifi-cli/internal/config"
	"github.com/noahjenkins/unifi-cli/internal/render"
	"github.com/spf13/cobra"
)

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Show configuration",
	}
	cmd.AddCommand(newConfigPathCmd(), newConfigShowCmd(), newConfigProfileCmd())
	return cmd
}

func newConfigPathCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "path",
		Short: "Print the effective config file path",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt := baseRuntime()
			selection, err := config.ResolveSelection(flagConfig, flagProfile, newProfileStore())
			if err != nil {
				return emitErr("config", "path", apperr.Newf(apperr.ValidationFailed, "%v", err))
			}
			path := selection.Path
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
		"profile":   rt.Profile,
		"path":      rt.ConfigPath,
		"host":      cfg.Host,
		"port":      cfg.Port,
		"insecure":  cfg.Insecure,
		"ca_cert":   cfg.CACert,
		"site":      cfg.Site,
		"safe_mode": cfg.SafeMode,
		"timeout":   cfg.Timeout.String(),
	}
}

func newConfigProfileCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "profile",
		Short: "Inspect and select named controller profiles",
	}
	cmd.AddCommand(newConfigProfileListCmd(), newConfigProfileShowCmd(), newConfigProfileSelectCmd())
	return cmd
}

func newConfigProfileListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List named controller profiles",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt := baseRuntime()
			profiles, err := newProfileStore().List()
			if err != nil {
				return emittedExit(rt.Emit("config", "profile list", nil, nil, apperr.Newf(apperr.ValidationFailed, "%v", err)))
			}
			if rt.JSON {
				return emittedExit(rt.Emit("config", "profile list", profiles, nil, nil))
			}
			rows := make([][]string, 0, len(profiles))
			for _, profile := range profiles {
				rows = append(rows, []string{
					profile.Name,
					fmt.Sprint(profile.Selected),
					fmt.Sprint(profile.Valid),
					profile.Path,
					profile.Error,
				})
			}
			return render.WriteTable(rt.Out, []string{"NAME", "SELECTED", "VALID", "PATH", "ERROR"}, rows)
		},
	}
}

func newConfigProfileShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show [name]",
		Short: "Show one effective non-secret profile",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store := newProfileStore()
			name := ""
			if len(args) == 1 {
				name = args[0]
			} else {
				selection, err := config.ResolveSelection(flagConfig, flagProfile, store)
				if err != nil {
					return emitErr("config", "profile show", apperr.Newf(apperr.ValidationFailed, "%v", err))
				}
				name = selection.Profile
				if name == "" {
					return emitErr("config", "profile show", apperr.New(apperr.ValidationFailed, "no profile is selected; provide a profile name"))
				}
			}
			info, cfg, err := store.Show(name)
			if err != nil {
				return emitErr("config", "profile show", apperr.Newf(apperr.ValidationFailed, "%v", err))
			}
			rt := baseRuntime()
			rt.Cfg = cfg
			rt.Profile = info.Name
			rt.ConfigPath = info.Path
			data := publicConfig(rt)
			data["valid"] = info.Valid
			data["selected"] = info.Selected
			return emittedExit(rt.Emit("config", "profile show", data, nil, nil))
		},
	}
}

func newConfigProfileSelectCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "select <name>",
		Short: "Select a valid controller profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store := newProfileStore()
			if err := store.Select(args[0]); err != nil {
				return emitErr("config", "profile select", apperr.Newf(apperr.ValidationFailed, "%v", err))
			}
			path, _ := store.ProfilePath(args[0])
			rt := baseRuntime()
			return emittedExit(rt.Emit("config", "profile select", map[string]any{"profile": args[0], "path": path}, nil, nil))
		},
	}
}
