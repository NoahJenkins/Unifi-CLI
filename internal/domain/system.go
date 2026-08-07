package domain

import (
	"context"
	"net/http"

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
