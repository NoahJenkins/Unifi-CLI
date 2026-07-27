package domain_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
	"github.com/noahjenkins/unifi-cli/internal/domain"
)

func sampleSwitchDevice() map[string]any {
	return map[string]any{
		"_id":  "sw1",
		"mac":  "aa:bb:cc:dd:ee:03",
		"name": "Switch-Core",
		"type": "usw",
		"port_table": []any{
			map[string]any{
				"port_idx":     float64(1),
				"name":         "Port 1",
				"media":        "GE",
				"speed":        float64(1000),
				"poe_mode":     "auto",
				"enable":       true,
				"portconf_id":  "prof-all",
				"native_networkconf_id": "net-lan",
			},
			map[string]any{
				"port_idx":    float64(12),
				"name":        "Port 12",
				"media":       "GE",
				"speed":       float64(100),
				"poe_mode":    "auto",
				"enable":      true,
				"portconf_id": "prof-all",
			},
		},
		"port_overrides": []any{
			map[string]any{
				"port_idx":  float64(12),
				"name":      "AP-Uplink",
				"poe_mode":  "pasv24",
				"portconf_id": "prof-ap",
			},
		},
	}
}

func TestExtractPortsFromDevice(t *testing.T) {
	dev := sampleSwitchDevice()
	ports := domain.ExtractPortsFromDevice(dev)
	if len(ports) != 2 {
		t.Fatalf("len = %d, want 2", len(ports))
	}

	p1 := ports[0]
	if p1.DeviceID != "sw1" || p1.DeviceName != "Switch-Core" {
		t.Fatalf("device identity: %+v", p1)
	}
	if p1.PortIdx != 1 || p1.Name != "Port 1" {
		t.Fatalf("port1 identity: %+v", p1)
	}
	if p1.Media != "GE" || p1.Speed != "1000" || p1.POE != "auto" {
		t.Fatalf("port1 media/speed/poe: %+v", p1)
	}
	if !p1.Enabled || p1.Profile != "prof-all" || p1.Networks != "net-lan" {
		t.Fatalf("port1 enabled/profile/networks: %+v", p1)
	}

	// override wins for name, poe, profile
	p12 := ports[1]
	if p12.PortIdx != 12 {
		t.Fatalf("port12 idx: %+v", p12)
	}
	if p12.Name != "AP-Uplink" {
		t.Fatalf("override name = %q, want AP-Uplink", p12.Name)
	}
	if p12.POE != "pasv24" {
		t.Fatalf("override poe = %q, want pasv24", p12.POE)
	}
	if p12.Profile != "prof-ap" {
		t.Fatalf("override profile = %q, want prof-ap", p12.Profile)
	}
	if p12.Speed != "100" || p12.Media != "GE" {
		t.Fatalf("port12 base fields from table: %+v", p12)
	}
}

func TestMergePortOverride(t *testing.T) {
	existing := []map[string]any{
		{"port_idx": float64(5), "name": "Cam", "poe_mode": "auto"},
		{"port_idx": float64(12), "name": "AP-Uplink", "poe_mode": "pasv24"},
	}
	patch := map[string]any{
		"port_idx": 12,
		"poe_mode": "off",
		"enable":   false,
	}
	merged := domain.MergePortOverrides(existing, patch)
	if len(merged) != 2 {
		t.Fatalf("len = %d, want 2", len(merged))
	}
	// port 5 unchanged
	if merged[0]["name"] != "Cam" || merged[0]["poe_mode"] != "auto" {
		t.Fatalf("port5: %+v", merged[0])
	}
	// port 12 merged
	m12 := merged[1]
	if m12["port_idx"] != 12 && m12["port_idx"] != float64(12) {
		// allow int or float depending on implementation
		if idx, ok := asTestInt(m12["port_idx"]); !ok || idx != 12 {
			t.Fatalf("port_idx = %v", m12["port_idx"])
		}
	}
	if m12["poe_mode"] != "off" {
		t.Fatalf("poe_mode = %v", m12["poe_mode"])
	}
	if m12["enable"] != false {
		t.Fatalf("enable = %v", m12["enable"])
	}
	if m12["name"] != "AP-Uplink" {
		t.Fatalf("name preserved = %v", m12["name"])
	}
}

