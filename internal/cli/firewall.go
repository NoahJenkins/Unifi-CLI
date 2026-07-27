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

func newFirewallCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "firewall",
		Short: "Manage UniFi firewall rules",
	}
	cmd.AddCommand(
		newFirewallListCmd(),
		newFirewallGetCmd(),
		newFirewallCreateCmd(),
		newFirewallUpdateCmd(),
		newFirewallDeleteCmd(),
		newFirewallReorderCmd(),
	)
	return cmd
}

func newFirewallListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List firewall rules",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runFirewallList()
		},
	}
}

func newFirewallGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <id>",
		Short: "Get a firewall rule by id or name",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runFirewallGet(args[0])
		},
	}
}

func newFirewallCreateCmd() *cobra.Command {
	var (
		name     string
		enabled  bool
		action   string
		ruleset  string
		src      string
		dst      string
		protocol string
		index    int
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a firewall rule",
		RunE: func(cmd *cobra.Command, args []string) error {
			in := domain.FirewallInput{
				Name:       name,
				Enabled:    enabled,
				SetEnabled: true,
				Action:     action,
				Ruleset:    ruleset,
				Src:        src,
				Dst:        dst,
				Protocol:   protocol,
				Index:      index,
				SetIndex:   cmd.Flags().Changed("index"),
			}
			if !cmd.Flags().Changed("enabled") {
				in.Enabled = true
			}
			return runFirewallCreate(in)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "rule name")
	cmd.Flags().BoolVar(&enabled, "enabled", true, "rule enabled")
	cmd.Flags().StringVar(&action, "action", "", "accept|drop|reject")
	cmd.Flags().StringVar(&ruleset, "ruleset", "", "e.g. LAN_IN, WAN_IN")
	cmd.Flags().StringVar(&src, "src", "", "source address/network")
	cmd.Flags().StringVar(&dst, "dst", "", "destination address/network")
	cmd.Flags().StringVar(&protocol, "protocol", "", "all|tcp|udp|…")
	cmd.Flags().IntVar(&index, "index", 0, "rule index")
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("action")
	_ = cmd.MarkFlagRequired("ruleset")
	return cmd
}

func newFirewallUpdateCmd() *cobra.Command {
	var (
		name     string
		enabled  bool
		action   string
		ruleset  string
		src      string
		dst      string
		protocol string
		index    int
	)
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update a firewall rule",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			in := domain.FirewallInput{
				Name:     name,
				Action:   action,
				Ruleset:  ruleset,
				Src:      src,
				Dst:      dst,
				Protocol: protocol,
			}
			if cmd.Flags().Changed("enabled") {
				in.Enabled = enabled
				in.SetEnabled = true
			}
			if cmd.Flags().Changed("index") {
				in.Index = index
				in.SetIndex = true
			}
			return runFirewallUpdate(args[0], in)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "rule name")
	cmd.Flags().BoolVar(&enabled, "enabled", true, "rule enabled")
	cmd.Flags().StringVar(&action, "action", "", "accept|drop|reject")
	cmd.Flags().StringVar(&ruleset, "ruleset", "", "e.g. LAN_IN, WAN_IN")
	cmd.Flags().StringVar(&src, "src", "", "source address/network")
	cmd.Flags().StringVar(&dst, "dst", "", "destination address/network")
	cmd.Flags().StringVar(&protocol, "protocol", "", "all|tcp|udp|…")
	cmd.Flags().IntVar(&index, "index", 0, "rule index")
	return cmd
}

func newFirewallDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a firewall rule (requires --yes; safe_mode does not block)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runFirewallDelete(args[0])
		},
	}
}

func newFirewallReorderCmd() *cobra.Command {
	var (
		ids   string
		id    string
		index int
	)
	cmd := &cobra.Command{
		Use:   "reorder",
		Short: "Reorder firewall rules",
		RunE: func(cmd *cobra.Command, args []string) error {
			ro := domain.FirewallReorder{}
			if cmd.Flags().Changed("ids") {
				ro.IDs = splitCSV(ids)
			}
			if cmd.Flags().Changed("id") {
				ro.ID = id
			}
			if cmd.Flags().Changed("index") {
				ro.Index = index
				ro.SetIndex = true
			}
			if len(ro.IDs) == 0 && (ro.ID == "" || !ro.SetIndex) {
				return emitErr("firewall", "reorder", apperr.New(apperr.ValidationFailed,
					"reorder requires --ids id1,id2,... or --id X --index N"))
			}
			return runFirewallReorder(ro)
		},
	}
	cmd.Flags().StringVar(&ids, "ids", "", "comma-separated rule ids in desired order")
	cmd.Flags().StringVar(&id, "id", "", "rule id to move")
	cmd.Flags().IntVar(&index, "index", 0, "zero-based position for --id")
	return cmd
}

