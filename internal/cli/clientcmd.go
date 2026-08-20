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

func newClientCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "client",
		Short: "Manage UniFi clients",
	}
	cmd.AddCommand(
		newClientListCmd(),
		newClientGetCmd(),
		newClientFixedIPCmd(),
		newClientReconnectCmd(),
		newClientBlockCmd(),
		newClientUnblockCmd(),
	)
	return cmd
}

func newClientFixedIPCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "fixed-ip",
		Short: "Manage a known client's fixed-IP reservation",
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "list",
			Short: "List enabled fixed-IP reservations",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				return runClientFixedIPList()
			},
		},
		&cobra.Command{
			Use:   "get <id>",
			Short: "Get fixed-IP state for a known client",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				return runClientFixedIPGet(args[0])
			},
		},
		&cobra.Command{
			Use:   "set <id> <ipv4>",
			Short: "Set a fixed-IP reservation for a known client",
			Args:  cobra.ExactArgs(2),
			RunE: func(cmd *cobra.Command, args []string) error {
				return runClientFixedIPMutation("set", args[0], args[1])
			},
		},
		&cobra.Command{
			Use:   "clear <id>",
			Short: "Clear a known client's fixed-IP reservation",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				return runClientFixedIPMutation("clear", args[0], "")
			},
		},
	)
	return cmd
}

func runClientFixedIPList() error {
	rt, err := loadRuntime(true)
	if err != nil {
		return emitErr("client", "fixed-ip list", err)
	}
	rt.CommandExperimental = true
	items, err := domain.NewClientFixedIPService(rt.Client).List(context.Background())
	if err != nil {
		return emittedExit(rt.Emit("client", "fixed-ip list", nil, nil, err))
	}
	if rt.JSON {
		return emittedExit(rt.Emit("client", "fixed-ip list", items, nil, nil))
	}
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		rows = append(rows, []string{item.Name, item.MAC, item.FixedIP, item.NetworkID})
	}
	return render.WriteTable(rt.Out, []string{"NAME", "MAC", "FIXED IP", "NETWORK ID"}, rows)
}

func runClientFixedIPGet(id string) error {
	rt, err := loadRuntime(true)
	if err != nil {
		return emitErr("client", "fixed-ip get", err)
	}
	rt.CommandExperimental = true
	reservation, err := domain.NewClientFixedIPService(rt.Client).Get(context.Background(), id)
	return emittedExit(rt.Emit("client", "fixed-ip get", reservation, nil, err))
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
	fmt.Fprintf(rt.Out, "id: %s\n", render.SafeText(c.ID))
	fmt.Fprintf(rt.Out, "mac: %s\n", render.SafeText(c.MAC))
	fmt.Fprintf(rt.Out, "hostname: %s\n", render.SafeText(c.Hostname))
	fmt.Fprintf(rt.Out, "name: %s\n", render.SafeText(c.Name))
	fmt.Fprintf(rt.Out, "ip: %s\n", render.SafeText(c.IP))
	fmt.Fprintf(rt.Out, "essid: %s\n", render.SafeText(c.ESSID))
	fmt.Fprintf(rt.Out, "network: %s\n", render.SafeText(c.Network))
	fmt.Fprintf(rt.Out, "is_wired: %s\n", strconv.FormatBool(c.IsWired))
	fmt.Fprintf(rt.Out, "blocked: %s\n", strconv.FormatBool(c.Blocked))
	fmt.Fprintf(rt.Out, "last_seen: %s\n", render.SafeText(c.LastSeen))
	return nil
}

func newClientReconnectCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "reconnect <id>",
		Short: "Reconnect (kick) a client",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runClientMutation("reconnect", args[0])
		},
	}
}

func newClientBlockCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "block <id>",
		Short: "Block a client",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runClientMutation("block", args[0])
		},
	}
}

func newClientUnblockCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unblock <id>",
		Short: "Unblock a client",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runClientMutation("unblock", args[0])
		},
	}
}

func runClientMutation(action, id string) error {
	rt, err := loadRuntime(true)
	if err != nil {
		return emitErr("client", action, err)
	}
	svc := domain.NewClientService(rt.Client)
	ctx := context.Background()
	build := func(query string) (plan.Plan, domain.Client, error) {
		switch action {
		case "reconnect":
			return svc.Reconnect(ctx, query)
		case "block":
			return svc.Block(ctx, query)
		case "unblock":
			return svc.Unblock(ctx, query)
		default:
			return plan.Plan{}, domain.Client{}, fmt.Errorf("unknown action %s", action)
		}
	}

	code := RunPreparedMutation(rt, "client", action,
		func() (plan.PreparedMutation, error) {
			p, c, err := build(id)
			if err != nil {
				return plan.PreparedMutation{}, err
			}
			risk, experimental := task7MutationPolicy("client", action)
			return plan.Targeted(p, c.ID, p.Changes, risk, experimental)
		},
		func(target plan.Target) (any, error) {
			p, _, err := build(target.ID())
			return p.Changes, err
		},
		func(target plan.Target) (any, error) {
			switch action {
			case "reconnect":
				return svc.ApplyReconnectPrepared(ctx, target, target.ID())
			case "block":
				return svc.ApplyBlockPrepared(ctx, target, target.ID())
			case "unblock":
				return svc.ApplyUnblockPrepared(ctx, target, target.ID())
			default:
				return nil, fmt.Errorf("unknown action %s", action)
			}
		},
	)
	return emittedExit(code)
}

func runClientFixedIPMutation(action, id, fixedIP string) error {
	actionName := "fixed-ip " + action
	rt, err := loadRuntime(true)
	if err != nil {
		return emitErr("client", actionName, err)
	}
	svc := domain.NewClientFixedIPService(rt.Client)
	ctx := context.Background()
	build := func(query string) (plan.Plan, domain.ClientFixedIPSnapshot, error) {
		switch action {
		case "set":
			return svc.Set(ctx, query, fixedIP)
		case "clear":
			return svc.Clear(ctx, query)
		default:
			return plan.Plan{}, domain.ClientFixedIPSnapshot{}, fmt.Errorf("unknown fixed-IP action %s", action)
		}
	}

	code := RunPreparedMutation(rt, "client", actionName,
		func() (plan.PreparedMutation, error) {
			p, snapshot, err := build(id)
			if err != nil {
				return plan.PreparedMutation{}, err
			}
			risk, experimental := task7MutationPolicy("client", actionName)
			return plan.Targeted(p, snapshot.ClientID, snapshot, risk, experimental)
		},
		func(target plan.Target) (any, error) {
			_, snapshot, err := build(target.ID())
			return snapshot, err
		},
		func(target plan.Target) (any, error) {
			switch action {
			case "set":
				return svc.ApplySetPrepared(ctx, target, target.ID(), fixedIP)
			case "clear":
				return svc.ApplyClearPrepared(ctx, target, target.ID())
			default:
				return nil, fmt.Errorf("unknown fixed-IP action %s", action)
			}
		},
	)
	return emittedExit(code)
}
