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

func newDNSCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dns",
		Short: "Manage local DNS records and network resolvers",
	}
	cmd.AddCommand(
		newDNSListCmd(),
		newDNSGetCmd(),
		newDNSCreateCmd(),
		newDNSUpdateCmd(),
		newDNSDeleteCmd(),
		newDNSResolversCmd(),
	)
	return cmd
}

func newDNSListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List local DNS records",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDNSList()
		},
	}
}

func newDNSGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <id>",
		Short: "Get a local DNS record by id or name",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDNSGet(args[0])
		},
	}
}

func newDNSCreateCmd() *cobra.Command {
	var (
		name    string
		ip      string
		enabled bool
		ttl     int
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a local DNS record",
		RunE: func(cmd *cobra.Command, args []string) error {
			in := domain.DNSInput{
				Name:       name,
				IP:         ip,
				Enabled:    enabled,
				SetEnabled: true,
				TTLSeconds: ttl,
				SetTTL:     cmd.Flags().Changed("ttl"),
			}
			if !cmd.Flags().Changed("enabled") {
				in.Enabled = true
			}
			return runDNSCreate(in)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "hostname (e.g. nas.lan)")
	cmd.Flags().StringVar(&ip, "ip", "", "IPv4 address")
	cmd.Flags().BoolVar(&enabled, "enabled", true, "record enabled")
	cmd.Flags().IntVar(&ttl, "ttl", 0, "positive TTL in seconds (default 300)")
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("ip")
	return cmd
}

func newDNSUpdateCmd() *cobra.Command {
	var (
		name    string
		ip      string
		enabled bool
		ttl     int
	)
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update a local DNS record",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			in := domain.DNSInput{
				Name:       name,
				SetName:    cmd.Flags().Changed("name"),
				IP:         ip,
				SetIP:      cmd.Flags().Changed("ip"),
				TTLSeconds: ttl,
				SetTTL:     cmd.Flags().Changed("ttl"),
			}
			if cmd.Flags().Changed("enabled") {
				in.Enabled = enabled
				in.SetEnabled = true
			}
			return runDNSUpdate(args[0], in)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "hostname")
	cmd.Flags().StringVar(&ip, "ip", "", "IPv4 address")
	cmd.Flags().BoolVar(&enabled, "enabled", true, "record enabled")
	cmd.Flags().IntVar(&ttl, "ttl", 0, "positive TTL in seconds")
	return cmd
}

func newDNSDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a local DNS record",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDNSDelete(args[0])
		},
	}
}

func newDNSResolversCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "resolvers",
		Short: "Manage per-network DNS resolvers",
	}
	cmd.AddCommand(
		newDNSResolversListCmd(),
		newDNSResolversSetCmd(),
	)
	return cmd
}

func newDNSResolversListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List DNS resolvers per network",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDNSResolversList()
		},
	}
}

func newDNSResolversSetCmd() *cobra.Command {
	var (
		network string
		servers string
	)
	cmd := &cobra.Command{
		Use:   "set",
		Short: "Set DNS resolvers on a network",
		RunE: func(cmd *cobra.Command, args []string) error {
			list := splitServers(servers)
			if len(list) == 0 {
				return emitErr("dns", "resolvers set", apperr.New(apperr.ValidationFailed, "--servers requires at least one address"))
			}
			return runDNSResolversSet(network, list)
		},
	}
	cmd.Flags().StringVar(&network, "network", "", "network id or name (e.g. LAN)")
	cmd.Flags().StringVar(&servers, "servers", "", "comma-separated DNS servers (e.g. 1.1.1.1,8.8.8.8)")
	_ = cmd.MarkFlagRequired("network")
	_ = cmd.MarkFlagRequired("servers")
	return cmd
}

