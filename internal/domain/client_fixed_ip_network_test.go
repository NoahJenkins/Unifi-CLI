package domain_test

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
	"github.com/noahjenkins/unifi-cli/internal/domain"
	"github.com/noahjenkins/unifi-cli/internal/plan"
)

// newFixedIPAPIWithoutUserNetwork models a known client whose legacy user
// record has no stored network ID while the client is connected to network-1.
func newFixedIPAPIWithoutUserNetwork() *fixedIPAPI {
	api := newFixedIPAPI()
	api.clients[0]["ip"] = "192.0.2.20"
	api.clients[1]["ip"] = "192.0.2.30"
	api.users[1]["fixed_ip"] = "192.0.2.60"
	api.network["ip_subnet"] = "192.0.2.1/24"
	delete(api.users[0], "network_id")
	api.networks = []map[string]any{
		{"_id": "network-2", "name": "IoT", "ip_subnet": "198.51.100.1/24", "dhcpd_enabled": true},
	}
	return api
}

func TestClientFixedIPSetSelectsExplicitNetworkWhenUserRecordHasNone(t *testing.T) {
	for _, selector := range []string{"LAN", "network-1"} {
		t.Run(selector, func(t *testing.T) {
			api := newFixedIPAPIWithoutUserNetwork()
			svc := domain.NewClientFixedIPService(api)
			ctx := context.Background()

			p, snapshot, err := svc.SetOnNetwork(ctx, "Laptop", "192.0.2.50", selector)
			if err != nil {
				t.Fatal(err)
			}
			if snapshot.NetworkID != "network-1" || snapshot.ReservationNetworkID != "" || snapshot.Subnet != "192.0.2.1/24" {
				t.Fatalf("snapshot = %+v", snapshot)
			}
			wantBefore := domain.ClientFixedIPReservation{
				ClientID: "client-1", MAC: "001122334455", Name: "Laptop", NetworkID: "",
				FixedIPEnabled: false, FixedIP: "",
			}
			wantAfter := domain.ClientFixedIPReservation{
				ClientID: "client-1", MAC: "001122334455", Name: "Laptop", NetworkID: "network-1",
				FixedIPEnabled: true, FixedIP: "192.0.2.50",
			}
			if !reflect.DeepEqual(p.Changes[0].Before, wantBefore) || !reflect.DeepEqual(p.Changes[0].After, wantAfter) {
				t.Fatalf("plan before=%#v after=%#v", p.Changes[0].Before, p.Changes[0].After)
			}

			prepared, err := plan.Targeted(p, snapshot.ClientID, snapshot, plan.HighImpact, true)
			if err != nil {
				t.Fatal(err)
			}
			target, _ := prepared.Target()
			got, err := svc.ApplySetOnNetworkPrepared(ctx, target, snapshot.ClientID, "192.0.2.50", selector)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, wantAfter) {
				t.Fatalf("result = %#v, want %#v", got, wantAfter)
			}
			var puts []mutateCall
			for _, call := range api.calls {
				if call.method == http.MethodPut {
					puts = append(puts, call)
				}
			}
			wantBody := map[string]any{"_id": "client-1", "use_fixedip": true, "network_id": "network-1", "fixed_ip": "192.0.2.50"}
			if len(puts) != 1 || !reflect.DeepEqual(puts[0].body, wantBody) {
				t.Fatalf("PUTs = %#v, want one body %#v", puts, wantBody)
			}
		})
	}
}

