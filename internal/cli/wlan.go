package cli

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
	"github.com/noahjenkins/unifi-cli/internal/domain"
	"github.com/noahjenkins/unifi-cli/internal/plan"
	"github.com/noahjenkins/unifi-cli/internal/render"
	"github.com/spf13/cobra"
)

func newWlanCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "wlan",
		Short: "Manage UniFi WLANs (SSIDs)",
	}
	cmd.AddCommand(
		newWlanListCmd(),
		newWlanGetCmd(),
		newWlanCreateCmd(),
		newWlanUpdateCmd(),
		newWlanDeleteCmd(),
		newWlanEnableCmd(),
		newWlanDisableCmd(),
	)
	return cmd
}

func newWlanListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List WLANs",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWlanList()
		},
	}
}

func newWlanGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <id>",
		Short: "Get a WLAN by id or name",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWlanGet(args[0])
		},
	}
}

func newWlanCreateCmd() *cobra.Command {
	var (
		name          string
		security      string
		network       string
		password      bool
		passwordStdin bool
		securityFlags wlanSecurityFlagValues
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a WLAN",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 0 {
				return apperr.New(apperr.ValidationFailed, "wlan create does not accept positional arguments")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			secret, err := resolveWlanPassword(cmd, password, passwordStdin)
			if err != nil {
				return emitErr("wlan", "create", err)
			}
			if err := validateWlanCreatePassword(security, secret); err != nil {
				return emitErr("wlan", "create", err)
			}
			in := domain.WlanInput{
				Name:        name,
				SetName:     true,
				Security:    security,
				SetSecurity: true,
				Network:     network,
				SetNetwork:  cmd.Flags().Changed("network"),
				Password:    secret,
				SetPassword: password || passwordStdin,
			}
			securityFlags.apply(cmd, &in)
			return runWlanCreate(in)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "SSID name")
	cmd.Flags().StringVar(&security, "security", "wpapsk", "security mode (wpapsk|open|…)")
	cmd.Flags().StringVar(&network, "network", "", "network id")
	cmd.Flags().BoolVar(&password, "password", false, "prompt for the WLAN password")
	cmd.Flags().BoolVar(&passwordStdin, "password-stdin", false, "read the WLAN password from stdin")
	securityFlags.add(cmd)
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

func newWlanUpdateCmd() *cobra.Command {
	var (
		name          string
		security      string
		network       string
		password      bool
		passwordStdin bool
		securityFlags wlanSecurityFlagValues
	)
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update a WLAN",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			secret, err := resolveWlanPassword(cmd, password, passwordStdin)
			if err != nil {
				return emitErr("wlan", "update", err)
			}
			in := domain.WlanInput{
				Name:        name,
				SetName:     cmd.Flags().Changed("name"),
				Security:    security,
				SetSecurity: cmd.Flags().Changed("security"),
				Network:     network,
				SetNetwork:  cmd.Flags().Changed("network"),
				Password:    secret,
				SetPassword: password || passwordStdin,
			}
			securityFlags.apply(cmd, &in)
			return runWlanUpdate(args[0], in)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "SSID name")
	cmd.Flags().StringVar(&security, "security", "", "security mode (wpapsk|open|…)")
	cmd.Flags().StringVar(&network, "network", "", "network id")
	cmd.Flags().BoolVar(&password, "password", false, "prompt for the WLAN password")
	cmd.Flags().BoolVar(&passwordStdin, "password-stdin", false, "read the WLAN password from stdin")
	securityFlags.add(cmd)
	return cmd
}

type wlanSecurityFlagValues struct {
	pmfMode                string
	saeAnticloggingSeconds int
	saeSyncSeconds         int
	fastRoaming            bool
	wpa3FastRoaming        bool
	radiusProfile          string
	radiusNASIDSource      string
	radiusNASID            string
	coa                    bool
	wpa3SecurityMode       string
}

func (v *wlanSecurityFlagValues) add(cmd *cobra.Command) {
	cmd.Flags().StringVar(&v.pmfMode, "pmf-mode", "", "protected management frames mode (required|optional)")
	cmd.Flags().IntVar(&v.saeAnticloggingSeconds, "sae-anticlogging-seconds", 0, "SAE anticlogging threshold in seconds (1-60)")
	cmd.Flags().IntVar(&v.saeSyncSeconds, "sae-sync-seconds", 0, "SAE synchronization time in seconds (1-60)")
	cmd.Flags().BoolVar(&v.fastRoaming, "fast-roaming", false, "enable fast roaming")
	cmd.Flags().BoolVar(&v.wpa3FastRoaming, "wpa3-fast-roaming", false, "enable WPA3 fast roaming for transition security")
	cmd.Flags().StringVar(&v.radiusProfile, "radius-profile", "", "RADIUS profile UUID for enterprise security")
	cmd.Flags().StringVar(&v.radiusNASIDSource, "radius-nas-id-source", "", "derived NAS ID source (device-mac-address|device-name|site-name|bssid)")
	cmd.Flags().StringVar(&v.radiusNASID, "radius-nas-id", "", "user-defined RADIUS NAS ID")
	cmd.Flags().BoolVar(&v.coa, "coa", false, "enable RADIUS Change of Authorization")
	cmd.Flags().StringVar(&v.wpa3SecurityMode, "wpa3-security-mode", "", "WPA3 enterprise mode (default|high-security-192-bit)")
	cmd.MarkFlagsMutuallyExclusive("radius-nas-id-source", "radius-nas-id")
}

