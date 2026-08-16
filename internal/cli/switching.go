package cli

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/noahjenkins/unifi-cli/internal/domain"
	"github.com/noahjenkins/unifi-cli/internal/render"
	"github.com/spf13/cobra"
)

func newSwitchingCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "switching", Short: "Read official switching resources"}
	cmd.AddCommand(
		newSwitchingResourceCmd("lag", "link aggregation groups", runSwitchingLAGList, runSwitchingLAGGet),
		newSwitchingResourceCmd("mc-lag", "multi-chassis LAG domains", runSwitchingMCLAGList, runSwitchingMCLAGGet),
		newSwitchingResourceCmd("stack", "switch stacks", runSwitchingStackList, runSwitchingStackGet),
	)
	return cmd
}

func newSwitchingResourceCmd(name, description string, list func() error, get func(string) error) *cobra.Command {
	cmd := &cobra.Command{Use: name, Short: "Read " + description}
	cmd.AddCommand(
		&cobra.Command{Use: "list", Short: "List " + description, RunE: func(cmd *cobra.Command, args []string) error { return list() }},
		&cobra.Command{Use: "get <id|exact-name>", Short: "Get " + strings.TrimSuffix(description, "s"), Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error { return get(args[0]) }},
	)
	return cmd
}

func runSwitchingLAGList() error {
	rt, err := loadRuntime(true)
	if err != nil {
		return emitErr("switching_lag", "list", err)
	}
	items, err := domain.NewSwitchingService(rt.Client).ListLAGs(context.Background())
	if err != nil {
		return emittedRuntimeError(rt, "switching_lag", "list", err)
	}
	if rt.JSON {
		return emittedRuntimeCode(rt.Emit("switching_lag", "list", items, nil, nil))
	}
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		rows = append(rows, []string{item.Type, switchingMembersText(item.Members), item.SwitchStackID, item.MCLAGDomainID, item.ID})
	}
	return render.WriteTable(rt.Out, []string{"TYPE", "MEMBERS", "STACK", "MC-LAG", "ID"}, rows)
}

func runSwitchingLAGGet(query string) error {
	rt, err := loadRuntime(true)
	if err != nil {
		return emitErr("switching_lag", "get", err)
	}
	item, err := domain.NewSwitchingService(rt.Client).GetLAG(context.Background(), query)
	if err != nil {
		return emittedRuntimeError(rt, "switching_lag", "get", err)
	}
	if rt.JSON {
		return emittedRuntimeCode(rt.Emit("switching_lag", "get", item, nil, nil))
	}
	fmt.Fprintf(rt.Out, "id: %s\ntype: %s\nmembers: %s\nswitch_stack_id: %s\nmc_lag_domain_id: %s\norigin: %s\n", render.SafeText(item.ID), render.SafeText(item.Type), render.SafeText(switchingMembersText(item.Members)), render.SafeText(item.SwitchStackID), render.SafeText(item.MCLAGDomainID), render.SafeText(item.Origin))
	return nil
}

func runSwitchingMCLAGList() error {
	rt, err := loadRuntime(true)
	if err != nil {
		return emitErr("switching_mc_lag", "list", err)
	}
	items, err := domain.NewSwitchingService(rt.Client).ListMCLAGDomains(context.Background())
	if err != nil {
		return emittedRuntimeError(rt, "switching_mc_lag", "list", err)
	}
	if rt.JSON {
		return emittedRuntimeCode(rt.Emit("switching_mc_lag", "list", items, nil, nil))
	}
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		rows = append(rows, []string{item.Name, strconv.Itoa(len(item.Peers)), strconv.Itoa(len(item.LAGIDs)), item.ID})
	}
	return render.WriteTable(rt.Out, []string{"NAME", "PEERS", "LAGS", "ID"}, rows)
}

func runSwitchingMCLAGGet(query string) error {
	rt, err := loadRuntime(true)
	if err != nil {
		return emitErr("switching_mc_lag", "get", err)
	}
	item, err := domain.NewSwitchingService(rt.Client).GetMCLAGDomain(context.Background(), query)
	if err != nil {
		return emittedRuntimeError(rt, "switching_mc_lag", "get", err)
	}
	if rt.JSON {
		return emittedRuntimeCode(rt.Emit("switching_mc_lag", "get", item, nil, nil))
	}
	fmt.Fprintf(rt.Out, "id: %s\nname: %s\npeers: %d\nlag_ids: %s\norigin: %s\n", render.SafeText(item.ID), render.SafeText(item.Name), len(item.Peers), render.SafeText(strings.Join(item.LAGIDs, ",")), render.SafeText(item.Origin))
	return nil
}

