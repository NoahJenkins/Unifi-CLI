package cli

import (
	"context"
	"fmt"
	"strconv"
	"strings"

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
		poe          string
		name         string
		profile      string
		enabled      bool
		clearName    bool
		clearProfile bool
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
				Name:         name,
				SetName:      cmd.Flags().Changed("name"),
				ClearName:    clearName,
				Profile:      profile,
				SetProfile:   cmd.Flags().Changed("profile"),
				ClearProfile: clearProfile,
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
	cmd.Flags().BoolVar(&clearName, "clear-name", false, "clear the port name")
	cmd.Flags().BoolVar(&clearProfile, "clear-profile", false, "clear the port profile")
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
	fmt.Fprintf(rt.Out, "device_id: %s\n", render.SafeText(p.DeviceID))
	fmt.Fprintf(rt.Out, "device_name: %s\n", render.SafeText(p.DeviceName))
	fmt.Fprintf(rt.Out, "port_idx: %d\n", p.PortIdx)
	fmt.Fprintf(rt.Out, "name: %s\n", render.SafeText(p.Name))
	fmt.Fprintf(rt.Out, "media: %s\n", render.SafeText(p.Media))
	fmt.Fprintf(rt.Out, "speed: %s\n", render.SafeText(p.Speed))
	fmt.Fprintf(rt.Out, "poe: %s\n", render.SafeText(p.POE))
	fmt.Fprintf(rt.Out, "enabled: %s\n", strconv.FormatBool(p.Enabled))
	fmt.Fprintf(rt.Out, "profile: %s\n", render.SafeText(p.Profile))
	fmt.Fprintf(rt.Out, "networks: %s\n", render.SafeText(p.Networks))
	return nil
}

func runPortUpdate(device string, portIdx int, in domain.PortInput) error {
	rt, err := loadRuntime(true)
	if err != nil {
		return emitErr("port", "update", err)
	}
	svc := domain.NewPortService(rt.Client)
	ctx := context.Background()

	code := RunPreparedMutation(rt, "port", "update",
		func() (plan.PreparedMutation, error) {
			p, _, err := svc.Update(ctx, device, portIdx, in)
			if err != nil {
				return plan.PreparedMutation{}, err
			}
			if len(p.Changes) != 1 || p.Changes[0].ID == "" {
				return plan.PreparedMutation{}, fmt.Errorf("port update produced invalid target plan")
			}
			return plan.Targeted(p, p.Changes[0].ID, p.Changes, plan.Routine, false)
		},
		func(target plan.Target) (any, error) {
			deviceID, targetPortIdx, err := parsePortTarget(target.ID())
			if err != nil {
				return nil, err
			}
			p, _, err := svc.Update(ctx, deviceID, targetPortIdx, in)
			return p.Changes, err
		},
		func(target plan.Target) (any, error) {
			deviceID, targetPortIdx, err := parsePortTarget(target.ID())
			if err != nil {
				return nil, err
			}
			return svc.ApplyUpdate(ctx, deviceID, targetPortIdx, in)
		},
	)
	return emittedExit(code)
}

func parsePortTarget(targetID string) (string, int, error) {
	separator := strings.LastIndexByte(targetID, '/')
	if separator <= 0 || separator == len(targetID)-1 {
		return "", 0, fmt.Errorf("invalid prepared port target %q", targetID)
	}
	portIdx, err := strconv.Atoi(targetID[separator+1:])
	if err != nil {
		return "", 0, fmt.Errorf("invalid prepared port target %q: %w", targetID, err)
	}
	return targetID[:separator], portIdx, nil
}
