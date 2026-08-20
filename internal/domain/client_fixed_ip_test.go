package domain_test

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
	"github.com/noahjenkins/unifi-cli/internal/client"
	"github.com/noahjenkins/unifi-cli/internal/domain"
	"github.com/noahjenkins/unifi-cli/internal/plan"
)

type fixedIPAPI struct {
	clients   []map[string]any
	users     []map[string]any
	network   map[string]any
	calls     []mutateCall
	ignorePUT bool
}

func newFixedIPAPI() *fixedIPAPI {
	return &fixedIPAPI{
		clients: []map[string]any{
			{"_id": "client-1", "mac": "00:11:22:33:44:55", "name": "Laptop", "ip": "192.168.1.20", "network_id": "network-1"},
			{"_id": "client-2", "mac": "00:11:22:33:44:66", "name": "Printer", "ip": "192.168.1.30", "network_id": "network-1"},
		},
		users: []map[string]any{
			{"_id": "client-1", "mac": "00:11:22:33:44:55", "name": "Laptop", "network_id": "network-1", "use_fixedip": false},
			{"_id": "client-2", "mac": "00:11:22:33:44:66", "name": "Printer", "network_id": "network-1", "use_fixedip": true, "fixed_ip": "192.168.1.60"},
		},
		network: map[string]any{"_id": "network-1", "name": "LAN", "ip_subnet": "192.168.1.1/24", "dhcpd_enabled": true},
	}
}

func (f *fixedIPAPI) SitePath(parts ...string) string {
	p := "/proxy/network/api/s/default"
	for _, part := range parts {
		p += "/" + part
	}
	return p
}

