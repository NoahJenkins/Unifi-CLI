package domain_test

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
	"github.com/noahjenkins/unifi-cli/internal/client"
	"github.com/noahjenkins/unifi-cli/internal/domain"
	"github.com/noahjenkins/unifi-cli/internal/plan"
)

const mdnsNetworkID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"

func mdnsNetworkPath() string {
	return client.OfficialPath("sites", mutationSiteID, "networks", mdnsNetworkID)
}

// echoNetworkPUT stores the PUT body as the observed document, then lets the
// test change the observed state before verification.
func echoNetworkPUT(t *testing.T, api *officialMutationAPI, change func(map[string]any)) {
	t.Helper()
	api.mutate = func(method, gotPath string, in, out any) error {
		if method != http.MethodPut || gotPath != mdnsNetworkPath() {
			t.Fatalf("mutation = %s %s", method, gotPath)
		}
		observed := cloneMutationTestValue(in).(map[string]any)
		observed["id"] = mdnsNetworkID
		observed["default"] = false
		observed["metadata"] = map[string]any{"origin": "USER", "configurable": true}
		if change != nil {
			change(observed)
		}
		api.details[mdnsNetworkPath()] = observed
		return copyTestJSON(observed, out)
	}
}

func boolPtr(value bool) *bool { return &value }

func TestNetworkMDNSForwardingUpdateChangesOnlyThatField(t *testing.T) {
	for _, tt := range []struct {
		name    string
		current any
		want    bool
	}{
		{name: "disable", current: true, want: false},
		{name: "enable", current: false, want: true},
		{name: "enable from site default", current: nil, want: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			doc := officialNetworkDocument()
			if tt.current == nil {
				delete(doc, "mdnsForwardingEnabled")
			} else {
				doc["mdnsForwardingEnabled"] = tt.current
			}
			api := networkMutationAPIForDocument(doc)
			echoNetworkPUT(t, api, nil)
			svc := domain.NewNetworkService(api)
			in := domain.NetworkInput{MDNSForwardingEnabled: tt.want, SetMDNSForwardingEnabled: true}

			p, n, err := svc.Update(context.Background(), mdnsNetworkID, in)
			if err != nil {
				t.Fatal(err)
			}
			before := p.Changes[0].Before.(map[string]any)
			after := p.Changes[0].After.(map[string]any)
			if got, present := before["mdns_forwarding_enabled"]; tt.current == nil && present || tt.current != nil && got != tt.current {
				t.Fatalf("plan before mDNS = %#v (present %t)", got, present)
			}
			if after["mdns_forwarding_enabled"] != tt.want {
				t.Fatalf("plan after mDNS = %#v", after["mdns_forwarding_enabled"])
			}
			for key, value := range before {
				if key != "mdns_forwarding_enabled" && !reflect.DeepEqual(after[key], value) {
					t.Fatalf("plan changed %q: %#v -> %#v", key, value, after[key])
				}
			}

			prepared, err := plan.Targeted(p, n.ID, p.Changes, plan.HighImpact, true)
			if err != nil {
				t.Fatal(err)
			}
			target, _ := prepared.Target()
			got, err := svc.ApplyUpdatePrepared(context.Background(), target, mdnsNetworkID, in)
			if err != nil {
				t.Fatal(err)
			}
			if got.MDNSForwardingEnabled == nil || *got.MDNSForwardingEnabled != tt.want {
				t.Fatalf("observed mDNS = %v", got.MDNSForwardingEnabled)
			}
			puts := mutationCalls(api.official, http.MethodPut)
			if len(puts) != 1 {
				t.Fatalf("PUT count = %d, want 1", len(puts))
			}
			wantBody := cloneMutationTestValue(doc).(map[string]any)
			for _, responseOnly := range []string{"id", "default", "metadata"} {
				delete(wantBody, responseOnly)
			}
			wantBody["mdnsForwardingEnabled"] = tt.want
			if !reflect.DeepEqual(puts[0].body, wantBody) {
				t.Fatalf("PUT body = %#v\nwant %#v", puts[0].body, wantBody)
			}
			if len(api.legacy) != 0 {
				t.Fatalf("legacy calls = %+v", api.legacy)
			}
		})
	}
}

