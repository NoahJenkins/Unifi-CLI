package domain

import (
	"context"
	"net/http"
	"strings"
	"unicode"

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
	ApplicationVersion string            `json:"application_version"`
	Status             string            `json:"status"` // ok|degraded|error
	DeviceTotal        int               `json:"device_total"`
	DeviceConnected    int               `json:"device_connected"`
	ClientTotal        int               `json:"client_total"`
	Subsystems         []HealthSubsystem `json:"subsystems,omitempty"`
}

type SystemService struct {
	api SystemAPI
}

func NewSystemService(api SystemAPI) *SystemService {
	return &SystemService{api: api}
}

func (s *SystemService) Health(ctx context.Context) (Health, error) {
	var info struct {
		ApplicationVersion any `json:"applicationVersion"`
	}
	if err := s.api.Do(ctx, http.MethodGet, client.OfficialPath("info"), nil, &info); err != nil {
		return Health{}, err
	}
	applicationVersion, ok := info.ApplicationVersion.(string)
	applicationVersion = strings.TrimSpace(applicationVersion)
	if !ok || applicationVersion == "" || len(applicationVersion) > 128 || strings.IndexFunc(applicationVersion, unicode.IsControl) >= 0 {
		return Health{}, apperr.New(apperr.Internal, "official application info contains an invalid application version")
	}

	devices, err := NewDeviceService(s.api).List(ctx)
	if err != nil {
		return Health{}, err
	}
	clients, err := NewClientService(s.api).List(ctx)
	if err != nil {
		return Health{}, err
	}

	h := Health{
		ApplicationVersion: applicationVersion,
		DeviceTotal:        len(devices),
		ClientTotal:        len(clients),
	}
	adoptedTotal := 0
	adoptedConnected := 0
	for _, d := range devices {
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
