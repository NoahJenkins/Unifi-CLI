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
		name        string
		description string
		enabled     bool
		action      string
		ruleset     string
		src         string
		dst         string
		protocol    string
		index       int
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a firewall rule",
		RunE: func(cmd *cobra.Command, args []string) error {
			in := domain.FirewallInput{
				Name:           name,
				SetName:        true,
				Description:    description,
				SetDescription: cmd.Flags().Changed("description"),
				Enabled:        enabled,
				SetEnabled:     true,
				Action:         action,
				SetAction:      true,
				Ruleset:        ruleset,
				SetRuleset:     true,
				Src:            src,
				SetSrc:         cmd.Flags().Changed("src"),
				Dst:            dst,
				SetDst:         cmd.Flags().Changed("dst"),
				Protocol:       protocol,
				SetProtocol:    cmd.Flags().Changed("protocol"),
				Index:          index,
				SetIndex:       cmd.Flags().Changed("index"),
			}
			if !cmd.Flags().Changed("enabled") {
				in.Enabled = true
			}
			return runFirewallCreate(in)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "rule name")
	cmd.Flags().StringVar(&description, "description", "", "rule description")
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
		name             string
		description      string
		clearDescription bool
		enabled          bool
		action           string
		ruleset          string
		src              string
		dst              string
		protocol         string
		index            int
		clearSrc         bool
		clearDst         bool
	)
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update a firewall rule",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			in := domain.FirewallInput{
				Name:             name,
				SetName:          cmd.Flags().Changed("name"),
				Description:      description,
				SetDescription:   cmd.Flags().Changed("description"),
				ClearDescription: clearDescription,
				Action:           action,
				SetAction:        cmd.Flags().Changed("action"),
				Ruleset:          ruleset,
				SetRuleset:       cmd.Flags().Changed("ruleset"),
				Src:              src,
				SetSrc:           cmd.Flags().Changed("src"),
				ClearSrc:         clearSrc,
				Dst:              dst,
				SetDst:           cmd.Flags().Changed("dst"),
				ClearDst:         clearDst,
				Protocol:         protocol,
				SetProtocol:      cmd.Flags().Changed("protocol"),
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
	cmd.Flags().StringVar(&description, "description", "", "rule description")
	cmd.Flags().BoolVar(&clearDescription, "clear-description", false, "clear the rule description")
	cmd.Flags().BoolVar(&enabled, "enabled", true, "rule enabled")
	cmd.Flags().StringVar(&action, "action", "", "accept|drop|reject")
	cmd.Flags().StringVar(&ruleset, "ruleset", "", "e.g. LAN_IN, WAN_IN")
	cmd.Flags().StringVar(&src, "src", "", "source address/network")
	cmd.Flags().StringVar(&dst, "dst", "", "destination address/network")
	cmd.Flags().BoolVar(&clearSrc, "clear-src", false, "clear the source address/network")
	cmd.Flags().BoolVar(&clearDst, "clear-dst", false, "clear the destination address/network")
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
	fmt.Fprintf(rt.Out, "id: %s\n", render.SafeText(r.ID))
	fmt.Fprintf(rt.Out, "name: %s\n", render.SafeText(r.Name))
	fmt.Fprintf(rt.Out, "description: %s\n", render.SafeText(r.Description))
	fmt.Fprintf(rt.Out, "enabled: %s\n", strconv.FormatBool(r.Enabled))
	fmt.Fprintf(rt.Out, "action: %s\n", render.SafeText(r.Action))
	fmt.Fprintf(rt.Out, "ruleset: %s\n", render.SafeText(r.Ruleset))
	fmt.Fprintf(rt.Out, "src: %s\n", render.SafeText(r.Src))
	fmt.Fprintf(rt.Out, "dst: %s\n", render.SafeText(r.Dst))
	fmt.Fprintf(rt.Out, "protocol: %s\n", render.SafeText(r.Protocol))
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
	code := RunPreparedMutation(rt, "firewall", "create",
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

func runFirewallUpdate(id string, in domain.FirewallInput) error {
	rt, err := loadRuntime(true)
	if err != nil {
		return emitErr("firewall", "update", err)
	}
	svc := domain.NewFirewallService(rt.Client)
	ctx := context.Background()
	code := RunPreparedMutation(rt, "firewall", "update",
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

func runFirewallDelete(id string) error {
	rt, err := loadRuntime(true)
	if err != nil {
		return emitErr("firewall", "delete", err)
	}
	svc := domain.NewFirewallService(rt.Client)
	ctx := context.Background()
	// Requires --yes; safe_mode does NOT block (forget + WAN only).
	code := RunPreparedMutation(rt, "firewall", "delete",
		func() (plan.PreparedMutation, error) {
			p, n, err := svc.Delete(ctx, id)
			if err != nil {
				return plan.PreparedMutation{}, err
			}
			return plan.Targeted(p, n.ID, p.Changes, plan.Routine, false)
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

func runFirewallReorder(ro domain.FirewallReorder) error {
	rt, err := loadRuntime(true)
	if err != nil {
		return emitErr("firewall", "reorder", err)
	}
	svc := domain.NewFirewallService(rt.Client)
	ctx := context.Background()
	const targetSeparator = "\x1f"
	code := RunPreparedMutation(rt, "firewall", "reorder",
		func() (plan.PreparedMutation, error) {
			p, err := svc.Reorder(ctx, ro)
			if err != nil {
				return plan.PreparedMutation{}, err
			}
			if len(p.Changes) != 1 {
				return plan.PreparedMutation{}, fmt.Errorf("firewall reorder produced %d plan changes, want 1", len(p.Changes))
			}
			after, ok := p.Changes[0].After.(map[string]any)
			if !ok {
				return plan.PreparedMutation{}, fmt.Errorf("firewall reorder plan has invalid after snapshot")
			}
			order, ok := after["order"].([]string)
			if !ok || len(order) == 0 {
				return plan.PreparedMutation{}, fmt.Errorf("firewall reorder plan has invalid target order")
			}
			return plan.Targeted(p, strings.Join(order, targetSeparator), p.Changes, plan.Routine, false)
		},
		func(target plan.Target) (any, error) {
			check, err := svc.Reorder(ctx, domain.FirewallReorder{IDs: strings.Split(target.ID(), targetSeparator)})
			if err != nil {
				return nil, err
			}
			if len(check.Changes) != 1 {
				return nil, fmt.Errorf("firewall reorder revalidation produced %d plan changes, want 1", len(check.Changes))
			}
			return check.Changes, nil
		},
		func(target plan.Target) (any, error) {
			resolved := domain.FirewallReorder{IDs: strings.Split(target.ID(), targetSeparator)}
			if err := svc.ApplyReorder(ctx, resolved); err != nil {
				return nil, err
			}
			return map[string]any{"reordered": true}, nil
		},
	)
	return emittedExit(code)
}