func (v wlanSecurityFlagValues) apply(cmd *cobra.Command, in *domain.WlanInput) {
	in.PMFMode, in.SetPMFMode = v.pmfMode, cmd.Flags().Changed("pmf-mode")
	in.SAEAnticloggingThresholdSeconds, in.SetSAEAnticloggingThresholdSeconds = v.saeAnticloggingSeconds, cmd.Flags().Changed("sae-anticlogging-seconds")
	in.SAESyncTimeSeconds, in.SetSAESyncTimeSeconds = v.saeSyncSeconds, cmd.Flags().Changed("sae-sync-seconds")
	in.FastRoamingEnabled, in.SetFastRoamingEnabled = v.fastRoaming, cmd.Flags().Changed("fast-roaming")
	in.WPA3FastRoamingEnabled, in.SetWPA3FastRoamingEnabled = v.wpa3FastRoaming, cmd.Flags().Changed("wpa3-fast-roaming")
	in.RadiusProfileID, in.SetRadiusProfileID = v.radiusProfile, cmd.Flags().Changed("radius-profile")
	in.RadiusNASIDSource, in.SetRadiusNASIDSource = v.radiusNASIDSource, cmd.Flags().Changed("radius-nas-id-source")
	in.RadiusNASID, in.SetRadiusNASID = v.radiusNASID, cmd.Flags().Changed("radius-nas-id")
	in.COAEnabled, in.SetCOAEnabled = v.coa, cmd.Flags().Changed("coa")
	in.WPA3SecurityMode, in.SetWPA3SecurityMode = v.wpa3SecurityMode, cmd.Flags().Changed("wpa3-security-mode")
}

func resolveWlanPassword(cmd *cobra.Command, prompt, stdin bool) (string, error) {
	if prompt && stdin {
		return "", apperr.New(apperr.ValidationFailed, "choose only one WLAN password source")
	}
	if prompt {
		return promptWlanPassword(os.Stdin, cmd.ErrOrStderr())
	}
	if stdin {
		return readWlanPasswordFromStdin(cmd.InOrStdin())
	}
	return "", nil
}

func validateWlanCreatePassword(security, password string) error {
	canonical := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(security), "-", "_"))
	personal := canonical == "wpapsk" || canonical == "wpa2_personal" || canonical == "wpa3_personal" || canonical == "wpa2_wpa3_personal"
	if personal && password == "" {
		return apperr.WithHint(
			apperr.New(apperr.ValidationFailed, "secured WLAN creation requires a password"),
			"pass --password or --password-stdin",
		)
	}
	return nil
}

func newWlanDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a WLAN",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWlanDelete(args[0])
		},
	}
}

func newWlanEnableCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "enable <id>",
		Short: "Enable a WLAN",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWlanEnable(args[0])
		},
	}
}

func newWlanDisableCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "disable <id>",
		Short: "Disable a WLAN",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWlanDisable(args[0])
		},
	}
}

func runWlanList() error {
	rt, err := loadRuntime(true)
	if err != nil {
		return emitErr("wlan", "list", err)
	}
	svc := domain.NewWlanService(rt.Client)
	items, err := svc.List(context.Background())
	if err != nil {
		code := rt.Emit("wlan", "list", nil, nil, err)
		if code != 0 {
			return err
		}
		return nil
	}
	if rt.JSON {
		code := rt.Emit("wlan", "list", items, nil, nil)
		if code != 0 {
			return err
		}
		return nil
	}
	headers := []string{"NAME", "ENABLED", "SECURITY", "NETWORK", "BAND", "GUEST"}
	rows := make([][]string, 0, len(items))
	for _, w := range items {
		rows = append(rows, []string{
			w.Name,
			strconv.FormatBool(w.Enabled),
			w.Security,
			w.NetworkID,
			w.Band,
			strconv.FormatBool(w.Guest),
		})
	}
	return render.WriteTable(rt.Out, headers, rows)
}

func runWlanGet(id string) error {
	rt, err := loadRuntime(true)
	if err != nil {
		return emitErr("wlan", "get", err)
	}
	svc := domain.NewWlanService(rt.Client)
	w, err := svc.Get(context.Background(), id)
	if err != nil {
		code := rt.Emit("wlan", "get", nil, nil, err)
		if code != 0 {
			return err
		}
		return nil
	}
	if rt.JSON {
		code := rt.Emit("wlan", "get", w, nil, nil)
		if code != 0 {
			return err
		}
		return nil
	}
	fmt.Fprintf(rt.Out, "id: %s\n", render.SafeText(w.ID))
	fmt.Fprintf(rt.Out, "name: %s\n", render.SafeText(w.Name))
	fmt.Fprintf(rt.Out, "enabled: %s\n", strconv.FormatBool(w.Enabled))
	fmt.Fprintf(rt.Out, "security: %s\n", render.SafeText(w.Security))
	fmt.Fprintf(rt.Out, "network_id: %s\n", render.SafeText(w.NetworkID))
	fmt.Fprintf(rt.Out, "band: %s\n", render.SafeText(w.Band))
	fmt.Fprintf(rt.Out, "guest: %s\n", strconv.FormatBool(w.Guest))
	return nil
}

