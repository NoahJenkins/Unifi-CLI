package domain_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
	"github.com/noahjenkins/unifi-cli/internal/client"
	"github.com/noahjenkins/unifi-cli/internal/domain"
)

type officialReadAPI struct {
	mu          sync.Mutex
	collections map[string][]map[string]any
	details     map[string]map[string]any
	legacy      map[string][]map[string]any
	errs        map[string]error
	calls       []string
	requests    []officialTestRequest
	detailDelay time.Duration
	active      int
	maxActive   int
}

type officialTestRequest struct {
	method string
	path   string
	body   any
}

const (
	officialSiteID           = "11111111-1111-4111-8111-111111111111"
	officialGatewayID        = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa1"
	officialSwitchID         = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa2"
	officialWirelessID       = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbb1"
	officialLANID            = "cccccccc-cccc-4ccc-8ccc-ccccccccccc1"
	officialMainWifiID       = "dddddddd-dddd-4ddd-8ddd-ddddddddddd1"
	officialFirewallPolicyID = "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeee1"
)

func (f *officialReadAPI) Do(_ context.Context, method, path string, in any, out any) error {
	f.mu.Lock()
	f.calls = append(f.calls, "LEGACY "+method+" "+path)
	f.requests = append(f.requests, officialTestRequest{method: method, path: path, body: cloneTestValue(in)})
	f.mu.Unlock()
	if method != "GET" {
		body, _ := in.(map[string]any)
		if method == http.MethodPut {
			if items := f.legacy[path]; len(items) > 0 {
				for key, value := range body {
					items[0][key] = value
				}
			}
			separator := strings.LastIndex(path, "/")
			id := path[separator+1:]
			for _, item := range f.legacy[path[:separator]] {
				if strFieldTest(item, "_id", "id") == id {
					for key, value := range body {
						item[key] = value
					}
				}
			}
			for _, item := range f.legacy[f.SitePath(client.PathStatDevice)] {
				if strFieldTest(item, "_id", "id") == id {
					for key, value := range body {
						item[key] = value
					}
				}
			}
		}
		if method == http.MethodPost {
			cmd, _ := body["cmd"].(string)
			mac, _ := body["mac"].(string)
			if cmd == "block-sta" || cmd == "unblock-sta" {
				for _, item := range f.legacy[f.SitePath(client.PathStatSta)] {
					if strFieldTest(item, "mac") == mac {
						item["blocked"] = cmd == "block-sta"
					}
				}
			}
		}
		return nil
	}
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
	return client.OfficialPath(append([]string{"sites", officialSiteID}, parts...)...), nil
}

func (f *officialReadAPI) FetchOfficialObjects(_ context.Context, path string) ([]map[string]any, error) {
	f.mu.Lock()
	f.calls = append(f.calls, "OFFICIAL LIST "+path)
	f.mu.Unlock()
	if err := f.errs[path]; err != nil {
		return nil, err
	}
	items := f.collections[path]
	return append([]map[string]any(nil), items...), nil
}

func (f *officialReadAPI) DoOfficial(ctx context.Context, method, path string, in any, out any) error {
	f.mu.Lock()
	f.calls = append(f.calls, "OFFICIAL "+method+" "+path)
	f.active++
	if f.active > f.maxActive {
		f.maxActive = f.active
	}
	delay := f.detailDelay
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.active--
		f.mu.Unlock()
	}()
	if delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	if err := f.errs[path]; err != nil {
		return err
	}
	if method == http.MethodPut {
		body, _ := in.(map[string]any)
		updated := cloneTestMap(body)
		if existing := f.details[path]; existing != nil {
			for _, key := range []string{"id", "default", "metadata"} {
				if value, ok := existing[key]; ok {
					updated[key] = cloneTestValue(value)
				}
			}
		}
		f.details[path] = updated
		return decodeInto(updated, out)
	}
	if method == http.MethodDelete {
		delete(f.details, path)
		return nil
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

func cloneTestValue(value any) any {
	if value == nil {
		return nil
	}
	b, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	var cloned any
	if err := json.Unmarshal(b, &cloned); err != nil {
		panic(err)
	}
	return cloned
}

func (f *officialReadAPI) maxDetailConcurrency() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.maxActive
}

func (f *officialReadAPI) activeDetailCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.active
}

