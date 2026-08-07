package domain_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
	"github.com/noahjenkins/unifi-cli/internal/client"
	"github.com/noahjenkins/unifi-cli/internal/domain"
)

const mutationSiteID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"

type officialMutationCall struct {
	method string
	path   string
	body   any
}

type officialMutationAPI struct {
	collections  map[string][]map[string]any
	details      map[string]map[string]any
	detailErrors map[string]error
	official     []officialMutationCall
	legacy       []officialMutationCall
	mutate       func(method, path string, in, out any) error
}

func (f *officialMutationAPI) SitePath(parts ...string) string {
	return "/proxy/network/api/s/default/" + strings.Join(parts, "/")
}

func (f *officialMutationAPI) IntegrationSitePath(_ context.Context, parts ...string) (string, error) {
	return client.OfficialPath(append([]string{"sites", mutationSiteID}, parts...)...), nil
}

func (f *officialMutationAPI) FetchOfficialObjects(_ context.Context, path string) ([]map[string]any, error) {
	items, ok := f.collections[path]
	if !ok {
		return nil, errors.New("unexpected official collection " + path)
	}
	return cloneTestMaps(items), nil
}

func (f *officialMutationAPI) DoOfficial(_ context.Context, method, path string, in, out any) error {
	f.official = append(f.official, officialMutationCall{method: method, path: path, body: cloneMutationTestValue(in)})
	if f.mutate != nil && method != http.MethodGet {
		return f.mutate(method, path, in, out)
	}
	if method == http.MethodGet {
		if err := f.detailErrors[path]; err != nil {
			return err
		}
		item, ok := f.details[path]
		if !ok {
			return apperr.New(apperr.NotFound, "not found")
		}
		return copyTestJSON(item, out)
	}
	return nil
}

func (f *officialMutationAPI) Do(_ context.Context, method, path string, in, out any) error {
	f.legacy = append(f.legacy, officialMutationCall{method: method, path: path, body: cloneMutationTestValue(in)})
	return errors.New("legacy endpoint used")
}

