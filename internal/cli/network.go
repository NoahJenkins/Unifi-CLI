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

func newNetworkCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "network",
		Short: "Manage UniFi networks (LAN/VLAN/WAN)",
	}
	cmd.AddCommand(
		newNetworkListCmd(),
		newNetworkGetCmd(),
		newNetworkCreateCmd(),
		newNetworkUpdateCmd(),
		newNetworkDeleteCmd(),
	)
	return cmd
}

func newNetworkListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List networks",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runNetworkList()
		},
	}
}

func newNetworkGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <id>",
		Short: "Get a network by id or name",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runNetworkGet(args[0])
		},
	}
}

func newNetworkCreateCmd() *cobra.Command {
	var (
		name    string
		vlan    int
		subnet  string
		purpose string
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a network",
		RunE: func(cmd *cobra.Command, args []string) error {
			in := domain.NetworkInput{
				Name:    name,
				Purpose: purpose,
				Subnet:  subnet,
			}
			if cmd.Flags().Changed("vlan") {
				v := vlan
				in.VLAN = &v
			}
			return runNetworkCreate(in)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "network name")
	cmd.Flags().IntVar(&vlan, "vlan", 0, "VLAN id")
	cmd.Flags().StringVar(&subnet, "subnet", "", "subnet CIDR (gateway/prefix)")
	cmd.Flags().StringVar(&purpose, "purpose", "corporate", "purpose (corporate|guest|wan|…)")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

func newNetworkUpdateCmd() *cobra.Command {
	var (
		name    string
		vlan    int
		subnet  string
		purpose string
	)
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update a network",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			in := domain.NetworkInput{
				Name:    name,
				Purpose: purpose,
				Subnet:  subnet,
			}
			if cmd.Flags().Changed("vlan") {
				v := vlan
				in.VLAN = &v
			}
			return runNetworkUpdate(args[0], in)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "network name")
	cmd.Flags().IntVar(&vlan, "vlan", 0, "VLAN id")
	cmd.Flags().StringVar(&subnet, "subnet", "", "subnet CIDR (gateway/prefix)")
	cmd.Flags().StringVar(&purpose, "purpose", "", "purpose (corporate|guest|wan|…)")
	return cmd
}

func newNetworkDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a network (WAN delete is destructive under safe_mode)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runNetworkDelete(args[0])
		},
	}
}

func runNetworkList() error {
	rt, err := loadRuntime(true)
	if err != nil {
		return emitErr("network", "list", err)
	}
	svc := domain.NewNetworkService(rt.Client)
	items, err := svc.List(context.Background())
	if err != nil {
		code := rt.Emit("network", "list", nil, nil, err)
		if code != 0 {
			return err
		}
		return nil
	}
	if rt.JSON {
		code := rt.Emit("network", "list", items, nil, nil)
		if code != 0 {
			return err
		}
		return nil
	}
	headers := []string{"NAME", "PURPOSE", "VLAN", "SUBNET", "DHCP", "WAN"}
	rows := make([][]string, 0, len(items))
	for _, n := range items {
		vlan := ""
		if n.VLAN != nil {
			vlan = strconv.Itoa(*n.VLAN)
		}
		rows = append(rows, []string{
			n.Name,
			n.Purpose,
			vlan,
			n.Subnet,
			strconv.FormatBool(n.DHCPEnabled),
			strconv.FormatBool(n.WAN),
		})
	}
	return render.WriteTable(rt.Out, headers, rows)
}

func runNetworkGet(id string) error {
	rt, err := loadRuntime(true)
	if err != nil {
		return emitErr("network", "get", err)
	}
	svc := domain.NewNetworkService(rt.Client)
	n, err := svc.Get(context.Background(), id)
	if err != nil {
		code := rt.Emit("network", "get", nil, nil, err)
		if code != 0 {
			return err
		}
		return nil
	}
	if rt.JSON {
		code := rt.Emit("network", "get", n, nil, nil)
		if code != 0 {
			return err
		}
		return nil
	}
	fmt.Fprintf(rt.Out, "id: %s\n", render.SafeText(n.ID))
	fmt.Fprintf(rt.Out, "name: %s\n", render.SafeText(n.Name))
	fmt.Fprintf(rt.Out, "purpose: %s\n", render.SafeText(n.Purpose))
	if n.VLAN != nil {
		fmt.Fprintf(rt.Out, "vlan: %d\n", *n.VLAN)
	} else {
		fmt.Fprintf(rt.Out, "vlan:\n")
	}
	fmt.Fprintf(rt.Out, "subnet: %s\n", render.SafeText(n.Subnet))
	fmt.Fprintf(rt.Out, "dhcp_enabled: %s\n", strconv.FormatBool(n.DHCPEnabled))
	fmt.Fprintf(rt.Out, "domain_name: %s\n", render.SafeText(n.DomainName))
	fmt.Fprintf(rt.Out, "wan: %s\n", strconv.FormatBool(n.WAN))
	return nil
}

func runNetworkCreate(in domain.NetworkInput) error {
	rt, err := loadRuntime(true)
	if err != nil {
		return emitErr("network", "create", err)
	}
	svc := domain.NewNetworkService(rt.Client)
	ctx := context.Background()
	code := RunMutation(rt, "network", "create", false,
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

func runNetworkUpdate(id string, in domain.NetworkInput) error {
	rt, err := loadRuntime(true)
	if err != nil {
		return emitErr("network", "update", err)
	}
	svc := domain.NewNetworkService(rt.Client)
	ctx := context.Background()
	code := RunMutation(rt, "network", "update", false,
		func() (plan.Plan, any, error) {
			p, n, err := svc.Update(ctx, id, in)
			return p, n, err
		},
		func() (any, error) {
			return svc.ApplyUpdate(ctx, id, in)
		},
	)
	return emittedExit(code)
}

func runNetworkDelete(id string) error {
	rt, err := loadRuntime(true)
	if err != nil {
		return emitErr("network", "delete", err)
	}
	svc := domain.NewNetworkService(rt.Client)
	ctx := context.Background()

	// Resolve once for destructive flag; plan/apply resolve again inside.
	existing, err := svc.Get(ctx, id)
	if err != nil {
		code := rt.Emit("network", "delete", nil, nil, err)
		return emittedExit(code)
	}
	destructive := domain.NetworkDeleteDestructive(existing)

	code := RunMutation(rt, "network", "delete", destructive,
		func() (plan.Plan, any, error) {
			p, n, err := svc.Delete(ctx, id)
			return p, n, err
		},
		func() (any, error) {
			return svc.ApplyDelete(ctx, id)
		},
	)
	return emittedExit(code)
}
