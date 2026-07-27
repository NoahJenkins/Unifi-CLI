package cli

import (
	"context"
	"fmt"
	"strconv"

	"github.com/noahjenkins/unifi-cli/internal/domain"
	"github.com/noahjenkins/unifi-cli/internal/render"
	"github.com/spf13/cobra"
)

func newClientCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "client",
		Short: "Manage UniFi clients",
	}
	cmd.AddCommand(newClientListCmd(), newClientGetCmd())
	return cmd
}

func newClientListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List active clients",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runClientList()
		},
	}
}

func newClientGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <id>",
		Short: "Get a client by id, mac, or name",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runClientGet(args[0])
		},
	}
}

func runClientList() error {
	rt, err := loadRuntime(true)
	if err != nil {
		return emitErr("client", "list", err)
	}
	svc := domain.NewClientService(rt.Client)
	items, err := svc.List(context.Background())
	if err != nil {
		code := rt.Emit("client", "list", nil, nil, err)
		if code != 0 {
			return err
		}
		return nil
	}
	if rt.JSON {
		code := rt.Emit("client", "list", items, nil, nil)
		if code != 0 {
			return err
		}
		return nil
	}
	headers := []string{"NAME", "MAC", "IP", "NETWORK", "ESSID", "WIRED", "BLOCKED"}
	rows := make([][]string, 0, len(items))
	for _, c := range items {
		rows = append(rows, []string{
			c.Name,
			c.MAC,
			c.IP,
			c.Network,
			c.ESSID,
			strconv.FormatBool(c.IsWired),
			strconv.FormatBool(c.Blocked),
		})
	}
	return render.WriteTable(rt.Out, headers, rows)
}

func runClientGet(id string) error {
	rt, err := loadRuntime(true)
	if err != nil {
		return emitErr("client", "get", err)
	}
	svc := domain.NewClientService(rt.Client)
	c, err := svc.Get(context.Background(), id)
	if err != nil {
		code := rt.Emit("client", "get", nil, nil, err)
		if code != 0 {
			return err
		}
		return nil
	}
	if rt.JSON {
		code := rt.Emit("client", "get", c, nil, nil)
		if code != 0 {
			return err
		}
		return nil
	}
	fmt.Fprintf(rt.Out, "id: %s\n", c.ID)
	fmt.Fprintf(rt.Out, "mac: %s\n", c.MAC)
	fmt.Fprintf(rt.Out, "hostname: %s\n", c.Hostname)
	fmt.Fprintf(rt.Out, "name: %s\n", c.Name)
	fmt.Fprintf(rt.Out, "ip: %s\n", c.IP)
	fmt.Fprintf(rt.Out, "essid: %s\n", c.ESSID)
	fmt.Fprintf(rt.Out, "network: %s\n", c.Network)
	fmt.Fprintf(rt.Out, "is_wired: %s\n", strconv.FormatBool(c.IsWired))
	fmt.Fprintf(rt.Out, "blocked: %s\n", strconv.FormatBool(c.Blocked))
	fmt.Fprintf(rt.Out, "last_seen: %s\n", c.LastSeen)
	return nil
}
