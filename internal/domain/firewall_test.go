package domain_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
	"github.com/noahjenkins/unifi-cli/internal/client"
	"github.com/noahjenkins/unifi-cli/internal/domain"
)

const (
	firewallSiteID      = "11111111-1111-4111-8111-111111111111"
	internalZoneID      = "ffffffff-ffff-4fff-8fff-fffffffffff1"
	externalZoneID      = "ffffffff-ffff-4fff-8fff-fffffffffff2"
	allowDNSPolicyID    = "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeee1"
	blockWebPolicyID    = "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeee2"
	systemGuardPolicyID = "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeee3"
	createdPolicyID     = "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeee9"
)

type modernFirewallCall struct {
	method string
	path   string
	body   any
}

type modernFirewallAPI struct {
	zones               []map[string]any
	policies            []map[string]any
	details             map[string]map[string]any
	orderingReads       []domain.FirewallOrdering
	orderingReadIndex   int
	postResponse        map[string]any
	putResponse         map[string]any
	retainDeletedDetail bool
	calls               []modernFirewallCall
	errByMethodAndPath  map[string]error
}

func newModernFirewallAPI(t *testing.T) *modernFirewallAPI {
	t.Helper()
	zones := officialFixtureData(t, "firewall-zones.json")
	policies := officialFixtureData(t, "firewall-policies-complete.json")
	api := &modernFirewallAPI{
		zones:    zones,
		policies: policies,
		details:  make(map[string]map[string]any),
		orderingReads: []domain.FirewallOrdering{{
			BeforeSystemDefined: []string{allowDNSPolicyID},
			AfterSystemDefined:  []string{blockWebPolicyID},
		}},
		errByMethodAndPath: make(map[string]error),
	}
	for _, zone := range zones {
		api.details[client.OfficialPath("sites", firewallSiteID, "firewall", "zones", zone["id"].(string))] = cloneFirewallMap(t, zone)
	}
	for _, policy := range policies {
		api.details[client.OfficialPath("sites", firewallSiteID, "firewall", "policies", policy["id"].(string))] = cloneFirewallMap(t, policy)
	}
	return api
}

func (f *modernFirewallAPI) IntegrationSitePath(_ context.Context, parts ...string) (string, error) {
	all := append([]string{"sites", firewallSiteID}, parts...)
	return client.OfficialPath(all...), nil
}

func (f *modernFirewallAPI) FetchOfficialObjects(_ context.Context, path string) ([]map[string]any, error) {
	f.calls = append(f.calls, modernFirewallCall{method: "LIST", path: path})
	if err := f.errByMethodAndPath["LIST "+path]; err != nil {
		return nil, err
	}
	switch path {
	case client.OfficialPath("sites", firewallSiteID, "firewall", "zones"):
		return cloneFirewallMaps(f.zones), nil
	case client.OfficialPath("sites", firewallSiteID, "firewall", "policies"):
		return cloneFirewallMaps(f.policies), nil
	default:
		return nil, errors.New("unexpected official collection path: " + path)
	}
}

func (f *modernFirewallAPI) DoOfficial(_ context.Context, method, path string, in, out any) error {
	f.calls = append(f.calls, modernFirewallCall{method: method, path: path, body: in})
	if err := f.errByMethodAndPath[method+" "+path]; err != nil {
		return err
	}
	parsed, err := url.Parse(path)
	if err != nil {
		return err
	}
	orderingPath := client.OfficialPath("sites", firewallSiteID, "firewall", "policies", "ordering")
	if parsed.Path == orderingPath {
		switch method {
		case http.MethodGet:
			if f.orderingReadIndex >= len(f.orderingReads) {
				return errors.New("unexpected extra ordering read")
			}
			ordering := f.orderingReads[f.orderingReadIndex]
			f.orderingReadIndex++
			return decodeFirewallInto(map[string]any{"orderedFirewallPolicyIds": map[string]any{
				"beforeSystemDefined": ordering.BeforeSystemDefined,
				"afterSystemDefined":  ordering.AfterSystemDefined,
			}}, out)
		case http.MethodPut:
			return decodeFirewallInto(in, out)
		}
	}
	if method == http.MethodGet {
		item, ok := f.details[parsed.Path]
		if !ok {
			return apperr.New(apperr.NotFound, "not found")
		}
		return decodeFirewallInto(item, out)
	}
	policiesPath := client.OfficialPath("sites", firewallSiteID, "firewall", "policies")
	switch {
	case method == http.MethodPost && parsed.Path == policiesPath:
		return decodeFirewallInto(f.postResponse, out)
	case method == http.MethodPut && strings.HasPrefix(parsed.Path, policiesPath+"/"):
		return decodeFirewallInto(f.putResponse, out)
	case method == http.MethodDelete && strings.HasPrefix(parsed.Path, policiesPath+"/"):
		if !f.retainDeletedDetail {
			delete(f.details, parsed.Path)
		}
		return nil
	default:
		return errors.New("unexpected official request: " + method + " " + path)
	}
}

// The fake never uses this fallback; it exists only to prove that firewall
// code no longer sends classic rest/firewallrule requests.
func (f *modernFirewallAPI) Do(_ context.Context, method, path string, in, out any) error {
	f.calls = append(f.calls, modernFirewallCall{method: "LEGACY " + method, path: path, body: in})
	return errors.New("legacy firewall request")
}

func (f *modernFirewallAPI) SitePath(parts ...string) string {
	return "/proxy/network/api/s/default/" + strings.Join(parts, "/")
}

