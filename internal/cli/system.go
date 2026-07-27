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
		Short: "System health, events, and alerts",
	}
	cmd.AddCommand(newSystemHealthCmd(), newSystemEventsCmd(), newSystemAlertsCmd())
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

func newSystemEventsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "events",
		Short: "List recent system events",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSystemEvents()
		},
	}
}

func newSystemAlertsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "alerts",
		Short: "List system alerts/alarms",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSystemAlerts()
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
	fmt.Fprintf(rt.Out, "status: %s\n", h.Status)
	fmt.Fprintf(rt.Out, "device_total: %s\n", strconv.Itoa(h.DeviceTotal))
	fmt.Fprintf(rt.Out, "device_connected: %s\n", strconv.Itoa(h.DeviceConnected))
	fmt.Fprintf(rt.Out, "client_total: %s\n", strconv.Itoa(h.ClientTotal))
	for _, sub := range h.Subsystems {
		fmt.Fprintf(rt.Out, "subsystem %s: %s\n", sub.Name, sub.Status)
	}
	return nil
}

func runSystemEvents() error {
	return runSystemEventList("events", func(svc *domain.SystemService) ([]domain.Event, error) {
		return svc.Events(context.Background())
	})
}

func runSystemAlerts() error {
	return runSystemEventList("alerts", func(svc *domain.SystemService) ([]domain.Event, error) {
		return svc.Alerts(context.Background())
	})
}

func runSystemEventList(action string, list func(*domain.SystemService) ([]domain.Event, error)) error {
	rt, err := loadRuntime(true)
	if err != nil {
		return emitErr("system", action, err)
	}
	svc := domain.NewSystemService(rt.Client)
	items, err := list(svc)
	if err != nil {
		code := rt.Emit("system", action, nil, nil, err)
		if code != 0 {
			return err
		}
		return nil
	}
	if rt.JSON {
		code := rt.Emit("system", action, items, nil, nil)
		if code != 0 {
			return err
		}
		return nil
	}
	headers := []string{"TIME", "SEVERITY", "MESSAGE", "ID"}
	rows := make([][]string, 0, len(items))
	for _, e := range items {
		rows = append(rows, []string{e.Time, e.Severity, e.Message, e.ID})
	}
	return render.WriteTable(rt.Out, headers, rows)
}