func (f *officialReadAPI) legacyRequests(method string) []officialTestRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]officialTestRequest, 0, len(f.requests))
	for _, request := range f.requests {
		if request.method == method {
			out = append(out, request)
		}
	}
	return out
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
	sitePath := client.OfficialPath("sites", officialSiteID)
	wifiItems := officialFixtureData(t, "wifi-broadcasts.json")
	details := map[string]map[string]any{
		sitePath + "/devices/" + officialGatewayID:                  officialFixtureObject(t, "device-gateway.json"),
		sitePath + "/devices/" + officialSwitchID:                   officialFixtureObject(t, "device-switch.json"),
		sitePath + "/networks/" + officialLANID:                     officialFixtureObject(t, "network-lan.json"),
		sitePath + "/networks/cccccccc-cccc-4ccc-8ccc-ccccccccccc2": officialFixtureObject(t, "network-iot.json"),
	}
	for _, item := range wifiItems {
		detail := cloneTestMap(item)
		for key, value := range map[string]any{
			"clientIsolationEnabled": false, "hideName": false, "multicastToUnicastConversionEnabled": false,
			"uapsdEnabled": true, "advertiseDeviceName": false, "arpProxyEnabled": false, "bssTransitionEnabled": true,
		} {
			detail[key] = value
		}
		details[sitePath+"/wifi/broadcasts/"+strFieldTest(item, "id")] = detail
	}
	return &officialReadAPI{
		collections: map[string][]map[string]any{
			client.OfficialPath("sites"):    officialFixtureData(t, "sites.json"),
			sitePath + "/devices":           officialFixtureData(t, "devices.json"),
			sitePath + "/clients":           officialFixtureData(t, "clients.json"),
			sitePath + "/networks":          officialFixtureData(t, "networks.json"),
			sitePath + "/wifi/broadcasts":   wifiItems,
			sitePath + "/firewall/policies": officialFixtureData(t, "firewall-policies.json"),
			sitePath + "/dns/policies":      officialFixtureData(t, "dns-policies-all-types.json"),
		},
		details: details,
		errs:    map[string]error{},
		legacy:  map[string][]map[string]any{},
	}
}

func TestStableReadServicesUseOfficialNetworkAPI(t *testing.T) {
	ctx := context.Background()
	sitePath := client.OfficialPath("sites", officialSiteID)

	t.Run("sites", func(t *testing.T) {
		api := newOfficialReadAPI(t)
		items, err := domain.NewSiteService(api).List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 2 || items[0].ID != officialSiteID || items[0].Name != "Default" {
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
		if items[1].Version != "7.1.26" || items[1].Uplink != "" {
			t.Fatalf("device overview was not preserved honestly: %+v", items[1])
		}
		assertExactCalls(t, api.calls, "OFFICIAL LIST "+sitePath+"/devices")

		item, err := domain.NewDeviceService(api).Get(ctx, officialGatewayID)
		if err != nil {
			t.Fatal(err)
		}
		if item.Type != "gateway" {
			t.Fatalf("gateway classification was lost during detail enrichment: %+v", item)
		}
		assertCallCount(t, api.calls, "OFFICIAL GET "+sitePath+"/devices/"+officialGatewayID, 1)
		assertCallCount(t, api.calls, "OFFICIAL GET "+sitePath+"/devices/"+officialSwitchID, 0)
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
		if !items[1].IsWired || items[1].LastSeen != "" {
			t.Fatalf("wired client = %+v", items[1])
		}
		assertOnlyOfficialCalls(t, api.calls, "OFFICIAL LIST "+sitePath+"/clients")
	})

	t.Run("networks", func(t *testing.T) {
		api := newOfficialReadAPI(t)
		svc := domain.NewNetworkService(api)
		items, err := svc.List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 2 || items[0].VLAN == nil || *items[0].VLAN != 1 {
			t.Fatalf("networks = %+v", items)
		}
		if items[0].Subnet != "" || items[0].DHCPEnabled || items[0].DomainName != "" || items[0].Purpose != "gateway" {
			t.Fatalf("network overview invented legacy/detail fields: %+v", items[0])
		}
		assertExactCalls(t, api.calls, "OFFICIAL LIST "+sitePath+"/networks")

		item, err := svc.Get(ctx, officialLANID)
		if err != nil {
			t.Fatal(err)
		}
		if item.Subnet != "192.0.2.1/24" || !item.DHCPEnabled || item.DomainName != "example.test" || item.Purpose != "gateway" {
			t.Fatalf("target network detail = %+v", item)
		}
		assertCallCount(t, api.calls, "OFFICIAL GET "+sitePath+"/networks/"+officialLANID, 1)
		assertCallCount(t, api.calls, "OFFICIAL GET "+sitePath+"/networks/cccccccc-cccc-4ccc-8ccc-ccccccccccc2", 0)
	})

	t.Run("wifi broadcasts", func(t *testing.T) {
		api := newOfficialReadAPI(t)
		items, err := domain.NewWlanService(api).List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 2 || items[0].Security != "wpa2_personal" || items[0].NetworkID != officialLANID || items[0].Band != "2.4+6" {
			t.Fatalf("wifi broadcasts = %+v", items)
		}
		if !items[1].Guest || items[1].Band != "5+6" {
			t.Fatalf("guest wifi = %+v", items[1])
		}
		assertOnlyOfficialCalls(t, api.calls, "OFFICIAL LIST "+sitePath+"/wifi/broadcasts")
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
		encoded, err := json.Marshal(items[0])
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), `"enabled"`) {
			t.Fatalf("official port invented administrative enabled state: %s", encoded)
		}
		assertCallCount(t, api.calls, "OFFICIAL GET "+sitePath+"/devices/"+officialSwitchID, 1)
		assertCallCount(t, api.calls, "OFFICIAL GET "+sitePath+"/devices/"+officialGatewayID, 0)
	})

	t.Run("firewall policies", func(t *testing.T) {
		api := newOfficialReadAPI(t)
		items, err := domain.NewFirewallService(api).List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 1 || items[0].ID != officialFirewallPolicyID || items[0].Action != "allow" || items[0].Index != 100 {
			t.Fatalf("firewall policies = %+v", items)
		}
		if items[0].Protocol != "ipv4:udp" {
			t.Fatalf("official protocol scope was not normalized honestly: %+v", items[0])
		}
		assertOnlyOfficialCalls(t, api.calls, "OFFICIAL LIST "+sitePath+"/firewall/policies")
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
			"OFFICIAL LIST "+sitePath+"/devices",
			"OFFICIAL LIST "+sitePath+"/clients")
		for _, call := range api.calls {
			if strings.HasPrefix(call, "OFFICIAL GET ") {
				t.Fatalf("health made detail call %q", call)
			}
		}
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
		{name: "devices", path: client.OfficialPath("sites", officialSiteID, "devices"), read: func(api *officialReadAPI) (any, error) { return domain.NewDeviceService(api).List(ctx) }},
		{name: "clients", path: client.OfficialPath("sites", officialSiteID, "clients"), read: func(api *officialReadAPI) (any, error) { return domain.NewClientService(api).List(ctx) }},
		{name: "networks", path: client.OfficialPath("sites", officialSiteID, "networks"), read: func(api *officialReadAPI) (any, error) { return domain.NewNetworkService(api).List(ctx) }},
		{name: "wifi", path: client.OfficialPath("sites", officialSiteID, "wifi", "broadcasts"), read: func(api *officialReadAPI) (any, error) { return domain.NewWlanService(api).List(ctx) }},
		{name: "ports", path: client.OfficialPath("sites", officialSiteID, "devices"), read: func(api *officialReadAPI) (any, error) { return domain.NewPortService(api).List(ctx, "") }},
		{name: "firewall", path: client.OfficialPath("sites", officialSiteID, "firewall", "policies"), read: func(api *officialReadAPI) (any, error) { return domain.NewFirewallService(api).List(ctx) }},
		{name: "dns", path: client.OfficialPath("sites", officialSiteID, "dns", "policies"), read: func(api *officialReadAPI) (any, error) { return domain.NewDNSService(api).List(ctx) }},
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
	path := client.OfficialPath("sites", officialSiteID, "clients")
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
	return client.OfficialPath(append([]string{"sites", officialSiteID}, parts...)...), nil
}
func (f *collectionOnlyPortAPI) FetchOfficialObjects(context.Context, string) ([]map[string]any, error) {
	return f.devices, nil
}

