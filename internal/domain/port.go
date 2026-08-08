package domain

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"sort"
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
	DeviceID     string `json:"device_id"`
	DeviceName   string `json:"device_name"`
	PortIdx      int    `json:"port_idx"`
	Name         string `json:"name"`
	Media        string `json:"media"`
	Speed        string `json:"speed"`
	POE          string `json:"poe"`
	Enabled      bool   `json:"enabled"`
	EnabledKnown bool   `json:"-"`
	Profile      string `json:"profile"`
	Networks     string `json:"networks"`
}

func (p Port) MarshalJSON() ([]byte, error) {
	type portJSON struct {
		DeviceID   string `json:"device_id"`
		DeviceName string `json:"device_name"`
		PortIdx    int    `json:"port_idx"`
		Name       string `json:"name"`
		Media      string `json:"media"`
		Speed      string `json:"speed"`
		POE        string `json:"poe"`
		Enabled    *bool  `json:"enabled,omitempty"`
		Profile    string `json:"profile"`
		Networks   string `json:"networks"`
	}
	var enabled *bool
	if p.EnabledKnown {
		enabled = &p.Enabled
	}
	return json.Marshal(portJSON{
		DeviceID: p.DeviceID, DeviceName: p.DeviceName, PortIdx: p.PortIdx, Name: p.Name,
		Media: p.Media, Speed: p.Speed, POE: p.POE, Enabled: enabled, Profile: p.Profile, Networks: p.Networks,
	})
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
	ctx, cancel := officialOperationContext(ctx)
	defer cancel()
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
	if len(raw) == 0 {
		return []Port{}, nil
	}
	selected := make([]map[string]any, 0, len(raw))
	if deviceQuery != "" {
		dev, err := resolveDeviceRaw(raw, deviceQuery)
		if err != nil {
			return nil, err
		}
		selected = []map[string]any{dev}
	} else {
		for _, overview := range raw {
			if officialDeviceHasPorts(overview) {
				selected = append(selected, overview)
			}
		}
	}
	details, err := fetchOfficialSiteDetails(ctx, s.api, selected, "devices")
	if err != nil {
		return nil, err
	}
	out := make([]Port, 0)
	for _, detail := range details {
		out = append(out, ExtractOfficialPortsFromDevice(detail)...)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].DeviceName != out[j].DeviceName {
			return out[i].DeviceName < out[j].DeviceName
		}
		if out[i].DeviceID != out[j].DeviceID {
			return out[i].DeviceID < out[j].DeviceID
		}
		return out[i].PortIdx < out[j].PortIdx
	})
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
	p, cur, _, err := s.PrepareUpdate(ctx, deviceQuery, portIdx, in)
	return p, cur, err
}

// PrepareUpdate captures both the user-visible change and the complete
// authoritative override document that a full legacy PUT would replace.
func (s *PortService) PrepareUpdate(ctx context.Context, deviceQuery string, portIdx int, in PortInput) (plan.Plan, Port, any, error) {
	if err := validatePortUpdate(portIdx, in); err != nil {
		return plan.Plan{}, Port{}, nil, err
	}
	cur, existing, err := s.loadAuthoritativePort(ctx, deviceQuery, portIdx)
	if err != nil {
		return plan.Plan{}, Port{}, nil, err
	}
	return preparePortUpdate(cur, existing, in)
}

func preparePortUpdate(cur Port, existing []map[string]any, in PortInput) (plan.Plan, Port, any, error) {
	before := portSnapshot(cur)
	after := mergePortAfter(cur, in)
	if reflect.DeepEqual(before, after) {
		return plan.Plan{}, Port{}, nil, apperr.New(apperr.ValidationFailed, "port update would not change controller state")
	}
	name := fmt.Sprintf("%s:%d", cur.DeviceName, cur.PortIdx)
	p := plan.Update("port", fmt.Sprintf("%s/%d", cur.DeviceID, cur.PortIdx), name,
		fmt.Sprintf("update port %d on %s", cur.PortIdx, cur.DeviceName),
		before,
		after,
	)
	snapshot := map[string]any{
		"changes":        p.Changes,
		"port_overrides": canonicalPortOverrides(existing),
	}
	return p, cur, snapshot, nil
}

