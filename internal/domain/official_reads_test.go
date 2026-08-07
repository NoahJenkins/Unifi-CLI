package domain_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
	"github.com/noahjenkins/unifi-cli/internal/client"
	"github.com/noahjenkins/unifi-cli/internal/domain"
)

type officialReadAPI struct {
	collections map[string][]map[string]any
	details     map[string]map[string]any
	legacy      map[string][]map[string]any
	errs        map[string]error
	calls       []string
}

func (f *officialReadAPI) Do(_ context.Context, method, path string, _ any, out any) error {
	f.calls = append(f.calls, "LEGACY "+method+" "+path)
	if items, ok := f.legacy[path]; ok {
		b, err := json.Marshal(items)
		if err != nil {
			return err
		}
		return json.Unmarshal(b, out)
	}
	return apperr.New(apperr.Internal, "stable read used the legacy Network API")
}

func (f *officialReadAPI) SitePath(parts ...string) string {
	return "/proxy/network/api/s/default/" + strings.Join(parts, "/")
}

func (f *officialReadAPI) IntegrationSitePath(_ context.Context, parts ...string) (string, error) {
	return client.OfficialPath(append([]string{"sites", "site-uuid"}, parts...)...), nil
}

func (f *officialReadAPI) FetchOfficialObjects(_ context.Context, path string) ([]map[string]any, error) {
	f.calls = append(f.calls, "OFFICIAL LIST "+path)
	if err := f.errs[path]; err != nil {
		return nil, err
	}
	items := f.collections[path]
	return append([]map[string]any(nil), items...), nil
}

func (f *officialReadAPI) DoOfficial(_ context.Context, method, path string, _ any, out any) error {
	f.calls = append(f.calls, "OFFICIAL "+method+" "+path)
	if err := f.errs[path]; err != nil {
		return err
	}
	item, ok := f.details[path]
	if !ok {
		return apperr.Newf(apperr.NotFound, "official fixture missing for %s", path)
	}
	b, err := json.Marshal(item)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

func officialFixtureData(t *testing.T, name string) []map[string]any {
	t.Helper()
	var page struct {
		Data []map[string]any `json:"data"`
	}
	readOfficialFixture(t, name, &page)
	return page.Data
}

func officialFixtureObject(t *testing.T, name string) map[string]any {
	t.Helper()
	var object map[string]any
	readOfficialFixture(t, name, &object)
	return object
}

func readOfficialFixture(t *testing.T, name string, out any) {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	path := filepath.Join(filepath.Dir(file), "..", "client", "fixtures", "official", name)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, out); err != nil {
		t.Fatal(err)
	}
}

func newOfficialReadAPI(t *testing.T) *officialReadAPI {
	t.Helper()
	sitePath := client.OfficialPath("sites", "site-uuid")
	return &officialReadAPI{
		collections: map[string][]map[string]any{
			client.OfficialPath("sites"):    officialFixtureData(t, "sites.json"),
			sitePath + "/devices":           officialFixtureData(t, "devices.json"),
			sitePath + "/clients":           officialFixtureData(t, "clients.json"),
			sitePath + "/networks":          officialFixtureData(t, "networks.json"),
			sitePath + "/wifi/broadcasts":   officialFixtureData(t, "wifi-broadcasts.json"),
			sitePath + "/firewall/policies": officialFixtureData(t, "firewall-policies.json"),
			sitePath + "/dns/policies":      officialFixtureData(t, "dns-policies-all-types.json"),
		},
		details: map[string]map[string]any{
			sitePath + "/devices/device-gateway": officialFixtureObject(t, "device-gateway.json"),
			sitePath + "/devices/device-switch":  officialFixtureObject(t, "device-switch.json"),
			sitePath + "/networks/network-lan":   officialFixtureObject(t, "network-lan.json"),
			sitePath + "/networks/network-iot":   officialFixtureObject(t, "network-iot.json"),
		},
		errs:   map[string]error{},
		legacy: map[string][]map[string]any{},
	}
}