func TestOfficialPortInventoryRejectsTransportWithoutDetailSupport(t *testing.T) {
	api := &collectionOnlyPortAPI{devices: []map[string]any{{"id": "device-1", "name": "Switch"}}}
	_, err := domain.NewPortService(api).List(context.Background(), "Switch")
	if !apperr.Is(err, apperr.Internal) {
		t.Fatalf("missing official detail support error = %v", err)
	}
}

func TestOfficialReadGetRejectsAmbiguousNames(t *testing.T) {
	api := newOfficialReadAPI(t)
	path := client.OfficialPath("sites", officialSiteID, "clients")
	api.collections[path] = []map[string]any{
		{"id": "client-1", "name": "Duplicate", "macAddress": "00:00:00:00:00:01", "type": "WIRED"},
		{"id": "client-2", "name": "Duplicate", "macAddress": "00:00:00:00:00:02", "type": "WIRELESS"},
	}
	_, err := domain.NewClientService(api).Get(context.Background(), "Duplicate")
	if !apperr.Is(err, apperr.AmbiguousID) {
		t.Fatalf("ambiguous official client error = %v", err)
	}
}

func TestTargetGetDoesNotFetchOrFailOnUnrelatedOfficialDetails(t *testing.T) {
	ctx := context.Background()
	api := newOfficialReadAPI(t)
	sitePath := client.OfficialPath("sites", officialSiteID)
	api.errs[sitePath+"/devices/"+officialSwitchID] = apperr.New(apperr.PermissionDenied, "unrelated switch detail forbidden")
	device, err := domain.NewDeviceService(api).Get(ctx, officialGatewayID)
	if err != nil {
		t.Fatal(err)
	}
	if device.ID != officialGatewayID || device.Type != "gateway" {
		t.Fatalf("device = %+v", device)
	}
	assertCallCount(t, api.calls, "OFFICIAL GET "+sitePath+"/devices/"+officialGatewayID, 1)
	assertCallCount(t, api.calls, "OFFICIAL GET "+sitePath+"/devices/"+officialSwitchID, 0)

	api = newOfficialReadAPI(t)
	iotPath := sitePath + "/networks/cccccccc-cccc-4ccc-8ccc-ccccccccccc2"
	api.errs[iotPath] = apperr.New(apperr.NotFound, "unrelated IoT detail missing")
	network, err := domain.NewNetworkService(api).Get(ctx, officialLANID)
	if err != nil {
		t.Fatal(err)
	}
	if network.ID != officialLANID || network.Subnet != "192.0.2.1/24" {
		t.Fatalf("network = %+v", network)
	}
	assertCallCount(t, api.calls, "OFFICIAL GET "+sitePath+"/networks/"+officialLANID, 1)
	assertCallCount(t, api.calls, "OFFICIAL GET "+iotPath, 0)
}