func TestFirewallZoneListAndGetUseOfficialSchemaAndSelectors(t *testing.T) {
	ctx := context.Background()
	api := newModernFirewallAPI(t)
	svc := domain.NewFirewallService(api)

	zones, err := svc.ListZones(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(zones) != 3 || zones[0].Name != "External" || zones[1].Name != "Internal" || zones[2].Name != "Lab" {
		t.Fatalf("zones = %+v", zones)
	}
	if zones[1].ID != internalZoneID || zones[1].Origin != "SYSTEM_DEFINED" || !zones[1].Configurable ||
		!reflect.DeepEqual(zones[1].NetworkIDs, []string{"cccccccc-cccc-4ccc-8ccc-ccccccccccc1"}) {
		t.Fatalf("normalized internal zone = %+v", zones[1])
	}

	byName, err := svc.GetZone(ctx, "Internal")
	if err != nil {
		t.Fatal(err)
	}
	if byName.ID != internalZoneID || byName.Name != "Internal" {
		t.Fatalf("zone by name = %+v", byName)
	}
	assertFirewallCall(t, api.calls, http.MethodGet, client.OfficialPath("sites", firewallSiteID, "firewall", "zones", internalZoneID), 1)

	api = newModernFirewallAPI(t)
	api.zones = append(api.zones, map[string]any{
		"id": "ffffffff-ffff-4fff-8fff-fffffffffff9", "name": "Internal", "networkIds": []any{},
		"metadata": map[string]any{"origin": "USER_DEFINED"},
	})
	if _, err := domain.NewFirewallService(api).GetZone(ctx, "Internal"); !apperr.Is(err, apperr.AmbiguousID) {
		t.Fatalf("ambiguous zone error = %v", err)
	}
	if _, err := domain.NewFirewallService(api).GetZone(ctx, "Missing"); !apperr.Is(err, apperr.NotFound) {
		t.Fatalf("missing zone error = %v", err)
	}
}

func TestFirewallPoliciesNormalizeOfficialZonesActionsAndProtocols(t *testing.T) {
	api := newModernFirewallAPI(t)
	items, err := domain.NewFirewallService(api).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 || items[0].ID != allowDNSPolicyID || items[0].Name != "Allow DNS" {
		t.Fatalf("policies = %+v", items)
	}
	want := domain.FirewallRule{
		ID: allowDNSPolicyID, Name: "Allow DNS", Description: "Permit DNS from internal clients",
		Enabled: true, Action: "allow", SourceZoneID: internalZoneID, DestinationZoneID: externalZoneID,
		Protocol: "ipv4:udp", LoggingEnabled: true, Index: 100, Origin: "USER_DEFINED",
	}
	if !reflect.DeepEqual(items[0], want) {
		t.Fatalf("normalized policy = %+v, want %+v", items[0], want)
	}
	if items[2].Action != "block" || items[2].Protocol != "ipv4_and_ipv6:tcp_udp" {
		t.Fatalf("second normalized policy = %+v", items[2])
	}
	for _, call := range api.calls {
		if strings.HasPrefix(call.method, "LEGACY") || strings.Contains(call.path, "rest/firewallrule") {
			t.Fatalf("legacy firewall read: %+v", call)
		}
	}
}

func TestFirewallCompleteFixtureUsesOfficialNestedDiscriminatorShapes(t *testing.T) {
	policies := newModernFirewallAPI(t).policies
	policy := policies[0]

	t.Run("all policies contain required writable and response fields", func(t *testing.T) {
		for i, item := range policies {
			for _, field := range []string{"id", "name", "enabled", "index", "action", "source", "destination", "ipProtocolScope", "loggingEnabled", "metadata"} {
				if _, ok := item[field]; !ok {
					t.Errorf("policy %d missing required field %s", i, field)
				}
			}
		}
	})

	t.Run("action variants contain only their required fields", func(t *testing.T) {
		allow := policies[0]["action"].(map[string]any)
		if allow["type"] != "ALLOW" || allow["allowReturnTraffic"] != false {
			t.Fatalf("ALLOW action = %#v", allow)
		}
		for _, index := range []int{1, 2} {
			action := policies[index]["action"].(map[string]any)
			if _, unsupported := action["allowReturnTraffic"]; unsupported {
				t.Fatalf("%s action contains ALLOW-only field: %#v", action["type"], action)
			}
		}
	})

	t.Run("IP_ADDRESS uses required typed nested filters", func(t *testing.T) {
		source := policy["source"].(map[string]any)
		traffic := source["trafficFilter"].(map[string]any)
		ipFilter := traffic["ipAddressFilter"].(map[string]any)
		items, ok := ipFilter["items"].([]any)
		if traffic["type"] != "IP_ADDRESS" || ipFilter["type"] != "IP_ADDRESSES" || ipFilter["matchOpposite"] != false || !ok || len(items) != 1 {
			t.Fatalf("IP_ADDRESS filter = %#v", traffic)
		}
		subnet := items[0].(map[string]any)
		if subnet["type"] != "SUBNET" || subnet["value"] != "192.0.2.0/24" {
			t.Fatalf("IP_ADDRESS item = %#v", subnet)
		}
	})

	t.Run("PORTS uses typed items", func(t *testing.T) {
		source := policy["source"].(map[string]any)
		traffic := source["trafficFilter"].(map[string]any)
		ports := traffic["portFilter"].(map[string]any)
		if _, invented := ports["ports"]; invented {
			t.Fatalf("PORTS filter contains unsupported ports field: %#v", ports)
		}
		items, ok := ports["items"].([]any)
		if !ok || len(items) != 1 {
			t.Fatalf("PORTS items = %#v, want one typed item", ports["items"])
		}
		item, ok := items[0].(map[string]any)
		if !ok || item["type"] != "PORT_NUMBER_RANGE" || item["start"] != float64(1024) || item["stop"] != float64(65535) {
			t.Fatalf("PORTS item = %#v, want typed number range", items[0])
		}
	})

	t.Run("DOMAIN uses required domainFilter", func(t *testing.T) {
		destination := policy["destination"].(map[string]any)
		traffic := destination["trafficFilter"].(map[string]any)
		if _, invented := traffic["domain"]; invented {
			t.Fatalf("DOMAIN filter contains unsupported domain field: %#v", traffic)
		}
		domainFilter, ok := traffic["domainFilter"].(map[string]any)
		if !ok || domainFilter["type"] != "DOMAINS" || !reflect.DeepEqual(domainFilter["domains"], []any{"resolver.example.test"}) {
			t.Fatalf("DOMAIN domainFilter = %#v", traffic["domainFilter"])
		}
	})

	t.Run("EVERY_DAY uses required timeFilter", func(t *testing.T) {
		schedule := policy["schedule"].(map[string]any)
		if _, invented := schedule["startTime"]; invented {
			t.Fatalf("EVERY_DAY contains unsupported flat time fields: %#v", schedule)
		}
		timeFilter, ok := schedule["timeFilter"].(map[string]any)
		if !ok || timeFilter["startTime"] != "08:00" || timeFilter["stopTime"] != "18:00" {
			t.Fatalf("EVERY_DAY timeFilter = %#v", schedule["timeFilter"])
		}
	})

	t.Run("IP protocol variants contain required nested discriminators", func(t *testing.T) {
		named := policies[0]["ipProtocolScope"].(map[string]any)
		namedFilter := named["protocolFilter"].(map[string]any)
		if named["ipVersion"] != "IPV4" || namedFilter["type"] != "NAMED_PROTOCOL" || namedFilter["matchOpposite"] != false || namedFilter["protocol"].(map[string]any)["name"] != "udp" {
			t.Fatalf("NAMED_PROTOCOL scope = %#v", named)
		}
		preset := policies[1]["ipProtocolScope"].(map[string]any)
		presetFilter := preset["protocolFilter"].(map[string]any)
		if preset["ipVersion"] != "IPV4_AND_IPV6" || presetFilter["type"] != "PRESET" || presetFilter["preset"].(map[string]any)["name"] != "TCP_UDP" {
			t.Fatalf("PRESET scope = %#v", preset)
		}
		all := policies[2]["ipProtocolScope"].(map[string]any)
		if all["ipVersion"] != "IPV4_AND_IPV6" {
			t.Fatalf("all-protocol scope = %#v", all)
		}
		if _, unexpected := all["protocolFilter"]; unexpected {
			t.Fatalf("all-protocol scope contains filter: %#v", all)
		}
	})
}

func TestFirewallCreateResolvesZonesAndSendsCompleteOfficialPolicyDocument(t *testing.T) {
	ctx := context.Background()
	api := newModernFirewallAPI(t)
	created := map[string]any{
		"id": createdPolicyID, "name": "Allow HTTPS", "enabled": true, "index": float64(120),
		"action": map[string]any{"type": "ALLOW", "allowReturnTraffic": false},
		"source": map[string]any{"zoneId": internalZoneID}, "destination": map[string]any{"zoneId": externalZoneID},
		"ipProtocolScope": map[string]any{"ipVersion": "IPV4", "protocolFilter": map[string]any{"type": "NAMED_PROTOCOL", "protocol": map[string]any{"name": "tcp"}, "matchOpposite": false}},
		"loggingEnabled":  false, "metadata": map[string]any{"origin": "USER_DEFINED"},
	}
	api.postResponse = cloneFirewallMap(t, created)
	api.details[client.OfficialPath("sites", firewallSiteID, "firewall", "policies", createdPolicyID)] = cloneFirewallMap(t, created)
	in := domain.FirewallInput{
		Name: "Allow HTTPS", Action: "allow", SourceZone: "Internal", DestinationZone: externalZoneID,
		IPVersion: "ipv4", Protocol: "tcp",
	}

	p, err := domain.NewFirewallService(api).Create(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	after := p.Changes[0].After.(map[string]any)
	if after["source_zone_id"] != internalZoneID || after["destination_zone_id"] != externalZoneID {
		t.Fatalf("resolved plan = %#v", after)
	}
	got, err := domain.NewFirewallService(api).ApplyCreate(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != createdPolicyID || got.SourceZoneID != internalZoneID || got.DestinationZoneID != externalZoneID {
		t.Fatalf("controller-observed created policy = %+v", got)
	}
	wantBody := map[string]any{
		"name": "Allow HTTPS", "enabled": true, "action": map[string]any{"type": "ALLOW", "allowReturnTraffic": false},
		"source": map[string]any{"zoneId": internalZoneID}, "destination": map[string]any{"zoneId": externalZoneID},
		"ipProtocolScope": map[string]any{"ipVersion": "IPV4", "protocolFilter": map[string]any{"type": "NAMED_PROTOCOL", "protocol": map[string]any{"name": "tcp"}, "matchOpposite": false}},
		"loggingEnabled":  false,
	}
	post := firewallCalls(api.calls, http.MethodPost)
	if len(post) != 1 || !reflect.DeepEqual(post[0].body, wantBody) {
		t.Fatalf("create requests = %#v, want body %#v", post, wantBody)
	}
	assertFirewallCall(t, api.calls, http.MethodGet, client.OfficialPath("sites", firewallSiteID, "firewall", "policies", createdPolicyID), 1)
}

func TestFirewallCreateVerificationComparesCompleteWritableDocument(t *testing.T) {
	baseObserved := map[string]any{
		"id": createdPolicyID, "name": "Allow HTTPS", "description": "Verified create", "enabled": true, "index": float64(120),
		"action": map[string]any{"type": "ALLOW", "allowReturnTraffic": true},
		"source": map[string]any{"zoneId": internalZoneID}, "destination": map[string]any{"zoneId": externalZoneID},
		"ipProtocolScope": map[string]any{"ipVersion": "IPV4", "protocolFilter": map[string]any{"type": "NAMED_PROTOCOL", "protocol": map[string]any{"name": "tcp"}, "matchOpposite": false}},
		"loggingEnabled":  true, "metadata": map[string]any{"origin": "USER_DEFINED"},
	}
	in := domain.FirewallInput{
		Name: "Allow HTTPS", Description: "Verified create", Action: "allow", AllowReturnTraffic: true, SetAllowReturnTraffic: true,
		SourceZone: "Internal", DestinationZone: "External", IPVersion: "ipv4", Protocol: "tcp", LoggingEnabled: true,
	}
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "allow return traffic mismatch", mutate: func(observed map[string]any) {
			observed["action"].(map[string]any)["allowReturnTraffic"] = false
		}},
		{name: "source filter mismatch", mutate: func(observed map[string]any) {
			observed["source"].(map[string]any)["trafficFilter"] = map[string]any{
				"type": "PORT",
				"portFilter": map[string]any{
					"type": "PORTS", "matchOpposite": false,
					"items": []any{map[string]any{"type": "PORT_NUMBER", "value": float64(443)}},
				},
			}
		}},
		{name: "destination filter mismatch", mutate: func(observed map[string]any) {
			observed["destination"].(map[string]any)["trafficFilter"] = map[string]any{
				"type": "DOMAIN", "domainFilter": map[string]any{"type": "DOMAINS", "domains": []any{"example.test"}},
			}
		}},
		{name: "connection state mismatch", mutate: func(observed map[string]any) {
			observed["connectionStateFilter"] = []any{"NEW"}
		}},
		{name: "IPsec mismatch", mutate: func(observed map[string]any) {
			observed["ipsecFilter"] = "MATCH_ENCRYPTED"
		}},
		{name: "schedule mismatch", mutate: func(observed map[string]any) {
			observed["schedule"] = map[string]any{"mode": "EVERY_DAY", "timeFilter": map[string]any{"startTime": "08:00", "stopTime": "18:00"}}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := newModernFirewallAPI(t)
			observed := cloneFirewallMap(t, baseObserved)
			tt.mutate(observed)
			api.postResponse = map[string]any{"id": createdPolicyID}
			api.details[client.OfficialPath("sites", firewallSiteID, "firewall", "policies", createdPolicyID)] = observed

			_, err := domain.NewFirewallService(api).ApplyCreate(context.Background(), in)
			if !apperr.Is(err, apperr.Conflict) || !strings.Contains(strings.ToLower(err.Error()), "verification") {
				t.Fatalf("error = %v, want explicit verification conflict", err)
			}
			if got := len(firewallCalls(api.calls, http.MethodPost)); got != 1 {
				t.Fatalf("POST attempts = %d, want 1", got)
			}
			if got := len(firewallCalls(api.calls, http.MethodGet)); got != 1 {
				t.Fatalf("verification GETs = %d, want 1", got)
			}
		})
	}
}

func TestFirewallCreatePreservesExplicitZeroValueFields(t *testing.T) {
	api := newModernFirewallAPI(t)
	in := domain.FirewallInput{
		Name: "Disabled allow", Description: "", SetDescription: true,
		Enabled: false, SetEnabled: true, Action: "allow", AllowReturnTraffic: false, SetAllowReturnTraffic: true,
		SourceZone: "Internal", DestinationZone: "External", IPVersion: "ipv4", Protocol: "tcp",
		LoggingEnabled: false, SetLoggingEnabled: true,
	}
	observed := map[string]any{
		"id": createdPolicyID, "name": "Disabled allow", "description": "", "enabled": false, "index": float64(120),
		"action": map[string]any{"type": "ALLOW", "allowReturnTraffic": false},
		"source": map[string]any{"zoneId": internalZoneID}, "destination": map[string]any{"zoneId": externalZoneID},
		"ipProtocolScope": map[string]any{"ipVersion": "IPV4", "protocolFilter": map[string]any{"type": "NAMED_PROTOCOL", "protocol": map[string]any{"name": "tcp"}, "matchOpposite": false}},
		"loggingEnabled":  false, "metadata": map[string]any{"origin": "USER_DEFINED"},
	}
	api.postResponse = map[string]any{"id": createdPolicyID}
	api.details[client.OfficialPath("sites", firewallSiteID, "firewall", "policies", createdPolicyID)] = observed

	if _, err := domain.NewFirewallService(api).ApplyCreate(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	posts := firewallCalls(api.calls, http.MethodPost)
	if len(posts) != 1 {
		t.Fatalf("POST attempts = %d, want 1", len(posts))
	}
	body := posts[0].body.(map[string]any)
	if description, present := body["description"]; !present || description != "" {
		t.Fatalf("explicit empty description = %#v present=%t", description, present)
	}
}

func TestFirewallCreateRejectsRequiredEnumsAndZoneResolutionFailures(t *testing.T) {
	tests := []struct {
		name string
		in   domain.FirewallInput
		code apperr.Code
	}{
		{name: "missing name", in: domain.FirewallInput{Action: "block", SourceZone: "Internal", DestinationZone: "External"}, code: apperr.ValidationFailed},
		{name: "missing source zone", in: domain.FirewallInput{Name: "x", Action: "block", DestinationZone: "External"}, code: apperr.ValidationFailed},
		{name: "missing destination zone", in: domain.FirewallInput{Name: "x", Action: "block", SourceZone: "Internal"}, code: apperr.ValidationFailed},
		{name: "invalid action", in: domain.FirewallInput{Name: "x", Action: "accept", SourceZone: "Internal", DestinationZone: "External"}, code: apperr.ValidationFailed},
		{name: "invalid IP version", in: domain.FirewallInput{Name: "x", Action: "block", SourceZone: "Internal", DestinationZone: "External", IPVersion: "both"}, code: apperr.ValidationFailed},
		{name: "invalid protocol", in: domain.FirewallInput{Name: "x", Action: "block", SourceZone: "Internal", DestinationZone: "External", Protocol: "gre"}, code: apperr.ValidationFailed},
		{name: "IPv4 ICMPv6", in: domain.FirewallInput{Name: "x", Action: "block", SourceZone: "Internal", DestinationZone: "External", IPVersion: "ipv4", Protocol: "icmpv6"}, code: apperr.ValidationFailed},
		{name: "block with explicit return traffic", in: domain.FirewallInput{Name: "x", Action: "block", SetAllowReturnTraffic: true, SourceZone: "Internal", DestinationZone: "External"}, code: apperr.ValidationFailed},
		{name: "reject with explicit return traffic", in: domain.FirewallInput{Name: "x", Action: "reject", AllowReturnTraffic: true, SetAllowReturnTraffic: true, SourceZone: "Internal", DestinationZone: "External"}, code: apperr.ValidationFailed},
		{name: "missing named zone", in: domain.FirewallInput{Name: "x", Action: "block", SourceZone: "Missing", DestinationZone: "External"}, code: apperr.NotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := newModernFirewallAPI(t)
			_, err := domain.NewFirewallService(api).Create(context.Background(), tt.in)
			if !apperr.Is(err, tt.code) {
				t.Fatalf("error = %v, want %s", err, tt.code)
			}
			if len(firewallMutationCalls(api.calls)) != 0 {
				t.Fatalf("validation performed a write: %+v", api.calls)
			}
		})
	}

	api := newModernFirewallAPI(t)
	api.zones = append(api.zones, map[string]any{"id": "ffffffff-ffff-4fff-8fff-fffffffffff9", "name": "Internal", "networkIds": []any{}, "metadata": map[string]any{"origin": "USER_DEFINED"}})
	_, err := domain.NewFirewallService(api).Create(context.Background(), domain.FirewallInput{Name: "x", Action: "block", SourceZone: "Internal", DestinationZone: "External"})
	if !apperr.Is(err, apperr.AmbiguousID) {
		t.Fatalf("ambiguous source zone error = %v", err)
	}
}

func TestFirewallUpdatePreservesCompleteOfficialWireDocument(t *testing.T) {
	ctx := context.Background()
	api := newModernFirewallAPI(t)
	updated := cloneFirewallMap(t, api.policies[0])
	updated["name"] = "Allow Secure DNS"
	api.putResponse = cloneFirewallMap(t, updated)
	api.details[client.OfficialPath("sites", firewallSiteID, "firewall", "policies", allowDNSPolicyID)] = cloneFirewallMap(t, updated)
	in := domain.FirewallInput{Name: "Allow Secure DNS", SetName: true}

	p, before, err := domain.NewFirewallService(api).Update(ctx, allowDNSPolicyID, in)
	if err != nil {
		t.Fatal(err)
	}
	if before.ID != allowDNSPolicyID || p.Changes[0].ID != allowDNSPolicyID {
		t.Fatalf("update target = %+v plan=%+v", before, p)
	}
	got, err := domain.NewFirewallService(api).ApplyUpdate(ctx, allowDNSPolicyID, in)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Allow Secure DNS" || got.ID != allowDNSPolicyID {
		t.Fatalf("observed update = %+v", got)
	}
	puts := firewallCalls(api.calls, http.MethodPut)
	if len(puts) != 1 {
		t.Fatalf("PUT calls = %+v", puts)
	}
	wantBody := cloneFirewallMap(t, api.policies[0])
	delete(wantBody, "id")
	delete(wantBody, "index")
	delete(wantBody, "metadata")
	wantBody["name"] = "Allow Secure DNS"
	if !reflect.DeepEqual(puts[0].body, wantBody) {
		t.Fatalf("update body = %#v\nwant complete supported wire document = %#v", puts[0].body, wantBody)
	}
	for _, field := range []string{"connectionStateFilter", "ipsecFilter", "schedule", "source", "destination", "ipProtocolScope"} {
		if _, ok := puts[0].body.(map[string]any)[field]; !ok {
			t.Fatalf("update body lost %s: %#v", field, puts[0].body)
		}
	}
}