func TestStableReadServicesUseOfficialNetworkAPI(t *testing.T) {
	ctx := context.Background()

	t.Run("sites", func(t *testing.T) {
		api := newOfficialReadAPI(t)
		items, err := domain.NewSiteService(api).List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 2 || items[0].ID != "site-uuid" || items[0].Name != "Default" {
			t.Fatalf("sites = %+v", items)
		}
		assertOnlyOfficialCalls(t, api.calls, "OFFICIAL LIST /proxy/network/integration/v1/sites")
	})

	t.Run("devices", func(t *testing.T) {
		api := newOfficialReadAPI(t)
		items, err := domain.NewDeviceService(api).List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 2 || items[0].Type != "gateway" || items[0].State != "connected" || !items[0].Adopted {
			t.Fatalf("devices = %+v", items)
		}
		if items[1].Version != "7.1.26" || items[1].Uplink != "device-gateway" {
			t.Fatalf("switch detail was not preserved: %+v", items[1])
		}
		assertOnlyOfficialCalls(t, api.calls, "OFFICIAL LIST /proxy/network/integration/v1/sites/site-uuid/devices")
	})

	t.Run("clients", func(t *testing.T) {
		api := newOfficialReadAPI(t)
		items, err := domain.NewClientService(api).List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 2 || items[0].Name != "Laptop" || items[0].MAC != "11:22:33:44:55:01" || items[0].IsWired {
			t.Fatalf("clients = %+v", items)
		}
		if !items[1].IsWired || items[1].LastSeen != "2026-08-07T12:01:00Z" {
			t.Fatalf("wired client = %+v", items[1])
		}
		assertOnlyOfficialCalls(t, api.calls, "OFFICIAL LIST /proxy/network/integration/v1/sites/site-uuid/clients")
	})

	t.Run("networks and resolvers", func(t *testing.T) {
		api := newOfficialReadAPI(t)
		svc := domain.NewNetworkService(api)
		items, err := svc.List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 2 || items[0].VLAN == nil || *items[0].VLAN != 1 || items[0].Subnet != "192.0.2.1/24" {
			t.Fatalf("networks = %+v", items)
		}
		if !items[0].DHCPEnabled || items[0].DomainName != "example.test" || items[0].Purpose != "gateway" {
			t.Fatalf("network detail was not preserved: %+v", items[0])
		}
		resolvers, err := domain.NewDNSService(api).ListResolvers(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(resolvers) != 2 || len(resolvers[0].DNS) != 2 || resolvers[0].DNS[1] != "2001:db8::53" {
			t.Fatalf("resolvers = %+v", resolvers)
		}
		assertOnlyOfficialCalls(t, api.calls, "OFFICIAL LIST /proxy/network/integration/v1/sites/site-uuid/networks")
	})

	t.Run("wifi broadcasts", func(t *testing.T) {
		api := newOfficialReadAPI(t)
		items, err := domain.NewWlanService(api).List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 2 || items[0].Security != "wpa2_personal" || items[0].NetworkID != "network-lan" || items[0].Band != "both" {
			t.Fatalf("wifi broadcasts = %+v", items)
		}
		if !items[1].Guest || items[1].Band != "5g" {
			t.Fatalf("guest wifi = %+v", items[1])
		}
		assertOnlyOfficialCalls(t, api.calls, "OFFICIAL LIST /proxy/network/integration/v1/sites/site-uuid/wifi/broadcasts")
	})

	t.Run("port inventory", func(t *testing.T) {
		api := newOfficialReadAPI(t)
		items, err := domain.NewPortService(api).List(ctx, "Core Switch")
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 2 || items[0].PortIdx != 1 || items[0].Media != "RJ45" || items[0].POE != "off" {
			t.Fatalf("ports = %+v", items)
		}
		if items[1].PortIdx != 24 || items[1].Speed != "10000" || items[1].Media != "SFPPLUS" {
			t.Fatalf("uplink port = %+v", items[1])
		}
		assertOnlyOfficialCalls(t, api.calls, "OFFICIAL LIST /proxy/network/integration/v1/sites/site-uuid/devices")
	})

	t.Run("firewall policies", func(t *testing.T) {
		api := newOfficialReadAPI(t)
		items, err := domain.NewFirewallService(api).List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 1 || items[0].ID != "policy-allow-dns" || items[0].Action != "accept" || items[0].Index != 100 {
			t.Fatalf("firewall policies = %+v", items)
		}
		encoded, err := json.Marshal(items[0])
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(encoded), `"description":"Permit DNS from internal clients"`) {
			t.Fatalf("firewall description was lost: %s", encoded)
		}
		assertOnlyOfficialCalls(t, api.calls, "OFFICIAL LIST /proxy/network/integration/v1/sites/site-uuid/firewall/policies")
	})

	t.Run("health", func(t *testing.T) {
		api := newOfficialReadAPI(t)
		h, err := domain.NewSystemService(api).Health(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if h.Status != "degraded" || h.DeviceTotal != 2 || h.DeviceConnected != 1 || h.ClientTotal != 2 {
			t.Fatalf("health = %+v", h)
		}
		if len(h.Subsystems) != 0 {
			t.Fatalf("official health must not probe legacy subsystem endpoint: %+v", h.Subsystems)
		}
		assertOnlyOfficialCalls(t, api.calls,
			"OFFICIAL LIST /proxy/network/integration/v1/sites/site-uuid/devices",
			"OFFICIAL LIST /proxy/network/integration/v1/sites/site-uuid/clients")
	})
}

func TestStableOfficialReadsPreserveEmptyCollectionsAndErrors(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name string
		path string
		read func(*officialReadAPI) (any, error)
	}{
		{name: "sites", path: client.OfficialPath("sites"), read: func(api *officialReadAPI) (any, error) { return domain.NewSiteService(api).List(ctx) }},
		{name: "devices", path: client.OfficialPath("sites", "site-uuid", "devices"), read: func(api *officialReadAPI) (any, error) { return domain.NewDeviceService(api).List(ctx) }},
		{name: "clients", path: client.OfficialPath("sites", "site-uuid", "clients"), read: func(api *officialReadAPI) (any, error) { return domain.NewClientService(api).List(ctx) }},
		{name: "networks", path: client.OfficialPath("sites", "site-uuid", "networks"), read: func(api *officialReadAPI) (any, error) { return domain.NewNetworkService(api).List(ctx) }},
		{name: "wifi", path: client.OfficialPath("sites", "site-uuid", "wifi", "broadcasts"), read: func(api *officialReadAPI) (any, error) { return domain.NewWlanService(api).List(ctx) }},
		{name: "ports", path: client.OfficialPath("sites", "site-uuid", "devices"), read: func(api *officialReadAPI) (any, error) { return domain.NewPortService(api).List(ctx, "") }},
		{name: "firewall", path: client.OfficialPath("sites", "site-uuid", "firewall", "policies"), read: func(api *officialReadAPI) (any, error) { return domain.NewFirewallService(api).List(ctx) }},
		{name: "dns", path: client.OfficialPath("sites", "site-uuid", "dns", "policies"), read: func(api *officialReadAPI) (any, error) { return domain.NewDNSService(api).List(ctx) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := newOfficialReadAPI(t)
			api.collections[tt.path] = []map[string]any{}
			items, err := tt.read(api)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(items)
			if err != nil {
				t.Fatal(err)
			}
			if string(encoded) != "[]" {
				t.Fatalf("empty %s JSON = %s, want []", tt.name, encoded)
			}
		})
	}

	api := newOfficialReadAPI(t)
	path := client.OfficialPath("sites", "site-uuid", "clients")
	permission := apperr.New(apperr.PermissionDenied, "official clients are forbidden")
	api.errs[path] = permission
	_, err := domain.NewClientService(api).List(ctx)
	if !apperr.Is(err, apperr.PermissionDenied) {
		t.Fatalf("permission error = %v", err)
	}
}