func TestNetworkMDNSForwardingUpdateRejectsUnsupportedRequestsWithoutWrite(t *testing.T) {
	tests := []struct {
		name string
		doc  map[string]any
		in   domain.NetworkInput
		code apperr.Code
		want string
	}{
		{name: "no-op", doc: officialNetworkDocument(), code: apperr.ValidationFailed, want: "would not change",
			in: domain.NetworkInput{MDNSForwardingEnabled: true, SetMDNSForwardingEnabled: true}},
		{name: "switch-managed network", doc: officialNetworkDocumentForManagement("SWITCH"), code: apperr.ValidationFailed, want: "gateway-managed",
			in: domain.NetworkInput{MDNSForwardingEnabled: true, SetMDNSForwardingEnabled: true}},
		{name: "unmanaged network", doc: officialNetworkDocumentForManagement("UNMANAGED"), code: apperr.ValidationFailed, want: "gateway-managed",
			in: domain.NetworkInput{MDNSForwardingEnabled: true, SetMDNSForwardingEnabled: true}},
		{name: "transition away from gateway", doc: officialNetworkDocument(), code: apperr.ValidationFailed, want: "gateway-managed",
			in: domain.NetworkInput{Purpose: "unmanaged", SetPurpose: true, MDNSForwardingEnabled: false, SetMDNSForwardingEnabled: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := networkMutationAPIForDocument(tt.doc)
			_, _, err := domain.NewNetworkService(api).Update(context.Background(), mdnsNetworkID, tt.in)
			if !apperr.Is(err, tt.code) || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %s containing %q", err, tt.code, tt.want)
			}
			_, err = domain.NewNetworkService(api).ApplyUpdate(context.Background(), mdnsNetworkID, tt.in)
			if !apperr.Is(err, tt.code) {
				t.Fatalf("apply error = %v, want %s", err, tt.code)
			}
			if got := len(mutationCalls(api.official, http.MethodPut)); got != 0 {
				t.Fatalf("PUT count = %d, want 0", got)
			}
		})
	}
}

func TestNetworkMDNSForwardingRequiresOfficialNetworkAPI(t *testing.T) {
	api := newFixedIPAPI()
	in := domain.NetworkInput{MDNSForwardingEnabled: true, SetMDNSForwardingEnabled: true}
	_, _, err := domain.NewNetworkService(api).Update(context.Background(), "network-1", in)
	if !apperr.Is(err, apperr.ValidationFailed) || !strings.Contains(err.Error(), "official") {
		t.Fatalf("plan error = %v, want official API requirement", err)
	}
	_, err = domain.NewNetworkService(api).ApplyUpdate(context.Background(), "network-1", in)
	if !apperr.Is(err, apperr.ValidationFailed) {
		t.Fatalf("apply error = %v, want validation_failed", err)
	}
	for _, call := range api.calls {
		if call.method != http.MethodGet {
			t.Fatalf("legacy mDNS request wrote: %+v", call)
		}
	}
}

func TestNetworkMDNSForwardingVerificationFailsClosedWithoutRetry(t *testing.T) {
	tests := []struct {
		name   string
		change func(map[string]any)
	}{
		{name: "mDNS not applied", change: func(observed map[string]any) { observed["mdnsForwardingEnabled"] = true }},
		{name: "mDNS removed", change: func(observed map[string]any) { delete(observed, "mdnsForwardingEnabled") }},
		{name: "isolation changed", change: func(observed map[string]any) { observed["isolationEnabled"] = true }},
		{name: "name changed", change: func(observed map[string]any) { observed["name"] = "Other" }},
		{name: "DHCP range changed", change: func(observed map[string]any) {
			ipv4 := observed["ipv4Configuration"].(map[string]any)
			dhcp := ipv4["dhcpConfiguration"].(map[string]any)
			dhcp["ipAddressRange"] = map[string]any{"start": "192.0.2.20", "stop": "192.0.2.200"}
		}},
		{name: "field added", change: func(observed map[string]any) { observed["zoneId"] = "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := networkMutationAPI()
			echoNetworkPUT(t, api, tt.change)
			_, err := domain.NewNetworkService(api).ApplyUpdate(context.Background(), mdnsNetworkID,
				domain.NetworkInput{MDNSForwardingEnabled: false, SetMDNSForwardingEnabled: true})
			if !apperr.Is(err, apperr.Conflict) || !strings.Contains(err.Error(), "verification") {
				t.Fatalf("error = %v, want verification conflict", err)
			}
			if got := len(mutationCalls(api.official, http.MethodPut)); got != 1 {
				t.Fatalf("PUT count = %d, want 1", got)
			}
		})
	}
}

