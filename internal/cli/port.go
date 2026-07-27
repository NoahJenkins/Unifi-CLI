package cli

import (
	"context"
	"fmt"
	"strconv"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
	"github.com/noahjenkins/unifi-cli/internal/domain"
	"github.com/noahjenkins/unifi-cli/internal/plan"
	"github.com/noahjenkins/unifi-cli/internal/render"
	"github.com/spf13/cobra"
)

func newPortCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "port",
		Short: "Manage switch ports",
	}
	cmd.AddCommand(
		newPortListCmd(),
		newPortGetCmd(),
		newPortUpdateCmd(),
	)
	return cmd
}

func newPortListCmd() *cobra.Command {
	var device string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List switch ports",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPortList(device)
		},
	}
	cmd.Flags().StringVar(&device, "device", "", "filter by device id, mac, or name")
	return cmd
}

func newPortGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <device> <port>",
		Short: "Get a switch port by device and port index",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			idx, err := strconv.Atoi(args[1])
			if err != nil {
				return emitErr("port", "get", apperr.Newf(apperr.ValidationFailed, "invalid port index %q", args[1]))
			}
			return runPortGet(args[0], idx)
		},
	}
}

func newPortUpdateCmd() *cobra.Command {
	var (
		poe     string
		name    string
		profile string
		enabled bool
	)
	cmd := &cobra.Command{
		Use:   "update <device> <port>",
		Short: "Update a switch port (PoE, enable, profile, name)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			idx, err := strconv.Atoi(args[1])
			if err != nil {
				return emitErr("port", "update", apperr.Newf(apperr.ValidationFailed, "invalid port index %q", args[1]))
			}
			in := domain.PortInput{
				Name:    name,
				Profile: profile,
			}
			if cmd.Flags().Changed("poe") {
				in.POE = poe
				in.SetPOE = true
			}
			if cmd.Flags().Changed("enabled") {
				in.Enabled = enabled
				in.SetEnabled = true
			}
			return runPortUpdate(args[0], idx, in)
		},
	}
	cmd.Flags().StringVar(&poe, "poe", "", "PoE mode (auto|off|pasv24|passthrough|…)")
	cmd.Flags().StringVar(&name, "name", "", "port name")
	cmd.Flags().StringVar(&profile, "profile", "", "port profile id")
	cmd.Flags().BoolVar(&enabled, "enabled", true, "enable or disable the port")
	return cmd
}

func runPortList(device string) error {
	rt, err := loadRuntime(true)
	if err != nil {
		return emitErr("port", "list", err)
	}
	svc := domain.NewPortService(rt.Client)
	items, err := svc.List(context.Background(), device)
	if err != nil {
		code := rt.Emit("port", "list", nil, nil, err)
		if code != 0 {
			return err
		}
		return nil
	}
	if rt.JSON {
		code := rt.Emit("port", "list", items, nil, nil)
		if code != 0 {
			return err
		}
		return nil
	}
	headers := []string{"DEVICE", "PORT", "NAME", "MEDIA", "SPEED", "POE", "ENABLED", "PROFILE"}
	rows := make([][]string, 0, len(items))
	for _, p := range items {
		rows = append(rows, []string{
			p.DeviceName,
			strconv.Itoa(p.PortIdx),
			p.Name,
			p.Media,
			p.Speed,
			p.POE,
			strconv.FormatBool(p.Enabled),
			p.Profile,
		})
	}
	return render.WriteTable(rt.Out, headers, rows)
}

func runPortGet(device string, portIdx int) error {
	rt, err := loadRuntime(true)
	if err != nil {
		return emitErr("port", "get", err)
	}
	svc := domain.NewPortService(rt.Client)
	p, err := svc.Get(context.Background(), device, portIdx)
	if err != nil {
		code := rt.Emit("port", "get", nil, nil, err)
		if code != 0 {
			return err
		}
		return nil
	}
	if rt.JSON {
		code := rt.Emit("port", "get", p, nil, nil)
		if code != 0 {
			return err
		}
		return nil
	}
	fmt.Fprintf(rt.Out, "device_id: %s\n", p.DeviceID)
	fmt.Fprintf(rt.Out, "device_name: %s\n", p.DeviceName)
	fmt.Fprintf(rt.Out, "port_idx: %d\n", p.PortIdx)
	fmt.Fprintf(rt.Out, "name: %s\n", p.Name)
	fmt.Fprintf(rt.Out, "media: %s\n", p.Media)
	fmt.Fprintf(rt.Out, "speed: %s\n", p.Speed)
	fmt.Fprintf(rt.Out, "poe: %s\n", p.POE)
	fmt.Fprintf(rt.Out, "enabled: %s\n", strconv.FormatBool(p.Enabled))
	fmt.Fprintf(rt.Out, "profile: %s\n", p.Profile)
	fmt.Fprintf(rt.Out, "networks: %s\n", p.Networks)
	return nil
}

func runPortUpdate(device string, portIdx int, in domain.PortInput) error {
	rt, err := loadRuntime(true)
	if err != nil {
		return emitErr("port", "update", err)
	}
	svc := domain.NewPortService(rt.Client)
	ctx := context.Background()

	code := RunMutation(rt, "port", "update", false,
		func() (plan.Plan, any, error) {
			p, cur, err := svc.Update(ctx, device, portIdx, in)
			return p, cur, err
		},
		func() (any, error) {
			return svc.ApplyUpdate(ctx, device, portIdx, in)
		},
	)
	return emittedExit(code)
}
