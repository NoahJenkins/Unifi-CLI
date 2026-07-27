package cli

import (
	"context"
	"fmt"
	"strconv"

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
		name     string
		security string
		network  string
		password string
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a WLAN",
		RunE: func(cmd *cobra.Command, args []string) error {
			in := domain.WlanInput{
				Name:     name,
				Security: security,
				Network:  network,
				Password: password,
			}
			return runWlanCreate(in)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "SSID name")
	cmd.Flags().StringVar(&security, "security", "wpapsk", "security mode (wpapsk|open|…)")
	cmd.Flags().StringVar(&network, "network", "", "network id")
	cmd.Flags().StringVar(&password, "password", "", "WLAN password (masked in plans)")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

func newWlanUpdateCmd() *cobra.Command {
	var (
		name     string
		security string
		network  string
		password string
	)
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update a WLAN",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			in := domain.WlanInput{
				Name:     name,
				Security: security,
				Network:  network,
				Password: password,
			}
			return runWlanUpdate(args[0], in)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "SSID name")
	cmd.Flags().StringVar(&security, "security", "", "security mode (wpapsk|open|…)")
	cmd.Flags().StringVar(&network, "network", "", "network id")
	cmd.Flags().StringVar(&password, "password", "", "WLAN password (masked in plans)")
	return cmd
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
	fmt.Fprintf(rt.Out, "id: %s\n", w.ID)
	fmt.Fprintf(rt.Out, "name: %s\n", w.Name)
	fmt.Fprintf(rt.Out, "enabled: %s\n", strconv.FormatBool(w.Enabled))
	fmt.Fprintf(rt.Out, "security: %s\n", w.Security)
	fmt.Fprintf(rt.Out, "network_id: %s\n", w.NetworkID)
	fmt.Fprintf(rt.Out, "band: %s\n", w.Band)
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
	code := RunMutation(rt, "wlan", "create", false,
		func() (plan.Plan, any, error) {
			p, err := svc.Create(ctx, in)
			return p, nil, err
		},
		func() (any, error) {
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
	code := RunMutation(rt, "wlan", "update", false,
		func() (plan.Plan, any, error) {
			p, w, err := svc.Update(ctx, id, in)
			return p, w, err
		},
		func() (any, error) {
			return svc.ApplyUpdate(ctx, id, in)
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
	code := RunMutation(rt, "wlan", "delete", false,
		func() (plan.Plan, any, error) {
			p, w, err := svc.Delete(ctx, id)
			return p, w, err
		},
		func() (any, error) {
			return svc.ApplyDelete(ctx, id)
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
	code := RunMutation(rt, "wlan", "enable", false,
		func() (plan.Plan, any, error) {
			p, w, err := svc.Enable(ctx, id)
			return p, w, err
		},
		func() (any, error) {
			return svc.ApplyEnable(ctx, id)
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
	code := RunMutation(rt, "wlan", "disable", false,
		func() (plan.Plan, any, error) {
			p, w, err := svc.Disable(ctx, id)
			return p, w, err
		},
		func() (any, error) {
			return svc.ApplyDisable(ctx, id)
		},
	)
	return emittedExit(code)
}
