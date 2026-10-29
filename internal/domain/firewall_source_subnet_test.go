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
)

func exactSourceSubnetInput() domain.FirewallInput {
	return domain.FirewallInput{
		Name: "Allow one TCP service", Action: "allow", AllowReturnTraffic: true, SetAllowReturnTraffic: true,
		SourceZone: "Internal", DestinationZone: "External", IPVersion: "ipv4", Protocol: "tcp",
		SourceSubnet: "192.0.2.0/24", SetSourceSubnet: true,
		DestinationIP: "198.51.100.20", SetDestinationIP: true,
		DestinationPort: 1514, SetDestinationPort: true,
	}
}

func exactSourceSubnetObservedPolicy() map[string]any {
	observed := exactFirewallObservedPolicy()
	exactIPAddressFilter(observed, "source")["items"] = []any{map[string]any{"type": "SUBNET", "value": "192.0.2.0/24"}}
	return observed
}

func TestFirewallExactCreateSendsOneOfficialSourceSubnet(t *testing.T) {
	api := newModernFirewallAPI(t)
	observed := exactSourceSubnetObservedPolicy()
	api.postResponse = map[string]any{"id": createdPolicyID}
	api.details[client.OfficialPath("sites", firewallSiteID, "firewall", "policies", createdPolicyID)] = observed

	got, err := domain.NewFirewallService(api).ApplyCreate(context.Background(), exactSourceSubnetInput())
	if err != nil {
		t.Fatal(err)
	}
	posts := firewallCalls(api.calls, http.MethodPost)
	if len(posts) != 1 {
		t.Fatalf("POST attempts = %d, want 1", len(posts))
	}
	wantBody := cloneFirewallMap(t, observed)
	delete(wantBody, "id")
	delete(wantBody, "index")
	delete(wantBody, "metadata")
	if gotBody := cloneFirewallMap(t, posts[0].body.(map[string]any)); !reflect.DeepEqual(gotBody, wantBody) {
		t.Fatalf("source-subnet create body = %#v\nwant %#v", gotBody, wantBody)
	}
	if got.SourceFilter == nil || got.SourceFilter.IPAddressFilter == nil ||
		!reflect.DeepEqual(got.SourceFilter.IPAddressFilter.Items, []domain.FirewallIPMatch{{Type: "subnet", Value: "192.0.2.0/24"}}) {
		t.Fatalf("observed source filter = %#v", got.SourceFilter)
	}
}

func TestFirewallExactCreatePlanShowsSourceSubnet(t *testing.T) {
	api := newModernFirewallAPI(t)
	p, err := domain.NewFirewallService(api).Create(context.Background(), exactSourceSubnetInput())
	if err != nil {
		t.Fatal(err)
	}
	after, ok := p.Changes[0].After.(map[string]any)
	if !ok {
		t.Fatalf("plan after = %#v", p.Changes[0].After)
	}
	source, ok := after["source_filter"].(*domain.FirewallTrafficFilter)
	if !ok || source.IPAddressFilter == nil ||
		!reflect.DeepEqual(source.IPAddressFilter.Items, []domain.FirewallIPMatch{{Type: "subnet", Value: "192.0.2.0/24"}}) {
		t.Fatalf("plan source filter = %#v", after["source_filter"])
	}
	if calls := firewallMutationCalls(api.calls); len(calls) != 0 {
		t.Fatalf("plan wrote: %+v", calls)
	}
}

func TestFirewallExactCreateRejectsUnsupportedSourceSubnetWithoutWrite(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*domain.FirewallInput)
		want   string
	}{
		{name: "source IP and subnet together", want: "mutually exclusive", mutate: func(in *domain.FirewallInput) {
			in.SourceIP, in.SetSourceIP = "192.0.2.10", true
		}},
		{name: "subnet without destination", want: "together", mutate: func(in *domain.FirewallInput) {
			in.DestinationIP, in.SetDestinationIP = "", false
		}},
		{name: "subnet without port", want: "together", mutate: func(in *domain.FirewallInput) {
			in.DestinationPort, in.SetDestinationPort = 0, false
		}},
		{name: "empty subnet", want: "canonical", mutate: func(in *domain.FirewallInput) { in.SourceSubnet = "" }},
		{name: "host bits set", want: "canonical", mutate: func(in *domain.FirewallInput) { in.SourceSubnet = "192.0.2.1/24" }},
		{name: "bare address", want: "canonical", mutate: func(in *domain.FirewallInput) { in.SourceSubnet = "192.0.2.0" }},
		{name: "prefix below minimum", want: "/8", mutate: func(in *domain.FirewallInput) { in.SourceSubnet = "10.0.0.0/7" }},
		{name: "default route", want: "/8", mutate: func(in *domain.FirewallInput) { in.SourceSubnet = "0.0.0.0/0" }},
		{name: "prefix above maximum", want: "canonical", mutate: func(in *domain.FirewallInput) { in.SourceSubnet = "192.0.2.0/33" }},
		{name: "leading-zero prefix", want: "canonical", mutate: func(in *domain.FirewallInput) { in.SourceSubnet = "192.0.2.0/024" }},
		{name: "leading-zero octet", want: "canonical", mutate: func(in *domain.FirewallInput) { in.SourceSubnet = "192.0.02.0/24" }},
		{name: "IPv6 subnet", want: "canonical", mutate: func(in *domain.FirewallInput) { in.SourceSubnet = "2001:db8::/32" }},
		{name: "IPv4-mapped IPv6 subnet", want: "canonical", mutate: func(in *domain.FirewallInput) { in.SourceSubnet = "::ffff:192.0.2.0/120" }},
		{name: "range", want: "canonical", mutate: func(in *domain.FirewallInput) { in.SourceSubnet = "192.0.2.0-192.0.2.255" }},
		{name: "list", want: "canonical", mutate: func(in *domain.FirewallInput) { in.SourceSubnet = "192.0.2.0/24,198.51.100.0/24" }},
		{name: "any", want: "canonical", mutate: func(in *domain.FirewallInput) { in.SourceSubnet = "any" }},
		{name: "whitespace", want: "canonical", mutate: func(in *domain.FirewallInput) { in.SourceSubnet = " 192.0.2.0/24" }},
		{name: "block action", want: "allow", mutate: func(in *domain.FirewallInput) { in.Action = "block" }},
		{name: "missing return traffic", want: "allow", mutate: func(in *domain.FirewallInput) { in.AllowReturnTraffic = false }},
		{name: "dual stack scope", want: "ipv4", mutate: func(in *domain.FirewallInput) { in.IPVersion = "ipv4_and_ipv6" }},
		{name: "UDP protocol", want: "tcp", mutate: func(in *domain.FirewallInput) { in.Protocol = "udp" }},
		{name: "destination subnet", want: "canonical", mutate: func(in *domain.FirewallInput) { in.DestinationIP = "198.51.100.0/24" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := newModernFirewallAPI(t)
			in := exactSourceSubnetInput()
			tt.mutate(&in)
			_, err := domain.NewFirewallService(api).Create(context.Background(), in)
			if !apperr.Is(err, apperr.ValidationFailed) || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want validation_failed containing %q", err, tt.want)
			}
			if calls := firewallMutationCalls(api.calls); len(calls) != 0 {
				t.Fatalf("invalid source-subnet input wrote: %+v", calls)
			}
		})
	}
}

