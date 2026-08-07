package cli

import (
	"context"
	"fmt"
	"strconv"

	"github.com/noahjenkins/unifi-cli/internal/domain"
	"github.com/noahjenkins/unifi-cli/internal/render"
	"github.com/spf13/cobra"
)

func newSystemCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "system",
		Short: "System health",
	}
	cmd.AddCommand(newSystemHealthCmd())
	return cmd
}

func newSystemHealthCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "health",
		Short: "Show system health summary",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSystemHealth()
		},
	}
}

func runSystemHealth() error {
	rt, err := loadRuntime(true)
	if err != nil {
		return emitErr("system", "health", err)
	}
	svc := domain.NewSystemService(rt.Client)
	h, err := svc.Health(context.Background())
	if err != nil {
		code := rt.Emit("system", "health", nil, nil, err)
		if code != 0 {
			return err
		}
		return nil
	}
	if rt.JSON {
		code := rt.Emit("system", "health", h, nil, nil)
		if code != 0 {
			return err
		}
		return nil
	}
	fmt.Fprintf(rt.Out, "status: %s\n", render.SafeText(h.Status))
	fmt.Fprintf(rt.Out, "device_total: %s\n", strconv.Itoa(h.DeviceTotal))
	fmt.Fprintf(rt.Out, "device_connected: %s\n", strconv.Itoa(h.DeviceConnected))
	fmt.Fprintf(rt.Out, "client_total: %s\n", strconv.Itoa(h.ClientTotal))
	for _, sub := range h.Subsystems {
		fmt.Fprintf(rt.Out, "subsystem %s: %s\n", render.SafeText(sub.Name), render.SafeText(sub.Status))
	}
	return nil
}
