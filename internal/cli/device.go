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

func newDeviceRenameCmd() *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:   "rename <id>",
		Short: "Rename a device",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDeviceMutation("rename", args[0], name)
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
			return runDeviceMutation("restart", args[0], "")
		},
	}
}

func newDeviceLocateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "locate <id>",
		Short: "Blink locate LEDs on a device",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDeviceMutation("locate", args[0], "")
		},
	}
}

func newDeviceUpgradeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "upgrade <id>",
		Short: "Upgrade device firmware",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDeviceMutation("upgrade", args[0], "")
		},
	}
}

func newDeviceAdoptCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "adopt <id>",
		Short: "Adopt a pending device",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDeviceMutation("adopt", args[0], "")
		},
	}
}

func newDeviceForgetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "forget <id>",
		Short: "Forget (delete) a device — destructive",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDeviceMutation("forget", args[0], "")
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

func runDeviceMutation(action, id, newName string) error {
	rt, err := loadRuntime(true)
	if err != nil {
		return emitErr("device", action, err)
	}
	svc := domain.NewDeviceService(rt.Client)
	ctx := context.Background()
	build := func(query string) (plan.Plan, domain.Device, error) {
		switch action {
		case "rename":
			return svc.Rename(ctx, query, newName)
		case "restart":
			return svc.Restart(ctx, query)
		case "locate":
			return svc.Locate(ctx, query)
		case "upgrade":
			return svc.Upgrade(ctx, query)
		case "adopt":
			return svc.Adopt(ctx, query)
		case "forget":
			return svc.Forget(ctx, query)
		default:
			return plan.Plan{}, domain.Device{}, fmt.Errorf("unknown action %s", action)
		}
	}

	code := RunPreparedMutation(rt, "device", action,
		func() (plan.PreparedMutation, error) {
			p, d, err := build(id)
			if err != nil {
				return plan.PreparedMutation{}, err
			}
			risk, experimental := task7MutationPolicy("device", action)
			return plan.Targeted(p, d.ID, p.Changes, risk, experimental)
		},
		func(target plan.Target) (any, error) {
			p, _, err := build(target.ID())
			return p.Changes, err
		},
		func(target plan.Target) (any, error) {
			switch action {
			case "rename":
				return svc.ApplyRename(ctx, target.ID(), newName)
			case "restart":
				return svc.ApplyRestart(ctx, target.ID())
			case "locate":
				return svc.ApplyLocate(ctx, target.ID())
			case "upgrade":
				return svc.ApplyUpgrade(ctx, target.ID())
			case "adopt":
				return svc.ApplyAdopt(ctx, target.ID())
			case "forget":
				return svc.ApplyForget(ctx, target.ID())
			default:
				return nil, fmt.Errorf("unknown action %s", action)
			}
		},
	)
	// Emit already wrote output; preserve numeric exit (e.g. validation=2).
	return emittedExit(code)
}