func TestFirewallExactCreateAcceptsSourceSubnetPrefixBounds(t *testing.T) {
	for _, subnet := range []string{"10.0.0.0/8", "192.0.2.10/32"} {
		t.Run(subnet, func(t *testing.T) {
			in := exactSourceSubnetInput()
			in.SourceSubnet = subnet
			if _, err := domain.NewFirewallService(newModernFirewallAPI(t)).Create(context.Background(), in); err != nil {
				t.Fatalf("subnet %s: %v", subnet, err)
			}
		})
	}
}

func TestFirewallSourceSubnetVerificationFailsClosed(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "broadened subnet", mutate: func(observed map[string]any) {
			exactIPItems(observed, "source")[0].(map[string]any)["value"] = "192.0.0.0/16"
		}},
		{name: "narrowed subnet", mutate: func(observed map[string]any) {
			exactIPItems(observed, "source")[0].(map[string]any)["value"] = "192.0.2.0/25"
		}},
		{name: "non-canonical subnet", mutate: func(observed map[string]any) {
			exactIPItems(observed, "source")[0].(map[string]any)["value"] = "192.0.2.1/24"
		}},
		{name: "subnet reported as address", mutate: func(observed map[string]any) {
			exactIPItems(observed, "source")[0].(map[string]any)["type"] = "IP_ADDRESS"
		}},
		{name: "subnet replaced by range", mutate: func(observed map[string]any) {
			exactIPAddressFilter(observed, "source")["items"] = []any{map[string]any{"type": "IP_ADDRESS_RANGE", "start": "192.0.2.0", "stop": "192.0.2.255"}}
		}},
		{name: "extra source subnet", mutate: func(observed map[string]any) {
			filter := exactIPAddressFilter(observed, "source")
			filter["items"] = append(exactIPItems(observed, "source"), map[string]any{"type": "SUBNET", "value": "198.51.100.0/24"})
		}},
		{name: "opposite source subnet", mutate: func(observed map[string]any) { exactIPAddressFilter(observed, "source")["matchOpposite"] = true }},
		{name: "malformed subnet item", mutate: func(observed map[string]any) {
			exactIPAddressFilter(observed, "source")["items"] = []any{map[string]any{"type": "SUBNET"}}
		}},
		{name: "source filter removed", mutate: func(observed map[string]any) { delete(mapFieldTest(observed, "source"), "trafficFilter") }},
		{name: "destination broadened", mutate: func(observed map[string]any) {
			exactIPAddressFilter(observed, "destination")["items"] = []any{map[string]any{"type": "SUBNET", "value": "198.51.100.0/24"}}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := newModernFirewallAPI(t)
			observed := cloneFirewallMap(t, exactSourceSubnetObservedPolicy())
			tt.mutate(observed)
			api.postResponse = map[string]any{"id": createdPolicyID}
			path := client.OfficialPath("sites", firewallSiteID, "firewall", "policies", createdPolicyID)
			api.details[path] = observed

			_, err := domain.NewFirewallService(api).ApplyCreate(context.Background(), exactSourceSubnetInput())
			if err == nil {
				t.Fatal("verification accepted a changed source-subnet policy")
			}
			if !apperr.Is(err, apperr.Conflict) && !strings.Contains(strings.ToLower(err.Error()), "verif") {
				t.Fatalf("error = %v, want verification failure", err)
			}
			assertFirewallCall(t, api.calls, http.MethodPost, client.OfficialPath("sites", firewallSiteID, "firewall", "policies"), 1)
		})
	}
}
