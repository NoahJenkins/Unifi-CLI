package domain

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
	"github.com/noahjenkins/unifi-cli/internal/client"
	"github.com/noahjenkins/unifi-cli/internal/plan"
	"github.com/noahjenkins/unifi-cli/internal/resolve"
)

type PortAPI interface {
	Do(ctx context.Context, method, path string, in, out any) error
	SitePath(parts ...string) string
}

type Port struct {
	DeviceID   string `json:"device_id"`
	DeviceName string `json:"device_name"`
	PortIdx    int    `json:"port_idx"`
	Name       string `json:"name"`
	Media      string `json:"media"`
	Speed      string `json:"speed"`
	POE        string `json:"poe"`
	Enabled    bool   `json:"enabled"`
	Profile    string `json:"profile"`
	Networks   string `json:"networks"`
}

// PortInput is the update payload from CLI flags.
type PortInput struct {
	Name       string
	POE        string
	SetPOE     bool
	Enabled    bool
	SetEnabled bool
	Profile    string
}

type PortService struct {
	api PortAPI
}

func NewPortService(api PortAPI) *PortService {
	return &PortService{api: api}
}

func (s *PortService) List(ctx context.Context, deviceQuery string) ([]Port, error) {
	raw, err := s.loadDevices(ctx)
	if err != nil {
		return nil, err
	}
	if deviceQuery != "" {
		dev, err := resolveDeviceRaw(raw, deviceQuery)
		if err != nil {
			return nil, err
		}
		return ExtractPortsFromDevice(dev), nil
	}
	var out []Port
	for _, m := range raw {
		if !deviceHasPorts(m) {
			continue
		}
		out = append(out, ExtractPortsFromDevice(m)...)
	}
	return out, nil
}

func (s *PortService) Get(ctx context.Context, deviceQuery string, portIdx int) (Port, error) {
	ports, err := s.List(ctx, deviceQuery)
	if err != nil {
		return Port{}, err
	}
	for _, p := range ports {
		if p.PortIdx == portIdx {
			return p, nil
		}
	}
	return Port{}, apperr.WithHint(
		apperr.Newf(apperr.NotFound, "port %d not found on %s", portIdx, deviceQuery),
		"list ports with: unifi port list --device <device>",
	)
}

func (s *PortService) Update(ctx context.Context, deviceQuery string, portIdx int, in PortInput) (plan.Plan, Port, error) {
	cur, err := s.Get(ctx, deviceQuery, portIdx)
	if err != nil {
		return plan.Plan{}, Port{}, err
	}
	before := portSnapshot(cur)
	after := mergePortAfter(cur, in)
	name := fmt.Sprintf("%s:%d", cur.DeviceName, cur.PortIdx)
	p := plan.Update("port", fmt.Sprintf("%s/%d", cur.DeviceID, cur.PortIdx), name,
		fmt.Sprintf("update port %d on %s", cur.PortIdx, cur.DeviceName),
		before,
		after,
	)
	return p, cur, nil
}

func (s *PortService) ApplyUpdate(ctx context.Context, deviceQuery string, portIdx int, in PortInput) (Port, error) {
	raw, err := s.loadDevices(ctx)
	if err != nil {
		return Port{}, err
	}
	dev, err := resolveDeviceRaw(raw, deviceQuery)
	if err != nil {
		return Port{}, err
	}
	curPorts := ExtractPortsFromDevice(dev)
	var cur Port
	found := false
	for _, p := range curPorts {
		if p.PortIdx == portIdx {
			cur = p
			found = true
			break
		}
	}
	if !found {
		return Port{}, apperr.WithHint(
			apperr.Newf(apperr.NotFound, "port %d not found on %s", portIdx, deviceQuery),
			"list ports with: unifi port list --device <device>",
		)
	}

	existing := portOverridesFromDevice(dev)
	patch := portInputOverride(portIdx, in)
	merged := MergePortOverrides(existing, patch)

	devID := strField(dev, "_id", "id")
	path := s.api.SitePath(client.PathRestDevice, devID)
	body := map[string]any{"port_overrides": merged}
	if err := s.api.Do(ctx, http.MethodPut, path, body, nil); err != nil {
		return Port{}, err
	}

	// apply local view
	if in.Name != "" {
		cur.Name = in.Name
	}
	if in.SetPOE {
		cur.POE = in.POE
	}
	if in.SetEnabled {
		cur.Enabled = in.Enabled
	}
	if in.Profile != "" {
		cur.Profile = in.Profile
	}
	return cur, nil
}

