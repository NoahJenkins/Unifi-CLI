package domain_test

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
	"github.com/noahjenkins/unifi-cli/internal/client"
	"github.com/noahjenkins/unifi-cli/internal/domain"
)

// The official named-protocol and preset discriminators are uppercase
// mapping keys (for example TCP, UDP, ICMP, ICMPV6, and TCP_UDP). The CLI
// reads them as lowercase, so every write must convert them back.

func TestFirewallExactCreateSendsOfficialUppercaseTCPProtocolScope(t *testing.T) {
	api := newModernFirewallAPI(t)
	api.postResponse = map[string]any{"id": createdPolicyID}
	api.details[client.OfficialPath("sites", firewallSiteID, "firewall", "policies", createdPolicyID)] = exactFirewallObservedPolicy()
	in := domain.FirewallInput{
		Name: "Allow one TCP service", Action: "allow", AllowReturnTraffic: true, SetAllowReturnTraffic: true,
		SourceZone: "Internal", DestinationZone: "External", IPVersion: "ipv4", Protocol: "tcp",
		SourceIP: "192.0.2.10", SetSourceIP: true,
		DestinationIP: "198.51.100.20", SetDestinationIP: true,
		DestinationPort: 1514, SetDestinationPort: true,
	}

	_, applyErr := domain.NewFirewallService(api).ApplyCreate(context.Background(), in)
	posts := firewallCalls(api.calls, http.MethodPost)
	if len(posts) != 1 {
		t.Fatalf("POST attempts = %d, want 1 (apply error: %v)", len(posts), applyErr)
	}
	scope, err := json.Marshal(posts[0].body.(map[string]any)["ipProtocolScope"])
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"ipVersion":"IPV4","protocolFilter":{"matchOpposite":false,"protocol":{"name":"TCP"},"type":"NAMED_PROTOCOL"}}`
	if string(scope) != want {
		t.Fatalf("ipProtocolScope = %s\nwant %s", scope, want)
	}
	if applyErr != nil {
		t.Fatalf("apply with official uppercase read-back: %v", applyErr)
	}
}

func TestFirewallCreateSendsOfficialProtocolDiscriminators(t *testing.T) {
	tests := []struct {
		ipVersion string
		protocol  string
		want      map[string]any
	}{
		{ipVersion: "ipv4", protocol: "tcp", want: map[string]any{"ipVersion": "IPV4", "protocolFilter": map[string]any{
			"type": "NAMED_PROTOCOL", "protocol": map[string]any{"name": "TCP"}, "matchOpposite": false,
		}}},
		{ipVersion: "ipv6", protocol: "udp", want: map[string]any{"ipVersion": "IPV6", "protocolFilter": map[string]any{
			"type": "NAMED_PROTOCOL", "protocol": map[string]any{"name": "UDP"}, "matchOpposite": false,
		}}},
		{ipVersion: "ipv4", protocol: "icmp", want: map[string]any{"ipVersion": "IPV4", "protocolFilter": map[string]any{
			"type": "NAMED_PROTOCOL", "protocol": map[string]any{"name": "ICMP"}, "matchOpposite": false,
		}}},
		{ipVersion: "ipv6", protocol: "icmpv6", want: map[string]any{"ipVersion": "IPV6", "protocolFilter": map[string]any{
			"type": "NAMED_PROTOCOL", "protocol": map[string]any{"name": "ICMPV6"}, "matchOpposite": false,
		}}},
		{ipVersion: "ipv4_and_ipv6", protocol: "tcp_udp", want: map[string]any{"ipVersion": "IPV4_AND_IPV6", "protocolFilter": map[string]any{
			"type": "PRESET", "preset": map[string]any{"name": "TCP_UDP"},
		}}},
		{ipVersion: "ipv4_and_ipv6", protocol: "all", want: map[string]any{"ipVersion": "IPV4_AND_IPV6"}},
	}
	for _, tt := range tests {
		t.Run(tt.ipVersion+"/"+tt.protocol, func(t *testing.T) {
			api := newModernFirewallAPI(t)
			observed := map[string]any{
				"id": createdPolicyID, "name": "Protocol scope", "enabled": true, "index": float64(120),
				"action": map[string]any{"type": "BLOCK"},
				"source": map[string]any{"zoneId": internalZoneID}, "destination": map[string]any{"zoneId": externalZoneID},
				"ipProtocolScope": tt.want, "loggingEnabled": false,
				"metadata": map[string]any{"origin": "USER_DEFINED"},
			}
			api.postResponse = map[string]any{"id": createdPolicyID}
			api.details[client.OfficialPath("sites", firewallSiteID, "firewall", "policies", createdPolicyID)] = observed
			in := domain.FirewallInput{
				Name: "Protocol scope", Action: "block", SourceZone: "Internal", DestinationZone: "External",
				IPVersion: tt.ipVersion, Protocol: tt.protocol,
			}

			_, applyErr := domain.NewFirewallService(api).ApplyCreate(context.Background(), in)
			posts := firewallCalls(api.calls, http.MethodPost)
			if len(posts) != 1 {
				t.Fatalf("POST attempts = %d, want 1 (apply error: %v)", len(posts), applyErr)
			}
			if got := posts[0].body.(map[string]any)["ipProtocolScope"]; !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("ipProtocolScope = %#v\nwant %#v", got, tt.want)
			}
			if applyErr != nil {
				t.Fatalf("apply with official read-back: %v", applyErr)
			}
		})
	}
}

func TestFirewallProtocolUpdateSendsOfficialUppercaseName(t *testing.T) {
	api := newModernFirewallAPI(t)
	updated := cloneFirewallMap(t, api.policies[0])
	mapFieldTest(mapFieldTest(updated, "ipProtocolScope"), "protocolFilter")["protocol"] = map[string]any{"name": "TCP"}
	api.putResponse = cloneFirewallMap(t, updated)
	api.details[client.OfficialPath("sites", firewallSiteID, "firewall", "policies", allowDNSPolicyID)] = cloneFirewallMap(t, updated)
	in := domain.FirewallInput{Protocol: "tcp", SetProtocol: true}

	_, applyErr := domain.NewFirewallService(api).ApplyUpdate(context.Background(), allowDNSPolicyID, in)
	puts := firewallCalls(api.calls, http.MethodPut)
	if len(puts) != 1 {
		t.Fatalf("PUT attempts = %d, want 1 (apply error: %v)", len(puts), applyErr)
	}
	want := map[string]any{"ipVersion": "IPV4", "protocolFilter": map[string]any{
		"type": "NAMED_PROTOCOL", "protocol": map[string]any{"name": "TCP"}, "matchOpposite": false,
	}}
	if got := puts[0].body.(map[string]any)["ipProtocolScope"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("ipProtocolScope = %#v\nwant %#v", got, want)
	}
	if applyErr != nil {
		t.Fatalf("apply with official read-back: %v", applyErr)
	}
}

func TestFirewallProtocolUpdateToCurrentOfficialNameIsNoOp(t *testing.T) {
	api := newModernFirewallAPI(t)
	scope := mapFieldTest(api.policies[0], "ipProtocolScope")
	if name := mapFieldTest(mapFieldTest(scope, "protocolFilter"), "protocol")["name"]; name != "UDP" {
		t.Fatalf("fixture protocol name = %#v, want official UDP", name)
	}

	_, _, err := domain.NewFirewallService(api).Update(context.Background(), allowDNSPolicyID, domain.FirewallInput{
		Protocol: "udp", SetProtocol: true,
	})
	if !apperr.Is(err, apperr.ValidationFailed) {
		t.Fatalf("error = %v, want no-op validation failure", err)
	}
	if calls := firewallMutationCalls(api.calls); len(calls) != 0 {
		t.Fatalf("no-op protocol update wrote: %+v", calls)
	}
}
