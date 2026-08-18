package domain_test

import (
	"context"
	"net/http"
	"reflect"
	"testing"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
	"github.com/noahjenkins/unifi-cli/internal/client"
	"github.com/noahjenkins/unifi-cli/internal/domain"
)

const switchDeviceID = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"

func TestOfficialNetworkCreateBuildsCompleteTypedManagementDocuments(t *testing.T) {
	tests := []struct {
		name string
		in   domain.NetworkInput
		want map[string]any
	}{
		{
			name: "gateway DHCP server",
			in: domain.NetworkInput{
				Name: "Guest", Purpose: "gateway", VLAN: intPtr(30), Subnet: "192.0.2.1/24", SetSubnet: true,
				Enabled: true, SetEnabled: true, DHCPMode: "server", SetDHCPMode: true,
				DHCPRangeStart: "192.0.2.10", SetDHCPRangeStart: true, DHCPRangeStop: "192.0.2.200", SetDHCPRangeStop: true,
				DHCPLeaseTimeSeconds: 86400, SetDHCPLeaseTimeSeconds: true,
				DHCPConflictDetectionEnabled: false, SetDHCPConflictDetectionEnabled: true,
				DNSServerIPAddresses: []string{"192.0.2.53", "2001:db8::53"}, SetDNSServerIPAddresses: true,
				DomainName: "guest.example", SetDomainName: true,
			},
			want: map[string]any{
				"name": "Guest", "enabled": true, "management": "GATEWAY", "vlanId": 30,
				"cellularBackupEnabled": false, "internetAccessEnabled": true, "isolationEnabled": false,
				"ipv4Configuration": map[string]any{
					"hostIpAddress": "192.0.2.1", "prefixLength": 24, "autoScaleEnabled": false,
					"dhcpConfiguration": map[string]any{
						"mode": "SERVER", "domainName": "guest.example",
						"dnsServerIpAddressesOverride": []string{"192.0.2.53", "2001:db8::53"},
						"ipAddressRange":               map[string]any{"start": "192.0.2.10", "stop": "192.0.2.200"},
						"leaseTimeSeconds":             86400, "pingConflictDetectionEnabled": false,
					},
				},
			},
		},
		{
			name: "switch DHCP relay",
			in: domain.NetworkInput{
				Name: "Building", Purpose: "switch", VLAN: intPtr(40), Subnet: "198.51.100.1/24", SetSubnet: true,
				Enabled: false, SetEnabled: true, DeviceID: switchDeviceID, SetDeviceID: true,
				DHCPMode: "relay", SetDHCPMode: true,
				DHCPRelayServerIPAddresses: []string{"198.51.100.10", "198.51.100.11"}, SetDHCPRelayServerIPAddresses: true,
			},
			want: map[string]any{
				"name": "Building", "enabled": false, "management": "SWITCH", "vlanId": 40,
				"cellularBackupEnabled": false, "deviceId": switchDeviceID, "isolationEnabled": false,
				"ipv4Configuration": map[string]any{
					"hostIpAddress": "198.51.100.1", "prefixLength": 24, "autoScaleEnabled": false,
					"dhcpConfiguration": map[string]any{"mode": "RELAY", "dhcpServerIpAddresses": []string{"198.51.100.10", "198.51.100.11"}},
				},
			},
		},
		{
			name: "gateway static addressing",
			in: domain.NetworkInput{
				Name: "Servers", Purpose: "gateway", VLAN: intPtr(50), Subnet: "203.0.113.1/24", SetSubnet: true,
				DHCPMode: "none", SetDHCPMode: true,
			},
			want: map[string]any{
				"name": "Servers", "enabled": true, "management": "GATEWAY", "vlanId": 50,
				"cellularBackupEnabled": false, "internetAccessEnabled": true, "isolationEnabled": false,
				"ipv4Configuration": map[string]any{"hostIpAddress": "203.0.113.1", "prefixLength": 24, "autoScaleEnabled": false},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := domain.NewNetworkService(networkMutationAPI()).Create(context.Background(), tt.in)
			if err != nil {
				t.Fatal(err)
			}
			if got := p.Changes[0].After; !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("create body = %#v\nwant = %#v", got, tt.want)
			}
		})
	}
}