type collectionOnlyPortAPI struct {
	devices []map[string]any
}

func (f *collectionOnlyPortAPI) Do(context.Context, string, string, any, any) error {
	return apperr.New(apperr.Internal, "unexpected legacy call")
}
func (f *collectionOnlyPortAPI) SitePath(...string) string { return "" }
func (f *collectionOnlyPortAPI) IntegrationSitePath(_ context.Context, parts ...string) (string, error) {
	return client.OfficialPath(append([]string{"sites", "site-uuid"}, parts...)...), nil
}
func (f *collectionOnlyPortAPI) FetchOfficialObjects(context.Context, string) ([]map[string]any, error) {
	return f.devices, nil
}

func TestOfficialPortInventoryRejectsTransportWithoutDetailSupport(t *testing.T) {
	api := &collectionOnlyPortAPI{devices: []map[string]any{{"id": "device-1", "name": "Switch"}}}
	_, err := domain.NewPortService(api).List(context.Background(), "")
	if !apperr.Is(err, apperr.Internal) {
		t.Fatalf("missing official detail support error = %v", err)
	}
}

func TestOfficialReadGetRejectsAmbiguousNames(t *testing.T) {
	api := newOfficialReadAPI(t)
	path := client.OfficialPath("sites", "site-uuid", "clients")
	api.collections[path] = []map[string]any{
		{"id": "client-1", "name": "Duplicate", "macAddress": "00:00:00:00:00:01", "type": "WIRED"},
		{"id": "client-2", "name": "Duplicate", "macAddress": "00:00:00:00:00:02", "type": "WIRELESS"},
	}
	_, err := domain.NewClientService(api).Get(context.Background(), "Duplicate")
	if !apperr.Is(err, apperr.AmbiguousID) {
		t.Fatalf("ambiguous official client error = %v", err)
	}
}

