package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/noahjenkins/unifi-cli/internal/domain"
	"github.com/noahjenkins/unifi-cli/internal/plan"
	"github.com/noahjenkins/unifi-cli/internal/render"
	"github.com/spf13/cobra"
)

// firewallMutationRisk is the approved risk classification for every firewall
// write. Keep command wiring and the exhaustive test in sync when adding verbs.
var firewallMutationRisk = map[string]plan.RiskClass{
	"create":  plan.HighImpact,
	"update":  plan.HighImpact,
	"delete":  plan.Destructive,
	"reorder": plan.HighImpact,
}

func newFirewallCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "firewall",
		Short: "Manage UniFi firewall policies and zones",
	}
	cmd.AddCommand(
		newFirewallListCmd(),
		newFirewallGetCmd(),
		newFirewallZoneCmd(),
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
		Short: "List firewall policies",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runFirewallList()
		},
	}
}

func newFirewallGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <id|exact-name>",
		Short: "Get a firewall policy",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runFirewallGet(args[0])
		},
	}
}

func newFirewallZoneCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "zone", Short: "Read firewall zones"}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "list",
			Short: "List firewall zones",
			RunE: func(cmd *cobra.Command, args []string) error {
				return runFirewallZoneList()
			},
		},
		&cobra.Command{
			Use:   "get <id|exact-name>",
			Short: "Get a firewall zone",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				return runFirewallZoneGet(args[0])
			},
		},
	)
	return cmd
}

func newFirewallCreateCmd() *cobra.Command {
	var in domain.FirewallInput
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create an experimental firewall policy",
		RunE: func(cmd *cobra.Command, args []string) error {
			in.SetName = true
			in.SetDescription = cmd.Flags().Changed("description")
			in.SetEnabled = cmd.Flags().Changed("enabled")
			in.SetAction = true
			in.SetAllowReturnTraffic = cmd.Flags().Changed("allow-return-traffic")
			in.SetSourceZone = true
			in.SetDestinationZone = true
			in.SetIPVersion = cmd.Flags().Changed("ip-version")
			in.SetProtocol = cmd.Flags().Changed("protocol")
			in.SetLoggingEnabled = cmd.Flags().Changed("logging-enabled")
			return runFirewallCreate(in)
		},
	}
	cmd.Flags().StringVar(&in.Name, "name", "", "policy name")
	cmd.Flags().StringVar(&in.Description, "description", "", "policy description")
	cmd.Flags().BoolVar(&in.Enabled, "enabled", true, "policy enabled")
	cmd.Flags().StringVar(&in.Action, "action", "", "allow|block|reject")
	cmd.Flags().BoolVar(&in.AllowReturnTraffic, "allow-return-traffic", false, "allow mirrored return traffic for action allow")
	cmd.Flags().StringVar(&in.SourceZone, "source-zone", "", "source firewall zone id or exact name")
	cmd.Flags().StringVar(&in.DestinationZone, "destination-zone", "", "destination firewall zone id or exact name")
	cmd.Flags().StringVar(&in.IPVersion, "ip-version", "ipv4_and_ipv6", "ipv4|ipv6|ipv4_and_ipv6")
	cmd.Flags().StringVar(&in.Protocol, "protocol", "all", "all|tcp|udp|tcp_udp|icmp|icmpv6")
	cmd.Flags().BoolVar(&in.LoggingEnabled, "logging-enabled", false, "send matches to the configured remote syslog server")
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("action")
	_ = cmd.MarkFlagRequired("source-zone")
	_ = cmd.MarkFlagRequired("destination-zone")
	return cmd
}