func copyTestJSON(in, out any) error {
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

func cloneTestMaps(in []map[string]any) []map[string]any {
	var out []map[string]any
	_ = copyTestJSON(in, &out)
	return out
}

func cloneMutationTestValue(in any) any {
	if in == nil {
		return nil
	}
	var out any
	_ = copyTestJSON(in, &out)
	return out
}

func officialNetworkDocument() map[string]any {
	return map[string]any{
		"id": "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", "name": "LAN", "enabled": true,
		"default": false, "management": "GATEWAY", "vlanId": 20,
		"metadata":              map[string]any{"origin": "USER", "configurable": true},
		"cellularBackupEnabled": false, "internetAccessEnabled": true, "isolationEnabled": false,
		"mdnsForwardingEnabled": true,
		"dhcpGuarding":          map[string]any{"trustedDhcpServerIpAddresses": []any{"192.0.2.2"}},
		"ipv4Configuration": map[string]any{
			"hostIpAddress": "192.0.2.1", "prefixLength": 24, "autoScaleEnabled": false,
			"dhcpConfiguration": map[string]any{
				"mode": "SERVER", "domainName": "example.test", "leaseTimeSeconds": 86400,
				"pingConflictDetectionEnabled": true,
				"ipAddressRange":               map[string]any{"start": "192.0.2.10", "stop": "192.0.2.200"},
			},
		},
		"ipv6Configuration": map[string]any{"interfaceType": "PREFIX_DELEGATION"},
	}
}

func networkMutationAPI() *officialMutationAPI {
	return networkMutationAPIForDocument(officialNetworkDocument())
}

func networkMutationAPIForDocument(doc map[string]any) *officialMutationAPI {
	collection := client.OfficialPath("sites", mutationSiteID, "networks")
	detail := client.OfficialPath("sites", mutationSiteID, "networks", doc["id"].(string))
	return &officialMutationAPI{
		collections: map[string][]map[string]any{collection: {doc}},
		details:     map[string]map[string]any{detail: doc},
	}
}

func officialNetworkDocumentForManagement(management string) map[string]any {
	doc := map[string]any{
		"id": "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", "name": "LAN", "enabled": true,
		"default": false, "management": management, "vlanId": 20,
		"metadata": map[string]any{"origin": "USER", "configurable": true},
	}
	switch management {
	case "GATEWAY":
		for key, value := range officialNetworkDocument() {
			doc[key] = value
		}
	case "SWITCH":
		doc["cellularBackupEnabled"] = false
		doc["deviceId"] = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
		doc["isolationEnabled"] = false
		doc["ipv4Configuration"] = map[string]any{
			"hostIpAddress": "192.0.2.1", "prefixLength": 24, "autoScaleEnabled": false,
		}
	}
	return doc
}

func TestOfficialNetworkUpdatePreservesCompleteWritableDocumentAndVerifies(t *testing.T) {
	api := networkMutationAPI()
	id := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	path := client.OfficialPath("sites", mutationSiteID, "networks", id)
	api.mutate = func(method, gotPath string, in, out any) error {
		if method != http.MethodPut || gotPath != path {
			t.Fatalf("mutation = %s %s", method, gotPath)
		}
		body := cloneMutationTestValue(in).(map[string]any)
		observed := cloneMutationTestValue(body).(map[string]any)
		observed["id"] = id
		observed["default"] = false
		observed["metadata"] = map[string]any{"origin": "USER", "configurable": true}
		api.details[path] = observed
		return copyTestJSON(observed, out)
	}

	got, err := domain.NewNetworkService(api).ApplyUpdate(context.Background(), id, domain.NetworkInput{Name: "LAN Renamed", SetName: true})
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "LAN Renamed" {
		t.Fatalf("observed network = %+v", got)
	}
	if len(api.legacy) != 0 {
		t.Fatalf("legacy calls = %+v", api.legacy)
	}
	puts := mutationCalls(api.official, http.MethodPut)
	if len(puts) != 1 {
		t.Fatalf("PUT count = %d, want 1", len(puts))
	}
	body := puts[0].body.(map[string]any)
	expected := cloneMutationTestValue(officialNetworkDocument()).(map[string]any)
	for _, responseOnly := range []string{"id", "default", "metadata"} {
		if _, ok := body[responseOnly]; ok {
			t.Fatalf("PUT retained response-only %q: %+v", responseOnly, body)
		}
	}
	if body["name"] != "LAN Renamed" || !reflect.DeepEqual(body["ipv4Configuration"], expected["ipv4Configuration"]) ||
		!reflect.DeepEqual(body["ipv6Configuration"], expected["ipv6Configuration"]) ||
		!reflect.DeepEqual(body["dhcpGuarding"], expected["dhcpGuarding"]) {
		t.Fatalf("PUT did not preserve complete writable document: %#v", body)
	}
}

func TestOfficialNetworkMutationFailuresAreExplicitAndNeverRetried(t *testing.T) {
	t.Run("gateway create accepts allowed controller defaults but verifies requested fields", func(t *testing.T) {
		api := networkMutationAPI()
		createdID := "ffffffff-ffff-4fff-8fff-ffffffffffff"
		api.mutate = func(method, path string, in, out any) error {
			observed := cloneMutationTestValue(in).(map[string]any)
			observed["id"] = createdID
			observed["default"] = false
			observed["metadata"] = map[string]any{"origin": "USER"}
			observed["mdnsForwardingEnabled"] = false
			observed["zoneId"] = "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"
			ipv4 := observed["ipv4Configuration"].(map[string]any)
			ipv4["additionalHostIpSubnets"] = []any{"198.51.100.1/24"}
			api.details[client.OfficialPath("sites", mutationSiteID, "networks", createdID)] = observed
			return copyTestJSON(observed, out)
		}
		got, err := domain.NewNetworkService(api).ApplyCreate(context.Background(), domain.NetworkInput{
			Name: "Lab", Purpose: "gateway", VLAN: intPtr(30), Subnet: "192.0.2.1/24",
		})
		if err != nil || got.ID != createdID {
			t.Fatalf("created network = %+v, error = %v", got, err)
		}
		if got := len(mutationCalls(api.official, http.MethodPost)); got != 1 {
			t.Fatalf("POST count = %d, want 1", got)
		}
	})

	t.Run("gateway create rejects requested nested-field mismatch without retry", func(t *testing.T) {
		api := networkMutationAPI()
		createdID := "ffffffff-ffff-4fff-8fff-ffffffffffff"
		api.mutate = func(method, path string, in, out any) error {
			observed := cloneMutationTestValue(in).(map[string]any)
			observed["id"] = createdID
			observed["mdnsForwardingEnabled"] = false
			observed["ipv4Configuration"].(map[string]any)["hostIpAddress"] = "192.0.2.2"
			api.details[client.OfficialPath("sites", mutationSiteID, "networks", createdID)] = observed
			return copyTestJSON(observed, out)
		}
		_, err := domain.NewNetworkService(api).ApplyCreate(context.Background(), domain.NetworkInput{
			Name: "Lab", Purpose: "gateway", VLAN: intPtr(30), Subnet: "192.0.2.1/24",
		})
		if !apperr.Is(err, apperr.Conflict) || !strings.Contains(err.Error(), "verification") {
			t.Fatalf("error = %v", err)
		}
		if got := len(mutationCalls(api.official, http.MethodPost)); got != 1 {
			t.Fatalf("POST count = %d, want 1", got)
		}
	})

	t.Run("create verifies JSON numeric semantics after re-read", func(t *testing.T) {
		api := networkMutationAPI()
		createdID := "ffffffff-ffff-4fff-8fff-ffffffffffff"
		api.mutate = func(method, path string, in, out any) error {
			body := cloneMutationTestValue(in).(map[string]any)
			body["id"] = createdID
			api.details[client.OfficialPath("sites", mutationSiteID, "networks", createdID)] = body
			return copyTestJSON(body, out)
		}
		got, err := domain.NewNetworkService(api).ApplyCreate(context.Background(), domain.NetworkInput{Name: "Lab", Purpose: "unmanaged", VLAN: intPtr(30)})
		if err != nil || got.ID != createdID {
			t.Fatalf("created network = %+v, error = %v", got, err)
		}
	})

	t.Run("create missing ID", func(t *testing.T) {
		api := networkMutationAPI()
		api.mutate = func(method, path string, in, out any) error { return copyTestJSON(map[string]any{"name": "Lab"}, out) }
		_, err := domain.NewNetworkService(api).ApplyCreate(context.Background(), domain.NetworkInput{Name: "Lab", Purpose: "unmanaged", VLAN: intPtr(30)})
		if !apperr.Is(err, apperr.Conflict) || !strings.Contains(err.Error(), "unverified") {
			t.Fatalf("error = %v", err)
		}
		if got := len(mutationCalls(api.official, http.MethodPost)); got != 1 {
			t.Fatalf("POST count = %d, want 1", got)
		}
	})

	t.Run("create post-write read failure", func(t *testing.T) {
		api := networkMutationAPI()
		api.mutate = func(method, path string, in, out any) error {
			return copyTestJSON(map[string]any{"id": "ffffffff-ffff-4fff-8fff-ffffffffffff"}, out)
		}
		_, err := domain.NewNetworkService(api).ApplyCreate(context.Background(), domain.NetworkInput{Name: "Lab", Purpose: "unmanaged", VLAN: intPtr(30)})
		if !apperr.Is(err, apperr.Conflict) || !strings.Contains(err.Error(), "could not be verified") {
			t.Fatalf("error = %v", err)
		}
		if got := len(mutationCalls(api.official, http.MethodPost)); got != 1 {
			t.Fatalf("POST count = %d, want 1", got)
		}
	})

	t.Run("update post-write mismatch", func(t *testing.T) {
		api := networkMutationAPI()
		api.mutate = func(method, path string, in, out any) error { return nil }
		_, err := domain.NewNetworkService(api).ApplyUpdate(context.Background(), "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", domain.NetworkInput{Name: "Changed", SetName: true})
		if !apperr.Is(err, apperr.Conflict) || !strings.Contains(err.Error(), "verification") {
			t.Fatalf("error = %v", err)
		}
		if got := len(mutationCalls(api.official, http.MethodPut)); got != 1 {
			t.Fatalf("PUT count = %d, want 1", got)
		}
	})

	t.Run("update post-write read failure", func(t *testing.T) {
		api := networkMutationAPI()
		path := client.OfficialPath("sites", mutationSiteID, "networks", "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
		api.mutate = func(method, gotPath string, in, out any) error {
			delete(api.details, path)
			return nil
		}
		_, err := domain.NewNetworkService(api).ApplyUpdate(context.Background(), "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", domain.NetworkInput{Name: "Changed", SetName: true})
		if !apperr.Is(err, apperr.Conflict) || !strings.Contains(err.Error(), "could not be verified") {
			t.Fatalf("error = %v", err)
		}
		if got := len(mutationCalls(api.official, http.MethodPut)); got != 1 {
			t.Fatalf("PUT count = %d, want 1", got)
		}
	})

	t.Run("delete still present", func(t *testing.T) {
		api := networkMutationAPI()
		_, err := domain.NewNetworkService(api).ApplyDelete(context.Background(), "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
		if !apperr.Is(err, apperr.Conflict) || !strings.Contains(err.Error(), "still present") {
			t.Fatalf("error = %v", err)
		}
		if got := len(mutationCalls(api.official, http.MethodDelete)); got != 1 {
			t.Fatalf("DELETE count = %d, want 1", got)
		}
	})
}

func TestOfficialNetworkMutationEnforcesOpenAPIBoundsBeforeWrites(t *testing.T) {
	t.Run("VLAN bounds", func(t *testing.T) {
		for _, vlan := range []int{0, 4010, 4094} {
			t.Run(fmt.Sprint(vlan), func(t *testing.T) {
				api := networkMutationAPI()
				_, err := domain.NewNetworkService(api).ApplyCreate(context.Background(), domain.NetworkInput{Name: "Lab", Purpose: "unmanaged", VLAN: intPtr(vlan)})
				if !apperr.Is(err, apperr.ValidationFailed) {
					t.Fatalf("error = %v, want validation", err)
				}
				if got := len(nonGetMutationCalls(api.official)); got != 0 {
					t.Fatalf("writes = %d, want 0", got)
				}
				api = networkMutationAPI()
				_, err = domain.NewNetworkService(api).ApplyUpdate(context.Background(), "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", domain.NetworkInput{VLAN: intPtr(vlan)})
				if !apperr.Is(err, apperr.ValidationFailed) {
					t.Fatalf("update error = %v, want validation", err)
				}
				if got := len(mutationCalls(api.official, http.MethodPut)); got != 0 {
					t.Fatalf("update PUTs = %d, want 0", got)
				}
			})
		}
		for _, vlan := range []int{1, 4009} {
			api := networkMutationAPI()
			if _, err := domain.NewNetworkService(api).Create(context.Background(), domain.NetworkInput{Name: "Lab", Purpose: "unmanaged", VLAN: intPtr(vlan)}); err != nil {
				t.Fatalf("VLAN %d rejected: %v", vlan, err)
			}
		}
	})

	t.Run("gateway prefix bounds", func(t *testing.T) {
		for _, subnet := range []string{"192.0.2.1/7", "192.0.2.1/31", "192.0.2.1/32"} {
			t.Run(subnet, func(t *testing.T) {
				api := networkMutationAPI()
				_, err := domain.NewNetworkService(api).ApplyCreate(context.Background(), domain.NetworkInput{Name: "Lab", Purpose: "gateway", VLAN: intPtr(30), Subnet: subnet})
				if !apperr.Is(err, apperr.ValidationFailed) {
					t.Fatalf("error = %v, want validation", err)
				}
				if got := len(nonGetMutationCalls(api.official)); got != 0 {
					t.Fatalf("writes = %d, want 0", got)
				}
				api = networkMutationAPI()
				_, err = domain.NewNetworkService(api).ApplyUpdate(context.Background(), "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", domain.NetworkInput{Subnet: subnet, SetSubnet: true})
				if !apperr.Is(err, apperr.ValidationFailed) {
					t.Fatalf("update error = %v, want validation", err)
				}
				if got := len(mutationCalls(api.official, http.MethodPut)); got != 0 {
					t.Fatalf("update PUTs = %d, want 0", got)
				}
			})
		}
		for _, subnet := range []string{"192.0.2.1/8", "192.0.2.1/30"} {
			api := networkMutationAPI()
			if _, err := domain.NewNetworkService(api).Create(context.Background(), domain.NetworkInput{Name: "Lab", Purpose: "gateway", VLAN: intPtr(30), Subnet: subnet}); err != nil {
				t.Fatalf("subnet %s rejected: %v", subnet, err)
			}
		}
	})
}

func TestOfficialNetworkUpdateRejectsEveryManagementTransitionBeforeWrite(t *testing.T) {
	for _, current := range []string{"GATEWAY", "SWITCH", "UNMANAGED"} {
		for _, target := range []string{"GATEWAY", "SWITCH", "UNMANAGED"} {
			if target == current {
				continue
			}
			t.Run(current+"_to_"+target, func(t *testing.T) {
				api := networkMutationAPIForDocument(officialNetworkDocumentForManagement(current))
				_, err := domain.NewNetworkService(api).ApplyUpdate(context.Background(), "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", domain.NetworkInput{Purpose: strings.ToLower(target), SetPurpose: true})
				if !apperr.Is(err, apperr.ValidationFailed) || !strings.Contains(err.Error(), "management") {
					t.Fatalf("error = %v", err)
				}
				if got := len(mutationCalls(api.official, http.MethodPut)); got != 0 {
					t.Fatalf("PUT count = %d, want 0", got)
				}
			})
		}
	}
}

func TestOfficialNetworkUpdateAllowsExplicitCurrentManagementWithOtherChanges(t *testing.T) {
	for _, current := range []string{"GATEWAY", "SWITCH", "UNMANAGED"} {
		t.Run(current, func(t *testing.T) {
			api := networkMutationAPIForDocument(officialNetworkDocumentForManagement(current))
			path := client.OfficialPath("sites", mutationSiteID, "networks", "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
			api.mutate = func(method, gotPath string, in, out any) error {
				observed := cloneMutationTestValue(in).(map[string]any)
				observed["id"] = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
				observed["default"] = false
				observed["metadata"] = map[string]any{"origin": "USER"}
				api.details[path] = observed
				return copyTestJSON(observed, out)
			}
			got, err := domain.NewNetworkService(api).ApplyUpdate(context.Background(), "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", domain.NetworkInput{
				Name: "Renamed", SetName: true, Purpose: strings.ToLower(current), SetPurpose: true,
			})
			if err != nil || got.Name != "Renamed" {
				t.Fatalf("network = %+v, error = %v", got, err)
			}
			if got := len(mutationCalls(api.official, http.MethodPut)); got != 1 {
				t.Fatalf("PUT count = %d, want 1", got)
			}
		})
	}
}

func TestOfficialNetworkCreateRejectsLegacyPurposeSemanticAliases(t *testing.T) {
	for _, purpose := range []string{"corporate", "guest", "wan"} {
		t.Run(purpose, func(t *testing.T) {
			api := networkMutationAPI()
			_, err := domain.NewNetworkService(api).Create(context.Background(), domain.NetworkInput{
				Name: "Unsafe Alias", Purpose: purpose, VLAN: intPtr(30), Subnet: "192.0.2.1/24",
			})
			if !apperr.Is(err, apperr.ValidationFailed) || !strings.Contains(err.Error(), "management") {
				t.Fatalf("error = %v", err)
			}
			if len(nonGetMutationCalls(api.official)) != 0 {
				t.Fatalf("legacy purpose made writes: %+v", api.official)
			}
		})
	}
}

func TestOfficialUnmanagedNetworkCreateRejectsGatewayOnlyFieldsBeforeWrite(t *testing.T) {
	for _, in := range []domain.NetworkInput{
		{Name: "Unsafe", Purpose: "unmanaged", VLAN: intPtr(30), Subnet: "192.0.2.1/24", SetSubnet: true},
		{Name: "Unsafe", Purpose: "unmanaged", VLAN: intPtr(30), DHCPEnabled: true, SetDHCPEnabled: true},
		{Name: "Unsafe", Purpose: "unmanaged", VLAN: intPtr(30), DomainName: "example.test", SetDomainName: true},
	} {
		api := networkMutationAPI()
		_, err := domain.NewNetworkService(api).ApplyCreate(context.Background(), in)
		if !apperr.Is(err, apperr.ValidationFailed) {
			t.Fatalf("input = %+v, error = %v", in, err)
		}
		if got := len(nonGetMutationCalls(api.official)); got != 0 {
			t.Fatalf("input = %+v, writes = %d", in, got)
		}
	}
}

func officialWlanDocument() map[string]any {
	return map[string]any{
		"id": "cccccccc-cccc-4ccc-8ccc-cccccccccccc", "type": "STANDARD", "name": "Main", "enabled": true,
		"metadata": map[string]any{"origin": "USER"}, "hideName": false, "clientIsolationEnabled": false,
		"multicastToUnicastConversionEnabled": true, "uapsdEnabled": true, "advertiseDeviceName": false,
		"arpProxyEnabled": false, "bssTransitionEnabled": true, "bandSteeringEnabled": true,
		"broadcastingFrequenciesGHz": []any{2.4, 5.0},
		"network":                    map[string]any{"type": "SPECIFIC", "networkId": "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"},
		"securityConfiguration":      map[string]any{"type": "WPA2_PERSONAL", "passphrase": "not-rendered-secret", "fastRoamingEnabled": true},
		"clientFilteringPolicy":      map[string]any{"action": "ALLOW", "macAddressFilter": []any{"00:11:22:33:44:55"}},
	}
}

func wlanMutationAPI() *officialMutationAPI {
	doc := officialWlanDocument()
	collection := client.OfficialPath("sites", mutationSiteID, "wifi", "broadcasts")
	detail := client.OfficialPath("sites", mutationSiteID, "wifi", "broadcasts", doc["id"].(string))
	return &officialMutationAPI{collections: map[string][]map[string]any{collection: {doc}}, details: map[string]map[string]any{detail: doc}}
}

func TestOfficialWlanUpdatePreservesCompleteWritableDocumentAndVerifies(t *testing.T) {
	api := wlanMutationAPI()
	id := "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	path := client.OfficialPath("sites", mutationSiteID, "wifi", "broadcasts", id)
	api.mutate = func(method, gotPath string, in, out any) error {
		body := cloneMutationTestValue(in).(map[string]any)
		observed := cloneMutationTestValue(body).(map[string]any)
		observed["id"] = id
		observed["metadata"] = map[string]any{"origin": "USER"}
		api.details[path] = observed
		return copyTestJSON(observed, out)
	}
	got, err := domain.NewWlanService(api).ApplyUpdate(context.Background(), id, domain.WlanInput{Name: "Main Renamed", SetName: true})
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Main Renamed" {
		t.Fatalf("observed WLAN = %+v", got)
	}
	puts := mutationCalls(api.official, http.MethodPut)
	if len(puts) != 1 {
		t.Fatalf("PUT count = %d, want 1", len(puts))
	}
	body := puts[0].body.(map[string]any)
	if !reflect.DeepEqual(body["securityConfiguration"], officialWlanDocument()["securityConfiguration"]) ||
		!reflect.DeepEqual(body["clientFilteringPolicy"], officialWlanDocument()["clientFilteringPolicy"]) {
		t.Fatalf("PUT lost untouched WLAN fields: %#v", body)
	}
}

func TestOfficialWlanCreateEmitsExactSupportedOpenAPISchema(t *testing.T) {
	tests := []struct {
		name string
		in   domain.WlanInput
		want map[string]any
	}{
		{
			name: "open",
			in:   domain.WlanInput{Name: "Lab", Security: "open"},
			want: map[string]any{
				"type": "STANDARD", "name": "Lab", "enabled": true, "hideName": false,
				"clientIsolationEnabled": false, "multicastToUnicastConversionEnabled": false, "uapsdEnabled": true,
				"advertiseDeviceName": false, "arpProxyEnabled": false, "bssTransitionEnabled": true,
				"broadcastingFrequenciesGHz": []any{2.4, 5.0},
				"securityConfiguration":      map[string]any{"type": "OPEN"},
				"network":                    map[string]any{"type": "NATIVE"},
			},
		},
		{
			name: "WPA2 personal",
			in:   domain.WlanInput{Name: "Lab", Security: "wpa2_personal", Password: "password"},
			want: map[string]any{
				"type": "STANDARD", "name": "Lab", "enabled": true, "hideName": false,
				"clientIsolationEnabled": false, "multicastToUnicastConversionEnabled": false, "uapsdEnabled": true,
				"advertiseDeviceName": false, "arpProxyEnabled": false, "bssTransitionEnabled": true,
				"broadcastingFrequenciesGHz": []any{2.4, 5.0},
				"securityConfiguration":      map[string]any{"type": "WPA2_PERSONAL", "passphrase": "password"},
				"network":                    map[string]any{"type": "NATIVE"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := wlanMutationAPI()
			createdID := "ffffffff-ffff-4fff-8fff-ffffffffffff"
			path := client.OfficialPath("sites", mutationSiteID, "wifi", "broadcasts", createdID)
			api.mutate = func(method, gotPath string, in, out any) error {
				observed := cloneMutationTestValue(in).(map[string]any)
				observed["id"] = createdID
				api.details[path] = observed
				return copyTestJSON(observed, out)
			}
			if _, err := domain.NewWlanService(api).ApplyCreate(context.Background(), tt.in); err != nil {
				t.Fatal(err)
			}
			posts := mutationCalls(api.official, http.MethodPost)
			if len(posts) != 1 || !reflect.DeepEqual(posts[0].body, tt.want) {
				t.Fatalf("POST body = %#v, want %#v", posts, tt.want)
			}
		})
	}
}

func TestOfficialWlanMutationEnforcesSecurityVariantsAndPassphraseBoundsBeforeWrites(t *testing.T) {
	for _, security := range []string{"wpa3_personal", "wpa2_wpa3_personal"} {
		t.Run("create rejects incomplete "+security, func(t *testing.T) {
			api := wlanMutationAPI()
			_, err := domain.NewWlanService(api).ApplyCreate(context.Background(), domain.WlanInput{Name: "Lab", Security: security, Password: "password"})
			if !apperr.Is(err, apperr.ValidationFailed) {
				t.Fatalf("error = %v, want validation", err)
			}
			if got := len(mutationCalls(api.official, http.MethodPost)); got != 0 {
				t.Fatalf("POST count = %d, want 0", got)
			}
		})
		t.Run("update rejects incomplete "+security, func(t *testing.T) {
			api := wlanMutationAPI()
			_, err := domain.NewWlanService(api).ApplyUpdate(context.Background(), "cccccccc-cccc-4ccc-8ccc-cccccccccccc", domain.WlanInput{Security: security, SetSecurity: true, Password: "password", SetPassword: true})
			if !apperr.Is(err, apperr.ValidationFailed) {
				t.Fatalf("error = %v, want validation", err)
			}
			if got := len(mutationCalls(api.official, http.MethodPut)); got != 0 {
				t.Fatalf("PUT count = %d, want 0", got)
			}
		})
	}

	for _, length := range []int{7, 64} {
		t.Run(fmt.Sprintf("create passphrase length %d", length), func(t *testing.T) {
			api := wlanMutationAPI()
			_, err := domain.NewWlanService(api).ApplyCreate(context.Background(), domain.WlanInput{Name: "Lab", Security: "wpa2_personal", Password: strings.Repeat("p", length)})
			if !apperr.Is(err, apperr.ValidationFailed) {
				t.Fatalf("error = %v, want validation", err)
			}
			if got := len(nonGetMutationCalls(api.official)); got != 0 {
				t.Fatalf("writes = %d, want 0", got)
			}
		})
		t.Run(fmt.Sprintf("password-only update length %d", length), func(t *testing.T) {
			api := wlanMutationAPI()
			_, err := domain.NewWlanService(api).ApplyUpdate(context.Background(), "cccccccc-cccc-4ccc-8ccc-cccccccccccc", domain.WlanInput{Password: strings.Repeat("p", length), SetPassword: true})
			if !apperr.Is(err, apperr.ValidationFailed) {
				t.Fatalf("error = %v, want validation", err)
			}
			if got := len(mutationCalls(api.official, http.MethodPut)); got != 0 {
				t.Fatalf("PUT count = %d, want 0", got)
			}
		})
	}
	for _, length := range []int{8, 63} {
		api := wlanMutationAPI()
		if _, err := domain.NewWlanService(api).Create(context.Background(), domain.WlanInput{Name: "Lab", Security: "wpa2_personal", Password: strings.Repeat("p", length)}); err != nil {
			t.Fatalf("passphrase length %d rejected: %v", length, err)
		}
	}
}

func TestOfficialWlanVerificationReordersOnlyDocumentedSetArrays(t *testing.T) {
	t.Run("documented sets may reorder", func(t *testing.T) {
		doc := officialWlanDocument()
		doc["broadcastingDeviceFilter"] = map[string]any{"type": "DEVICES", "deviceIds": []any{
			"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa1", "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa2",
		}}
		doc["multicastFilteringPolicy"] = map[string]any{"action": "ALLOW", "sourceMacAddressFilter": []any{"00:11:22:33:44:55", "00:11:22:33:44:66"}}
		api := networklessWlanMutationAPI(doc)
		path := client.OfficialPath("sites", mutationSiteID, "wifi", "broadcasts", "cccccccc-cccc-4ccc-8ccc-cccccccccccc")
		api.mutate = func(method, gotPath string, in, out any) error {
			observed := cloneMutationTestValue(in).(map[string]any)
			observed["id"] = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
			observed["metadata"] = map[string]any{"origin": "USER"}
			observed["broadcastingFrequenciesGHz"] = []any{5.0, 2.4}
			observed["clientFilteringPolicy"].(map[string]any)["macAddressFilter"] = []any{"00:11:22:33:44:55"}
			observed["broadcastingDeviceFilter"].(map[string]any)["deviceIds"] = []any{
				"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa2", "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa1",
			}
			observed["multicastFilteringPolicy"].(map[string]any)["sourceMacAddressFilter"] = []any{"00:11:22:33:44:66", "00:11:22:33:44:55"}
			api.details[path] = observed
			return copyTestJSON(observed, out)
		}
		if _, err := domain.NewWlanService(api).ApplyUpdate(context.Background(), "cccccccc-cccc-4ccc-8ccc-cccccccccccc", domain.WlanInput{Name: "Renamed", SetName: true}); err != nil {
			t.Fatalf("set reordering rejected: %v", err)
		}
	})

	t.Run("ordered arrays may not reorder", func(t *testing.T) {
		doc := officialWlanDocument()
		doc["blackoutScheduleConfiguration"] = map[string]any{"days": []any{
			map[string]any{"day": "MON", "type": "ALL_DAY"},
			map[string]any{"day": "TUE", "type": "ALL_DAY"},
		}}
		api := networklessWlanMutationAPI(doc)
		path := client.OfficialPath("sites", mutationSiteID, "wifi", "broadcasts", "cccccccc-cccc-4ccc-8ccc-cccccccccccc")
		api.mutate = func(method, gotPath string, in, out any) error {
			observed := cloneMutationTestValue(in).(map[string]any)
			observed["id"] = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
			observed["metadata"] = map[string]any{"origin": "USER"}
			days := observed["blackoutScheduleConfiguration"].(map[string]any)["days"].([]any)
			observed["blackoutScheduleConfiguration"].(map[string]any)["days"] = []any{days[1], days[0]}
			api.details[path] = observed
			return copyTestJSON(observed, out)
		}
		_, err := domain.NewWlanService(api).ApplyUpdate(context.Background(), "cccccccc-cccc-4ccc-8ccc-cccccccccccc", domain.WlanInput{Name: "Renamed", SetName: true})
		if !apperr.Is(err, apperr.Conflict) || !strings.Contains(err.Error(), "verification") {
			t.Fatalf("error = %v, want ordered-array verification conflict", err)
		}
	})
}

func networklessWlanMutationAPI(doc map[string]any) *officialMutationAPI {
	collection := client.OfficialPath("sites", mutationSiteID, "wifi", "broadcasts")
	detail := client.OfficialPath("sites", mutationSiteID, "wifi", "broadcasts", doc["id"].(string))
	return &officialMutationAPI{collections: map[string][]map[string]any{collection: {doc}}, details: map[string]map[string]any{detail: doc}}
}

func TestOfficialWlanMutationFailuresAreExplicitAndNeverRetried(t *testing.T) {
	t.Run("create missing ID", func(t *testing.T) {
		api := wlanMutationAPI()
		api.mutate = func(method, path string, in, out any) error {
			return copyTestJSON(map[string]any{"name": "Lab"}, out)
		}
		_, err := domain.NewWlanService(api).ApplyCreate(context.Background(), domain.WlanInput{Name: "Lab", Security: "open"})
		if !apperr.Is(err, apperr.Conflict) || !strings.Contains(err.Error(), "unverified") {
			t.Fatalf("error = %v", err)
		}
		if got := len(mutationCalls(api.official, http.MethodPost)); got != 1 {
			t.Fatalf("POST count = %d, want 1", got)
		}
	})

	t.Run("update post-write read failure", func(t *testing.T) {
		api := wlanMutationAPI()
		path := client.OfficialPath("sites", mutationSiteID, "wifi", "broadcasts", "cccccccc-cccc-4ccc-8ccc-cccccccccccc")
		api.mutate = func(method, gotPath string, in, out any) error {
			delete(api.details, path)
			return nil
		}
		_, err := domain.NewWlanService(api).ApplyUpdate(context.Background(), "cccccccc-cccc-4ccc-8ccc-cccccccccccc", domain.WlanInput{Name: "Changed", SetName: true})
		if !apperr.Is(err, apperr.Conflict) || !strings.Contains(err.Error(), "could not be verified") {
			t.Fatalf("error = %v", err)
		}
		if got := len(mutationCalls(api.official, http.MethodPut)); got != 1 {
			t.Fatalf("PUT count = %d, want 1", got)
		}
	})

	t.Run("delete still present", func(t *testing.T) {
		api := wlanMutationAPI()
		_, err := domain.NewWlanService(api).ApplyDelete(context.Background(), "cccccccc-cccc-4ccc-8ccc-cccccccccccc")
		if !apperr.Is(err, apperr.Conflict) || !strings.Contains(err.Error(), "still present") {
			t.Fatalf("error = %v", err)
		}
		if got := len(mutationCalls(api.official, http.MethodDelete)); got != 1 {
			t.Fatalf("DELETE count = %d, want 1", got)
		}
	})
}

func TestOfficialAdoptRejectsMissingResponseIDWithoutRetry(t *testing.T) {
	pendingPath := client.OfficialPath("pending-devices")
	devicesPath := client.OfficialPath("sites", mutationSiteID, "devices")
	api := &officialMutationAPI{collections: map[string][]map[string]any{
		pendingPath: {officialPendingDevice("00:11:22:33:44:66", []any{mutationSiteID})},
	}}
	api.mutate = func(method, path string, in, out any) error {
		return copyTestJSON(map[string]any{"macAddress": "00:11:22:33:44:66"}, out)
	}
	_, err := domain.NewDeviceService(api).ApplyAdopt(context.Background(), "00:11:22:33:44:66")
	if !apperr.Is(err, apperr.Conflict) || !strings.Contains(err.Error(), "unverified") {
		t.Fatalf("error = %v", err)
	}
	writes := nonGetMutationCalls(api.official)
	if len(writes) != 1 || writes[0].method != http.MethodPost || writes[0].path != devicesPath {
		t.Fatalf("writes = %+v", writes)
	}
}

func officialPendingDevice(mac string, targetSiteIDs any) map[string]any {
	pending := map[string]any{
		"features": []any{"accessPoint"}, "firmwareUpdatable": true, "firmwareVersion": "7.0.1",
		"ipAddress": "192.0.2.50", "macAddress": mac, "model": "U7", "state": "PENDING_ADOPTION", "supported": true,
	}
	if targetSiteIDs != nil {
		pending["adoptionTargetSiteIds"] = targetSiteIDs
	}
	return pending
}

func TestOfficialAdoptRequiresUnambiguousEligibilityForSelectedSite(t *testing.T) {
	pendingPath := client.OfficialPath("pending-devices")
	tests := []struct {
		name    string
		pending []map[string]any
	}{
		{name: "ineligible", pending: []map[string]any{officialPendingDevice("00:11:22:33:44:66", []any{"ffffffff-ffff-4fff-8fff-ffffffffffff"})}},
		{name: "empty target set", pending: []map[string]any{officialPendingDevice("00:11:22:33:44:66", []any{})}},
		{name: "missing target set", pending: []map[string]any{officialPendingDevice("00:11:22:33:44:66", nil)}},
		{name: "malformed target set", pending: []map[string]any{officialPendingDevice("00:11:22:33:44:66", []any{42})}},
		{name: "duplicate target set", pending: []map[string]any{officialPendingDevice("00:11:22:33:44:66", []any{mutationSiteID, mutationSiteID})}},
		{name: "ambiguous pending MAC", pending: []map[string]any{
			officialPendingDevice("00:11:22:33:44:66", []any{mutationSiteID}),
			officialPendingDevice("00:11:22:33:44:66", []any{mutationSiteID}),
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := &officialMutationAPI{collections: map[string][]map[string]any{pendingPath: tt.pending}}
			_, err := domain.NewDeviceService(api).ApplyAdopt(context.Background(), "00:11:22:33:44:66")
			if err == nil {
				t.Fatal("adoption eligibility failure returned success")
			}
			if got := len(mutationCalls(api.official, http.MethodPost)); got != 0 {
				t.Fatalf("POST count = %d, want 0", got)
			}
		})
	}
}

func TestOfficialForgetVerifiesAbsenceWithoutRetry(t *testing.T) {
	device := map[string]any{
		"id": "dddddddd-dddd-4ddd-8ddd-dddddddddddd", "macAddress": "00:11:22:33:44:55", "name": "AP",
		"model": "U7", "state": "ONLINE", "supported": true, "firmwareUpdatable": true,
		"features": []any{"accessPoint"}, "interfaces": []any{"radios"},
	}
	devicesPath := client.OfficialPath("sites", mutationSiteID, "devices")
	detailPath := client.OfficialPath("sites", mutationSiteID, "devices", device["id"].(string))
	newAPI := func() *officialMutationAPI {
		return &officialMutationAPI{
			collections:  map[string][]map[string]any{devicesPath: {device}},
			details:      map[string]map[string]any{detailPath: device},
			detailErrors: map[string]error{},
		}
	}

	t.Run("confirmed absent", func(t *testing.T) {
		api := newAPI()
		api.mutate = func(method, path string, in, out any) error {
			delete(api.details, detailPath)
			return nil
		}
		got, err := domain.NewDeviceService(api).ApplyForget(context.Background(), device["id"].(string))
		if err != nil || !got.Accepted {
			t.Fatalf("result = %+v, error = %v", got, err)
		}
		if got := len(mutationCalls(api.official, http.MethodDelete)); got != 1 {
			t.Fatalf("DELETE count = %d, want 1", got)
		}
	})

	t.Run("still present", func(t *testing.T) {
		api := newAPI()
		_, err := domain.NewDeviceService(api).ApplyForget(context.Background(), device["id"].(string))
		if !apperr.Is(err, apperr.Conflict) || !strings.Contains(err.Error(), "verification") {
			t.Fatalf("error = %v, want verification conflict", err)
		}
		if got := len(mutationCalls(api.official, http.MethodDelete)); got != 1 {
			t.Fatalf("DELETE count = %d, want 1", got)
		}
	})

	t.Run("post-delete read failure", func(t *testing.T) {
		api := newAPI()
		api.mutate = func(method, path string, in, out any) error {
			api.detailErrors[detailPath] = apperr.New(apperr.PermissionDenied, "read denied")
			return nil
		}
		_, err := domain.NewDeviceService(api).ApplyForget(context.Background(), device["id"].(string))
		if !apperr.Is(err, apperr.Conflict) || !strings.Contains(err.Error(), "could not be verified") {
			t.Fatalf("error = %v, want unverified conflict", err)
		}
		if got := len(mutationCalls(api.official, http.MethodDelete)); got != 1 {
			t.Fatalf("DELETE count = %d, want 1", got)
		}
	})
}

func TestOfficialDeviceActionsUseAcceptedSemantics(t *testing.T) {
	device := map[string]any{"id": "dddddddd-dddd-4ddd-8ddd-dddddddddddd", "macAddress": "00:11:22:33:44:55", "name": "AP", "model": "U7", "state": "ONLINE", "supported": true, "firmwareUpdatable": true, "features": []any{"accessPoint"}, "interfaces": []any{"radios"}}
	devicesPath := client.OfficialPath("sites", mutationSiteID, "devices")
	detailPath := client.OfficialPath("sites", mutationSiteID, "devices", device["id"].(string))
	pendingPath := client.OfficialPath("pending-devices")

	tests := []struct {
		name       string
		apply      func(*domain.DeviceService) (any, error)
		wantMethod string
		wantPath   string
		wantBody   map[string]any
	}{
		{name: "restart", wantMethod: http.MethodPost, wantPath: detailPath + "/actions", wantBody: map[string]any{"action": "RESTART"}, apply: func(s *domain.DeviceService) (any, error) {
			return s.ApplyRestart(context.Background(), device["id"].(string))
		}},
		{name: "adopt", wantMethod: http.MethodPost, wantPath: devicesPath, wantBody: map[string]any{"ignoreDeviceLimit": false, "macAddress": "00:11:22:33:44:66"}, apply: func(s *domain.DeviceService) (any, error) {
			return s.ApplyAdopt(context.Background(), "00:11:22:33:44:66")
		}},
		{name: "forget", wantMethod: http.MethodDelete, wantPath: detailPath, apply: func(s *domain.DeviceService) (any, error) {
			return s.ApplyForget(context.Background(), device["id"].(string))
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pending := officialPendingDevice("00:11:22:33:44:66", []any{mutationSiteID})
			api := &officialMutationAPI{
				collections: map[string][]map[string]any{
					devicesPath: []map[string]any{device}, pendingPath: []map[string]any{pending},
				},
				details: map[string]map[string]any{detailPath: device},
			}
			api.mutate = func(method, path string, in, out any) error {
				if tt.name == "adopt" {
					return copyTestJSON(map[string]any{"id": "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee", "macAddress": "00:11:22:33:44:66"}, out)
				}
				if tt.name == "forget" {
					delete(api.details, detailPath)
				}
				return nil
			}
			got, err := tt.apply(domain.NewDeviceService(api))
			if err != nil {
				t.Fatal(err)
			}
			encoded, _ := json.Marshal(got)
			if string(encoded) != `{"accepted":true}` {
				t.Fatalf("action data = %s, want accepted", encoded)
			}
			writes := nonGetMutationCalls(api.official)
			if len(writes) != 1 || writes[0].method != tt.wantMethod || writes[0].path != tt.wantPath {
				t.Fatalf("writes = %+v", writes)
			}
			if tt.wantBody != nil && !reflect.DeepEqual(writes[0].body, tt.wantBody) {
				t.Fatalf("body = %#v, want %#v", writes[0].body, tt.wantBody)
			}
		})
	}
}

func mutationCalls(calls []officialMutationCall, method string) []officialMutationCall {
	var out []officialMutationCall
	for _, call := range calls {
		if call.method == method {
			out = append(out, call)
		}
	}
	return out
}

func nonGetMutationCalls(calls []officialMutationCall) []officialMutationCall {
	var out []officialMutationCall
	for _, call := range calls {
		if call.method != http.MethodGet {
			out = append(out, call)
		}
	}
	return out
}
