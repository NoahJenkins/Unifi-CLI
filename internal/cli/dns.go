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
	var values dnsPolicyFlagValues
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create an official local DNS policy",
		RunE: func(cmd *cobra.Command, args []string) error {
			in, err := values.input(cmd, true)
			if err != nil {
				return emitErr("dns", "create", err)
			}
			return runDNSCreate(in)
		},
	}
	values.bind(cmd, true)
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

func newDNSUpdateCmd() *cobra.Command {
	var values dnsPolicyFlagValues
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update an official local DNS policy",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			in, err := values.input(cmd, false)
			if err != nil {
				return emitErr("dns", "update", err)
			}
			return runDNSUpdate(args[0], in)
		},
	}
	values.bind(cmd, false)
	return cmd
}

type dnsPolicyFlagValues struct {
	recordType       string
	name             string
	ip               string
	ipv4             string
	ipv6             string
	targetDomain     string
	mailServerDomain string
	text             string
	serverDomain     string
	serverIP         string
	priority         int
	service          string
	protocol         string
	port             int
	weight           int
	enabled          bool
	ttl              int
}

func (v *dnsPolicyFlagValues) bind(cmd *cobra.Command, includeType bool) {
	if includeType {
		cmd.Flags().StringVar(&v.recordType, "type", "a", "policy type: a, aaaa, cname, mx, txt, srv, or forward-domain")
	}
	cmd.Flags().StringVar(&v.name, "name", "", "policy domain name")
	cmd.Flags().StringVar(&v.ip, "ip", "", "IPv4 address (legacy alias for --ipv4)")
	cmd.Flags().StringVar(&v.ipv4, "ipv4", "", "A policy IPv4 address")
	cmd.Flags().StringVar(&v.ipv6, "ipv6", "", "AAAA policy IPv6 address")
	cmd.Flags().StringVar(&v.targetDomain, "target-domain", "", "CNAME target domain")
	cmd.Flags().StringVar(&v.mailServerDomain, "mail-server-domain", "", "MX mail server domain")
	cmd.Flags().StringVar(&v.text, "text", "", "TXT policy text")
	cmd.Flags().StringVar(&v.serverDomain, "server-domain", "", "SRV server domain")
	cmd.Flags().StringVar(&v.serverIP, "server-ip", "", "forward-domain DNS server IP address")
	cmd.Flags().IntVar(&v.priority, "priority", 0, "MX or SRV priority (0-65535)")
	cmd.Flags().StringVar(&v.service, "service", "", "SRV service token (for example, _sip)")
	cmd.Flags().StringVar(&v.protocol, "protocol", "", "SRV protocol token (for example, _tcp)")
	cmd.Flags().IntVar(&v.port, "port", 0, "SRV port (0-65535)")
	cmd.Flags().IntVar(&v.weight, "weight", 0, "SRV weight (0-65535)")
	cmd.Flags().BoolVar(&v.enabled, "enabled", true, "policy enabled")
	cmd.Flags().IntVar(&v.ttl, "ttl", 0, "A, AAAA, or CNAME TTL in seconds (default 300 on create)")
}

func (v dnsPolicyFlagValues) input(cmd *cobra.Command, create bool) (domain.DNSInput, error) {
	if cmd.Flags().Changed("ip") && cmd.Flags().Changed("ipv4") {
		return domain.DNSInput{}, apperr.New(apperr.ValidationFailed, "use only one of --ip and --ipv4")
	}
	ipv4 := v.ipv4
	if cmd.Flags().Changed("ip") {
		ipv4 = v.ip
	}
	in := domain.DNSInput{
		Name:                v.name,
		SetName:             cmd.Flags().Changed("name"),
		IP:                  ipv4,
		SetIP:               cmd.Flags().Changed("ip") || cmd.Flags().Changed("ipv4"),
		IPv6Address:         v.ipv6,
		SetIPv6Address:      cmd.Flags().Changed("ipv6"),
		TargetDomain:        v.targetDomain,
		SetTargetDomain:     cmd.Flags().Changed("target-domain"),
		MailServerDomain:    v.mailServerDomain,
		SetMailServerDomain: cmd.Flags().Changed("mail-server-domain"),
		Text:                v.text,
		SetText:             cmd.Flags().Changed("text"),
		ServerDomain:        v.serverDomain,
		SetServerDomain:     cmd.Flags().Changed("server-domain"),
		ServerIP:            v.serverIP,
		SetServerIP:         cmd.Flags().Changed("server-ip"),
		Priority:            v.priority,
		SetPriority:         cmd.Flags().Changed("priority"),
		Service:             v.service,
		SetService:          cmd.Flags().Changed("service"),
		Protocol:            v.protocol,
		SetProtocol:         cmd.Flags().Changed("protocol"),
		Port:                v.port,
		SetPort:             cmd.Flags().Changed("port"),
		Weight:              v.weight,
		SetWeight:           cmd.Flags().Changed("weight"),
		Enabled:             v.enabled,
		SetEnabled:          cmd.Flags().Changed("enabled"),
		TTLSeconds:          v.ttl,
		SetTTL:              cmd.Flags().Changed("ttl"),
	}
	if create {
		in.Type = v.recordType
	}
	return in, nil
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
			return svc.ApplyUpdatePrepared(ctx, target, target.ID(), in)
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
			return svc.ApplyDeletePrepared(ctx, target, target.ID())
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
			risk, experimental := task7MutationPolicy("dns", "resolvers set")
			return plan.Targeted(p, n.NetworkID, p.Changes, risk, experimental)
		},
		func(target plan.Target) (any, error) {
			p, _, err := svc.SetResolvers(ctx, target.ID(), servers)
			return p.Changes, err
		},
		func(target plan.Target) (any, error) {
			return svc.ApplySetResolversPrepared(ctx, target, target.ID(), servers)
		},
	)
	return emittedExit(code)
}
