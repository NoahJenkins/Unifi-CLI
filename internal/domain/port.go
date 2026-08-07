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
	Name         string
	SetName      bool
	ClearName    bool
	POE          string
	SetPOE       bool
	Enabled      bool
	SetEnabled   bool
	Profile      string
	SetProfile   bool
	ClearProfile bool
}

type PortService struct {
	api PortAPI
}

func NewPortService(api PortAPI) *PortService {
	return &PortService{api: api}
}

func (s *PortService) List(ctx context.Context, deviceQuery string) ([]Port, error) {
	if raw, official, err := fetchOfficialSite(s.api, ctx, "devices"); official {
		if err != nil {
			return nil, err
		}
		return s.listOfficial(ctx, raw, deviceQuery)
	}
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
	out := make([]Port, 0)
	for _, m := range raw {
		if !deviceHasPorts(m) {
			continue
		}
		out = append(out, ExtractPortsFromDevice(m)...)
	}
	return out, nil
}

func (s *PortService) listOfficial(ctx context.Context, raw []map[string]any, deviceQuery string) ([]Port, error) {
	selected := raw
	if deviceQuery != "" {
		dev, err := resolveDeviceRaw(raw, deviceQuery)
		if err != nil {
			return nil, err
		}
		selected = []map[string]any{dev}
	}
	out := make([]Port, 0)
	for _, overview := range selected {
		id := strField(overview, "id")
		if id == "" {
			continue
		}
		detail, err := fetchOfficialSiteDetail(s.api, ctx, id, "devices")
		if err != nil {
			return nil, err
		}
		out = append(out, ExtractOfficialPortsFromDevice(detail)...)
	}
	return out, nil
}

func (s *PortService) Get(ctx context.Context, deviceQuery string, portIdx int) (Port, error) {
	if err := validatePortIndex(portIdx); err != nil {
		return Port{}, err
	}
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
	if err := validatePortUpdate(portIdx, in); err != nil {
		return plan.Plan{}, Port{}, err
	}
	cur, _, err := s.loadAuthoritativePort(ctx, deviceQuery, portIdx)
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
	if err := validatePortUpdate(portIdx, in); err != nil {
		return Port{}, err
	}
	cur, existing, err := s.loadAuthoritativePort(ctx, deviceQuery, portIdx)
	if err != nil {
		return Port{}, err
	}
	patch := portInputOverride(portIdx, in)
	merged := MergePortOverrides(existing, patch)

	path := s.api.SitePath(client.PathRestDevice, cur.DeviceID)
	body := map[string]any{"port_overrides": merged}
	if err := s.api.Do(ctx, http.MethodPut, path, body, nil); err != nil {
		return Port{}, err
	}

	// apply local view
	if inputSetsPortName(in) {
		cur.Name = in.Name
	}
	if in.ClearName {
		cur.Name = ""
	}
	if in.SetPOE {
		cur.POE = in.POE
	}
	if in.SetEnabled {
		cur.Enabled = in.Enabled
	}
	if inputSetsPortProfile(in) {
		cur.Profile = in.Profile
	}
	if in.ClearProfile {
		cur.Profile = ""
	}
	return cur, nil
}

func (s *PortService) loadAuthoritativePort(ctx context.Context, deviceQuery string, portIdx int) (Port, []map[string]any, error) {
	raw, err := s.loadDevices(ctx)
	if err != nil {
		return Port{}, nil, err
	}
	dev, err := resolveDeviceRaw(raw, deviceQuery)
	if err != nil {
		return Port{}, nil, err
	}
	devID := strField(dev, "_id", "id")
	// rest/device/{id} is authoritative for configured overrides. Merge those
	// overrides onto stat/device's port table so plan, observation, and apply
	// all derive the selected port from the same state source.
	existing, err := s.loadRestPortOverrides(ctx, devID)
	if err != nil {
		return Port{}, nil, err
	}
	mergedDevice := cloneMap(dev)
	mergedDevice["port_overrides"] = existing
	curPorts := ExtractPortsFromDevice(mergedDevice)
	var cur Port
	for _, p := range curPorts {
		if p.PortIdx == portIdx {
			cur = p
			return cur, existing, nil
		}
	}
	return Port{}, nil, apperr.WithHint(
		apperr.Newf(apperr.NotFound, "port %d not found on %s", portIdx, deviceQuery),
		"list ports with: unifi port list --device <device>",
	)
}