func TestOfficialPortListFansOutBoundedAndDeterministically(t *testing.T) {
	api := newOfficialReadAPI(t)
	configureOfficialPortFanout(t, api, 9)
	api.detailDelay = 20 * time.Millisecond

	ports, err := domain.NewPortService(api).List(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(ports) != 9 {
		t.Fatalf("ports = %d, want 9", len(ports))
	}
	for i, port := range ports {
		wantName := fmt.Sprintf("Switch %02d", i+1)
		if port.DeviceName != wantName || port.PortIdx != i+1 {
			t.Fatalf("ports[%d] = %+v, want device %q port %d", i, port, wantName, i+1)
		}
	}
	if got := api.maxDetailConcurrency(); got < 2 || got > 4 {
		t.Fatalf("max detail concurrency = %d, want 2..4", got)
	}
	if got := api.activeDetailCalls(); got != 0 {
		t.Fatalf("active detail calls after return = %d", got)
	}
	for _, call := range api.calls {
		if strings.HasPrefix(call, "LEGACY ") {
			t.Fatalf("official port list made legacy call %q", call)
		}
	}
}

func TestOfficialPortListFailsWholeResultOnAnyDetailError(t *testing.T) {
	api := newOfficialReadAPI(t)
	configureOfficialPortFanout(t, api, 6)
	failedID := fanoutTestUUID(3)
	failedPath := client.OfficialPath("sites", officialSiteID, "devices", failedID)
	api.errs[failedPath] = apperr.New(apperr.PermissionDenied, "device detail forbidden")

	ports, err := domain.NewPortService(api).List(context.Background(), "")
	if !apperr.Is(err, apperr.PermissionDenied) {
		t.Fatalf("detail error = %v", err)
	}
	if ports != nil {
		t.Fatalf("partial ports returned on detail error: %+v", ports)
	}
	if got := api.activeDetailCalls(); got != 0 {
		t.Fatalf("active detail calls after error = %d", got)
	}
}

func TestOfficialPortListFetchesNoDetailsForEmptyOrNonPortDevices(t *testing.T) {
	api := newOfficialReadAPI(t)
	path := client.OfficialPath("sites", officialSiteID, "devices")
	api.collections[path] = []map[string]any{}
	ports, err := domain.NewPortService(api).List(context.Background(), "")
	if err != nil || len(ports) != 0 {
		t.Fatalf("empty ports = %+v, err=%v", ports, err)
	}

	api = newOfficialReadAPI(t)
	overview := cloneTestMap(officialFixtureData(t, "devices.json")[0])
	overview["interfaces"] = []any{"radios"}
	api.collections[path] = []map[string]any{overview}
	ports, err = domain.NewPortService(api).List(context.Background(), "")
	if err != nil || len(ports) != 0 {
		t.Fatalf("non-port ports = %+v, err=%v", ports, err)
	}
	for _, call := range api.calls {
		if strings.HasPrefix(call, "OFFICIAL GET ") {
			t.Fatalf("non-port inventory made detail call %q", call)
		}
	}
}

func TestOfficialDNSResolverListFansOutBoundedAndDeterministically(t *testing.T) {
	api := newOfficialReadAPI(t)
	configureOfficialNetworkFanout(t, api, 9)
	api.detailDelay = 20 * time.Millisecond

	resolvers, err := domain.NewDNSService(api).ListResolvers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(resolvers) != 9 {
		t.Fatalf("resolvers = %d, want 9", len(resolvers))
	}
	for i, resolver := range resolvers {
		wantName := fmt.Sprintf("Network %02d", i+1)
		wantDNS := fmt.Sprintf("192.0.2.%d", i+1)
		if resolver.NetworkName != wantName || len(resolver.DNS) != 1 || resolver.DNS[0] != wantDNS {
			t.Fatalf("resolvers[%d] = %+v, want name=%q dns=%q", i, resolver, wantName, wantDNS)
		}
	}
	if got := api.maxDetailConcurrency(); got < 2 || got > 4 {
		t.Fatalf("max detail concurrency = %d, want 2..4", got)
	}
	if got := api.activeDetailCalls(); got != 0 {
		t.Fatalf("active detail calls after return = %d", got)
	}
	for _, call := range api.calls {
		if strings.HasPrefix(call, "LEGACY ") {
			t.Fatalf("official resolver list made legacy call %q", call)
		}
	}
}

func TestOfficialDNSResolverListFailsWholeResultOnAnyDetailError(t *testing.T) {
	api := newOfficialReadAPI(t)
	configureOfficialNetworkFanout(t, api, 6)
	failedPath := client.OfficialPath("sites", officialSiteID, "networks", fanoutTestUUID(4))
	api.errs[failedPath] = apperr.New(apperr.PermissionDenied, "network detail forbidden")

	resolvers, err := domain.NewDNSService(api).ListResolvers(context.Background())
	if !apperr.Is(err, apperr.PermissionDenied) {
		t.Fatalf("detail error = %v", err)
	}
	if resolvers != nil {
		t.Fatalf("partial resolvers returned on detail error: %+v", resolvers)
	}
	if got := api.activeDetailCalls(); got != 0 {
		t.Fatalf("active detail calls after error = %d", got)
	}
}

func TestOfficialDNSResolverListEmptyMakesNoDetailOrLegacyCalls(t *testing.T) {
	api := newOfficialReadAPI(t)
	path := client.OfficialPath("sites", officialSiteID, "networks")
	api.collections[path] = []map[string]any{}

	resolvers, err := domain.NewDNSService(api).ListResolvers(context.Background())
	if err != nil || len(resolvers) != 0 {
		t.Fatalf("empty resolvers = %+v, err=%v", resolvers, err)
	}
	assertExactCalls(t, api.calls, "OFFICIAL LIST "+path)
}

func TestOfficialDNSResolverReadIDTranslatesToLegacyMutation(t *testing.T) {
	ctx := context.Background()
	api := newOfficialReadAPI(t)
	legacyPath := api.SitePath(client.PathRestNetwork)
	api.legacy[legacyPath] = []map[string]any{{
		"_id": "legacy-network", "name": "LAN", "purpose": "corporate",
		"dhcpd_dns_1": "192.0.2.53", "dhcpd_dns_enabled": true,
	}}

	resolvers, err := domain.NewDNSService(api).ListResolvers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var read domain.DNSResolver
	for _, resolver := range resolvers {
		if resolver.NetworkName == "LAN" {
			read = resolver
			break
		}
	}
	if read.NetworkID != officialLANID {
		t.Fatalf("official resolver read = %+v", read)
	}

	servers := []string{"1.1.1.1", "8.8.8.8"}
	p, before, err := domain.NewDNSService(api).SetResolvers(ctx, read.NetworkID, servers)
	if err != nil {
		t.Fatal(err)
	}
	if before.NetworkID != "legacy-network" || p.Changes[0].ID != "legacy-network" {
		t.Fatalf("resolver plan target: before=%+v plan=%+v", before, p)
	}
	got, err := domain.NewDNSService(api).ApplySetResolvers(ctx, read.NetworkID, servers)
	if err != nil {
		t.Fatal(err)
	}
	if got.NetworkID != "legacy-network" {
		t.Fatalf("resolver apply target = %+v", got)
	}
	puts := api.legacyRequests(http.MethodPut)
	if len(puts) != 1 || puts[0].path != api.SitePath(client.PathRestNetwork, "legacy-network") {
		t.Fatalf("resolver PUTs = %+v", puts)
	}
	body, ok := puts[0].body.(map[string]any)
	if !ok || body["dhcpd_dns_1"] != "1.1.1.1" || body["dhcpd_dns_2"] != "8.8.8.8" {
		t.Fatalf("resolver PUT body = %#v", puts[0].body)
	}
	encoded, _ := json.Marshal(puts)
	if strings.Contains(string(encoded), officialLANID) {
		t.Fatalf("official network UUID reached legacy resolver request: %s", encoded)
	}
}

func TestOfficialFirewallProtocolUnionNormalization(t *testing.T) {
	tests := []struct {
		name  string
		scope map[string]any
		want  string
	}{
		{name: "named", scope: map[string]any{"ipVersion": "IPV4", "protocolFilter": map[string]any{"type": "NAMED_PROTOCOL", "protocol": map[string]any{"name": "udp"}, "matchOpposite": false}}, want: "ipv4:udp"},
		{name: "preset", scope: map[string]any{"ipVersion": "IPV4_AND_IPV6", "protocolFilter": map[string]any{"type": "PRESET", "preset": map[string]any{"name": "TCP_UDP"}}}, want: "ipv4_and_ipv6:tcp_udp"},
		{name: "number", scope: map[string]any{"ipVersion": "IPV6", "protocolFilter": map[string]any{"type": "PROTOCOL_NUMBER", "protocolNumber": float64(17), "matchOpposite": false}}, want: "ipv6:17"},
		{name: "all protocols", scope: map[string]any{"ipVersion": "IPV4"}, want: "ipv4"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := domain.NormalizeFirewallRule(map[string]any{"ipProtocolScope": tt.scope})
			if got.Protocol != tt.want {
				t.Fatalf("protocol = %q, want %q", got.Protocol, tt.want)
			}
		})
	}
}

