package domain

import (
	"context"
	"net/http"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
	"github.com/noahjenkins/unifi-cli/internal/client"
)

type SystemAPI interface {
	Do(ctx context.Context, method, path string, in, out any) error
	SitePath(parts ...string) string
}

type HealthSubsystem struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

type Health struct {
	Status          string            `json:"status"` // ok|degraded|error
	DeviceTotal     int               `json:"device_total"`
	DeviceConnected int               `json:"device_connected"`
	ClientTotal     int               `json:"client_total"`
	Subsystems      []HealthSubsystem `json:"subsystems,omitempty"`
}

type Event struct {
	ID       string `json:"id"`
	Time     string `json:"time"`
	Message  string `json:"message"`
	Severity string `json:"severity"`
}

type SystemService struct {
	api SystemAPI
}

func NewSystemService(api SystemAPI) *SystemService {
	return &SystemService{api: api}
}

func (s *SystemService) Health(ctx context.Context) (Health, error) {
	var devices []map[string]any
	if err := s.api.Do(ctx, http.MethodGet, s.api.SitePath(client.PathStatDevice), nil, &devices); err != nil {
		return Health{}, err
	}
	var clients []map[string]any
	if err := s.api.Do(ctx, http.MethodGet, s.api.SitePath(client.PathStatSta), nil, &clients); err != nil {
		return Health{}, err
	}

	h := Health{
		DeviceTotal: len(devices),
		ClientTotal: len(clients),
	}
	adoptedTotal := 0
	adoptedConnected := 0
	for _, m := range devices {
		d := NormalizeDevice(m)
		if d.State == "connected" {
			h.DeviceConnected++
		}
		if d.Adopted {
			adoptedTotal++
			if d.State == "connected" {
				adoptedConnected++
			}
		}
	}
	h.Status = healthStatus(adoptedTotal, adoptedConnected, h.DeviceTotal, h.DeviceConnected)

	// Optional subsystems from stat/health when present.
	var rawHealth []map[string]any
	if err := s.api.Do(ctx, http.MethodGet, s.api.SitePath(client.PathStatHealth), nil, &rawHealth); err == nil {
		for _, m := range rawHealth {
			h.Subsystems = append(h.Subsystems, HealthSubsystem{
				Name:   strField(m, "subsystem", "name"),
				Status: strField(m, "status", "state"),
			})
		}
	}

	return h, nil
}

func healthStatus(adoptedTotal, adoptedConnected, deviceTotal, deviceConnected int) string {
	// Prefer adopted devices when any exist.
	total, connected := deviceTotal, deviceConnected
	if adoptedTotal > 0 {
		total, connected = adoptedTotal, adoptedConnected
	}
	if total == 0 {
		return "ok"
	}
	if connected == total {
		return "ok"
	}
	if connected == 0 {
		return "error"
	}
	return "degraded"
}

func (s *SystemService) Events(ctx context.Context) ([]Event, error) {
	return s.listEvents(ctx, client.PathStatEvent, "events")
}

func (s *SystemService) Alerts(ctx context.Context) ([]Event, error) {
	return s.listEvents(ctx, client.PathStatAlarm, "alerts")
}

func (s *SystemService) listEvents(ctx context.Context, pathPart, label string) ([]Event, error) {
	var raw []map[string]any
	path := s.api.SitePath(pathPart)
	if err := s.api.Do(ctx, http.MethodGet, path, nil, &raw); err != nil {
		if apperr.Is(err, apperr.NotFound) {
			return nil, apperr.WithHint(
				apperr.Newf(apperr.NotImplemented, "%s endpoint unavailable on this controller", label),
				"controller returned 404 for "+pathPart+"; firmware may not expose this path",
			)
		}
		return nil, err
	}
	out := make([]Event, 0, len(raw))
	for _, m := range raw {
		out = append(out, NormalizeEvent(m))
	}
	return out, nil
}

func NormalizeEvent(m map[string]any) Event {
	sev := strField(m, "severity")
	if sev == "" {
		sev = strField(m, "subsystem", "key")
	}
	return Event{
		ID:       strField(m, "_id", "id"),
		Time:     strField(m, "time", "datetime", "timestamp"),
		Message:  strField(m, "msg", "message"),
		Severity: sev,
	}
}
