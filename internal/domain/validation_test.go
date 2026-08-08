package domain_test

import (
	"context"
	"testing"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
	"github.com/noahjenkins/unifi-cli/internal/domain"
)

func TestNetworkInputValidation(t *testing.T) {
	ctx := context.Background()
	svc := domain.NewNetworkService(&fakeNetworkAPI{networks: fixtureNetworks(t)})

	invalidCreates := []domain.NetworkInput{
		{Name: "", Purpose: "corporate"},
		{Name: "Bad VLAN", Purpose: "corporate", VLAN: intPtr(0)},
		{Name: "Bad VLAN", Purpose: "corporate", VLAN: intPtr(4095)},
		{Name: "Bad CIDR", Purpose: "corporate", Subnet: "192.0.2.1/33"},
		{Name: "Bad purpose", Purpose: "unsupported"},
		{Name: "Bad domain", Purpose: "corporate", DomainName: "bad_name"},
	}
	for _, input := range invalidCreates {
		if _, err := svc.Create(ctx, input); !apperr.Is(err, apperr.ValidationFailed) {
			t.Errorf("Create(%+v) error = %v, want validation_failed", input, err)
		}
	}

	if _, _, err := svc.Update(ctx, "net2", domain.NetworkInput{}); !apperr.Is(err, apperr.ValidationFailed) {
		t.Fatalf("zero-field network update error = %v", err)
	}
}

func TestWlanInputValidation(t *testing.T) {
	ctx := context.Background()
	svc := domain.NewWlanService(&fakeWlanAPI{wlans: fixtureWlans(t)})

	for _, input := range []domain.WlanInput{
		{Name: "", Security: "open"},
		{Name: "Bad security", Security: "rot13"},
		{Name: "Bad band", Security: "open", Band: "900mhz"},
		{Name: "Missing PSK", Security: "wpapsk"},
		{Name: "Missing WPA2 PSK", Security: "wpa2_personal"},
		{Name: "Missing WPA3 PSK", Security: "wpa3_personal"},
		{Name: "Missing transition PSK", Security: "wpa2_wpa3_personal"},
	} {
		if _, err := svc.Create(ctx, input); !apperr.Is(err, apperr.ValidationFailed) {
			t.Errorf("Create(%+v) error = %v, want validation_failed", input, err)
		}
	}

	if _, _, err := svc.Update(ctx, "wlan2", domain.WlanInput{}); !apperr.Is(err, apperr.ValidationFailed) {
		t.Fatalf("zero-field WLAN update error = %v", err)
	}
}

func TestPortInputValidation(t *testing.T) {
	ctx := context.Background()
	svc := domain.NewPortService(&fakePortAPI{devices: devicesWithPorts()})

	if _, err := svc.Get(ctx, "Switch-Core", 0); !apperr.Is(err, apperr.ValidationFailed) {
		t.Fatalf("port index 0 error = %v", err)
	}
	if _, _, err := svc.Update(ctx, "Switch-Core", 12, domain.PortInput{}); !apperr.Is(err, apperr.ValidationFailed) {
		t.Fatalf("zero-field port update error = %v", err)
	}
	if _, _, err := svc.Update(ctx, "Switch-Core", 12, domain.PortInput{POE: "laser", SetPOE: true}); !apperr.Is(err, apperr.ValidationFailed) {
		t.Fatalf("unsupported PoE error = %v", err)
	}
}

func TestRequiredDeviceRenameAndResolverValidation(t *testing.T) {
	ctx := context.Background()
	deviceSvc := domain.NewDeviceService(&fakeDeviceAPI{devices: fixtureDevices(t)})
	if _, _, err := deviceSvc.Rename(ctx, "gw1", ""); !apperr.Is(err, apperr.ValidationFailed) {
		t.Fatalf("empty device name error = %v", err)
	}
	if _, err := deviceSvc.ApplyRename(ctx, "gw1", ""); !apperr.Is(err, apperr.ValidationFailed) {
		t.Fatalf("ApplyRename empty device name error = %v", err)
	}

	dnsAPI := &fakeDNSAPI{networks: fixtureNetworks(t)}
	dnsSvc := domain.NewDNSService(dnsAPI)
	for _, servers := range [][]string{nil, {"not-an-ip"}, {"192.0.2.53", ""}, {"192.0.2.1", "192.0.2.2", "192.0.2.3", "192.0.2.4", "192.0.2.5"}} {
		if _, _, err := dnsSvc.SetResolvers(ctx, "LAN", servers); !apperr.Is(err, apperr.ValidationFailed) {
			t.Errorf("SetResolvers(%q) error = %v, want validation_failed", servers, err)
		}
	}
}

func TestNullableUpdateFieldsUseExplicitClearSemantics(t *testing.T) {
	ctx := context.Background()

	networkSvc := domain.NewNetworkService(&fakeNetworkAPI{networks: fixtureNetworks(t)})
	networkPlan, _, err := networkSvc.Update(ctx, "net2", domain.NetworkInput{ClearDomainName: true})
	if err != nil {
		t.Fatal(err)
	}
	networkAfter := networkPlan.Changes[0].After.(map[string]any)
	if value, ok := networkAfter["domain_name"]; !ok || value != "" {
		t.Fatalf("cleared network domain = %#v", networkAfter)
	}

	portSvc := domain.NewPortService(&fakePortAPI{devices: devicesWithPorts()})
	portPlan, _, err := portSvc.Update(ctx, "Switch-Core", 12, domain.PortInput{ClearName: true})
	if err != nil {
		t.Fatal(err)
	}
	portAfter := portPlan.Changes[0].After.(map[string]any)
	if value, ok := portAfter["name"]; !ok || value != "" {
		t.Fatalf("cleared port name = %#v", portAfter)
	}

}
