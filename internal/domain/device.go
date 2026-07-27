package domain

import (
	"context"
	"fmt"
	"net/http"

	"github.com/noahjenkins/unifi-cli/internal/client"
	"github.com/noahjenkins/unifi-cli/internal/resolve"
)

type DeviceAPI interface {
	Do(ctx context.Context, method, path string, in, out any) error
	SitePath(parts ...string) string
}

type Device struct {
	ID      string `json:"id"`
	MAC     string `json:"mac"`
	Name    string `json:"name"`
	Model   string `json:"model"`
	Type    string `json:"type"`
	State   string `json:"state"`
	IP      string `json:"ip"`
	Version string `json:"version"`
	Uplink  string `json:"uplink"`
	Adopted bool   `json:"adopted"`
}

func (d Device) GetID() string   { return d.ID }
func (d Device) GetMAC() string  { return d.MAC }
func (d Device) GetName() string { return d.Name }

type DeviceService struct {
	api DeviceAPI
}

func NewDeviceService(api DeviceAPI) *DeviceService {
	return &DeviceService{api: api}
}

func (s *DeviceService) List(ctx context.Context) ([]Device, error) {
	var raw []map[string]any
	path := s.api.SitePath(client.PathStatDevice)
	if err := s.api.Do(ctx, http.MethodGet, path, nil, &raw); err != nil {
		return nil, err
	}
	out := make([]Device, 0, len(raw))
	for _, m := range raw {
		out = append(out, NormalizeDevice(m))
	}
	return out, nil
}

func (s *DeviceService) Get(ctx context.Context, id string) (Device, error) {
	items, err := s.List(ctx)
	if err != nil {
		return Device{}, err
	}
	return resolve.One(items, id)
}

func NormalizeDevice(m map[string]any) Device {
	d := Device{
		ID:      strField(m, "_id", "id"),
		MAC:     strField(m, "mac"),
		Name:    strField(m, "name", "display_name"),
		Model:   strField(m, "model"),
		Type:    strField(m, "type"),
		IP:      strField(m, "ip", "last_ip"),
		Version: strField(m, "version", "sw_version"),
		Adopted: boolField(m, "adopted"),
		State:   mapDeviceState(m["state"]),
		Uplink:  uplinkMAC(m),
	}
	return d
}

func mapDeviceState(v any) string {
	n, ok := asInt(v)
	if !ok {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
		return "unknown"
	}
	switch n {
	case 0:
		return "disconnected"
	case 1:
		return "connected"
	case 2:
		return "pending"
	case 3:
		return "firmware_mismatch"
	case 4:
		return "upgrading"
	case 5:
		return "provisioning"
	case 6:
		return "heartbeat_missed"
	case 7:
		return "adopting"
	case 8:
		return "deleting"
	case 9:
		return "inform_error"
	case 10:
		return "adopting_failed"
	case 11:
		return "isolated"
	default:
		return "unknown"
	}
}

func uplinkMAC(m map[string]any) string {
	u, ok := m["uplink"].(map[string]any)
	if !ok {
		return ""
	}
	return strField(u, "uplink_mac", "mac")
}

func strField(m map[string]any, keys ...string) string {
	for _, k := range keys {
		v, ok := m[k]
		if !ok || v == nil {
			continue
		}
		switch t := v.(type) {
		case string:
			if t != "" {
				return t
			}
		default:
			s := fmt.Sprint(t)
			if s != "" && s != "<nil>" {
				return s
			}
		}
	}
	return ""
}

func boolField(m map[string]any, key string) bool {
	v, ok := m[key]
	if !ok || v == nil {
		return false
	}
	switch t := v.(type) {
	case bool:
		return t
	case float64:
		return t != 0
	case int:
		return t != 0
	default:
		return false
	}
}

func asInt(v any) (int, bool) {
	switch t := v.(type) {
	case float64:
		return int(t), true
	case int:
		return t, true
	case int64:
		return int(t), true
	case jsonNumber:
		n, err := t.Int64()
		if err != nil {
			return 0, false
		}
		return int(n), true
	default:
		return 0, false
	}
}

// jsonNumber matches encoding/json.Number without importing for the type assert path.
type jsonNumber interface {
	Int64() (int64, error)
}
