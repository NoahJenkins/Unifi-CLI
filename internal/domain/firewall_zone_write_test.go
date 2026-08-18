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

const zoneNetworkID = "cccccccc-cccc-4ccc-8ccc-ccccccccccc3"

func TestFirewallZoneCreatePlansAppliesAndVerifiesOfficialDocument(t *testing.T) {
	api := newModernFirewallAPI(t)
	api.postResponse = map[string]any{"id": createdZoneID}
	api.details[client.OfficialPath("sites", firewallSiteID, "firewall", "zones", createdZoneID)] = map[string]any{
		"id": createdZoneID, "name": "IoT", "networkIds": []any{zoneNetworkID},
		"metadata": map[string]any{"origin": "USER_DEFINED"},
	}
	in := domain.FirewallZoneInput{Name: "IoT", NetworkIDs: []string{zoneNetworkID}}
	svc := domain.NewFirewallService(api)

	p, err := svc.CreateZone(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if p.Changes[0].Resource != "firewall_zone" || p.Changes[0].Name != "IoT" {
		t.Fatalf("create plan = %#v", p)
	}
	got, err := svc.ApplyCreateZone(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != createdZoneID || got.Origin != "USER_DEFINED" || !reflect.DeepEqual(got.NetworkIDs, []string{zoneNetworkID}) {
		t.Fatalf("created zone = %#v", got)
	}
	writes := firewallCalls(api.calls, http.MethodPost)
	wantBody := map[string]any{"name": "IoT", "networkIds": []string{zoneNetworkID}}
	if len(writes) != 1 || !reflect.DeepEqual(writes[0].body, wantBody) {
		t.Fatalf("zone create writes = %#v, want body %#v", writes, wantBody)
	}
}

func TestFirewallZoneUpdatePreservesFullDocumentAndSystemZoneLimits(t *testing.T) {
	t.Run("user-defined name and networks", func(t *testing.T) {
		api := newModernFirewallAPI(t)
		api.putResponse = map[string]any{
			"id": labZoneID, "name": "Research", "networkIds": []any{zoneNetworkID},
			"metadata": map[string]any{"origin": "USER_DEFINED"},
		}
		in := domain.FirewallZoneInput{Name: "Research", SetName: true, NetworkIDs: []string{zoneNetworkID}, SetNetworkIDs: true}
		svc := domain.NewFirewallService(api)
		p, before, err := svc.UpdateZone(context.Background(), "Lab", in)
		if err != nil {
			t.Fatal(err)
		}
		if before.ID != labZoneID || p.Changes[0].ID != labZoneID {
			t.Fatalf("update plan = %#v before=%#v", p, before)
		}
		got, err := svc.ApplyUpdateZone(context.Background(), labZoneID, in)
		if err != nil {
			t.Fatal(err)
		}
		if got.Name != "Research" || !reflect.DeepEqual(got.NetworkIDs, []string{zoneNetworkID}) {
			t.Fatalf("updated zone = %#v", got)
		}
		writes := firewallCalls(api.calls, http.MethodPut)
		wantBody := map[string]any{"name": "Research", "networkIds": []string{zoneNetworkID}}
		if len(writes) != 1 || !reflect.DeepEqual(writes[0].body, wantBody) {
			t.Fatalf("zone update writes = %#v, want body %#v", writes, wantBody)
		}
	})

	t.Run("system-defined name is immutable", func(t *testing.T) {
		api := newModernFirewallAPI(t)
		_, _, err := domain.NewFirewallService(api).UpdateZone(context.Background(), "Internal", domain.FirewallZoneInput{Name: "Trusted", SetName: true})
		if !apperr.Is(err, apperr.ValidationFailed) {
			t.Fatalf("system rename error = %v", err)
		}
		if len(firewallMutationCalls(api.calls)) != 0 {
			t.Fatalf("system rename wrote: %#v", api.calls)
		}
	})

	t.Run("configurable system-defined networks can change", func(t *testing.T) {
		api := newModernFirewallAPI(t)
		api.putResponse = map[string]any{
			"id": internalZoneID, "name": "Internal", "networkIds": []any{zoneNetworkID},
			"metadata": map[string]any{"origin": "SYSTEM_DEFINED", "configurable": true},
		}
		got, err := domain.NewFirewallService(api).ApplyUpdateZone(context.Background(), "Internal", domain.FirewallZoneInput{NetworkIDs: []string{zoneNetworkID}, SetNetworkIDs: true})
		if err != nil {
			t.Fatal(err)
		}
		if got.Name != "Internal" || !got.Configurable {
			t.Fatalf("system zone update = %#v", got)
		}
	})
}

func TestFirewallZoneDeleteRejectsSystemAndVerifiesCapturedID(t *testing.T) {
	api := newModernFirewallAPI(t)
	svc := domain.NewFirewallService(api)
	if _, _, err := svc.DeleteZone(context.Background(), "External"); !apperr.Is(err, apperr.ValidationFailed) {
		t.Fatalf("system delete error = %v", err)
	}
	if len(firewallMutationCalls(api.calls)) != 0 {
		t.Fatalf("system delete wrote: %#v", api.calls)
	}

	p, before, err := svc.DeleteZone(context.Background(), "Lab")
	if err != nil {
		t.Fatal(err)
	}
	if before.ID != labZoneID || p.Changes[0].ID != labZoneID {
		t.Fatalf("delete plan = %#v before=%#v", p, before)
	}
	got, err := svc.ApplyDeleteZone(context.Background(), labZoneID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != labZoneID {
		t.Fatalf("deleted zone = %#v", got)
	}
	deletes := firewallCalls(api.calls, http.MethodDelete)
	if len(deletes) != 1 || deletes[0].path != client.OfficialPath("sites", firewallSiteID, "firewall", "zones", labZoneID) {
		t.Fatalf("zone delete calls = %#v", deletes)
	}
}

func TestFirewallZoneWritesFailClosedOnInvalidOrUnverifiedState(t *testing.T) {
	tests := []struct {
		name string
		in   domain.FirewallZoneInput
	}{
		{name: "missing name", in: domain.FirewallZoneInput{}},
		{name: "invalid network ID", in: domain.FirewallZoneInput{Name: "IoT", NetworkIDs: []string{"not-a-uuid"}}},
		{name: "duplicate network ID", in: domain.FirewallZoneInput{Name: "IoT", NetworkIDs: []string{zoneNetworkID, zoneNetworkID}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := newModernFirewallAPI(t)
			_, err := domain.NewFirewallService(api).CreateZone(context.Background(), tt.in)
			if !apperr.Is(err, apperr.ValidationFailed) {
				t.Fatalf("error = %v", err)
			}
			if len(firewallMutationCalls(api.calls)) != 0 {
				t.Fatalf("invalid create wrote: %#v", api.calls)
			}
		})
	}

	t.Run("created response must be user-defined", func(t *testing.T) {
		api := newModernFirewallAPI(t)
		api.postResponse = map[string]any{"id": createdZoneID}
		api.details[client.OfficialPath("sites", firewallSiteID, "firewall", "zones", createdZoneID)] = map[string]any{
			"id": createdZoneID, "name": "IoT", "networkIds": []any{},
			"metadata": map[string]any{"origin": "SYSTEM_DEFINED", "configurable": true},
		}
		_, err := domain.NewFirewallService(api).ApplyCreateZone(context.Background(), domain.FirewallZoneInput{Name: "IoT"})
		if !apperr.Is(err, apperr.Conflict) {
			t.Fatalf("origin verification error = %v", err)
		}
	})

	t.Run("delete must disappear", func(t *testing.T) {
		api := newModernFirewallAPI(t)
		api.retainDeletedDetail = true
		_, err := domain.NewFirewallService(api).ApplyDeleteZone(context.Background(), "Lab")
		if !apperr.Is(err, apperr.Conflict) {
			t.Fatalf("delete verification error = %v", err)
		}
	})
}