func splitCSV(s string) []string {
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

func runFirewallList() error {
	rt, err := loadRuntime(true)
	if err != nil {
		return emitErr("firewall", "list", err)
	}
	svc := domain.NewFirewallService(rt.Client)
	items, err := svc.List(context.Background())
	if err != nil {
		code := rt.Emit("firewall", "list", nil, nil, err)
		if code != 0 {
			return err
		}
		return nil
	}
	if rt.JSON {
		code := rt.Emit("firewall", "list", items, nil, nil)
		if code != 0 {
			return err
		}
		return nil
	}
	headers := []string{"INDEX", "NAME", "ACTION", "RULESET", "SRC", "DST", "PROTO", "ENABLED", "ID"}
	rows := make([][]string, 0, len(items))
	for _, r := range items {
		rows = append(rows, []string{
			strconv.Itoa(r.Index),
			r.Name,
			r.Action,
			r.Ruleset,
			r.Src,
			r.Dst,
			r.Protocol,
			strconv.FormatBool(r.Enabled),
			r.ID,
		})
	}
	return render.WriteTable(rt.Out, headers, rows)
}

func runFirewallGet(id string) error {
	rt, err := loadRuntime(true)
	if err != nil {
		return emitErr("firewall", "get", err)
	}
	svc := domain.NewFirewallService(rt.Client)
	r, err := svc.Get(context.Background(), id)
	if err != nil {
		code := rt.Emit("firewall", "get", nil, nil, err)
		if code != 0 {
			return err
		}
		return nil
	}
	if rt.JSON {
		code := rt.Emit("firewall", "get", r, nil, nil)
		if code != 0 {
			return err
		}
		return nil
	}
	fmt.Fprintf(rt.Out, "id: %s\n", r.ID)
	fmt.Fprintf(rt.Out, "name: %s\n", r.Name)
	fmt.Fprintf(rt.Out, "enabled: %s\n", strconv.FormatBool(r.Enabled))
	fmt.Fprintf(rt.Out, "action: %s\n", r.Action)
	fmt.Fprintf(rt.Out, "ruleset: %s\n", r.Ruleset)
	fmt.Fprintf(rt.Out, "src: %s\n", r.Src)
	fmt.Fprintf(rt.Out, "dst: %s\n", r.Dst)
	fmt.Fprintf(rt.Out, "protocol: %s\n", r.Protocol)
	fmt.Fprintf(rt.Out, "index: %d\n", r.Index)
	return nil
}

func runFirewallCreate(in domain.FirewallInput) error {
	rt, err := loadRuntime(true)
	if err != nil {
		return emitErr("firewall", "create", err)
	}
	svc := domain.NewFirewallService(rt.Client)
	ctx := context.Background()
	code := RunMutation(rt, "firewall", "create", false,
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

func runFirewallUpdate(id string, in domain.FirewallInput) error {
	rt, err := loadRuntime(true)
	if err != nil {
		return emitErr("firewall", "update", err)
	}
	svc := domain.NewFirewallService(rt.Client)
	ctx := context.Background()
	code := RunMutation(rt, "firewall", "update", false,
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

func runFirewallDelete(id string) error {
	rt, err := loadRuntime(true)
	if err != nil {
		return emitErr("firewall", "delete", err)
	}
	svc := domain.NewFirewallService(rt.Client)
	ctx := context.Background()
	// Requires --yes via RunMutation; safe_mode does NOT block (forget + WAN only).
	code := RunMutation(rt, "firewall", "delete", false,
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

func runFirewallReorder(ro domain.FirewallReorder) error {
	rt, err := loadRuntime(true)
	if err != nil {
		return emitErr("firewall", "reorder", err)
	}
	svc := domain.NewFirewallService(rt.Client)
	ctx := context.Background()
	code := RunMutation(rt, "firewall", "reorder", false,
		func() (plan.Plan, any, error) {
			p, err := svc.Reorder(ctx, ro)
			return p, nil, err
		},
		func() (any, error) {
			if err := svc.ApplyReorder(ctx, ro); err != nil {
				return nil, err
			}
			return map[string]any{"reordered": true}, nil
		},
	)
	return emittedExit(code)
}