func TestMergePortOverrideAppendsNew(t *testing.T) {
	existing := []map[string]any{
		{"port_idx": float64(5), "name": "Cam"},
	}
	patch := map[string]any{"port_idx": 12, "poe_mode": "off"}
	merged := domain.MergePortOverrides(existing, patch)
	if len(merged) != 2 {
		t.Fatalf("len = %d, want 2", len(merged))
	}
	last := merged[1]
	if idx, ok := asTestInt(last["port_idx"]); !ok || idx != 12 {
		t.Fatalf("appended port_idx = %v", last["port_idx"])
	}
	if last["poe_mode"] != "off" {
		t.Fatalf("poe = %v", last["poe_mode"])
	}
}

func asTestInt(v any) (int, bool) {
	switch t := v.(type) {
	case int:
		return t, true
	case int64:
		return int(t), true
	case float64:
		return int(t), true
	default:
		return 0, false
	}
}

type fakePortAPI struct {
	devices     []map[string]any
	restDevices map[string]map[string]any // keyed by device id for GET rest/device/{id}
	calls       []portCall
	err         error
}

type portCall struct {
	method string
	path   string
	body   any
}

func (f *fakePortAPI) Do(ctx context.Context, method, path string, in, out any) error {
	f.calls = append(f.calls, portCall{method: method, path: path, body: in})
	if f.err != nil {
		return f.err
	}
	if method == http.MethodGet {
		// GET rest/device/{id} — authoritative config
		const restPrefix = "/proxy/network/api/s/default/rest/device/"
		if len(path) > len(restPrefix) && path[:len(restPrefix)] == restPrefix {
			id := path[len(restPrefix):]
			dev, ok := f.restDevices[id]
			if !ok {
				// fall back to matching stat device if rest not stubbed
				for _, d := range f.devices {
					if strID(d) == id {
						dev = d
						ok = true
						break
					}
				}
			}
			if !ok {
				return json.Unmarshal([]byte(`[]`), out)
			}
			b, err := json.Marshal([]map[string]any{dev})
			if err != nil {
				return err
			}
			return json.Unmarshal(b, out)
		}
		b, err := json.Marshal(f.devices)
		if err != nil {
			return err
		}
		return json.Unmarshal(b, out)
	}
	if out != nil {
		_ = json.Unmarshal([]byte(`[]`), out)
	}
	return nil
}

func strID(m map[string]any) string {
	if v, ok := m["_id"].(string); ok {
		return v
	}
	if v, ok := m["id"].(string); ok {
		return v
	}
	return ""
}

func (f *fakePortAPI) SitePath(parts ...string) string {
	p := "/proxy/network/api/s/default"
	for _, part := range parts {
		p += "/" + part
	}
	return p
}

func devicesWithPorts() []map[string]any {
	ap := map[string]any{
		"_id":  "ap1",
		"mac":  "aa:bb:cc:dd:ee:02",
		"name": "AP-Office",
		"type": "uap",
	}
	return []map[string]any{ap, sampleSwitchDevice()}
}

func TestPortServiceList(t *testing.T) {
	api := &fakePortAPI{devices: devicesWithPorts()}
	svc := domain.NewPortService(api)

	// all switch ports
	all, err := svc.List(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("all ports = %d, want 2 (AP skipped)", len(all))
	}
	if api.methodPath() != "GET /proxy/network/api/s/default/stat/device" {
		t.Fatalf("call = %s", api.methodPath())
	}

	// filter by device name
	byDev, err := svc.List(context.Background(), "Switch-Core")
	if err != nil {
		t.Fatal(err)
	}
	if len(byDev) != 2 {
		t.Fatalf("device ports = %d", len(byDev))
	}
	if byDev[0].DeviceName != "Switch-Core" {
		t.Fatalf("device name: %+v", byDev[0])
	}
}

