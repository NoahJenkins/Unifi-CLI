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
		name       string
		vlan       int
		subnet     string
		management string
		domainName string
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a network",
		RunE: func(cmd *cobra.Command, args []string) error {
			in := domain.NetworkInput{
				Name:          name,
				SetName:       true,
				Purpose:       management,
				SetPurpose:    true,
				Subnet:        subnet,
				SetSubnet:     cmd.Flags().Changed("subnet"),
				DomainName:    domainName,
				SetDomainName: cmd.Flags().Changed("domain-name"),
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
	cmd.Flags().StringVar(&management, "management", "", "management mode (gateway|switch|unmanaged)")
	cmd.Flags().StringVar(&domainName, "domain-name", "", "DHCP domain name")
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("management")
	return cmd
}

func newNetworkUpdateCmd() *cobra.Command {
	var (
		name            string
		vlan            int
		subnet          string
		management      string
		domainName      string
		clearDomainName bool
	)
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update a network",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			in := domain.NetworkInput{
				Name:            name,
				SetName:         cmd.Flags().Changed("name"),
				Purpose:         management,
				SetPurpose:      cmd.Flags().Changed("management"),
				Subnet:          subnet,
				SetSubnet:       cmd.Flags().Changed("subnet"),
				DomainName:      domainName,
				SetDomainName:   cmd.Flags().Changed("domain-name"),
				ClearDomainName: clearDomainName,
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
	cmd.Flags().StringVar(&management, "management", "", "management mode (gateway|switch|unmanaged)")
	cmd.Flags().StringVar(&domainName, "domain-name", "", "DHCP domain name")
	cmd.Flags().BoolVar(&clearDomainName, "clear-domain-name", false, "clear the DHCP domain name")
	return cmd
}

func newNetworkDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a network (destructive under safe_mode)",
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
	code := RunPreparedMutation(rt, "network", "create",
		func() (plan.PreparedMutation, error) {
			p, err := svc.Create(ctx, in)
			if err != nil {
				return plan.PreparedMutation{}, err
			}
			risk, experimental := task7MutationPolicy("network", "create")
			return plan.Untargeted(p, risk, experimental), nil
		},
		nil,
		func(target plan.Target) (any, error) {
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
	code := RunPreparedMutation(rt, "network", "update",
		func() (plan.PreparedMutation, error) {
			p, n, err := svc.Update(ctx, id, in)
			if err != nil {
				return plan.PreparedMutation{}, err
			}
			risk, experimental := task7MutationPolicy("network", "update")
			return plan.Targeted(p, n.ID, p.Changes, risk, experimental)
		},
		func(target plan.Target) (any, error) {
			p, _, err := svc.Update(ctx, target.ID(), in)
			return p.Changes, err
		},
		func(target plan.Target) (any, error) {
			return svc.ApplyUpdate(ctx, target.ID(), in)
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

	code := RunPreparedMutation(rt, "network", "delete",
		func() (plan.PreparedMutation, error) {
			p, n, err := svc.Delete(ctx, id)
			if err != nil {
				return plan.PreparedMutation{}, err
			}
			risk, experimental := task7MutationPolicy("network", "delete")
			return plan.Targeted(p, n.ID, p.Changes, risk, experimental)
		},
		func(target plan.Target) (any, error) {
			p, _, err := svc.Delete(ctx, target.ID())
			return p.Changes, err
		},
		func(target plan.Target) (any, error) {
			return svc.ApplyDelete(ctx, target.ID())
		},
	)
	return emittedExit(code)
}