func TestOfficialDetailFixturesMatchDocumentedFeatureShapes(t *testing.T) {
	gateway := officialFixtureObject(t, "device-gateway.json")
	features, ok := gateway["features"].(map[string]any)
	if !ok {
		t.Fatalf("gateway detail features = %#v", gateway["features"])
	}
	if _, invented := features["gateway"]; invented {
		t.Fatalf("official device detail fixture invented features.gateway: %#v", features)
	}
	switching, ok := features["switching"].(map[string]any)
	if !ok {
		t.Fatalf("switching feature = %#v", features["switching"])
	}
	if _, ok := switching["lags"].([]any); !ok {
		t.Fatalf("switching feature lacks required lags array: %#v", switching)
	}
}

func TestOfficialReadIDsTranslateToExactLegacyMutationTargets(t *testing.T) {
	ctx := context.Background()
	api := newOfficialReadAPI(t)
	api.legacy[api.SitePath(client.PathStatDevice)] = []map[string]any{
		{"_id": "legacy-device", "name": "Gateway", "mac": "aa:bb:cc:dd:ee:01", "state": 1},
		{"_id": "legacy-switch", "name": "Core Switch", "mac": "aa:bb:cc:dd:ee:02", "state": 1, "port_table": []any{map[string]any{"port_idx": 1, "enable": true}}},
	}
	api.legacy[api.SitePath(client.PathRestDevice, "legacy-switch")] = []map[string]any{{"_id": "legacy-switch", "port_overrides": []any{}}}
	api.legacy[api.SitePath(client.PathStatSta)] = []map[string]any{{"_id": "legacy-client", "name": "Laptop", "mac": "11:22:33:44:55:01"}}
	api.legacy[api.SitePath(client.PathRestNetwork)] = []map[string]any{{"_id": "legacy-network", "name": "LAN", "purpose": "corporate"}}
	api.legacy[api.SitePath(client.PathRestWlan)] = []map[string]any{{"_id": "legacy-wlan", "name": "Main", "security": "wpapsk"}}

	deviceRead, err := domain.NewDeviceService(api).Get(ctx, "Gateway")
	if err != nil {
		t.Fatal(err)
	}
	clientRead, err := domain.NewClientService(api).Get(ctx, "Laptop")
	if err != nil {
		t.Fatal(err)
	}
	networkRead, err := domain.NewNetworkService(api).Get(ctx, "LAN")
	if err != nil {
		t.Fatal(err)
	}
	wlanRead, err := domain.NewWlanService(api).Get(ctx, "Main")
	if err != nil {
		t.Fatal(err)
	}
	portRead, err := domain.NewPortService(api).Get(ctx, "Core Switch", 1)
	if err != nil {
		t.Fatal(err)
	}

	if p, item, err := domain.NewDeviceService(api).Rename(ctx, deviceRead.ID, "Renamed"); err != nil || item.ID != "legacy-device" || p.Changes[0].ID != "legacy-device" {
		t.Fatalf("device mutation target: item=%+v plan=%+v err=%v", item, p, err)
	}
	if item, err := domain.NewDeviceService(api).ApplyRename(ctx, deviceRead.ID, "Renamed"); err != nil || item.ID != "legacy-device" {
		t.Fatalf("device apply target: item=%+v err=%v", item, err)
	}
	if p, item, err := domain.NewClientService(api).Block(ctx, clientRead.ID); err != nil || item.ID != "legacy-client" || p.Changes[0].ID != "legacy-client" {
		t.Fatalf("client mutation target: item=%+v plan=%+v err=%v", item, p, err)
	}
	if item, err := domain.NewClientService(api).ApplyBlock(ctx, clientRead.ID); err != nil || item.ID != "legacy-client" {
		t.Fatalf("client apply target: item=%+v err=%v", item, err)
	}
	if p, item, err := domain.NewNetworkService(api).Update(ctx, networkRead.ID, domain.NetworkInput{Name: "Renamed"}); err != nil || item.ID != officialLANID || p.Changes[0].ID != officialLANID {
		t.Fatalf("network mutation target: item=%+v plan=%+v err=%v", item, p, err)
	}
	if item, err := domain.NewNetworkService(api).ApplyUpdate(ctx, networkRead.ID, domain.NetworkInput{Name: "Renamed"}); err != nil || item.ID != officialLANID {
		t.Fatalf("network apply target: item=%+v err=%v", item, err)
	}
	if p, item, err := domain.NewWlanService(api).Update(ctx, wlanRead.ID, domain.WlanInput{Name: "Renamed"}); err != nil || item.ID != officialMainWifiID || p.Changes[0].ID != officialMainWifiID {
		t.Fatalf("WLAN mutation target: item=%+v plan=%+v err=%v", item, p, err)
	}
	if item, err := domain.NewWlanService(api).ApplyUpdate(ctx, wlanRead.ID, domain.WlanInput{Name: "Renamed"}); err != nil || item.ID != officialMainWifiID {
		t.Fatalf("WLAN apply target: item=%+v err=%v", item, err)
	}
	if p, item, err := domain.NewPortService(api).Update(ctx, portRead.DeviceID, 1, domain.PortInput{Name: "Uplink"}); err != nil || item.DeviceID != "legacy-switch" || p.Changes[0].ID != "legacy-switch/1" {
		t.Fatalf("port mutation target: item=%+v plan=%+v err=%v", item, p, err)
	}
	if item, err := domain.NewPortService(api).ApplyUpdate(ctx, portRead.DeviceID, 1, domain.PortInput{Name: "Uplink"}); err != nil || item.DeviceID != "legacy-switch" {
		t.Fatalf("port apply target: item=%+v err=%v", item, err)
	}

	for _, call := range api.calls {
		if !strings.HasPrefix(call, "LEGACY ") {
			continue
		}
		for _, officialID := range []string{officialGatewayID, officialWirelessID, officialLANID, officialMainWifiID, officialSwitchID} {
			if strings.Contains(call, officialID) {
				t.Fatalf("official UUID reached legacy request: %s", call)
			}
		}
	}
	for _, want := range []string{
		"LEGACY PUT " + api.SitePath(client.PathRestDevice, "legacy-device"),
		"LEGACY POST " + api.SitePath(client.PathCmdStaMgr),
		"LEGACY PUT " + api.SitePath(client.PathRestDevice, "legacy-switch"),
	} {
		assertCallCount(t, api.calls, want, 1)
	}
	assertCallCount(t, api.calls, "OFFICIAL PUT "+client.OfficialPath("sites", officialSiteID, "networks", officialLANID), 1)
	assertCallCount(t, api.calls, "OFFICIAL PUT "+client.OfficialPath("sites", officialSiteID, "wifi", "broadcasts", officialMainWifiID), 1)
}