func TestPortServiceGet(t *testing.T) {
	api := &fakePortAPI{devices: devicesWithPorts()}
	svc := domain.NewPortService(api)

	p, err := svc.Get(context.Background(), "Switch-Core", 12)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "AP-Uplink" || p.POE != "pasv24" || p.PortIdx != 12 {
		t.Fatalf("get: %+v", p)
	}

	_, err = svc.Get(context.Background(), "Switch-Core", 99)
	if !apperr.Is(err, apperr.NotFound) {
		t.Fatalf("missing port err = %v", err)
	}

	_, err = svc.Get(context.Background(), "missing-sw", 1)
	if !apperr.Is(err, apperr.NotFound) {
		t.Fatalf("missing device err = %v", err)
	}
}

func TestPortServiceUpdatePlanAndApply(t *testing.T) {
	api := &fakePortAPI{devices: devicesWithPorts()}
	svc := domain.NewPortService(api)

	in := domain.PortInput{POE: "off", SetPOE: true}
	pl, cur, err := svc.Update(context.Background(), "Switch-Core", 12, in)
	if err != nil {
		t.Fatal(err)
	}
	if cur.POE != "pasv24" {
		t.Fatalf("current poe = %q", cur.POE)
	}
	if pl.Summary == "" || len(pl.Changes) != 1 {
		t.Fatalf("plan: %+v", pl)
	}
	if pl.Changes[0].Op != "update" || pl.Changes[0].Resource != "port" {
		t.Fatalf("change: %+v", pl.Changes[0])
	}
	before, _ := pl.Changes[0].Before.(map[string]any)
	after, _ := pl.Changes[0].After.(map[string]any)
	if before["poe"] != "pasv24" || after["poe"] != "off" {
		t.Fatalf("before/after: %+v %+v", before, after)
	}

	got, err := svc.ApplyUpdate(context.Background(), "Switch-Core", 12, in)
	if err != nil {
		t.Fatal(err)
	}
	if got.POE != "off" || got.PortIdx != 12 {
		t.Fatalf("apply result: %+v", got)
	}

	// apply loads authoritative overrides via GET rest/device/{id} before PUT
	gotRestGet := false
	for _, c := range api.calls {
		if c.method == http.MethodGet && c.path == "/proxy/network/api/s/default/rest/device/sw1" {
			gotRestGet = true
			break
		}
	}
	if !gotRestGet {
		t.Fatalf("expected GET rest/device/sw1 before PUT; calls=%v", callSummary(api.calls))
	}

	// last call PUT rest/device/sw1 with merged port_overrides
	last := api.calls[len(api.calls)-1]
	if last.method != http.MethodPut {
		t.Fatalf("method = %q", last.method)
	}
	if last.path != "/proxy/network/api/s/default/rest/device/sw1" {
		t.Fatalf("path = %q", last.path)
	}
	overrides := portOverridesFromBody(t, last.body)
	if len(overrides) != 1 {
		t.Fatalf("override len = %d, want 1: %+v", len(overrides), overrides)
	}
	o := overrides[0]
	idx, _ := asTestInt(o["port_idx"])
	if idx != 12 {
		t.Fatalf("port_idx = %v, want 12", o["port_idx"])
	}
	if o["poe_mode"] != "off" {
		t.Fatalf("poe_mode = %v, want off", o["poe_mode"])
	}
	// sibling keys survive poe-only update
	if o["name"] != "AP-Uplink" {
		t.Fatalf("name = %v, want AP-Uplink (sibling key must survive)", o["name"])
	}
	if o["portconf_id"] != "prof-ap" {
		t.Fatalf("portconf_id = %v, want prof-ap", o["portconf_id"])
	}
}