func (f *fixedIPAPI) Do(_ context.Context, method, path string, in, out any) error {
	f.calls = append(f.calls, mutateCall{method: method, path: path, body: in})
	if method == http.MethodPut {
		if f.ignorePUT {
			return nil
		}
		body := in.(map[string]any)
		for _, user := range f.users {
			if user["_id"] != body["_id"] {
				continue
			}
			for key, value := range body {
				user[key] = value
			}
		}
		return nil
	}

	var value any
	switch path {
	case f.SitePath("stat/sta"):
		value = f.clients
	case f.SitePath("rest/user"):
		value = f.users
	case f.SitePath("rest/networkconf", "network-1"):
		value = []map[string]any{f.network}
	case f.SitePath("rest/user", "client-1"):
		value = []map[string]any{f.users[0]}
	default:
		value = []map[string]any{}
	}
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

func putCount(calls []mutateCall) int {
	count := 0
	for _, call := range calls {
		if call.method == http.MethodPut {
			count++
		}
	}
	return count
}

func TestClientFixedIPListReturnsOnlyEnabledReservationsInDeterministicOrder(t *testing.T) {
	api := newFixedIPAPI()
	api.users[0]["fixed_ip"] = "192.168.1.50"
	api.users = append(api.users, map[string]any{
		"_id": "client-3", "mac": "00:11:22:33:44:77", "name": "Camera",
		"network_id": "network-1", "use_fixedip": true, "fixed_ip": "192.168.1.70",
	})

	got, err := domain.NewClientFixedIPService(api).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []domain.ClientFixedIPReservation{
		{ClientID: "client-3", MAC: "001122334477", Name: "Camera", NetworkID: "network-1", FixedIPEnabled: true, FixedIP: "192.168.1.70"},
		{ClientID: "client-2", MAC: "001122334466", Name: "Printer", NetworkID: "network-1", FixedIPEnabled: true, FixedIP: "192.168.1.60"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("reservations = %#v, want %#v", got, want)
	}
}

func TestClientFixedIPGetResolvesKnownUsersAndHidesInactiveStoredAddress(t *testing.T) {
	api := newFixedIPAPI()
	api.users[0]["fixed_ip"] = "192.168.1.50"
	svc := domain.NewClientFixedIPService(api)

	for _, query := range []string{"client-1", "00-11-22-33-44-55", "Laptop"} {
		got, err := svc.Get(context.Background(), query)
		if err != nil {
			t.Fatalf("get %q: %v", query, err)
		}
		want := domain.ClientFixedIPReservation{
			ClientID: "client-1", MAC: "001122334455", Name: "Laptop", NetworkID: "network-1",
			FixedIPEnabled: false, FixedIP: "",
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("get %q = %#v, want %#v", query, got, want)
		}
	}

	if _, err := svc.Get(context.Background(), "Missing"); !apperr.Is(err, apperr.NotFound) {
		t.Fatalf("missing error = %v, want not_found", err)
	}
	api.users = append(api.users, map[string]any{
		"_id": "client-3", "mac": "00:11:22:33:44:77", "name": "Laptop",
		"network_id": "network-1", "use_fixedip": false,
	})
	if _, err := svc.Get(context.Background(), "Laptop"); !apperr.Is(err, apperr.AmbiguousID) {
		t.Fatalf("ambiguous error = %v, want ambiguous_id", err)
	}
}

func TestClientFixedIPOfflineSetAndClearUseTheLegacyUserRecord(t *testing.T) {
	t.Run("set", func(t *testing.T) {
		api := newFixedIPAPI()
		api.clients = api.clients[1:]
		p, snapshot, err := domain.NewClientFixedIPService(api).Set(context.Background(), "Laptop", "192.168.1.50")
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.ClientID != "client-1" || snapshot.MAC != "001122334455" || snapshot.NetworkID != "network-1" {
			t.Fatalf("snapshot = %+v", snapshot)
		}
		if p.Changes[0].ID != "client-1" {
			t.Fatalf("plan target = %q, want client-1", p.Changes[0].ID)
		}
	})

	t.Run("clear", func(t *testing.T) {
		api := newFixedIPAPI()
		api.clients = api.clients[1:]
		api.users[0]["use_fixedip"] = true
		api.users[0]["fixed_ip"] = "192.168.1.50"
		_, snapshot, err := domain.NewClientFixedIPService(api).Clear(context.Background(), "Laptop")
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.ClientID != "client-1" || !snapshot.FixedIPEnabled {
			t.Fatalf("snapshot = %+v", snapshot)
		}
	})
}

func TestClientFixedIPSetPlansWritesAndVerifiesReservation(t *testing.T) {
	api := newFixedIPAPI()
	svc := domain.NewClientFixedIPService(api)
	ctx := context.Background()

	p, snapshot, err := svc.Set(ctx, "Laptop", "192.168.1.50")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ClientID != "client-1" || snapshot.NetworkID != "network-1" || snapshot.Subnet != "192.168.1.1/24" {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	if len(p.Changes) != 1 {
		t.Fatalf("plan = %+v", p)
	}
	after := p.Changes[0].After
	wantAfter := domain.ClientFixedIPReservation{
		ClientID: "client-1", MAC: "001122334455", Name: "Laptop", NetworkID: "network-1",
		FixedIPEnabled: true, FixedIP: "192.168.1.50",
	}
	if !reflect.DeepEqual(after, wantAfter) {
		t.Fatalf("after = %#v, want %#v", after, wantAfter)
	}

	prepared, err := plan.Targeted(p, snapshot.ClientID, snapshot, plan.HighImpact, true)
	if err != nil {
		t.Fatal(err)
	}
	target, _ := prepared.Target()
	got, err := svc.ApplySetPrepared(ctx, target, snapshot.ClientID, "192.168.1.50")
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
	if len(puts) != 1 {
		t.Fatalf("PUT count = %d, want 1", len(puts))
	}
	if puts[0].path != api.SitePath("rest/user", "client-1") {
		t.Fatalf("PUT path = %q", puts[0].path)
	}
	wantBody := map[string]any{
		"_id": "client-1", "use_fixedip": true, "network_id": "network-1", "fixed_ip": "192.168.1.50",
	}
	if !reflect.DeepEqual(puts[0].body, wantBody) {
		t.Fatalf("PUT body = %#v, want %#v", puts[0].body, wantBody)
	}
}

func TestClientFixedIPClearDisablesReservationWithoutErasingStoredAddress(t *testing.T) {
	api := newFixedIPAPI()
	api.users[0]["use_fixedip"] = true
	api.users[0]["fixed_ip"] = "192.168.1.50"
	svc := domain.NewClientFixedIPService(api)
	ctx := context.Background()

	p, snapshot, err := svc.Clear(ctx, "00-11-22-33-44-55")
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := plan.Targeted(p, snapshot.ClientID, snapshot, plan.HighImpact, true)
	if err != nil {
		t.Fatal(err)
	}
	target, _ := prepared.Target()
	got, err := svc.ApplyClearPrepared(ctx, target, snapshot.ClientID)
	if err != nil {
		t.Fatal(err)
	}
	if got.FixedIPEnabled || got.FixedIP != "" {
		t.Fatalf("result = %+v, want disabled effective reservation", got)
	}
	if api.users[0]["fixed_ip"] != "192.168.1.50" {
		t.Fatalf("stored fixed_ip = %v, want retained", api.users[0]["fixed_ip"])
	}

	var put mutateCall
	for _, call := range api.calls {
		if call.method == http.MethodPut {
			put = call
		}
	}
	wantBody := map[string]any{"_id": "client-1", "use_fixedip": false}
	if !reflect.DeepEqual(put.body, wantBody) {
		t.Fatalf("PUT body = %#v, want %#v", put.body, wantBody)
	}
}

func TestClientFixedIPSetRejectsUnsafeAddressesWithoutWriting(t *testing.T) {
	tests := []struct {
		name    string
		fixedIP string
		mutate  func(*fixedIPAPI)
		code    apperr.Code
		want    string
	}{
		{name: "invalid", fixedIP: "999.1.1.1", code: apperr.ValidationFailed, want: "valid IPv4"},
		{name: "IPv6", fixedIP: "2001:db8::1", code: apperr.ValidationFailed, want: "valid IPv4"},
		{name: "DHCP disabled", fixedIP: "192.168.1.50", mutate: func(api *fixedIPAPI) { api.network["dhcpd_enabled"] = false }, code: apperr.ValidationFailed, want: "DHCP enabled"},
		{name: "malformed subnet", fixedIP: "192.168.1.50", mutate: func(api *fixedIPAPI) { api.network["ip_subnet"] = "bad" }, code: apperr.ValidationFailed, want: "usable IPv4 subnet"},
		{name: "outside subnet", fixedIP: "192.168.2.50", code: apperr.ValidationFailed, want: "inside"},
		{name: "network address", fixedIP: "192.168.1.0", code: apperr.ValidationFailed, want: "network, broadcast, or gateway"},
		{name: "broadcast address", fixedIP: "192.168.1.255", code: apperr.ValidationFailed, want: "network, broadcast, or gateway"},
		{name: "gateway address", fixedIP: "192.168.1.1", code: apperr.ValidationFailed, want: "network, broadcast, or gateway"},
		{name: "reserved by another client", fixedIP: "192.168.1.60", code: apperr.Conflict, want: "already reserved"},
		{name: "used by another connected client", fixedIP: "192.168.1.30", code: apperr.Conflict, want: "currently used"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := newFixedIPAPI()
			if tt.mutate != nil {
				tt.mutate(api)
			}
			_, _, err := domain.NewClientFixedIPService(api).Set(context.Background(), "client-1", tt.fixedIP)
			if !apperr.Is(err, tt.code) || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %s containing %q", err, tt.code, tt.want)
			}
			if got := putCount(api.calls); got != 0 {
				t.Fatalf("PUT count = %d, want 0", got)
			}
		})
	}
}

func TestClientFixedIPRejectsNoOpsAndMissingIdentityFields(t *testing.T) {
	t.Run("set no-op", func(t *testing.T) {
		api := newFixedIPAPI()
		api.users[0]["use_fixedip"] = true
		api.users[0]["fixed_ip"] = "192.168.1.50"
		_, _, err := domain.NewClientFixedIPService(api).Set(context.Background(), "client-1", "192.168.1.50")
		if !apperr.Is(err, apperr.ValidationFailed) || !strings.Contains(err.Error(), "already") {
			t.Fatalf("error = %v, want no-op validation", err)
		}
	})

	t.Run("clear no-op", func(t *testing.T) {
		api := newFixedIPAPI()
		_, _, err := domain.NewClientFixedIPService(api).Clear(context.Background(), "client-1")
		if !apperr.Is(err, apperr.ValidationFailed) || !strings.Contains(err.Error(), "already disabled") {
			t.Fatalf("error = %v, want no-op validation", err)
		}
	})

	t.Run("missing user network ID", func(t *testing.T) {
		api := newFixedIPAPI()
		delete(api.users[0], "network_id")
		_, _, err := domain.NewClientFixedIPService(api).Set(context.Background(), "client-1", "192.168.1.50")
		if !apperr.Is(err, apperr.Conflict) || !strings.Contains(err.Error(), "network ID") {
			t.Fatalf("error = %v, want missing network conflict", err)
		}
	})

	t.Run("invalid user MAC", func(t *testing.T) {
		api := newFixedIPAPI()
		api.users[0]["mac"] = ""
		_, _, err := domain.NewClientFixedIPService(api).Set(context.Background(), "client-1", "192.168.1.50")
		if !apperr.Is(err, apperr.Conflict) || !strings.Contains(err.Error(), "MAC address") {
			t.Fatalf("error = %v, want MAC conflict", err)
		}
	})

	t.Run("ambiguous user name", func(t *testing.T) {
		api := newFixedIPAPI()
		api.users[1]["name"] = "Laptop"
		_, _, err := domain.NewClientFixedIPService(api).Set(context.Background(), "Laptop", "192.168.1.50")
		if !apperr.Is(err, apperr.AmbiguousID) {
			t.Fatalf("error = %v, want ambiguous_id", err)
		}
	})
}

func TestClientFixedIPApplyRejectsDriftBeforeWrite(t *testing.T) {
	api := newFixedIPAPI()
	svc := domain.NewClientFixedIPService(api)
	p, snapshot, err := svc.Set(context.Background(), "client-1", "192.168.1.50")
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := plan.Targeted(p, snapshot.ClientID, snapshot, plan.HighImpact, true)
	if err != nil {
		t.Fatal(err)
	}
	target, _ := prepared.Target()
	api.network["ip_subnet"] = "192.168.1.1/25"

	_, err = svc.ApplySetPrepared(context.Background(), target, snapshot.ClientID, "192.168.1.50")
	if !apperr.Is(err, apperr.Conflict) || !strings.Contains(err.Error(), "target changed") {
		t.Fatalf("error = %v, want drift conflict", err)
	}
	if got := putCount(api.calls); got != 0 {
		t.Fatalf("PUT count = %d, want 0", got)
	}
}

func TestClientFixedIPApplyRejectsPostWriteMismatchWithoutRetry(t *testing.T) {
	api := newFixedIPAPI()
	api.ignorePUT = true
	svc := domain.NewClientFixedIPService(api)
	p, snapshot, err := svc.Set(context.Background(), "client-1", "192.168.1.50")
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := plan.Targeted(p, snapshot.ClientID, snapshot, plan.HighImpact, true)
	if err != nil {
		t.Fatal(err)
	}
	target, _ := prepared.Target()

	_, err = svc.ApplySetPrepared(context.Background(), target, snapshot.ClientID, "192.168.1.50")
	if !apperr.Is(err, apperr.Conflict) || !strings.Contains(err.Error(), "verification failed") {
		t.Fatalf("error = %v, want verification conflict", err)
	}
	if got := putCount(api.calls); got != 1 {
		t.Fatalf("PUT count = %d, want 1", got)
	}
}

func TestClientFixedIPClearRejectsPostWriteMismatchWithoutRetry(t *testing.T) {
	api := newFixedIPAPI()
	api.users[0]["use_fixedip"] = true
	api.users[0]["fixed_ip"] = "192.168.1.50"
	api.ignorePUT = true
	svc := domain.NewClientFixedIPService(api)
	p, snapshot, err := svc.Clear(context.Background(), "client-1")
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := plan.Targeted(p, snapshot.ClientID, snapshot, plan.HighImpact, true)
	if err != nil {
		t.Fatal(err)
	}
	target, _ := prepared.Target()

	_, err = svc.ApplyClearPrepared(context.Background(), target, snapshot.ClientID)
	if !apperr.Is(err, apperr.Conflict) || !strings.Contains(err.Error(), "verification failed") {
		t.Fatalf("error = %v, want verification conflict", err)
	}
	if got := putCount(api.calls); got != 1 {
		t.Fatalf("PUT count = %d, want 1", got)
	}
}

func TestClientFixedIPTranslatesOfficialClientIDToLegacyUserID(t *testing.T) {
	api := newOfficialReadAPI(t)
	api.legacy[api.SitePath(client.PathStatSta)] = []map[string]any{{
		"_id": "legacy-client", "name": "Laptop", "mac": "11:22:33:44:55:01",
		"ip": "192.0.2.50", "network_id": "legacy-network",
	}}
	user := map[string]any{
		"_id": "legacy-client", "name": "Laptop", "mac": "11:22:33:44:55:01",
		"network_id": "legacy-network", "use_fixedip": false,
	}
	api.legacy[api.SitePath(client.PathRestUser)] = []map[string]any{user}
	api.legacy[api.SitePath(client.PathRestUser, "legacy-client")] = []map[string]any{user}
	api.legacy[api.SitePath(client.PathRestNetwork, "legacy-network")] = []map[string]any{{
		"_id": "legacy-network", "name": "LAN", "ip_subnet": "192.0.2.1/24", "dhcpd_enabled": true,
	}}

	p, snapshot, err := domain.NewClientFixedIPService(api).Set(context.Background(), officialWirelessID, "192.0.2.60")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ClientID != "legacy-client" || p.Changes[0].ID != "legacy-client" {
		t.Fatalf("snapshot=%+v plan=%+v, want legacy target", snapshot, p)
	}
	for _, call := range api.calls {
		if strings.HasPrefix(call, "LEGACY ") && strings.Contains(call, officialWirelessID) {
			t.Fatalf("official UUID reached legacy request: %s", call)
		}
	}
}