func TestLegacyMutationPreparationDoesNotReuseOfficialReadIDs(t *testing.T) {
	ctx := context.Background()
	api := newOfficialReadAPI(t)
	api.legacy[api.SitePath(client.PathStatDevice)] = []map[string]any{{"_id": "legacy-device", "name": "Legacy Device", "mac": "00:11:22:33:44:55", "state": 1}}
	api.legacy[api.SitePath(client.PathStatSta)] = []map[string]any{{"_id": "legacy-client", "name": "Legacy Client", "mac": "00:11:22:33:44:56"}}
	api.legacy[api.SitePath(client.PathRestNetwork)] = []map[string]any{{"_id": "legacy-network", "name": "Legacy Network", "purpose": "corporate"}}
	api.legacy[api.SitePath(client.PathRestWlan)] = []map[string]any{{"_id": "legacy-wlan", "name": "Legacy WiFi", "security": "wpapsk"}}
	api.legacy[api.SitePath(client.PathRestFirewall)] = []map[string]any{{"_id": "legacy-firewall", "name": "Legacy Firewall", "action": "accept", "ruleset": "LAN_IN"}}

	if p, item, err := domain.NewDeviceService(api).Rename(ctx, "legacy-device", "Renamed"); err != nil || item.ID != "legacy-device" || p.Changes[0].ID != "legacy-device" {
		t.Fatalf("device mutation target: item=%+v plan=%+v err=%v", item, p, err)
	}
	if p, item, err := domain.NewClientService(api).Block(ctx, "legacy-client"); err != nil || item.ID != "legacy-client" || p.Changes[0].ID != "legacy-client" {
		t.Fatalf("client mutation target: item=%+v plan=%+v err=%v", item, p, err)
	}
	if p, item, err := domain.NewNetworkService(api).Update(ctx, "legacy-network", domain.NetworkInput{Name: "Renamed"}); err != nil || item.ID != "legacy-network" || p.Changes[0].ID != "legacy-network" {
		t.Fatalf("network mutation target: item=%+v plan=%+v err=%v", item, p, err)
	}
	if p, item, err := domain.NewWlanService(api).Update(ctx, "legacy-wlan", domain.WlanInput{Name: "Renamed"}); err != nil || item.ID != "legacy-wlan" || p.Changes[0].ID != "legacy-wlan" {
		t.Fatalf("WLAN mutation target: item=%+v plan=%+v err=%v", item, p, err)
	}
	if p, item, err := domain.NewFirewallService(api).Update(ctx, "legacy-firewall", domain.FirewallInput{Name: "Renamed"}); err != nil || item.ID != "legacy-firewall" || p.Changes[0].ID != "legacy-firewall" {
		t.Fatalf("firewall mutation target: item=%+v plan=%+v err=%v", item, p, err)
	}
}

func assertOnlyOfficialCalls(t *testing.T, calls []string, required ...string) {
	t.Helper()
	for _, call := range calls {
		if strings.HasPrefix(call, "LEGACY ") {
			t.Fatalf("legacy call made: %s; all calls=%v", call, calls)
		}
	}
	for _, want := range required {
		found := false
		for _, call := range calls {
			if call == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing call %q; calls=%v", want, calls)
		}
	}
}

var _ interface {
	Do(context.Context, string, string, any, any) error
	SitePath(...string) string
} = (*officialReadAPI)(nil)

var _ interface {
	DoOfficial(context.Context, string, string, any, any) error
	FetchOfficialObjects(context.Context, string) ([]map[string]any, error)
	IntegrationSitePath(context.Context, ...string) (string, error)
} = (*officialReadAPI)(nil)