func runWlanCreate(in domain.WlanInput) error {
	rt, err := loadRuntime(true)
	if err != nil {
		return emitErr("wlan", "create", err)
	}
	svc := domain.NewWlanService(rt.Client)
	ctx := context.Background()
	code := RunPreparedMutation(rt, "wlan", "create",
		func() (plan.PreparedMutation, error) {
			p, err := svc.Create(ctx, in)
			if err != nil {
				return plan.PreparedMutation{}, err
			}
			risk, experimental := task7MutationPolicy("wlan", "create")
			return plan.Untargeted(p, risk, experimental), nil
		},
		nil,
		func(target plan.Target) (any, error) {
			return svc.ApplyCreate(ctx, in)
		},
	)
	return emittedExit(code)
}

func runWlanUpdate(id string, in domain.WlanInput) error {
	rt, err := loadRuntime(true)
	if err != nil {
		return emitErr("wlan", "update", err)
	}
	svc := domain.NewWlanService(rt.Client)
	ctx := context.Background()
	code := RunPreparedMutation(rt, "wlan", "update",
		func() (plan.PreparedMutation, error) {
			p, w, err := svc.Update(ctx, id, in)
			if err != nil {
				return plan.PreparedMutation{}, err
			}
			risk, experimental := task7MutationPolicy("wlan", "update")
			return plan.Targeted(p, w.ID, p.Changes, risk, experimental)
		},
		func(target plan.Target) (any, error) {
			p, _, err := svc.Update(ctx, target.ID(), in)
			return p.Changes, err
		},
		func(target plan.Target) (any, error) {
			return svc.ApplyUpdatePrepared(ctx, target, target.ID(), in)
		},
	)
	return emittedExit(code)
}

func runWlanDelete(id string) error {
	rt, err := loadRuntime(true)
	if err != nil {
		return emitErr("wlan", "delete", err)
	}
	svc := domain.NewWlanService(rt.Client)
	ctx := context.Background()
	code := RunPreparedMutation(rt, "wlan", "delete",
		func() (plan.PreparedMutation, error) {
			p, w, err := svc.Delete(ctx, id)
			if err != nil {
				return plan.PreparedMutation{}, err
			}
			risk, experimental := task7MutationPolicy("wlan", "delete")
			return plan.Targeted(p, w.ID, p.Changes, risk, experimental)
		},
		func(target plan.Target) (any, error) {
			p, _, err := svc.Delete(ctx, target.ID())
			return p.Changes, err
		},
		func(target plan.Target) (any, error) {
			return svc.ApplyDeletePrepared(ctx, target, target.ID())
		},
	)
	return emittedExit(code)
}

func runWlanEnable(id string) error {
	rt, err := loadRuntime(true)
	if err != nil {
		return emitErr("wlan", "enable", err)
	}
	svc := domain.NewWlanService(rt.Client)
	ctx := context.Background()
	code := RunPreparedMutation(rt, "wlan", "enable",
		func() (plan.PreparedMutation, error) {
			p, w, err := svc.Enable(ctx, id)
			if err != nil {
				return plan.PreparedMutation{}, err
			}
			risk, experimental := task7MutationPolicy("wlan", "enable")
			return plan.Targeted(p, w.ID, p.Changes, risk, experimental)
		},
		func(target plan.Target) (any, error) {
			p, _, err := svc.Enable(ctx, target.ID())
			return p.Changes, err
		},
		func(target plan.Target) (any, error) {
			return svc.ApplyEnablePrepared(ctx, target, target.ID())
		},
	)
	return emittedExit(code)
}

func runWlanDisable(id string) error {
	rt, err := loadRuntime(true)
	if err != nil {
		return emitErr("wlan", "disable", err)
	}
	svc := domain.NewWlanService(rt.Client)
	ctx := context.Background()
	code := RunPreparedMutation(rt, "wlan", "disable",
		func() (plan.PreparedMutation, error) {
			p, w, err := svc.Disable(ctx, id)
			if err != nil {
				return plan.PreparedMutation{}, err
			}
			risk, experimental := task7MutationPolicy("wlan", "disable")
			return plan.Targeted(p, w.ID, p.Changes, risk, experimental)
		},
		func(target plan.Target) (any, error) {
			p, _, err := svc.Disable(ctx, target.ID())
			return p.Changes, err
		},
		func(target plan.Target) (any, error) {
			return svc.ApplyDisablePrepared(ctx, target, target.ID())
		},
	)
	return emittedExit(code)
}
