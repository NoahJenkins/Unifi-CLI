package cli

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/noahjenkins/unifi-cli/internal/domain"
	"github.com/noahjenkins/unifi-cli/internal/plan"
	"github.com/noahjenkins/unifi-cli/internal/render"
	"github.com/spf13/cobra"
)

func newTrafficListCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "traffic-list", Short: "Manage official traffic matching lists"}
	cmd.AddCommand(newTrafficListListCmd(), newTrafficListGetCmd(), newTrafficListCreateCmd(), newTrafficListUpdateCmd(), newTrafficListDeleteCmd())
	return cmd
}

func newTrafficListListCmd() *cobra.Command {
	return &cobra.Command{Use: "list", Short: "List traffic matching lists", RunE: func(cmd *cobra.Command, args []string) error { return runTrafficListList() }}
}

func newTrafficListGetCmd() *cobra.Command {
	return &cobra.Command{Use: "get <id|exact-name>", Short: "Get a traffic matching list", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error { return runTrafficListGet(args[0]) }}
}

func newTrafficListCreateCmd() *cobra.Command {
	var kind, name string
	var items []string
	cmd := &cobra.Command{Use: "create", Short: "Create an experimental traffic matching list", RunE: func(cmd *cobra.Command, args []string) error {
		return runTrafficListCreate(domain.TrafficListInput{Type: kind, Name: name, SetName: true, Items: items, SetItems: true})
	}}
	cmd.Flags().StringVar(&kind, "type", "", "ports|ipv4-addresses|ipv6-addresses")
	cmd.Flags().StringVar(&name, "name", "", "traffic list name")
	cmd.Flags().StringSliceVar(&items, "item", nil, "repeatable item: port, port range, address, subnet, or IPv4 range")
	_ = cmd.MarkFlagRequired("type")
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("item")
	return cmd
}

func newTrafficListUpdateCmd() *cobra.Command {
	var name string
	var items []string
	cmd := &cobra.Command{Use: "update <id|exact-name>", Short: "Update an experimental traffic matching list", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return runTrafficListUpdate(args[0], domain.TrafficListInput{Name: name, SetName: cmd.Flags().Changed("name"), Items: items, SetItems: cmd.Flags().Changed("item")})
	}}
	cmd.Flags().StringVar(&name, "name", "", "traffic list name")
	cmd.Flags().StringSliceVar(&items, "item", nil, "complete repeatable item replacement")
	return cmd
}

func newTrafficListDeleteCmd() *cobra.Command {
	return &cobra.Command{Use: "delete <id|exact-name>", Short: "Delete an experimental traffic matching list", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error { return runTrafficListDelete(args[0]) }}
}

func runTrafficListList() error {
	rt, err := loadRuntime(true)
	if err != nil {
		return emitErr("traffic_list", "list", err)
	}
	items, err := domain.NewTrafficListService(rt.Client).List(context.Background())
	if err != nil {
		return emittedRuntimeError(rt, "traffic_list", "list", err)
	}
	if rt.JSON {
		return emittedRuntimeCode(rt.Emit("traffic_list", "list", items, nil, nil))
	}
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		rows = append(rows, []string{item.Name, item.Type, strconv.Itoa(len(item.Items)), item.ID})
	}
	return render.WriteTable(rt.Out, []string{"NAME", "TYPE", "ITEMS", "ID"}, rows)
}

func runTrafficListGet(query string) error {
	rt, err := loadRuntime(true)
	if err != nil {
		return emitErr("traffic_list", "get", err)
	}
	item, err := domain.NewTrafficListService(rt.Client).Get(context.Background(), query)
	if err != nil {
		return emittedRuntimeError(rt, "traffic_list", "get", err)
	}
	if rt.JSON {
		return emittedRuntimeCode(rt.Emit("traffic_list", "get", item, nil, nil))
	}
	fmt.Fprintf(rt.Out, "id: %s\nname: %s\ntype: %s\nitems: %d\n", render.SafeText(item.ID), render.SafeText(item.Name), render.SafeText(item.Type), len(item.Items))
	return nil
}

func runTrafficListCreate(in domain.TrafficListInput) error {
	rt, err := loadRuntime(true)
	if err != nil {
		return emitErr("traffic_list", "create", err)
	}
	svc, ctx := domain.NewTrafficListService(rt.Client), context.Background()
	code := RunPreparedMutation(rt, "traffic_list", "create", func() (plan.PreparedMutation, error) {
		p, err := svc.Create(ctx, in)
		return plan.Untargeted(p, plan.HighImpact, true), err
	}, nil, func(target plan.Target) (any, error) { return svc.ApplyCreate(ctx, in) })
	return emittedExit(code)
}

func runTrafficListUpdate(query string, in domain.TrafficListInput) error {
	rt, err := loadRuntime(true)
	if err != nil {
		return emitErr("traffic_list", "update", err)
	}
	svc, ctx := domain.NewTrafficListService(rt.Client), context.Background()
	code := RunPreparedMutation(rt, "traffic_list", "update", func() (plan.PreparedMutation, error) {
		p, item, err := svc.Update(ctx, query, in)
		if err != nil {
			return plan.PreparedMutation{}, err
		}
		return plan.Targeted(p, item.ID, p.Changes, plan.HighImpact, true)
	}, func(target plan.Target) (any, error) {
		p, _, err := svc.Update(ctx, target.ID(), in)
		return p.Changes, err
	}, func(target plan.Target) (any, error) { return svc.ApplyUpdate(ctx, target.ID(), in) })
	return emittedExit(code)
}

func runTrafficListDelete(query string) error {
	rt, err := loadRuntime(true)
	if err != nil {
		return emitErr("traffic_list", "delete", err)
	}
	svc, ctx := domain.NewTrafficListService(rt.Client), context.Background()
	code := RunPreparedMutation(rt, "traffic_list", "delete", func() (plan.PreparedMutation, error) {
		p, item, err := svc.Delete(ctx, query)
		if err != nil {
			return plan.PreparedMutation{}, err
		}
		return plan.Targeted(p, item.ID, p.Changes, plan.Destructive, true)
	}, func(target plan.Target) (any, error) {
		p, _, err := svc.Delete(ctx, target.ID())
		return p.Changes, err
	}, func(target plan.Target) (any, error) { return svc.ApplyDelete(ctx, target.ID()) })
	return emittedExit(code)
}

func trafficListItemsText(items []domain.TrafficListItem) string {
	values := make([]string, len(items))
	for index, item := range items {
		values[index] = item.Type
	}
	return strings.Join(values, ",")
}