func TestOfficialNetworkTypedDHCPValidationFailsBeforeWrite(t *testing.T) {
	base := domain.NetworkInput{Name: "Lab", Purpose: "gateway", VLAN: intPtr(30), Subnet: "192.0.2.1/24", SetSubnet: true, DHCPMode: "server", SetDHCPMode: true}
	tests := []struct {
		name   string
		mutate func(*domain.NetworkInput)
	}{
		{name: "missing range", mutate: func(in *domain.NetworkInput) {
			in.DHCPLeaseTimeSeconds, in.SetDHCPLeaseTimeSeconds = 3600, true
			in.SetDHCPConflictDetectionEnabled = true
		}},
		{name: "missing conflict flag", mutate: func(in *domain.NetworkInput) {
			in.DHCPRangeStart, in.SetDHCPRangeStart = "192.0.2.10", true
			in.DHCPRangeStop, in.SetDHCPRangeStop = "192.0.2.20", true
			in.DHCPLeaseTimeSeconds, in.SetDHCPLeaseTimeSeconds = 3600, true
		}},
		{name: "range outside subnet", mutate: func(in *domain.NetworkInput) {
			in.DHCPRangeStart, in.SetDHCPRangeStart = "198.51.100.10", true
			in.DHCPRangeStop, in.SetDHCPRangeStop = "198.51.100.20", true
			in.DHCPLeaseTimeSeconds, in.SetDHCPLeaseTimeSeconds = 3600, true
			in.SetDHCPConflictDetectionEnabled = true
		}},
		{name: "too many DNS servers", mutate: func(in *domain.NetworkInput) {
			in.DHCPRangeStart, in.SetDHCPRangeStart = "192.0.2.10", true
			in.DHCPRangeStop, in.SetDHCPRangeStop = "192.0.2.20", true
			in.DHCPLeaseTimeSeconds, in.SetDHCPLeaseTimeSeconds = 3600, true
			in.SetDHCPConflictDetectionEnabled = true
			in.DNSServerIPAddresses, in.SetDNSServerIPAddresses = []string{"192.0.2.1", "192.0.2.2", "192.0.2.3", "192.0.2.4", "192.0.2.5"}, true
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := base
			tt.mutate(&in)
			api := networkMutationAPI()
			_, err := domain.NewNetworkService(api).ApplyCreate(context.Background(), in)
			if !apperr.Is(err, apperr.ValidationFailed) {
				t.Fatalf("error = %v", err)
			}
			if got := len(nonGetMutationCalls(api.official)); got != 0 {
				t.Fatalf("writes = %d, want 0", got)
			}
		})
	}

	for _, in := range []domain.NetworkInput{
		{Name: "Switch", Purpose: "switch", VLAN: intPtr(40), Subnet: "192.0.2.1/24", SetSubnet: true, DHCPMode: "none", SetDHCPMode: true},
		{Name: "Relay", Purpose: "gateway", VLAN: intPtr(40), Subnet: "192.0.2.1/24", SetSubnet: true, DHCPMode: "relay", SetDHCPMode: true},
	} {
		api := networkMutationAPI()
		if _, err := domain.NewNetworkService(api).ApplyCreate(context.Background(), in); !apperr.Is(err, apperr.ValidationFailed) {
			t.Fatalf("input %#v error = %v", in, err)
		}
		if got := len(nonGetMutationCalls(api.official)); got != 0 {
			t.Fatalf("input %#v writes = %d", in, got)
		}
	}
}

func TestOfficialNetworkManagementTransitionRequiresCompleteTargetMode(t *testing.T) {
	api := networkMutationAPIForDocument(officialNetworkDocumentForManagement("UNMANAGED"))
	p, _, err := domain.NewNetworkService(api).Update(context.Background(), "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", domain.NetworkInput{
		Purpose: "gateway", SetPurpose: true, Subnet: "192.0.2.1/24", SetSubnet: true,
		DHCPMode: "none", SetDHCPMode: true,
	})
	if err != nil || p.Changes[0].ID == "" {
		t.Fatalf("complete management transition error = %v, plan = %#v", err, p)
	}

	api = networkMutationAPIForDocument(officialNetworkDocumentForManagement("UNMANAGED"))
	_, err = domain.NewNetworkService(api).ApplyUpdate(context.Background(), "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", domain.NetworkInput{Purpose: "gateway", SetPurpose: true})
	if !apperr.Is(err, apperr.ValidationFailed) {
		t.Fatalf("incomplete transition error = %v", err)
	}
	if got := len(mutationCalls(api.official, "PUT")); got != 0 {
		t.Fatalf("incomplete transition writes = %d", got)
	}
}