func TestFirewallIPVersionUpdatePreservesUnmodifiedProtocolNumberFilter(t *testing.T) {
	api := newModernFirewallAPI(t)
	policy := api.policies[0]
	policy["ipProtocolScope"] = map[string]any{
		"ipVersion": "IPV4",
		"protocolFilter": map[string]any{
			"type": "PROTOCOL_NUMBER", "protocolNumber": float64(132), "matchOpposite": true,
		},
	}

	updated := cloneFirewallMap(t, policy)
	updated["ipProtocolScope"].(map[string]any)["ipVersion"] = "IPV6"
	api.details[client.OfficialPath("sites", firewallSiteID, "firewall", "policies", allowDNSPolicyID)] = updated
	got, err := domain.NewFirewallService(api).ApplyUpdate(context.Background(), allowDNSPolicyID, domain.FirewallInput{
		IPVersion: "ipv6", SetIPVersion: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Protocol != "ipv6:not(132)" {
		t.Fatalf("observed protocol = %q, want preserved protocol-number filter", got.Protocol)
	}
	puts := firewallCalls(api.calls, http.MethodPut)
	if len(puts) != 1 {
		t.Fatalf("PUT attempts = %d, want 1", len(puts))
	}
	wantBody := cloneFirewallMap(t, policy)
	delete(wantBody, "id")
	delete(wantBody, "index")
	delete(wantBody, "metadata")
	wantBody["ipProtocolScope"].(map[string]any)["ipVersion"] = "IPV6"
	if !reflect.DeepEqual(puts[0].body, wantBody) {
		t.Fatalf("PUT body = %#v\nwant protocol-number-preserving body = %#v", puts[0].body, wantBody)
	}
	if got := len(firewallCalls(api.calls, http.MethodGet)); got != 1 {
		t.Fatalf("verification GETs = %d, want 1", got)
	}

	p, _, err := domain.NewFirewallService(api).Update(context.Background(), allowDNSPolicyID, domain.FirewallInput{
		IPVersion: "ipv6", SetIPVersion: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	after := p.Changes[0].After.(map[string]any)
	if after["protocol"] != "ipv6:not(132)" {
		t.Fatalf("protocol snapshot = %#v, want preserved protocol-number filter", after["protocol"])
	}
}

func TestFirewallMutationVerificationFailuresAreExplicitAndNeverRetried(t *testing.T) {
	createInput := domain.FirewallInput{Name: "Allow HTTPS", Action: "allow", SourceZone: "Internal", DestinationZone: "External", IPVersion: "ipv4", Protocol: "tcp"}

	t.Run("create missing ID is unverified", func(t *testing.T) {
		api := newModernFirewallAPI(t)
		api.postResponse = map[string]any{}
		_, err := domain.NewFirewallService(api).ApplyCreate(context.Background(), createInput)
		if !apperr.Is(err, apperr.Conflict) || !strings.Contains(strings.ToLower(err.Error()), "unverified") {
			t.Fatalf("error = %v, want explicit unverified conflict", err)
		}
		if got := len(firewallCalls(api.calls, http.MethodPost)); got != 1 {
			t.Fatalf("POST attempts = %d, want 1", got)
		}
		if got := len(firewallCalls(api.calls, http.MethodGet)); got != 0 {
			t.Fatalf("verification GETs = %d, want 0 without an ID", got)
		}
	})

	t.Run("create observed mismatch", func(t *testing.T) {
		api := newModernFirewallAPI(t)
		api.postResponse = map[string]any{"id": createdPolicyID}
		observed := cloneFirewallMap(t, api.policies[1])
		observed["id"] = createdPolicyID
		api.details[client.OfficialPath("sites", firewallSiteID, "firewall", "policies", createdPolicyID)] = observed
		_, err := domain.NewFirewallService(api).ApplyCreate(context.Background(), createInput)
		if !apperr.Is(err, apperr.Conflict) || !strings.Contains(strings.ToLower(err.Error()), "verification") {
			t.Fatalf("error = %v, want explicit verification conflict", err)
		}
		if got := len(firewallCalls(api.calls, http.MethodPost)); got != 1 {
			t.Fatalf("POST attempts = %d, want 1", got)
		}
	})

	t.Run("update observed mismatch", func(t *testing.T) {
		api := newModernFirewallAPI(t)
		_, err := domain.NewFirewallService(api).ApplyUpdate(context.Background(), allowDNSPolicyID, domain.FirewallInput{Name: "Renamed", SetName: true})
		if !apperr.Is(err, apperr.Conflict) || !strings.Contains(strings.ToLower(err.Error()), "verification") {
			t.Fatalf("error = %v, want explicit verification conflict", err)
		}
		if got := len(firewallCalls(api.calls, http.MethodPut)); got != 1 {
			t.Fatalf("PUT attempts = %d, want 1", got)
		}
		if got := len(firewallCalls(api.calls, http.MethodGet)); got != 1 {
			t.Fatalf("verification GETs = %d, want 1", got)
		}
	})

	t.Run("delete still present", func(t *testing.T) {
		api := newModernFirewallAPI(t)
		api.retainDeletedDetail = true
		_, err := domain.NewFirewallService(api).ApplyDelete(context.Background(), allowDNSPolicyID)
		if !apperr.Is(err, apperr.Conflict) || !strings.Contains(strings.ToLower(err.Error()), "verification") {
			t.Fatalf("error = %v, want explicit verification conflict", err)
		}
		if got := len(firewallCalls(api.calls, http.MethodDelete)); got != 1 {
			t.Fatalf("DELETE attempts = %d, want 1", got)
		}
		if got := len(firewallCalls(api.calls, http.MethodGet)); got != 1 {
			t.Fatalf("verification GETs = %d, want 1", got)
		}
	})

	verificationReadTests := []struct {
		name        string
		writeMethod string
		configure   func(*modernFirewallAPI, string)
		run         func(*modernFirewallAPI) error
	}{
		{
			name: "create verification read failure", writeMethod: http.MethodPost,
			configure: func(api *modernFirewallAPI, path string) {
				api.postResponse = map[string]any{"id": createdPolicyID}
				api.errByMethodAndPath[http.MethodGet+" "+path] = errors.New("detail read failed")
			},
			run: func(api *modernFirewallAPI) error {
				_, err := domain.NewFirewallService(api).ApplyCreate(context.Background(), createInput)
				return err
			},
		},
		{
			name: "update verification read failure", writeMethod: http.MethodPut,
			configure: func(api *modernFirewallAPI, path string) {
				api.errByMethodAndPath[http.MethodGet+" "+path] = errors.New("detail read failed")
			},
			run: func(api *modernFirewallAPI) error {
				_, err := domain.NewFirewallService(api).ApplyUpdate(context.Background(), allowDNSPolicyID, domain.FirewallInput{Name: "Renamed", SetName: true})
				return err
			},
		},
		{
			name: "delete verification read failure", writeMethod: http.MethodDelete,
			configure: func(api *modernFirewallAPI, path string) {
				api.errByMethodAndPath[http.MethodGet+" "+path] = errors.New("detail read failed")
			},
			run: func(api *modernFirewallAPI) error {
				_, err := domain.NewFirewallService(api).ApplyDelete(context.Background(), allowDNSPolicyID)
				return err
			},
		},
	}
	for _, tt := range verificationReadTests {
		t.Run(tt.name, func(t *testing.T) {
			api := newModernFirewallAPI(t)
			id := allowDNSPolicyID
			if tt.writeMethod == http.MethodPost {
				id = createdPolicyID
			}
			path := client.OfficialPath("sites", firewallSiteID, "firewall", "policies", id)
			tt.configure(api, path)
			err := tt.run(api)
			if !apperr.Is(err, apperr.Conflict) || !strings.Contains(strings.ToLower(err.Error()), "verified") {
				t.Fatalf("error = %v, want explicit verification conflict", err)
			}
			if got := len(firewallCalls(api.calls, tt.writeMethod)); got != 1 {
				t.Fatalf("%s attempts = %d, want 1", tt.writeMethod, got)
			}
			if got := len(firewallCalls(api.calls, http.MethodGet)); got != 1 {
				t.Fatalf("verification GETs = %d, want 1", got)
			}
		})
	}
}

func TestFirewallUpdateRejectsZeroFieldAndEffectiveNoOpWithoutWrite(t *testing.T) {
	tests := []domain.FirewallInput{
		{},
		{Name: "Allow DNS", SetName: true},
		{Enabled: true, SetEnabled: true},
		{SourceZone: "Internal", SetSourceZone: true},
		{DestinationZone: externalZoneID, SetDestinationZone: true},
		{Action: "allow", SetAction: true},
		{IPVersion: "ipv4", Protocol: "udp", SetIPVersion: true, SetProtocol: true},
	}
	for i, in := range tests {
		api := newModernFirewallAPI(t)
		_, _, err := domain.NewFirewallService(api).Update(context.Background(), allowDNSPolicyID, in)
		if !apperr.Is(err, apperr.ValidationFailed) {
			t.Fatalf("case %d error = %v", i, err)
		}
		if len(firewallMutationCalls(api.calls)) != 0 {
			t.Fatalf("case %d wrote on no-op: %+v", i, api.calls)
		}
	}
}

func TestFirewallUpdateRejectsExplicitReturnTrafficForExistingNonAllowPolicy(t *testing.T) {
	api := newModernFirewallAPI(t)
	_, _, err := domain.NewFirewallService(api).Update(context.Background(), blockWebPolicyID, domain.FirewallInput{
		Name: "Renamed Block", SetName: true, AllowReturnTraffic: true, SetAllowReturnTraffic: true,
	})
	if !apperr.Is(err, apperr.ValidationFailed) || !strings.Contains(err.Error(), "allow-return-traffic") {
		t.Fatalf("error = %v, want explicit allow-return-traffic validation failure", err)
	}
	if len(firewallMutationCalls(api.calls)) != 0 {
		t.Fatalf("invalid update wrote: %+v", api.calls)
	}
}

func TestFirewallReorderUsesOneAtomicZonePairWriteAndVerifiesFullOrder(t *testing.T) {
	api := newModernFirewallAPI(t)
	api.orderingReads = []domain.FirewallOrdering{
		{BeforeSystemDefined: []string{allowDNSPolicyID}, AfterSystemDefined: []string{blockWebPolicyID}},
		{BeforeSystemDefined: []string{allowDNSPolicyID}, AfterSystemDefined: []string{blockWebPolicyID}},
		{BeforeSystemDefined: []string{blockWebPolicyID}, AfterSystemDefined: []string{allowDNSPolicyID}},
	}
	ro := domain.FirewallReorder{
		SourceZone: "Internal", DestinationZone: externalZoneID,
		BeforeSystemDefined: []string{"Block Web"}, AfterSystemDefined: []string{allowDNSPolicyID},
	}
	svc := domain.NewFirewallService(api)
	p, err := svc.Reorder(context.Background(), ro)
	if err != nil {
		t.Fatal(err)
	}
	after := p.Changes[0].After.(map[string]any)
	if !reflect.DeepEqual(after["before_system_defined"], []string{blockWebPolicyID}) || !reflect.DeepEqual(after["after_system_defined"], []string{allowDNSPolicyID}) {
		t.Fatalf("resolved reorder plan = %#v", after)
	}
	// Reorder planning consumed the first ordering read. Apply re-reads current
	// state, makes exactly one mutation, then consumes the final verification read.
	observed, err := svc.ApplyReorder(context.Background(), ro)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(observed.BeforeSystemDefined, []string{blockWebPolicyID}) || !reflect.DeepEqual(observed.AfterSystemDefined, []string{allowDNSPolicyID}) {
		t.Fatalf("observed final ordering = %+v", observed)
	}
	puts := firewallCalls(api.calls, http.MethodPut)
	if len(puts) != 1 {
		t.Fatalf("atomic mutation count = %d, calls=%+v", len(puts), api.calls)
	}
	parsed, err := url.Parse(puts[0].path)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Path != client.OfficialPath("sites", firewallSiteID, "firewall", "policies", "ordering") ||
		parsed.Query().Get("sourceFirewallZoneId") != internalZoneID || parsed.Query().Get("destinationFirewallZoneId") != externalZoneID {
		t.Fatalf("ordering endpoint = %s", puts[0].path)
	}
	wantBody := map[string]any{"orderedFirewallPolicyIds": map[string]any{
		"beforeSystemDefined": []string{blockWebPolicyID}, "afterSystemDefined": []string{allowDNSPolicyID},
	}}
	if !reflect.DeepEqual(puts[0].body, wantBody) {
		t.Fatalf("ordering body = %#v, want %#v", puts[0].body, wantBody)
	}
}

func TestFirewallReorderRejectsIncompleteDuplicateAndWrongZonePolicies(t *testing.T) {
	tests := []struct {
		name string
		ro   domain.FirewallReorder
		code apperr.Code
	}{
		{name: "missing source zone", ro: domain.FirewallReorder{DestinationZone: "External", BeforeSystemDefined: []string{allowDNSPolicyID}, AfterSystemDefined: []string{blockWebPolicyID}}, code: apperr.ValidationFailed},
		{name: "incomplete order", ro: domain.FirewallReorder{SourceZone: "Internal", DestinationZone: "External", BeforeSystemDefined: []string{allowDNSPolicyID}}, code: apperr.ValidationFailed},
		{name: "duplicate", ro: domain.FirewallReorder{SourceZone: "Internal", DestinationZone: "External", BeforeSystemDefined: []string{allowDNSPolicyID}, AfterSystemDefined: []string{allowDNSPolicyID}}, code: apperr.ValidationFailed},
		{name: "system policy", ro: domain.FirewallReorder{SourceZone: "Internal", DestinationZone: "External", BeforeSystemDefined: []string{allowDNSPolicyID}, AfterSystemDefined: []string{systemGuardPolicyID}}, code: apperr.NotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := newModernFirewallAPI(t)
			_, err := domain.NewFirewallService(api).Reorder(context.Background(), tt.ro)
			if !apperr.Is(err, tt.code) {
				t.Fatalf("error = %v, want %s", err, tt.code)
			}
			if len(firewallMutationCalls(api.calls)) != 0 {
				t.Fatalf("invalid order wrote: %+v", api.calls)
			}
		})
	}
}

func TestFirewallReorderVerificationMismatchAndWriteFailureDoNotRetry(t *testing.T) {
	ro := domain.FirewallReorder{
		SourceZone: "Internal", DestinationZone: "External",
		BeforeSystemDefined: []string{blockWebPolicyID}, AfterSystemDefined: []string{allowDNSPolicyID},
	}

	t.Run("verification mismatch", func(t *testing.T) {
		api := newModernFirewallAPI(t)
		api.orderingReads = []domain.FirewallOrdering{
			{BeforeSystemDefined: []string{allowDNSPolicyID}, AfterSystemDefined: []string{blockWebPolicyID}},
			{BeforeSystemDefined: []string{blockWebPolicyID}, AfterSystemDefined: []string{}},
		}
		_, err := domain.NewFirewallService(api).ApplyReorder(context.Background(), ro)
		if !apperr.Is(err, apperr.Conflict) {
			t.Fatalf("mismatch error = %v", err)
		}
		if got := len(firewallCalls(api.calls, http.MethodPut)); got != 1 {
			t.Fatalf("mismatch writes = %d, want 1", got)
		}
	})

	t.Run("write failure", func(t *testing.T) {
		api := newModernFirewallAPI(t)
		orderingPath := client.OfficialPath("sites", firewallSiteID, "firewall", "policies", "ordering") +
			"?destinationFirewallZoneId=" + url.QueryEscape(externalZoneID) + "&sourceFirewallZoneId=" + url.QueryEscape(internalZoneID)
		api.errByMethodAndPath[http.MethodPut+" "+orderingPath] = errors.New("controller write failed")
		_, err := domain.NewFirewallService(api).ApplyReorder(context.Background(), ro)
		if err == nil || !strings.Contains(err.Error(), "controller write failed") {
			t.Fatalf("write error = %v", err)
		}
		if got := len(firewallCalls(api.calls, http.MethodPut)); got != 1 {
			t.Fatalf("failed write attempts = %d, want 1", got)
		}
		if got := len(firewallCalls(api.calls, http.MethodGet)); got != 1 {
			t.Fatalf("ordering GET count = %d, want only pre-write read", got)
		}
	})
}

func cloneFirewallMap(t *testing.T, in map[string]any) map[string]any {
	t.Helper()
	var out map[string]any
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func cloneFirewallMaps(in []map[string]any) []map[string]any {
	var out []map[string]any
	b, _ := json.Marshal(in)
	_ = json.Unmarshal(b, &out)
	return out
}

func decodeFirewallInto(in, out any) error {
	if out == nil {
		return nil
	}
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

func firewallCalls(calls []modernFirewallCall, method string) []modernFirewallCall {
	var out []modernFirewallCall
	for _, call := range calls {
		if call.method == method {
			out = append(out, call)
		}
	}
	return out
}

func firewallMutationCalls(calls []modernFirewallCall) []modernFirewallCall {
	var out []modernFirewallCall
	for _, call := range calls {
		switch call.method {
		case http.MethodPost, http.MethodPut, http.MethodDelete:
			out = append(out, call)
		}
	}
	return out
}

func assertFirewallCall(t *testing.T, calls []modernFirewallCall, method, path string, want int) {
	t.Helper()
	got := 0
	for _, call := range calls {
		if call.method == method && call.path == path {
			got++
		}
	}
	if got != want {
		t.Fatalf("%s %s calls = %d, want %d; all=%+v", method, path, got, want, calls)
	}
}