func (s *PortService) loadDevices(ctx context.Context) ([]map[string]any, error) {
	var raw []map[string]any
	path := s.api.SitePath(client.PathStatDevice)
	if err := s.api.Do(ctx, http.MethodGet, path, nil, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// loadRestPortOverrides fetches port_overrides from GET rest/device/{id}.
func (s *PortService) loadRestPortOverrides(ctx context.Context, devID string) ([]map[string]any, error) {
	var raw []map[string]any
	path := s.api.SitePath(client.PathRestDevice, devID)
	if err := s.api.Do(ctx, http.MethodGet, path, nil, &raw); err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, nil
	}
	return portOverridesFromDevice(raw[0]), nil
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

func ExtractOfficialPortsFromDevice(dev map[string]any) []Port {
	interfaces, _ := dev["interfaces"].(map[string]any)
	rows := sliceOfMaps(interfaces["ports"])
	out := make([]Port, 0, len(rows))
	for _, row := range rows {
		idx, ok := asInt(row["idx"])
		if !ok || idx < 1 {
			continue
		}
		poe := ""
		if config, ok := row["poe"].(map[string]any); ok {
			if boolField(config, "enabled") {
				poe = "auto"
			} else {
				poe = "off"
			}
		}
		out = append(out, Port{
			DeviceID:   strField(dev, "id"),
			DeviceName: strField(dev, "name"),
			PortIdx:    idx,
			Media:      strField(row, "connector"),
			Speed:      speedString(row["speedMbps"]),
			POE:        poe,
			Enabled:    true,
		})
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
	if inputSetsPortName(in) {
		patch["name"] = in.Name
	}
	if in.ClearName {
		patch["name"] = ""
	}
	if in.SetPOE {
		patch["poe_mode"] = in.POE
	}
	if in.SetEnabled {
		patch["enable"] = in.Enabled
	}
	if inputSetsPortProfile(in) {
		patch["portconf_id"] = in.Profile
	}
	if in.ClearProfile {
		patch["portconf_id"] = ""
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
	if inputSetsPortName(in) {
		after["name"] = in.Name
	}
	if in.ClearName {
		after["name"] = ""
	}
	if in.SetPOE {
		after["poe"] = in.POE
	}
	if in.SetEnabled {
		after["enabled"] = in.Enabled
	}
	if inputSetsPortProfile(in) {
		after["profile"] = in.Profile
	}
	if in.ClearProfile {
		after["profile"] = ""
	}
	return after
}

func validatePortUpdate(portIdx int, in PortInput) error {
	if err := validatePortIndex(portIdx); err != nil {
		return err
	}
	if !inputSetsPortName(in) && !in.ClearName && !in.SetPOE && !in.SetEnabled &&
		!inputSetsPortProfile(in) && !in.ClearProfile {
		return apperr.New(apperr.ValidationFailed, "port update requires at least one changed field")
	}
	if in.ClearName && inputSetsPortName(in) {
		return apperr.New(apperr.ValidationFailed, "use either port name or clear name, not both")
	}
	if in.ClearProfile && inputSetsPortProfile(in) {
		return apperr.New(apperr.ValidationFailed, "use either port profile or clear profile, not both")
	}
	if in.SetPOE {
		return validateEnum("PoE mode", in.POE, "auto", "off", "pasv24", "passthrough")
	}
	return nil
}

func inputSetsPortName(in PortInput) bool    { return in.SetName || in.Name != "" }
func inputSetsPortProfile(in PortInput) bool { return in.SetProfile || in.Profile != "" }

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