func TestOfficialMutationIdentityTranslationRejectsAmbiguousAndMissingLegacyMatches(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name       string
		legacyPath func(*officialReadAPI) string
		ambiguous  []map[string]any
		missing    []map[string]any
		mutate     func(*officialReadAPI) error
	}{
		{
			name:       "device",
			legacyPath: func(api *officialReadAPI) string { return api.SitePath(client.PathStatDevice) },
			ambiguous: []map[string]any{
				{"_id": "legacy-device-1", "name": "Gateway A", "mac": "aa:bb:cc:dd:ee:01", "state": 1},
				{"_id": "legacy-device-2", "name": "Gateway B", "mac": "aa:bb:cc:dd:ee:01", "state": 1},
			},
			missing: []map[string]any{{"_id": "other", "name": "Other", "mac": "00:00:00:00:00:01", "state": 1}},
			mutate: func(api *officialReadAPI) error {
				_, _, err := domain.NewDeviceService(api).Rename(ctx, officialGatewayID, "Renamed")
				return err
			},
		},
		{
			name:       "client",
			legacyPath: func(api *officialReadAPI) string { return api.SitePath(client.PathStatSta) },
			ambiguous: []map[string]any{
				{"_id": "legacy-client-1", "name": "Laptop A", "mac": "11:22:33:44:55:01"},
				{"_id": "legacy-client-2", "name": "Laptop B", "mac": "11:22:33:44:55:01"},
			},
			missing: []map[string]any{{"_id": "other", "name": "Other", "mac": "00:00:00:00:00:02"}},
			mutate: func(api *officialReadAPI) error {
				_, _, err := domain.NewClientService(api).Block(ctx, officialWirelessID)
				return err
			},
		},
		{
			name:       "dns resolver network",
			legacyPath: func(api *officialReadAPI) string { return api.SitePath(client.PathRestNetwork) },
			ambiguous: []map[string]any{
				{"_id": "legacy-resolver-1", "name": "LAN", "purpose": "corporate"},
				{"_id": "legacy-resolver-2", "name": "LAN", "purpose": "corporate"},
			},
			missing: []map[string]any{{"_id": "other", "name": "Other", "purpose": "corporate"}},
			mutate: func(api *officialReadAPI) error {
				_, _, err := domain.NewDNSService(api).SetResolvers(ctx, officialLANID, []string{"1.1.1.1"})
				return err
			},
		},
		{
			name:       "port device",
			legacyPath: func(api *officialReadAPI) string { return api.SitePath(client.PathStatDevice) },
			ambiguous: []map[string]any{
				{"_id": "legacy-switch-1", "name": "Core Switch A", "mac": "aa:bb:cc:dd:ee:02", "state": 1},
				{"_id": "legacy-switch-2", "name": "Core Switch B", "mac": "aa:bb:cc:dd:ee:02", "state": 1},
			},
			missing: []map[string]any{{"_id": "other", "name": "Other", "mac": "00:00:00:00:00:03", "state": 1}},
			mutate: func(api *officialReadAPI) error {
				_, _, err := domain.NewPortService(api).Update(ctx, officialSwitchID, 1, domain.PortInput{Name: "Uplink"})
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := newOfficialReadAPI(t)
			api.legacy[tt.legacyPath(api)] = tt.ambiguous
			if err := tt.mutate(api); !apperr.Is(err, apperr.AmbiguousID) {
				t.Fatalf("ambiguous identity translation error = %v", err)
			}

			api = newOfficialReadAPI(t)
			api.legacy[tt.legacyPath(api)] = tt.missing
			if err := tt.mutate(api); !apperr.Is(err, apperr.NotFound) {
				t.Fatalf("missing identity translation error = %v", err)
			}
		})
	}
}