func newFirewallUpdateCmd() *cobra.Command {
	var in domain.FirewallInput
	cmd := &cobra.Command{
		Use:   "update <id|exact-name>",
		Short: "Update an experimental firewall policy",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			in.SetName = cmd.Flags().Changed("name")
			in.SetDescription = cmd.Flags().Changed("description")
			in.SetEnabled = cmd.Flags().Changed("enabled")
			in.SetAction = cmd.Flags().Changed("action")
			in.SetAllowReturnTraffic = cmd.Flags().Changed("allow-return-traffic")
			in.SetSourceZone = cmd.Flags().Changed("source-zone")
			in.SetDestinationZone = cmd.Flags().Changed("destination-zone")
			in.SetIPVersion = cmd.Flags().Changed("ip-version")
			in.SetProtocol = cmd.Flags().Changed("protocol")
			in.SetLoggingEnabled = cmd.Flags().Changed("logging-enabled")
			return runFirewallUpdate(args[0], in)
		},
	}
	cmd.Flags().StringVar(&in.Name, "name", "", "policy name")
	cmd.Flags().StringVar(&in.Description, "description", "", "policy description")
	cmd.Flags().BoolVar(&in.ClearDescription, "clear-description", false, "remove the policy description")
	cmd.Flags().BoolVar(&in.Enabled, "enabled", true, "policy enabled")
	cmd.Flags().StringVar(&in.Action, "action", "", "allow|block|reject")
	cmd.Flags().BoolVar(&in.AllowReturnTraffic, "allow-return-traffic", false, "allow mirrored return traffic for action allow")
	cmd.Flags().StringVar(&in.SourceZone, "source-zone", "", "source firewall zone id or exact name")
	cmd.Flags().StringVar(&in.DestinationZone, "destination-zone", "", "destination firewall zone id or exact name")
	cmd.Flags().StringVar(&in.IPVersion, "ip-version", "", "ipv4|ipv6|ipv4_and_ipv6")
	cmd.Flags().StringVar(&in.Protocol, "protocol", "", "all|tcp|udp|tcp_udp|icmp|icmpv6")
	cmd.Flags().BoolVar(&in.LoggingEnabled, "logging-enabled", false, "send matches to the configured remote syslog server")
	return cmd
}

func newFirewallDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <id|exact-name>",
		Short: "Delete an experimental firewall policy",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runFirewallDelete(args[0])
		},
	}
}

func newFirewallReorderCmd() *cobra.Command {
	var sourceZone, destinationZone, beforeIDs, afterIDs string
	cmd := &cobra.Command{
		Use:   "reorder",
		Short: "Atomically replace the complete user-defined policy order for a zone pair",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runFirewallReorder(domain.FirewallReorder{
				SourceZone: sourceZone, DestinationZone: destinationZone,
				BeforeSystemDefined: splitCSV(beforeIDs), AfterSystemDefined: splitCSV(afterIDs),
			})
		},
	}
	cmd.Flags().StringVar(&sourceZone, "source-zone", "", "source firewall zone id or exact name")
	cmd.Flags().StringVar(&destinationZone, "destination-zone", "", "destination firewall zone id or exact name")
	cmd.Flags().StringVar(&beforeIDs, "before-system-ids", "", "complete comma-separated policies before system-defined policies")
	cmd.Flags().StringVar(&afterIDs, "after-system-ids", "", "complete comma-separated policies after system-defined policies")
	_ = cmd.MarkFlagRequired("source-zone")
	_ = cmd.MarkFlagRequired("destination-zone")
	return cmd
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if item := strings.TrimSpace(part); item != "" {
			out = append(out, item)
		}
	}
	return out
}

func runFirewallList() error {
	rt, err := loadRuntime(true)
	if err != nil {
		return emitErr("firewall", "list", err)
	}
	items, err := domain.NewFirewallService(rt.Client).List(context.Background())
	if err != nil {
		return emittedRuntimeError(rt, "firewall", "list", err)
	}
	if rt.JSON {
		return emittedRuntimeCode(rt.Emit("firewall", "list", items, nil, nil))
	}
	headers := []string{"INDEX", "NAME", "ACTION", "SOURCE ZONE", "DESTINATION ZONE", "PROTOCOL", "ENABLED", "ID"}
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		rows = append(rows, []string{
			strconv.Itoa(item.Index), item.Name, item.Action, item.SourceZoneID, item.DestinationZoneID,
			item.Protocol, strconv.FormatBool(item.Enabled), item.ID,
		})
	}
	return render.WriteTable(rt.Out, headers, rows)
}

func runFirewallGet(query string) error {
	rt, err := loadRuntime(true)
	if err != nil {
		return emitErr("firewall", "get", err)
	}
	item, err := domain.NewFirewallService(rt.Client).Get(context.Background(), query)
	if err != nil {
		return emittedRuntimeError(rt, "firewall", "get", err)
	}
	if rt.JSON {
		return emittedRuntimeCode(rt.Emit("firewall", "get", item, nil, nil))
	}
	fmt.Fprintf(rt.Out, "id: %s\n", render.SafeText(item.ID))
	fmt.Fprintf(rt.Out, "name: %s\n", render.SafeText(item.Name))
	fmt.Fprintf(rt.Out, "description: %s\n", render.SafeText(item.Description))
	fmt.Fprintf(rt.Out, "enabled: %s\n", strconv.FormatBool(item.Enabled))
	fmt.Fprintf(rt.Out, "action: %s\n", render.SafeText(item.Action))
	fmt.Fprintf(rt.Out, "allow_return_traffic: %s\n", strconv.FormatBool(item.AllowReturnTraffic))
	fmt.Fprintf(rt.Out, "source_zone_id: %s\n", render.SafeText(item.SourceZoneID))
	fmt.Fprintf(rt.Out, "destination_zone_id: %s\n", render.SafeText(item.DestinationZoneID))
	fmt.Fprintf(rt.Out, "protocol: %s\n", render.SafeText(item.Protocol))
	fmt.Fprintf(rt.Out, "logging_enabled: %s\n", strconv.FormatBool(item.LoggingEnabled))
	fmt.Fprintf(rt.Out, "index: %d\n", item.Index)
	fmt.Fprintf(rt.Out, "origin: %s\n", render.SafeText(item.Origin))
	return nil
}