func (s *PortService) loadDevices(ctx context.Context) ([]map[string]any, error) {
	var raw []map[string]any
	path := s.api.SitePath(client.PathStatDevice)
	if err := s.api.Do(ctx, http.MethodGet, path, nil, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// ExtractPortsFromDevice builds Port DTOs from port_table, with port_overrides merged on top.
func ExtractPortsFromDevice(dev map[string]any) []Port {
	devID := strField(dev, "_id", "id")
	devName := strField(dev, "name", "display_name")
	table := sliceOfMaps(dev["port_table"])
	overrides := indexOverrides(portOverridesFromDevice(dev))

	out := make([]Port, 0, len(table))
	for _, row := range table {
		idx, ok := asInt(row["port_idx"])
		if !ok {
			continue
		}
		p := Port{
			DeviceID:   devID,
			DeviceName: devName,
			PortIdx:    idx,
			Name:       strField(row, "name"),
			Media:      strField(row, "media"),
			Speed:      speedString(row["speed"]),
			POE:        strField(row, "poe_mode", "poe"),
			Enabled:    boolFieldDefault(row, "enable", true),
			Profile:    strField(row, "portconf_id", "profile"),
			Networks:   strField(row, "native_networkconf_id", "networks"),
		}
		if ov, ok := overrides[idx]; ok {
			if v := strField(ov, "name"); v != "" {
				p.Name = v
			}
			if v := strField(ov, "poe_mode", "poe"); v != "" {
				p.POE = v
			}
			if v := strField(ov, "portconf_id", "profile"); v != "" {
				p.Profile = v
			}
			if _, has := ov["enable"]; has {
				p.Enabled = boolField(ov, "enable")
			}
			if v := strField(ov, "native_networkconf_id", "networks"); v != "" {
				p.Networks = v
			}
		}
		out = append(out, p)
	}
	return out
}

// MergePortOverrides merges patch into existing overrides by port_idx (replace fields, append if new).
func MergePortOverrides(existing []map[string]any, patch map[string]any) []map[string]any {
	idx, ok := asInt(patch["port_idx"])
	if !ok {
		out := make([]map[string]any, len(existing))
		copy(out, existing)
		return append(out, cloneMap(patch))
	}
	out := make([]map[string]any, 0, len(existing)+1)
	replaced := false
	for _, e := range existing {
		eIdx, eOK := asInt(e["port_idx"])
		if eOK && eIdx == idx {
			merged := cloneMap(e)
			for k, v := range patch {
				merged[k] = v
			}
			// normalize port_idx to int
			merged["port_idx"] = idx
			out = append(out, merged)
			replaced = true
			continue
		}
		out = append(out, cloneMap(e))
	}
	if !replaced {
		m := cloneMap(patch)
		m["port_idx"] = idx
		out = append(out, m)
	}
	return out
}

func portOverridesFromDevice(dev map[string]any) []map[string]any {
	return sliceOfMaps(dev["port_overrides"])
}

func indexOverrides(ovs []map[string]any) map[int]map[string]any {
	m := make(map[int]map[string]any, len(ovs))
	for _, o := range ovs {
		idx, ok := asInt(o["port_idx"])
		if !ok {
			continue
		}
		m[idx] = o
	}
	return m
}

func deviceHasPorts(m map[string]any) bool {
	table := sliceOfMaps(m["port_table"])
	return len(table) > 0
}

func resolveDeviceRaw(raw []map[string]any, query string) (map[string]any, error) {
	devices := make([]Device, 0, len(raw))
	byID := make(map[string]map[string]any, len(raw))
	for _, m := range raw {
		d := NormalizeDevice(m)
		devices = append(devices, d)
		byID[d.ID] = m
	}
	d, err := resolve.One(devices, query)
	if err != nil {
		return nil, err
	}
	return byID[d.ID], nil
}

func portInputOverride(portIdx int, in PortInput) map[string]any {
	patch := map[string]any{"port_idx": portIdx}
	if in.Name != "" {
		patch["name"] = in.Name
	}
	if in.SetPOE {
		patch["poe_mode"] = in.POE
	}
	if in.SetEnabled {
		patch["enable"] = in.Enabled
	}
	if in.Profile != "" {
		patch["portconf_id"] = in.Profile
	}
	return patch
}

func portSnapshot(p Port) map[string]any {
	return map[string]any{
		"device_id":   p.DeviceID,
		"device_name": p.DeviceName,
		"port_idx":    p.PortIdx,
		"name":        p.Name,
		"media":       p.Media,
		"speed":       p.Speed,
		"poe":         p.POE,
		"enabled":     p.Enabled,
		"profile":     p.Profile,
		"networks":    p.Networks,
	}
}

func mergePortAfter(p Port, in PortInput) map[string]any {
	after := portSnapshot(p)
	if in.Name != "" {
		after["name"] = in.Name
	}
	if in.SetPOE {
		after["poe"] = in.POE
	}
	if in.SetEnabled {
		after["enabled"] = in.Enabled
	}
	if in.Profile != "" {
		after["profile"] = in.Profile
	}
	return after
}

func sliceOfMaps(v any) []map[string]any {
	if v == nil {
		return nil
	}
	switch t := v.(type) {
	case []map[string]any:
		return t
	case []any:
		out := make([]map[string]any, 0, len(t))
		for _, item := range t {
			if m, ok := item.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	default:
		return nil
	}
}

func cloneMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func speedString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	if n, ok := asInt(v); ok {
		return strconv.Itoa(n)
	}
	return anyToString(v)
}

func boolFieldDefault(m map[string]any, key string, def bool) bool {
	if _, ok := m[key]; !ok {
		return def
	}
	return boolField(m, key)
}
