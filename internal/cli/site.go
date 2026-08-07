package cli

import (
	"context"
	"fmt"

	"github.com/noahjenkins/unifi-cli/internal/domain"
	"github.com/noahjenkins/unifi-cli/internal/render"
	"github.com/spf13/cobra"
)

func newSiteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "site",
		Short: "Manage UniFi sites",
	}
	cmd.AddCommand(newSiteListCmd(), newSiteGetCmd())
	return cmd
}

func newSiteListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List sites",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSiteList()
		},
	}
}

func newSiteGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <id>",
		Short: "Get a site by id or name",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSiteGet(args[0])
		},
	}
}

func runSiteList() error {
	rt, err := loadRuntime(true)
	if err != nil {
		return emitErr("site", "list", err)
	}
	svc := domain.NewSiteService(rt.Client)
	items, err := svc.List(context.Background())
	if err != nil {
		code := rt.Emit("site", "list", nil, nil, err)
		if code != 0 {
			return err
		}
		return nil
	}
	if rt.JSON {
		code := rt.Emit("site", "list", items, nil, nil)
		if code != 0 {
			return err
		}
		return nil
	}
	headers := []string{"NAME", "ID", "DESC", "ROLE"}
	rows := make([][]string, 0, len(items))
	for _, s := range items {
		rows = append(rows, []string{s.Name, s.ID, s.Desc, s.Role})
	}
	return render.WriteTable(rt.Out, headers, rows)
}

func runSiteGet(id string) error {
	rt, err := loadRuntime(true)
	if err != nil {
		return emitErr("site", "get", err)
	}
	svc := domain.NewSiteService(rt.Client)
	s, err := svc.Get(context.Background(), id)
	if err != nil {
		code := rt.Emit("site", "get", nil, nil, err)
		if code != 0 {
			return err
		}
		return nil
	}
	if rt.JSON {
		code := rt.Emit("site", "get", s, nil, nil)
		if code != 0 {
			return err
		}
		return nil
	}
	fmt.Fprintf(rt.Out, "id: %s\n", render.SafeText(s.ID))
	fmt.Fprintf(rt.Out, "name: %s\n", render.SafeText(s.Name))
	fmt.Fprintf(rt.Out, "desc: %s\n", render.SafeText(s.Desc))
	fmt.Fprintf(rt.Out, "role: %s\n", render.SafeText(s.Role))
	return nil
}