func TestClientFixedIPSetAcceptsExplicitNetworkForOfflineClient(t *testing.T) {
	api := newFixedIPAPIWithoutUserNetwork()
	api.clients = api.clients[1:]

	_, snapshot, err := domain.NewClientFixedIPService(api).SetOnNetwork(context.Background(), "Laptop", "198.51.100.50", "IoT")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.NetworkID != "network-2" || snapshot.Subnet != "198.51.100.1/24" {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}

func TestClientFixedIPSetAcceptsMatchingNetworkForStoredUserNetwork(t *testing.T) {
	api := newFixedIPAPIWithoutUserNetwork()
	api.users[0]["network_id"] = "network-1"
	_, snapshot, err := domain.NewClientFixedIPService(api).SetOnNetwork(context.Background(), "Laptop", "192.0.2.50", "LAN")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.NetworkID != "network-1" || snapshot.ReservationNetworkID != "network-1" {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}

func TestClientFixedIPSetNetworkSelectionFailsClosedWithoutWriting(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*fixedIPAPI)
		network string
		fixedIP string
		code    apperr.Code
		want    string
	}{
		{name: "missing user network without selector", network: "", fixedIP: "192.0.2.50", code: apperr.Conflict, want: "--network"},
		{name: "unknown network", network: "Missing", fixedIP: "192.0.2.50", code: apperr.NotFound, want: "not found"},
		{name: "ambiguous network name", network: "LAN", fixedIP: "192.0.2.50", code: apperr.AmbiguousID, want: "multiple",
			mutate: func(api *fixedIPAPI) {
				api.networks = append(api.networks, map[string]any{"_id": "network-3", "name": "LAN", "ip_subnet": "203.0.113.1/24", "dhcpd_enabled": true})
			}},
		{name: "connected address outside selected network", network: "IoT", fixedIP: "198.51.100.50", code: apperr.Conflict, want: "current IPv4 address"},
		{name: "connected network differs from selected network", network: "LAN", fixedIP: "192.0.2.50", code: apperr.Conflict, want: "connected to a different network",
			mutate: func(api *fixedIPAPI) { api.clients[0]["network_id"] = "network-2" }},
		{name: "connected address is not IPv4", network: "LAN", fixedIP: "192.0.2.50", code: apperr.Conflict, want: "current IPv4 address",
			mutate: func(api *fixedIPAPI) { api.clients[0]["ip"] = "2001:db8::20" }},
		{name: "fixed IP outside selected network", network: "LAN", fixedIP: "198.51.100.50", code: apperr.ValidationFailed, want: "inside"},
		{name: "gateway of selected network", network: "LAN", fixedIP: "192.0.2.1", code: apperr.ValidationFailed, want: "gateway"},
		{name: "selected network without DHCP", network: "LAN", fixedIP: "192.0.2.50", code: apperr.ValidationFailed, want: "DHCP enabled",
			mutate: func(api *fixedIPAPI) { api.network["dhcpd_enabled"] = false }},
		{name: "address reserved by another client", network: "LAN", fixedIP: "192.0.2.60", code: apperr.Conflict, want: "already reserved"},
		{name: "selector differs from stored user network", network: "IoT", fixedIP: "198.51.100.50", code: apperr.Conflict, want: "different network",
			mutate: func(api *fixedIPAPI) { api.users[0]["network_id"] = "network-1" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := newFixedIPAPIWithoutUserNetwork()
			if tt.mutate != nil {
				tt.mutate(api)
			}
			_, _, err := domain.NewClientFixedIPService(api).SetOnNetwork(context.Background(), "client-1", tt.fixedIP, tt.network)
			if !apperr.Is(err, tt.code) || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %s containing %q", err, tt.code, tt.want)
			}
			if got := putCount(api.calls); got != 0 {
				t.Fatalf("PUT count = %d, want 0", got)
			}
		})
	}
}

func TestClientFixedIPSelectedNetworkDriftIsRejectedBeforeWrite(t *testing.T) {
	api := newFixedIPAPIWithoutUserNetwork()
	svc := domain.NewClientFixedIPService(api)
	p, snapshot, err := svc.SetOnNetwork(context.Background(), "client-1", "192.0.2.50", "LAN")
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := plan.Targeted(p, snapshot.ClientID, snapshot, plan.HighImpact, true)
	if err != nil {
		t.Fatal(err)
	}
	target, _ := prepared.Target()
	api.users[0]["network_id"] = "network-1"

	_, err = svc.ApplySetOnNetworkPrepared(context.Background(), target, snapshot.ClientID, "192.0.2.50", "LAN")
	if !apperr.Is(err, apperr.Conflict) || !strings.Contains(err.Error(), "target changed") {
		t.Fatalf("error = %v, want drift conflict", err)
	}
	if got := putCount(api.calls); got != 0 {
		t.Fatalf("PUT count = %d, want 0", got)
	}
}

func TestClientFixedIPSetVerifiesSelectedNetworkAfterWriteWithoutRetry(t *testing.T) {
	tests := []struct {
		name     string
		afterPUT func(*fixedIPAPI)
	}{
		{name: "stored network differs", afterPUT: func(api *fixedIPAPI) { api.users[0]["network_id"] = "network-2" }},
		{name: "stored network missing", afterPUT: func(api *fixedIPAPI) { delete(api.users[0], "network_id") }},
		{name: "network subnet changed", afterPUT: func(api *fixedIPAPI) { api.network["ip_subnet"] = "192.0.2.1/25" }},
		{name: "network DHCP disabled", afterPUT: func(api *fixedIPAPI) { api.network["dhcpd_enabled"] = false }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := newFixedIPAPIWithoutUserNetwork()
			api.afterPUT = tt.afterPUT
			svc := domain.NewClientFixedIPService(api)
			p, snapshot, err := svc.SetOnNetwork(context.Background(), "client-1", "192.0.2.50", "LAN")
			if err != nil {
				t.Fatal(err)
			}
			prepared, err := plan.Targeted(p, snapshot.ClientID, snapshot, plan.HighImpact, true)
			if err != nil {
				t.Fatal(err)
			}
			target, _ := prepared.Target()

			_, err = svc.ApplySetOnNetworkPrepared(context.Background(), target, snapshot.ClientID, "192.0.2.50", "LAN")
			if !apperr.Is(err, apperr.Conflict) || !strings.Contains(err.Error(), "verification failed") {
				t.Fatalf("error = %v, want verification conflict", err)
			}
			if got := putCount(api.calls); got != 1 {
				t.Fatalf("PUT count = %d, want 1", got)
			}
		})
	}
}