func runSwitchingStackList() error {
	rt, err := loadRuntime(true)
	if err != nil {
		return emitErr("switching_stack", "list", err)
	}
	items, err := domain.NewSwitchingService(rt.Client).ListSwitchStacks(context.Background())
	if err != nil {
		return emittedRuntimeError(rt, "switching_stack", "list", err)
	}
	if rt.JSON {
		return emittedRuntimeCode(rt.Emit("switching_stack", "list", items, nil, nil))
	}
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		rows = append(rows, []string{item.Name, strconv.Itoa(len(item.DeviceIDs)), strconv.Itoa(len(item.LAGIDs)), item.ID})
	}
	return render.WriteTable(rt.Out, []string{"NAME", "DEVICES", "LAGS", "ID"}, rows)
}

func runSwitchingStackGet(query string) error {
	rt, err := loadRuntime(true)
	if err != nil {
		return emitErr("switching_stack", "get", err)
	}
	item, err := domain.NewSwitchingService(rt.Client).GetSwitchStack(context.Background(), query)
	if err != nil {
		return emittedRuntimeError(rt, "switching_stack", "get", err)
	}
	if rt.JSON {
		return emittedRuntimeCode(rt.Emit("switching_stack", "get", item, nil, nil))
	}
	fmt.Fprintf(rt.Out, "id: %s\nname: %s\ndevice_ids: %s\nlag_ids: %s\norigin: %s\n", render.SafeText(item.ID), render.SafeText(item.Name), render.SafeText(strings.Join(item.DeviceIDs, ",")), render.SafeText(strings.Join(item.LAGIDs, ",")), render.SafeText(item.Origin))
	return nil
}

func switchingMembersText(members []domain.SwitchingMember) string {
	values := make([]string, 0, len(members))
	for _, member := range members {
		ports := make([]string, len(member.PortIdxs))
		for index, port := range member.PortIdxs {
			ports[index] = strconv.Itoa(port)
		}
		values = append(values, member.DeviceID+":"+strings.Join(ports, ","))
	}
	return strings.Join(values, ";")
}

func newRadiusCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "radius", Short: "Read official RADIUS resources"}
	profile := &cobra.Command{Use: "profile", Short: "Read RADIUS profiles"}
	profile.AddCommand(
		&cobra.Command{Use: "list", Short: "List RADIUS profiles", RunE: func(cmd *cobra.Command, args []string) error { return runRadiusProfileList() }},
		&cobra.Command{Use: "get <id|exact-name>", Short: "Get a RADIUS profile", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error { return runRadiusProfileGet(args[0]) }},
	)
	cmd.AddCommand(profile)
	return cmd
}

func runRadiusProfileList() error {
	rt, err := loadRuntime(true)
	if err != nil {
		return emitErr("radius_profile", "list", err)
	}
	items, err := domain.NewRadiusService(rt.Client).ListProfiles(context.Background())
	if err != nil {
		return emittedRuntimeError(rt, "radius_profile", "list", err)
	}
	if rt.JSON {
		return emittedRuntimeCode(rt.Emit("radius_profile", "list", items, nil, nil))
	}
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		rows = append(rows, []string{item.Name, item.Origin, item.ID})
	}
	return render.WriteTable(rt.Out, []string{"NAME", "ORIGIN", "ID"}, rows)
}

func runRadiusProfileGet(query string) error {
	rt, err := loadRuntime(true)
	if err != nil {
		return emitErr("radius_profile", "get", err)
	}
	item, err := domain.NewRadiusService(rt.Client).GetProfile(context.Background(), query)
	if err != nil {
		return emittedRuntimeError(rt, "radius_profile", "get", err)
	}
	if rt.JSON {
		return emittedRuntimeCode(rt.Emit("radius_profile", "get", item, nil, nil))
	}
	fmt.Fprintf(rt.Out, "id: %s\nname: %s\norigin: %s\n", render.SafeText(item.ID), render.SafeText(item.Name), render.SafeText(item.Origin))
	return nil
}