func runFirewallZoneList() error {
	rt, err := loadRuntime(true)
	if err != nil {
		return emitErr("firewall", "zone list", err)
	}
	items, err := domain.NewFirewallService(rt.Client).ListZones(context.Background())
	if err != nil {
		return emittedRuntimeError(rt, "firewall", "zone list", err)
	}
	if rt.JSON {
		return emittedRuntimeCode(rt.Emit("firewall", "zone list", items, nil, nil))
	}
	headers := []string{"NAME", "ORIGIN", "CONFIGURABLE", "NETWORKS", "ID"}
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		networks := strings.Join(item.NetworkIDs, ",")
		if networks == "" {
			networks = "-"
		}
		rows = append(rows, []string{item.Name, item.Origin, strconv.FormatBool(item.Configurable), networks, item.ID})
	}
	return render.WriteTable(rt.Out, headers, rows)
}

func runFirewallZoneGet(query string) error {
	rt, err := loadRuntime(true)
	if err != nil {
		return emitErr("firewall", "zone get", err)
	}
	item, err := domain.NewFirewallService(rt.Client).GetZone(context.Background(), query)
	if err != nil {
		return emittedRuntimeError(rt, "firewall", "zone get", err)
	}
	if rt.JSON {
		return emittedRuntimeCode(rt.Emit("firewall", "zone get", item, nil, nil))
	}
	fmt.Fprintf(rt.Out, "id: %s\n", render.SafeText(item.ID))
	fmt.Fprintf(rt.Out, "name: %s\n", render.SafeText(item.Name))
	fmt.Fprintf(rt.Out, "origin: %s\n", render.SafeText(item.Origin))
	fmt.Fprintf(rt.Out, "configurable: %s\n", strconv.FormatBool(item.Configurable))
	fmt.Fprintf(rt.Out, "network_ids: %s\n", render.SafeText(strings.Join(item.NetworkIDs, ",")))
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
			p, binding, err := svc.PrepareCreate(ctx, in)
			if err != nil {
				return plan.PreparedMutation{}, err
			}
			encoded, err := json.Marshal(binding)
			if err != nil {
				return plan.PreparedMutation{}, err
			}
			snapshot := map[string]any{"source_zone_id": binding.SourceZoneID, "destination_zone_id": binding.DestinationZoneID}
			return plan.Targeted(p, string(encoded), snapshot, firewallMutationRisk["create"], true)
		},
		func(target plan.Target) (any, error) {
			binding, err := decodeFirewallCreateBinding(target.ID())
			if err != nil {
				return nil, err
			}
			return svc.ObserveCreateBinding(ctx, binding)
		},
		func(target plan.Target) (any, error) {
			binding, err := decodeFirewallCreateBinding(target.ID())
			if err != nil {
				return nil, err
			}
			return svc.ApplyCreateBound(ctx, in, binding)
		},
	)
	return emittedExit(code)
}

func decodeFirewallCreateBinding(value string) (domain.FirewallCreateBinding, error) {
	var binding domain.FirewallCreateBinding
	if err := json.Unmarshal([]byte(value), &binding); err != nil {
		return domain.FirewallCreateBinding{}, fmt.Errorf("decode firewall create zone binding: %w", err)
	}
	if binding.SourceZoneID == "" || binding.DestinationZoneID == "" {
		return domain.FirewallCreateBinding{}, fmt.Errorf("firewall create zone binding is incomplete")
	}
	return binding, nil
}

func runFirewallUpdate(query string, in domain.FirewallInput) error {
	rt, err := loadRuntime(true)
	if err != nil {
		return emitErr("firewall", "update", err)
	}
	svc := domain.NewFirewallService(rt.Client)
	ctx := context.Background()
	code := RunPreparedMutation(rt, "firewall", "update",
		func() (plan.PreparedMutation, error) {
			p, item, err := svc.Update(ctx, query, in)
			if err != nil {
				return plan.PreparedMutation{}, err
			}
			return plan.Targeted(p, item.ID, p.Changes, firewallMutationRisk["update"], true)
		},
		func(target plan.Target) (any, error) {
			p, _, err := svc.Update(ctx, target.ID(), in)
			return p.Changes, err
		},
		func(target plan.Target) (any, error) { return svc.ApplyUpdatePrepared(ctx, target, target.ID(), in) },
	)
	return emittedExit(code)
}