func TestLegacyUUIDMutationIDDoesNotRequireOfficialRead(t *testing.T) {
	ctx := context.Background()
	api := newOfficialReadAPI(t)
	legacyID := "f0000000-0000-4000-8000-000000000001"
	api.legacy[api.SitePath(client.PathStatDevice)] = []map[string]any{
		{"_id": legacyID, "name": "Legacy UUID", "mac": "00:00:00:00:00:04", "state": 1},
	}
	api.errs[client.OfficialPath("sites", officialSiteID, "devices")] = apperr.New(apperr.PermissionDenied, "official reads denied")

	p, item, err := domain.NewDeviceService(api).Rename(ctx, legacyID, "Renamed")
	if err != nil {
		t.Fatal(err)
	}
	if item.ID != legacyID || p.Changes[0].ID != legacyID {
		t.Fatalf("legacy UUID target: item=%+v plan=%+v", item, p)
	}
	for _, call := range api.calls {
		if strings.HasPrefix(call, "OFFICIAL ") {
			t.Fatalf("legacy UUID resolution made official call %q", call)
		}
	}
}

func configureOfficialPortFanout(t *testing.T, api *officialReadAPI, count int) {
	t.Helper()
	sitePath := client.OfficialPath("sites", officialSiteID)
	overviewTemplate := officialFixtureData(t, "devices.json")[1]
	detailTemplate := officialFixtureObject(t, "device-switch.json")
	overviews := make([]map[string]any, 0, count+1)
	for i := count; i >= 1; i-- {
		id := fanoutTestUUID(i)
		name := fmt.Sprintf("Switch %02d", i)
		overview := cloneTestMap(overviewTemplate)
		overview["id"] = id
		overview["name"] = name
		overview["macAddress"] = fmt.Sprintf("02:00:00:00:%02x:%02x", i/256, i%256)
		overviews = append(overviews, overview)

		detail := cloneTestMap(detailTemplate)
		detail["id"] = id
		detail["name"] = name
		detail["macAddress"] = overview["macAddress"]
		interfaces := detail["interfaces"].(map[string]any)
		ports := interfaces["ports"].([]any)
		ports = ports[:1]
		ports[0].(map[string]any)["idx"] = float64(i)
		interfaces["ports"] = ports
		api.details[sitePath+"/devices/"+id] = detail
	}
	nonPort := cloneTestMap(overviewTemplate)
	nonPort["id"] = "30000000-0000-4000-8000-000000000001"
	nonPort["name"] = "Access Point"
	nonPort["interfaces"] = []any{"radios"}
	overviews = append(overviews, nonPort)
	api.collections[sitePath+"/devices"] = overviews
}