func splitServers(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func runDNSList() error {
	rt, err := loadRuntime(true)
	if err != nil {
		return emitErr("dns", "list", err)
	}
	svc := domain.NewDNSService(rt.Client)
	items, err := svc.List(context.Background())
	if err != nil {
		code := rt.Emit("dns", "list", nil, nil, err)
		if code != 0 {
			return err
		}
		return nil
	}
	if rt.JSON {
		code := rt.Emit("dns", "list", items, nil, nil)
		if code != 0 {
			return err
		}
		return nil
	}
	headers := []string{"TYPE", "DOMAIN", "VALUE", "ENABLED", "ID"}
	rows := make([][]string, 0, len(items))
	for _, r := range items {
		rows = append(rows, []string{r.Type, r.Domain, dnsDisplayValue(r), strconv.FormatBool(r.Enabled), r.ID})
	}
	return render.WriteTable(rt.Out, headers, rows)
}

func runDNSGet(id string) error {
	rt, err := loadRuntime(true)
	if err != nil {
		return emitErr("dns", "get", err)
	}
	svc := domain.NewDNSService(rt.Client)
	r, err := svc.Get(context.Background(), id)
	if err != nil {
		code := rt.Emit("dns", "get", nil, nil, err)
		if code != 0 {
			return err
		}
		return nil
	}
	if rt.JSON {
		code := rt.Emit("dns", "get", r, nil, nil)
		if code != 0 {
			return err
		}
		return nil
	}
	fmt.Fprintf(rt.Out, "id: %s\n", render.SafeText(r.ID))
	fmt.Fprintf(rt.Out, "type: %s\n", render.SafeText(r.Type))
	fmt.Fprintf(rt.Out, "domain: %s\n", render.SafeText(r.Domain))
	fmt.Fprintf(rt.Out, "value: %s\n", render.SafeText(dnsDisplayValue(r)))
	fmt.Fprintf(rt.Out, "enabled: %s\n", strconv.FormatBool(r.Enabled))
	return nil
}

func runDNSCreate(in domain.DNSInput) error {
	rt, err := loadRuntime(true)
	if err != nil {
		return emitErr("dns", "create", err)
	}
	svc := domain.NewDNSService(rt.Client)
	ctx := context.Background()
	code := RunPreparedMutation(rt, "dns", "create",
		func() (plan.PreparedMutation, error) {
			p, err := svc.Create(ctx, in)
			if err != nil {
				return plan.PreparedMutation{}, err
			}
			return plan.Untargeted(p, plan.Routine, false), nil
		},
		nil,
		func(target plan.Target) (any, error) {
			return svc.ApplyCreate(ctx, in)
		},
	)
	return emittedExit(code)
}

func runDNSUpdate(id string, in domain.DNSInput) error {
	rt, err := loadRuntime(true)
	if err != nil {
		return emitErr("dns", "update", err)
	}
	svc := domain.NewDNSService(rt.Client)
	ctx := context.Background()
	code := RunPreparedMutation(rt, "dns", "update",
		func() (plan.PreparedMutation, error) {
			p, n, err := svc.Update(ctx, id, in)
			if err != nil {
				return plan.PreparedMutation{}, err
			}
			return plan.Targeted(p, n.ID, p.Changes, plan.Routine, false)
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

func dnsDisplayValue(record domain.DNSRecord) string {
	for _, value := range []string{
		record.IPv4Address,
		record.IPv6Address,
		record.TargetDomain,
		record.MailServerDomain,
		record.Text,
		record.ServerDomain,
		record.IPAddress,
	} {
		if value != "" {
			return value
		}
	}
	return ""
}

func runDNSDelete(id string) error {
	rt, err := loadRuntime(true)
	if err != nil {
		return emitErr("dns", "delete", err)
	}
	svc := domain.NewDNSService(rt.Client)
	ctx := context.Background()
	code := RunPreparedMutation(rt, "dns", "delete",
		func() (plan.PreparedMutation, error) {
			p, n, err := svc.Delete(ctx, id)
			if err != nil {
				return plan.PreparedMutation{}, err
			}
			return plan.Targeted(p, n.ID, p.Changes, plan.Destructive, false)
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

func runDNSResolversList() error {
	rt, err := loadRuntime(true)
	if err != nil {
		return emitErr("dns", "resolvers list", err)
	}
	svc := domain.NewDNSService(rt.Client)
	items, err := svc.ListResolvers(context.Background())
	if err != nil {
		code := rt.Emit("dns", "resolvers list", nil, nil, err)
		if code != 0 {
			return err
		}
		return nil
	}
	if rt.JSON {
		code := rt.Emit("dns", "resolvers list", items, nil, nil)
		if code != 0 {
			return err
		}
		return nil
	}
	headers := []string{"NETWORK", "DNS", "WAN", "ID"}
	rows := make([][]string, 0, len(items))
	for _, r := range items {
		rows = append(rows, []string{
			r.NetworkName,
			strings.Join(r.DNS, ","),
			strconv.FormatBool(r.WAN),
			r.NetworkID,
		})
	}
	return render.WriteTable(rt.Out, headers, rows)
}

func runDNSResolversSet(network string, servers []string) error {
	rt, err := loadRuntime(true)
	if err != nil {
		return emitErr("dns", "resolvers set", err)
	}
	svc := domain.NewDNSService(rt.Client)
	ctx := context.Background()
	code := RunPreparedMutation(rt, "dns", "resolvers set",
		func() (plan.PreparedMutation, error) {
			p, n, err := svc.SetResolvers(ctx, network, servers)
			if err != nil {
				return plan.PreparedMutation{}, err
			}
			return plan.Targeted(p, n.NetworkID, p.Changes, plan.Routine, false)
		},
		func(target plan.Target) (any, error) {
			p, _, err := svc.SetResolvers(ctx, target.ID(), servers)
			return p.Changes, err
		},
		func(target plan.Target) (any, error) {
			return svc.ApplySetResolvers(ctx, target.ID(), servers)
		},
	)
	return emittedExit(code)
}