// TestPortServiceApplyUsesRestDeviceOverrides ensures apply merges against
// GET rest/device/{id} overrides (authoritative), not incomplete stat/device data.
func TestPortServiceApplyUsesRestDeviceOverrides(t *testing.T) {
	// stat has incomplete overrides (missing port 5) — would wipe if used alone
	statSW := sampleSwitchDevice()
	statSW["port_overrides"] = []any{
		map[string]any{
			"port_idx": float64(12),
			"name":     "AP-Uplink",
			"poe_mode": "pasv24",
		},
	}
	// rest has full authoritative overrides including sibling port 5
	restSW := sampleSwitchDevice()
	restSW["port_overrides"] = []any{
		map[string]any{
			"port_idx":  float64(5),
			"name":      "Cam",
			"poe_mode":  "auto",
			"portconf_id": "prof-cam",
		},
		map[string]any{
			"port_idx":    float64(12),
			"name":        "AP-Uplink",
			"poe_mode":    "pasv24",
			"portconf_id": "prof-ap",
		},
	}

	api := &fakePortAPI{
		devices:     []map[string]any{statSW},
		restDevices: map[string]map[string]any{"sw1": restSW},
	}
	svc := domain.NewPortService(api)

	in := domain.PortInput{POE: "off", SetPOE: true}
	_, err := svc.ApplyUpdate(context.Background(), "Switch-Core", 12, in)
	if err != nil {
		t.Fatal(err)
	}

	last := api.calls[len(api.calls)-1]
	if last.method != http.MethodPut {
		t.Fatalf("last method = %q", last.method)
	}
	overrides := portOverridesFromBody(t, last.body)
	if len(overrides) != 2 {
		t.Fatalf("override len = %d, want 2 (must not wipe port 5): %+v", len(overrides), overrides)
	}

	byIdx := map[int]map[string]any{}
	for _, o := range overrides {
		idx, ok := asTestInt(o["port_idx"])
		if !ok {
			t.Fatalf("bad port_idx: %+v", o)
		}
		byIdx[idx] = o
	}
	// sibling port 5 from rest must survive
	p5, ok := byIdx[5]
	if !ok {
		t.Fatalf("port 5 override missing (would wipe if stat used): %+v", overrides)
	}
	if p5["name"] != "Cam" || p5["poe_mode"] != "auto" || p5["portconf_id"] != "prof-cam" {
		t.Fatalf("port 5 corrupted: %+v", p5)
	}
	// target port 12 updated, siblings preserved
	p12, ok := byIdx[12]
	if !ok {
		t.Fatalf("port 12 missing: %+v", overrides)
	}
	if p12["poe_mode"] != "off" {
		t.Fatalf("port 12 poe = %v, want off", p12["poe_mode"])
	}
	if p12["name"] != "AP-Uplink" || p12["portconf_id"] != "prof-ap" {
		t.Fatalf("port 12 siblings lost: %+v", p12)
	}
}

func portOverridesFromBody(t *testing.T, body any) []map[string]any {
	t.Helper()
	m, ok := body.(map[string]any)
	if !ok {
		t.Fatalf("body type %T", body)
	}
	overrides, ok := m["port_overrides"].([]map[string]any)
	if ok {
		return overrides
	}
	raw, ok := m["port_overrides"].([]any)
	if !ok {
		t.Fatalf("port_overrides type %T in %+v", m["port_overrides"], m)
	}
	out := make([]map[string]any, 0, len(raw))
	for _, r := range raw {
		om, ok := r.(map[string]any)
		if !ok {
			t.Fatalf("override entry type %T", r)
		}
		out = append(out, om)
	}
	return out
}

func callSummary(calls []portCall) []string {
	out := make([]string, len(calls))
	for i, c := range calls {
		out[i] = c.method + " " + c.path
	}
	return out
}

func (f *fakePortAPI) methodPath() string {
	if len(f.calls) == 0 {
		return ""
	}
	c := f.calls[len(f.calls)-1]
	return c.method + " " + c.path
}
