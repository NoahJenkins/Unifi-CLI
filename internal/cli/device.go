package cli

import (
	"context"
	"fmt"
	"strconv"

	"github.com/noahjenkins/unifi-cli/internal/domain"
	"github.com/noahjenkins/unifi-cli/internal/render"
	"github.com/spf13/cobra"
)

func newDeviceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "device",
		Short: "Manage UniFi devices",
	}
	cmd.AddCommand(newDeviceListCmd(), newDeviceGetCmd())
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
	fmt.Fprintf(rt.Out, "id: %s\n", d.ID)
	fmt.Fprintf(rt.Out, "mac: %s\n", d.MAC)
	fmt.Fprintf(rt.Out, "name: %s\n", d.Name)
	fmt.Fprintf(rt.Out, "model: %s\n", d.Model)
	fmt.Fprintf(rt.Out, "type: %s\n", d.Type)
	fmt.Fprintf(rt.Out, "state: %s\n", d.State)
	fmt.Fprintf(rt.Out, "ip: %s\n", d.IP)
	fmt.Fprintf(rt.Out, "version: %s\n", d.Version)
	fmt.Fprintf(rt.Out, "uplink: %s\n", d.Uplink)
	fmt.Fprintf(rt.Out, "adopted: %s\n", strconv.FormatBool(d.Adopted))
	return nil
}
