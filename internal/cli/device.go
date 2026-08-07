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

func newDeviceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "device",
		Short: "Manage UniFi devices",
	}
	cmd.AddCommand(
		newDeviceListCmd(),
		newDeviceGetCmd(),
		newDeviceRenameCmd(),
		newDeviceRestartCmd(),
		newDeviceLocateCmd(),
		newDeviceUpgradeCmd(),
		newDeviceAdoptCmd(),
		newDeviceForgetCmd(),
	)
	return cmd
}

func newDeviceListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List devices",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDeviceList()
		},
	}
}

func newDeviceGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <id>",
		Short: "Get a device by id, mac, or name",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDeviceGet(args[0])
		},
	}
}

// deviceMutationDestructive is the single source of truth for which device
// write actions require safe_mode + --force. CLI wiring must use this map.
var deviceMutationDestructive = map[string]bool{
	"rename":  false,
	"restart": false,
	"locate":  false,
	"upgrade": false,
	"adopt":   false,
	"forget":  true,
}

func newDeviceRenameCmd() *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:   "rename <id>",
		Short: "Rename a device",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDeviceMutation("rename", args[0], deviceMutationDestructive["rename"], name)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "new device name")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

func newDeviceRestartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restart <id>",
		Short: "Restart a device",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDeviceMutation("restart", args[0], deviceMutationDestructive["restart"], "")
		},
	}
}

func newDeviceLocateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "locate <id>",
		Short: "Blink locate LEDs on a device",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDeviceMutation("locate", args[0], deviceMutationDestructive["locate"], "")
		},
	}
}

func newDeviceUpgradeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "upgrade <id>",
		Short: "Upgrade device firmware",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDeviceMutation("upgrade", args[0], deviceMutationDestructive["upgrade"], "")
		},
	}
}

func newDeviceAdoptCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "adopt <id>",
		Short: "Adopt a pending device",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDeviceMutation("adopt", args[0], deviceMutationDestructive["adopt"], "")
		},
	}
}

func newDeviceForgetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "forget <id>",
		Short: "Forget (delete) a device — destructive",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDeviceMutation("forget", args[0], deviceMutationDestructive["forget"], "")
		},
	}
}

func runDeviceList() error {
	rt, err := loadRuntime(true)
	if err != nil {
		return emitErr("device", "list", err)
	}
	svc := domain.NewDeviceService(rt.Client)
	items, err := svc.List(context.Background())
	if err != nil {
		code := rt.Emit("device", "list", nil, nil, err)
		if code != 0 {
			return err
		}
		return nil
	}
	if rt.JSON {
		code := rt.Emit("device", "list", items, nil, nil)
		if code != 0 {
			return err
		}
		return nil
	}
	headers := []string{"NAME", "MAC", "MODEL", "TYPE", "STATE", "IP"}
	rows := make([][]string, 0, len(items))
	for _, d := range items {
		rows = append(rows, []string{d.Name, d.MAC, d.Model, d.Type, d.State, d.IP})
	}
	return render.WriteTable(rt.Out, headers, rows)
}

func runDeviceGet(id string) error {
	rt, err := loadRuntime(true)
	if err != nil {
		return emitErr("device", "get", err)
	}
	svc := domain.NewDeviceService(rt.Client)
	d, err := svc.Get(context.Background(), id)
	if err != nil {
		code := rt.Emit("device", "get", nil, nil, err)
		if code != 0 {
			return err
		}
		return nil
	}
	if rt.JSON {
		code := rt.Emit("device", "get", d, nil, nil)
		if code != 0 {
			return err
		}
		return nil
	}
	fmt.Fprintf(rt.Out, "id: %s\n", render.SafeText(d.ID))
	fmt.Fprintf(rt.Out, "mac: %s\n", render.SafeText(d.MAC))
	fmt.Fprintf(rt.Out, "name: %s\n", render.SafeText(d.Name))
	fmt.Fprintf(rt.Out, "model: %s\n", render.SafeText(d.Model))
	fmt.Fprintf(rt.Out, "type: %s\n", render.SafeText(d.Type))
	fmt.Fprintf(rt.Out, "state: %s\n", render.SafeText(d.State))
	fmt.Fprintf(rt.Out, "ip: %s\n", render.SafeText(d.IP))
	fmt.Fprintf(rt.Out, "version: %s\n", render.SafeText(d.Version))
	fmt.Fprintf(rt.Out, "uplink: %s\n", render.SafeText(d.Uplink))
	fmt.Fprintf(rt.Out, "adopted: %s\n", strconv.FormatBool(d.Adopted))
	return nil
}

func runDeviceMutation(action, id string, destructive bool, newName string) error {
	rt, err := loadRuntime(true)
	if err != nil {
		return emitErr("device", action, err)
	}
	svc := domain.NewDeviceService(rt.Client)
	ctx := context.Background()

	code := RunMutation(rt, "device", action, destructive,
		func() (plan.Plan, any, error) {
			var p plan.Plan
			var d domain.Device
			var err error
			switch action {
			case "rename":
				p, d, err = svc.Rename(ctx, id, newName)
			case "restart":
				p, d, err = svc.Restart(ctx, id)
			case "locate":
				p, d, err = svc.Locate(ctx, id)
			case "upgrade":
				p, d, err = svc.Upgrade(ctx, id)
			case "adopt":
				p, d, err = svc.Adopt(ctx, id)
			case "forget":
				p, d, err = svc.Forget(ctx, id)
			default:
				return plan.Plan{}, nil, fmt.Errorf("unknown action %s", action)
			}
			return p, d, err
		},
		func() (any, error) {
			switch action {
			case "rename":
				return svc.ApplyRename(ctx, id, newName)
			case "restart":
				return svc.ApplyRestart(ctx, id)
			case "locate":
				return svc.ApplyLocate(ctx, id)
			case "upgrade":
				return svc.ApplyUpgrade(ctx, id)
			case "adopt":
				return svc.ApplyAdopt(ctx, id)
			case "forget":
				return svc.ApplyForget(ctx, id)
			default:
				return nil, fmt.Errorf("unknown action %s", action)
			}
		},
	)
	// Emit already wrote output; preserve numeric exit (e.g. validation=2).
	return emittedExit(code)
}