func TestNetworkMDNSForwardingDriftIsRejectedBeforeWrite(t *testing.T) {
	api := networkMutationAPI()
	echoNetworkPUT(t, api, nil)
	svc := domain.NewNetworkService(api)
	in := domain.NetworkInput{MDNSForwardingEnabled: false, SetMDNSForwardingEnabled: true}
	p, n, err := svc.Update(context.Background(), mdnsNetworkID, in)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := plan.Targeted(p, n.ID, p.Changes, plan.HighImpact, true)
	if err != nil {
		t.Fatal(err)
	}
	target, _ := prepared.Target()
	api.details[mdnsNetworkPath()]["name"] = "LAN Renamed"

	_, err = svc.ApplyUpdatePrepared(context.Background(), target, mdnsNetworkID, in)
	if !apperr.Is(err, apperr.Conflict) || !strings.Contains(err.Error(), "target changed") {
		t.Fatalf("error = %v, want drift conflict", err)
	}
	if got := len(mutationCalls(api.official, http.MethodPut)); got != 0 {
		t.Fatalf("PUT count = %d, want 0", got)
	}
}

func TestNetworkReadsExposeOfficialMDNSForwardingState(t *testing.T) {
	enabled := officialNetworkDocument()
	disabled := officialNetworkDocument()
	disabled["id"] = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	disabled["name"] = "IoT"
	disabled["mdnsForwardingEnabled"] = false
	siteDefault := officialNetworkDocument()
	siteDefault["id"] = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	siteDefault["name"] = "Lab"
	delete(siteDefault, "mdnsForwardingEnabled")

	overview := func(doc map[string]any) map[string]any {
		return map[string]any{
			"id": doc["id"], "name": doc["name"], "enabled": true, "default": false,
			"management": "GATEWAY", "vlanId": doc["vlanId"], "metadata": doc["metadata"],
		}
	}
	api := &officialMutationAPI{
		collections: map[string][]map[string]any{
			client.OfficialPath("sites", mutationSiteID, "networks"): {overview(enabled), overview(disabled), overview(siteDefault)},
		},
		details: map[string]map[string]any{},
	}
	for _, doc := range []map[string]any{enabled, disabled, siteDefault} {
		api.details[client.OfficialPath("sites", mutationSiteID, "networks", doc["id"].(string))] = doc
	}

	items, err := domain.NewNetworkService(api).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []*bool{boolPtr(true), boolPtr(false), nil}
	if len(items) != len(want) {
		t.Fatalf("networks = %+v", items)
	}
	for i, item := range items {
		if !reflect.DeepEqual(item.MDNSForwardingEnabled, want[i]) {
			t.Fatalf("network %s mDNS = %v, want %v", item.Name, item.MDNSForwardingEnabled, want[i])
		}
		if item.Subnet != "192.0.2.1/24" || !item.DHCPEnabled {
			t.Fatalf("network %s did not use detail state: %+v", item.Name, item)
		}
	}
	if got := len(mutationCalls(api.official, http.MethodGet)); got != 3 {
		t.Fatalf("detail GET count = %d, want 3", got)
	}

	got, err := domain.NewNetworkService(api).Get(context.Background(), "IoT")
	if err != nil {
		t.Fatal(err)
	}
	if got.MDNSForwardingEnabled == nil || *got.MDNSForwardingEnabled {
		t.Fatalf("get mDNS = %v, want false", got.MDNSForwardingEnabled)
	}
}

func TestNetworkListDetailFailureReturnsNoPartialResult(t *testing.T) {
	api := networkMutationAPI()
	api.detailErrors = map[string]error{mdnsNetworkPath(): apperr.New(apperr.PermissionDenied, "forbidden")}
	items, err := domain.NewNetworkService(api).List(context.Background())
	if !apperr.Is(err, apperr.PermissionDenied) || items != nil {
		t.Fatalf("items=%+v error=%v, want permission error without partial result", items, err)
	}
}