func runFirewallDelete(query string) error {
	rt, err := loadRuntime(true)
	if err != nil {
		return emitErr("firewall", "delete", err)
	}
	svc := domain.NewFirewallService(rt.Client)
	ctx := context.Background()
	code := RunPreparedMutation(rt, "firewall", "delete",
		func() (plan.PreparedMutation, error) {
			p, item, err := svc.Delete(ctx, query)
			if err != nil {
				return plan.PreparedMutation{}, err
			}
			return plan.Targeted(p, item.ID, p.Changes, firewallMutationRisk["delete"], true)
		},
		func(target plan.Target) (any, error) {
			p, _, err := svc.Delete(ctx, target.ID())
			return p.Changes, err
		},
		func(target plan.Target) (any, error) { return svc.ApplyDeletePrepared(ctx, target, target.ID()) },
	)
	return emittedExit(code)
}

type firewallReorderTarget struct {
	SourceZoneID        string   `json:"source_zone_id"`
	DestinationZoneID   string   `json:"destination_zone_id"`
	BeforeSystemDefined []string `json:"before_system_defined"`
	AfterSystemDefined  []string `json:"after_system_defined"`
}

func runFirewallReorder(in domain.FirewallReorder) error {
	rt, err := loadRuntime(true)
	if err != nil {
		return emitErr("firewall", "reorder", err)
	}
	svc := domain.NewFirewallService(rt.Client)
	ctx := context.Background()
	code := RunPreparedMutation(rt, "firewall", "reorder",
		func() (plan.PreparedMutation, error) {
			p, err := svc.Reorder(ctx, in)
			if err != nil {
				return plan.PreparedMutation{}, err
			}
			target, err := firewallReorderTargetFromPlan(p)
			if err != nil {
				return plan.PreparedMutation{}, err
			}
			encoded, err := json.Marshal(target)
			if err != nil {
				return plan.PreparedMutation{}, err
			}
			return plan.Targeted(p, string(encoded), p.Changes, firewallMutationRisk["reorder"], true)
		},
		func(target plan.Target) (any, error) {
			resolved, err := decodeFirewallReorderTarget(target.ID())
			if err != nil {
				return nil, err
			}
			p, err := svc.Reorder(ctx, resolved)
			if err != nil {
				return nil, err
			}
			return p.Changes, nil
		},
		func(target plan.Target) (any, error) {
			resolved, err := decodeFirewallReorderTarget(target.ID())
			if err != nil {
				return nil, err
			}
			return svc.ApplyReorderPrepared(ctx, target, resolved)
		},
	)
	return emittedExit(code)
}

func firewallReorderTargetFromPlan(p plan.Plan) (firewallReorderTarget, error) {
	if len(p.Changes) != 1 {
		return firewallReorderTarget{}, fmt.Errorf("firewall reorder produced %d plan changes, want 1", len(p.Changes))
	}
	after, ok := p.Changes[0].After.(map[string]any)
	if !ok {
		return firewallReorderTarget{}, fmt.Errorf("firewall reorder plan has invalid after snapshot")
	}
	before, okBefore := after["before_system_defined"].([]string)
	afterIDs, okAfter := after["after_system_defined"].([]string)
	source, okSource := after["source_zone_id"].(string)
	destination, okDestination := after["destination_zone_id"].(string)
	if !okBefore || !okAfter || !okSource || !okDestination || source == "" || destination == "" {
		return firewallReorderTarget{}, fmt.Errorf("firewall reorder plan has invalid zone-pair ordering")
	}
	return firewallReorderTarget{
		SourceZoneID: source, DestinationZoneID: destination,
		BeforeSystemDefined: before, AfterSystemDefined: afterIDs,
	}, nil
}

func decodeFirewallReorderTarget(encoded string) (domain.FirewallReorder, error) {
	var target firewallReorderTarget
	if err := json.Unmarshal([]byte(encoded), &target); err != nil {
		return domain.FirewallReorder{}, err
	}
	return domain.FirewallReorder{
		SourceZone: target.SourceZoneID, DestinationZone: target.DestinationZoneID,
		BeforeSystemDefined: target.BeforeSystemDefined, AfterSystemDefined: target.AfterSystemDefined,
	}, nil
}

func emittedRuntimeError(rt *Runtime, resource, action string, err error) error {
	if rt.Emit(resource, action, nil, nil, err) != 0 {
		return err
	}
	return nil
}

func emittedRuntimeCode(code int) error {
	if code != 0 {
		return fmt.Errorf("render failed")
	}
	return nil
}