func (s *PortService) ApplyUpdate(ctx context.Context, deviceQuery string, portIdx int, in PortInput) (Port, error) {
	return s.applyUpdate(ctx, deviceQuery, portIdx, in, nil)
}

func (s *PortService) ApplyUpdatePrepared(ctx context.Context, target plan.Target, deviceQuery string, portIdx int, in PortInput) (Port, error) {
	return s.applyUpdate(ctx, deviceQuery, portIdx, in, &target)
}

func (s *PortService) applyUpdate(ctx context.Context, deviceQuery string, portIdx int, in PortInput, target *plan.Target) (Port, error) {
	if err := validatePortUpdate(portIdx, in); err != nil {
		return Port{}, err
	}
	cur, existing, err := s.loadAuthoritativePort(ctx, deviceQuery, portIdx)
	if err != nil {
		return Port{}, err
	}
	_, _, snapshot, err := preparePortUpdate(cur, existing, in)
	if err != nil {
		return Port{}, err
	}
	if target != nil {
		if err := requirePreparedTarget(*target, snapshot); err != nil {
			return Port{}, err
		}
	}
	patch := portInputOverride(portIdx, in)
	merged := MergePortOverrides(existing, patch)

	path := s.api.SitePath(client.PathRestDevice, cur.DeviceID)
	body := map[string]any{"port_overrides": merged}
	if err := s.api.Do(ctx, http.MethodPut, path, body, nil); err != nil {
		return Port{}, err
	}

	observed, observedOverrides, err := s.loadAuthoritativePort(ctx, cur.DeviceID, portIdx)
	if err != nil {
		return Port{}, verificationError("updated port could not be verified", err)
	}
	if !portMatchesInput(observed, in) {
		return Port{}, apperr.New(apperr.Conflict, "port update verification failed: observed fields differ from requested state")
	}
	if !portOverrideDocumentsEqual(observedOverrides, merged) {
		return Port{}, apperr.New(apperr.Conflict, "port update verification failed: complete override document differs from requested state")
	}
	return observed, nil
}

func canonicalPortOverrides(overrides []map[string]any) []map[string]any {
	canonical := make([]map[string]any, 0, len(overrides))
	for _, override := range overrides {
		canonical = append(canonical, deepCloneMap(override))
	}
	sort.SliceStable(canonical, func(i, j int) bool {
		left, _ := asInt(canonical[i]["port_idx"])
		right, _ := asInt(canonical[j]["port_idx"])
		return left < right
	})
	return canonical
}

func portMatchesInput(observed Port, in PortInput) bool {
	if inputSetsPortName(in) && observed.Name != in.Name {
		return false
	}
	if in.ClearName && observed.Name != "" {
		return false
	}
	if in.SetPOE && observed.POE != in.POE {
		return false
	}
	if in.SetEnabled && (!observed.EnabledKnown || observed.Enabled != in.Enabled) {
		return false
	}
	if inputSetsPortProfile(in) && observed.Profile != in.Profile {
		return false
	}
	return !in.ClearProfile || observed.Profile == ""
}

