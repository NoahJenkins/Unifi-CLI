package domain_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
	"github.com/noahjenkins/unifi-cli/internal/client"
	"github.com/noahjenkins/unifi-cli/internal/domain"
)

func fixtureNetworks(t *testing.T) []map[string]any {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	path := filepath.Join(filepath.Dir(file), "..", "client", "fixtures", "rest_networkconf.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw []map[string]any
	if err := client.DecodeData(b, &raw); err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestNormalizeNetwork(t *testing.T) {
	raw := fixtureNetworks(t)
	if len(raw) < 4 {
		t.Fatalf("fixture networks = %d, want >= 4", len(raw))
	}

	lan := domain.NormalizeNetwork(raw[0])
	if lan.ID != "net1" || lan.Name != "LAN" || lan.Purpose != "corporate" {
		t.Fatalf("lan identity: %+v", lan)
	}
	if lan.VLAN != nil {
		t.Fatalf("lan vlan = %v, want nil", *lan.VLAN)
	}
	if lan.Subnet != "192.168.1.1/24" || !lan.DHCPEnabled || lan.DomainName != "local" {
		t.Fatalf("lan fields: %+v", lan)
	}
	if lan.WAN {
		t.Fatal("lan should not be WAN")
	}

	iot := domain.NormalizeNetwork(raw[1])
	if iot.VLAN == nil || *iot.VLAN != 20 {
		t.Fatalf("iot vlan: %+v", iot.VLAN)
	}
	if iot.Subnet != "192.168.20.1/24" || iot.DomainName != "iot.local" {
		t.Fatalf("iot: %+v", iot)
	}

	guest := domain.NormalizeNetwork(raw[2])
	if guest.Purpose != "guest" || guest.Subnet != "192.168.30.1/24" {
		t.Fatalf("guest: %+v", guest)
	}
	if guest.VLAN == nil || *guest.VLAN != 30 {
		t.Fatalf("guest vlan: %+v", guest.VLAN)
	}
	if guest.DHCPEnabled {
		t.Fatal("guest dhcp should be false")
	}

	wan := domain.NormalizeNetwork(raw[3])
	if !wan.WAN || wan.Purpose != "wan" || wan.Name != "WAN" {
		t.Fatalf("wan: %+v", wan)
	}
}

type fakeNetworkAPI struct {
	networks []map[string]any
	calls    []networkCall
	err      error
}

type networkCall struct {
	method string
	path   string
	body   any
}

func (f *fakeNetworkAPI) Do(ctx context.Context, method, path string, in, out any) error {
	f.calls = append(f.calls, networkCall{method: method, path: path, body: in})
	if f.err != nil {
		return f.err
	}
	if method == http.MethodGet {
		b, err := json.Marshal(f.networks)
		if err != nil {
			return err
		}
		return json.Unmarshal(b, out)
	}
	if method == http.MethodPost && out != nil {
		// simulate create returning created object
		created := map[string]any{
			"_id":          "net-new",
			"name":         "Created",
			"purpose":      "corporate",
			"ip_subnet":    "10.0.0.1/24",
			"dhcpd_enabled": true,
		}
		if body, ok := in.(map[string]any); ok {
			for k, v := range body {
				created[k] = v
			}
			created["_id"] = "net-new"
		}
		b, err := json.Marshal([]map[string]any{created})
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

func (f *fakeNetworkAPI) SitePath(parts ...string) string {
	p := "/proxy/network/api/s/default"
	for _, part := range parts {
		p += "/" + part
	}
	return p
}

func TestNetworkServiceList(t *testing.T) {
	api := &fakeNetworkAPI{networks: fixtureNetworks(t)}
	svc := domain.NewNetworkService(api)
	got, err := svc.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(api.calls) != 1 {
		t.Fatalf("calls = %d", len(api.calls))
	}
	if api.calls[0].method != http.MethodGet {
		t.Fatalf("method = %q", api.calls[0].method)
	}
	if api.calls[0].path != "/proxy/network/api/s/default/rest/networkconf" {
		t.Fatalf("path = %q", api.calls[0].path)
	}
	if len(got) != 4 {
		t.Fatalf("len = %d", len(got))
	}
	if got[0].Name != "LAN" || got[3].WAN != true {
		t.Fatalf("list: %+v", got)
	}
}

func TestNetworkServiceGet(t *testing.T) {
	api := &fakeNetworkAPI{networks: fixtureNetworks(t)}
	svc := domain.NewNetworkService(api)

	byID, err := svc.Get(context.Background(), "net2")
	if err != nil {
		t.Fatal(err)
	}
	if byID.Name != "IoT" {
		t.Fatalf("by id: %+v", byID)
	}

	byName, err := svc.Get(context.Background(), "Guest")
	if err != nil {
		t.Fatal(err)
	}
	if byName.ID != "net3" {
		t.Fatalf("by name: %+v", byName)
	}

	_, err = svc.Get(context.Background(), "missing")
	if !apperr.Is(err, apperr.NotFound) {
		t.Fatalf("missing err = %v", err)
	}
}

func TestNetworkCreatePlanAndApply(t *testing.T) {
	api := &fakeNetworkAPI{networks: fixtureNetworks(t)}
	svc := domain.NewNetworkService(api)
	ctx := context.Background()

	in := domain.NetworkInput{
		Name:        "Cameras",
		Purpose:     "corporate",
		VLAN:        intPtr(40),
		Subnet:      "192.168.40.1/24",
		DHCPEnabled: true,
		DomainName:  "cam.local",
	}
	p, err := svc.Create(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Changes) != 1 || p.Changes[0].Op != "create" {
		t.Fatalf("plan: %+v", p)
	}
	if p.Changes[0].Name != "Cameras" {
		t.Fatalf("plan name = %q", p.Changes[0].Name)
	}
	after, _ := p.Changes[0].After.(map[string]any)
	if after["name"] != "Cameras" || after["purpose"] != "corporate" {
		t.Fatalf("after: %+v", after)
	}

	got, err := svc.ApplyCreate(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Cameras" {
		t.Fatalf("created: %+v", got)
	}
	last := api.calls[len(api.calls)-1]
	if last.method != http.MethodPost {
		t.Fatalf("method = %q", last.method)
	}
	if last.path != "/proxy/network/api/s/default/rest/networkconf" {
		t.Fatalf("path = %q", last.path)
	}
	body, _ := last.body.(map[string]any)
	if body["name"] != "Cameras" || body["purpose"] != "corporate" {
		t.Fatalf("body = %+v", body)
	}
	if body["vlan"] != 40 && body["vlan"] != float64(40) {
		// allow int or float from JSON roundtrip
		if v, ok := body["vlan"].(int); !ok || v != 40 {
			if v, ok := body["vlan"].(float64); !ok || int(v) != 40 {
				t.Fatalf("vlan body = %+v", body["vlan"])
			}
		}
	}
}

func TestNetworkUpdatePlanAndApply(t *testing.T) {
	api := &fakeNetworkAPI{networks: fixtureNetworks(t)}
	svc := domain.NewNetworkService(api)
	ctx := context.Background()

	in := domain.NetworkInput{
		Name:   "IoT-Renamed",
		Subnet: "192.168.20.1/24",
	}
	p, n, err := svc.Update(ctx, "net2", in)
	if err != nil {
		t.Fatal(err)
	}
	if n.Name != "IoT" {
		t.Fatalf("preview: %+v", n)
	}
	if len(p.Changes) != 1 || p.Changes[0].Op != "update" || p.Changes[0].ID != "net2" {
		t.Fatalf("plan: %+v", p)
	}
	before, _ := p.Changes[0].Before.(map[string]any)
	after, _ := p.Changes[0].After.(map[string]any)
	if before["name"] != "IoT" || after["name"] != "IoT-Renamed" {
		t.Fatalf("before/after: %+v %+v", before, after)
	}

	got, err := svc.ApplyUpdate(ctx, "net2", in)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "net2" {
		t.Fatalf("updated: %+v", got)
	}
	last := api.calls[len(api.calls)-1]
	if last.method != http.MethodPut {
		t.Fatalf("method = %q", last.method)
	}
	if last.path != "/proxy/network/api/s/default/rest/networkconf/net2" {
		t.Fatalf("path = %q", last.path)
	}
}

func TestNetworkDeletePlanAndApply(t *testing.T) {
	api := &fakeNetworkAPI{networks: fixtureNetworks(t)}
	svc := domain.NewNetworkService(api)
	ctx := context.Background()

	p, n, err := svc.Delete(ctx, "IoT")
	if err != nil {
		t.Fatal(err)
	}
	if n.ID != "net2" {
		t.Fatalf("network: %+v", n)
	}
	if len(p.Changes) != 1 || p.Changes[0].Op != "delete" {
		t.Fatalf("plan: %+v", p)
	}
	if n.WAN {
		t.Fatal("IoT should not be WAN")
	}

	got, err := svc.ApplyDelete(ctx, "IoT")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "net2" {
		t.Fatalf("deleted: %+v", got)
	}
	last := api.calls[len(api.calls)-1]
	if last.method != http.MethodDelete {
		t.Fatalf("method = %q", last.method)
	}
	if last.path != "/proxy/network/api/s/default/rest/networkconf/net2" {
		t.Fatalf("path = %q", last.path)
	}
}

func TestNetworkDeleteWANIsDestructive(t *testing.T) {
	api := &fakeNetworkAPI{networks: fixtureNetworks(t)}
	svc := domain.NewNetworkService(api)
	p, n, err := svc.Delete(context.Background(), "WAN")
	if err != nil {
		t.Fatal(err)
	}
	if !n.WAN {
		t.Fatalf("want WAN network: %+v", n)
	}
	if len(p.Changes) != 1 || p.Changes[0].Op != "delete" {
		t.Fatalf("plan: %+v", p)
	}
	// Service exposes IsDestructive for CLI safe_mode wiring
	if !domain.NetworkDeleteDestructive(n) {
		t.Fatal("WAN delete must be destructive")
	}
	lan, err := svc.Get(context.Background(), "LAN")
	if err != nil {
		t.Fatal(err)
	}
	if domain.NetworkDeleteDestructive(lan) {
		t.Fatal("LAN delete must not be destructive")
	}
}

func intPtr(v int) *int { return &v }