func configureOfficialNetworkFanout(t *testing.T, api *officialReadAPI, count int) {
	t.Helper()
	sitePath := client.OfficialPath("sites", officialSiteID)
	overviewTemplate := officialFixtureData(t, "networks.json")[0]
	detailTemplate := officialFixtureObject(t, "network-lan.json")
	overviews := make([]map[string]any, 0, count)
	for i := count; i >= 1; i-- {
		id := fanoutTestUUID(i)
		name := fmt.Sprintf("Network %02d", i)
		overview := cloneTestMap(overviewTemplate)
		overview["id"] = id
		overview["name"] = name
		overview["vlanId"] = float64(i)
		overview["default"] = i == 1
		overviews = append(overviews, overview)

		detail := cloneTestMap(detailTemplate)
		detail["id"] = id
		detail["name"] = name
		detail["vlanId"] = float64(i)
		detail["default"] = i == 1
		ipv4 := detail["ipv4Configuration"].(map[string]any)
		dhcp := ipv4["dhcpConfiguration"].(map[string]any)
		dhcp["dnsServerIpAddressesOverride"] = []any{fmt.Sprintf("192.0.2.%d", i)}
		api.details[sitePath+"/networks/"+id] = detail
	}
	api.collections[sitePath+"/networks"] = overviews
}

func fanoutTestUUID(index int) string {
	return fmt.Sprintf("20000000-0000-4000-8000-%012d", index)
}

func cloneTestMap(value map[string]any) map[string]any {
	return cloneTestValue(value).(map[string]any)
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

func assertExactCalls(t *testing.T, got []string, want ...string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("calls = %#v, want %#v", got, want)
	}
}

func assertCallCount(t *testing.T, calls []string, want string, count int) {
	t.Helper()
	got := 0
	for _, call := range calls {
		if call == want {
			got++
		}
	}
	if got != count {
		t.Fatalf("call %q count = %d, want %d; calls=%v", want, got, count, calls)
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