func (s *PortService) loadAuthoritativePort(ctx context.Context, deviceQuery string, portIdx int) (Port, []map[string]any, error) {
	raw, err := s.loadDevices(ctx)
	if err != nil {
		return Port{}, nil, err
	}
	dev, err := s.resolveLegacyDeviceRaw(ctx, raw, deviceQuery)
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

func (s *PortService) resolveLegacyDeviceRaw(ctx context.Context, raw []map[string]any, query string) (map[string]any, error) {
	legacy := make([]Device, 0, len(raw))
	byID := make(map[string]map[string]any, len(raw))
	for _, item := range raw {
		device := NormalizeDevice(item)
		legacy = append(legacy, device)
		byID[device.ID] = item
	}
	if item, ok := byID[query]; ok {
		return item, nil
	}
	if !looksLikeUUID(query) {
		device, err := resolve.One(legacy, query)
		if err != nil {
			return nil, err
		}
		return byID[device.ID], nil
	}
	officialRaw, official, err := fetchOfficialSite(s.api, ctx, "devices")
	if err != nil {
		return nil, err
	}
	if !official {
		device, err := resolve.One(legacy, query)
		if err != nil {
			return nil, err
		}
		return byID[device.ID], nil
	}
	officialDevices := make([]Device, 0, len(officialRaw))
	for _, item := range officialRaw {
		officialDevices = append(officialDevices, NormalizeDevice(item))
	}
	device, err := resolveLegacyMutationTarget(legacy, officialDevices, query, "port device", func(a, b Device) bool { return sameMAC(a, b) })
	if err != nil {
		return nil, err
	}
	return byID[device.ID], nil
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
	if len(raw) != 1 {
		return nil, apperr.Newf(apperr.Conflict, "port override detail returned %d devices, want exactly one", len(raw))
	}
	if strField(raw[0], "_id", "id") != devID {
		return nil, apperr.New(apperr.Conflict, "port override detail ID does not match requested device")
	}
	rawOverrides, present := raw[0]["port_overrides"]
	if !present || rawOverrides == nil {
		return nil, apperr.New(apperr.Conflict, "port override detail is missing the complete override document")
	}
	overrides, err := strictPortOverrides(rawOverrides)
	if err != nil {
		return nil, err
	}
	if overrides == nil {
		return nil, nil
	}
	return overrides, nil
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
			DeviceID:     devID,
			DeviceName:   devName,
			PortIdx:      idx,
			Name:         strField(row, "name"),
			Media:        strField(row, "media"),
			Speed:        speedString(row["speed"]),
			POE:          strField(row, "poe_mode", "poe"),
			Enabled:      boolFieldDefault(row, "enable", true),
			EnabledKnown: true,
			Profile:      strField(row, "portconf_id", "profile"),
			Networks:     strField(row, "native_networkconf_id", "networks"),
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

func officialDeviceHasPorts(m map[string]any) bool {
	for _, name := range anyStringSlice(m["interfaces"]) {
		if name == "ports" {
			return true
		}
	}
	return false
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

func strictPortOverrides(value any) ([]map[string]any, error) {
	if value == nil {
		return nil, nil
	}
	var raw []any
	switch typed := value.(type) {
	case []any:
		raw = typed
	case []map[string]any:
		raw = make([]any, len(typed))
		for i := range typed {
			raw[i] = typed[i]
		}
	default:
		return nil, apperr.New(apperr.Conflict, "port override document is not an array")
	}
	out := make([]map[string]any, 0, len(raw))
	seen := make(map[int]struct{}, len(raw))
	for _, item := range raw {
		override, ok := item.(map[string]any)
		if !ok {
			return nil, apperr.New(apperr.Conflict, "port override document contains a non-object entry")
		}
		index, ok := asInt(override["port_idx"])
		if !ok || index < 1 {
			return nil, apperr.New(apperr.Conflict, "port override document contains an invalid port index")
		}
		if _, duplicate := seen[index]; duplicate {
			return nil, apperr.New(apperr.Conflict, "port override document contains a duplicate port index")
		}
		seen[index] = struct{}{}
		cloned := cloneMap(override)
		cloned["port_idx"] = index
		out = append(out, cloned)
	}
	return out, nil
}

func portOverrideDocumentsEqual(left, right []map[string]any) bool {
	canonical := func(items []map[string]any) []map[string]any {
		out := make([]map[string]any, len(items))
		for i, item := range items {
			out[i] = cloneMap(item)
		}
		sort.Slice(out, func(i, j int) bool {
			leftIndex, _ := asInt(out[i]["port_idx"])
			rightIndex, _ := asInt(out[j]["port_idx"])
			return leftIndex < rightIndex
		})
		return out
	}
	return wireDocumentsEqual(canonical(left), canonical(right))
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