func TestOfficialNetworkTypedUpdatePlanCapturesCompleteChangedState(t *testing.T) {
	p, _, err := domain.NewNetworkService(networkMutationAPI()).Update(context.Background(), "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", domain.NetworkInput{
		Enabled: false, SetEnabled: true,
		DNSServerIPAddresses: []string{"192.0.2.53", "2001:db8::53"}, SetDNSServerIPAddresses: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	before := p.Changes[0].Before.(map[string]any)
	after := p.Changes[0].After.(map[string]any)
	if before["enabled"] != true || after["enabled"] != false {
		t.Fatalf("enabled plan = before %#v after %#v", before, after)
	}
	if !reflect.DeepEqual(after["dns_server_ip_addresses"], []string{"192.0.2.53", "2001:db8::53"}) || after["dhcp_mode"] != "server" {
		t.Fatalf("typed DHCP plan = %#v", after)
	}
}

func TestOfficialNetworkRejectsManagementSpecificFieldsInWrongMode(t *testing.T) {
	api := networkMutationAPI()
	_, err := domain.NewNetworkService(api).Create(context.Background(), domain.NetworkInput{
		Name: "Gateway", Purpose: "gateway", VLAN: intPtr(30), Subnet: "192.0.2.1/24", SetSubnet: true,
		DeviceID: switchDeviceID, SetDeviceID: true, DHCPMode: "none", SetDHCPMode: true,
	})
	if !apperr.Is(err, apperr.ValidationFailed) {
		t.Fatalf("gateway device error = %v", err)
	}

	api = networkMutationAPIForDocument(officialNetworkDocumentForManagement("UNMANAGED"))
	_, _, err = domain.NewNetworkService(api).Update(context.Background(), "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", domain.NetworkInput{
		Subnet: "192.0.2.1/24", SetSubnet: true,
	})
	if !apperr.Is(err, apperr.ValidationFailed) {
		t.Fatalf("unmanaged subnet error = %v", err)
	}
	if got := len(nonGetMutationCalls(api.official)); got != 0 {
		t.Fatalf("wrong-mode writes = %d", got)
	}
}

func TestOfficialNetworkCompleteTransitionAppliesOneFullDocument(t *testing.T) {
	api := networkMutationAPIForDocument(officialNetworkDocumentForManagement("GATEWAY"))
	id := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	detailPath := client.OfficialPath("sites", mutationSiteID, "networks", id)
	api.mutate = func(method, path string, in, out any) error {
		if method != http.MethodPut || path != detailPath {
			t.Fatalf("mutation = %s %s", method, path)
		}
		observed := cloneMutationTestValue(in).(map[string]any)
		observed["id"] = id
		observed["default"] = false
		observed["metadata"] = map[string]any{"origin": "USER"}
		api.details[detailPath] = observed
		return copyTestJSON(observed, out)
	}
	in := domain.NetworkInput{
		Purpose: "switch", SetPurpose: true, DeviceID: switchDeviceID, SetDeviceID: true,
		Subnet: "198.51.100.1/24", SetSubnet: true, DHCPMode: "relay", SetDHCPMode: true,
		DHCPRelayServerIPAddresses: []string{"198.51.100.10"}, SetDHCPRelayServerIPAddresses: true,
	}
	got, err := domain.NewNetworkService(api).ApplyUpdate(context.Background(), id, in)
	if err != nil {
		t.Fatal(err)
	}
	if got.Purpose != "switch" || got.ID != id {
		t.Fatalf("transition result = %#v", got)
	}
	puts := mutationCalls(api.official, http.MethodPut)
	if len(puts) != 1 {
		t.Fatalf("transition PUTs = %#v", puts)
	}
	want := map[string]any{
		"name": "LAN", "enabled": true, "management": "SWITCH", "vlanId": 20,
		"cellularBackupEnabled": false, "deviceId": switchDeviceID, "isolationEnabled": false,
		"dhcpGuarding": map[string]any{"trustedDhcpServerIpAddresses": []any{"192.0.2.2"}},
		"ipv4Configuration": map[string]any{
			"hostIpAddress": "198.51.100.1", "prefixLength": 24, "autoScaleEnabled": false,
			"dhcpConfiguration": map[string]any{"mode": "RELAY", "dhcpServerIpAddresses": []any{"198.51.100.10"}},
		},
	}
	if !reflect.DeepEqual(puts[0].body, cloneMutationTestValue(want)) {
		t.Fatalf("transition body = %#v\nwant = %#v", puts[0].body, want)
	}
}
