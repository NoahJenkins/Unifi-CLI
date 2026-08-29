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
	labZoneID           = "ffffffff-ffff-4fff-8fff-fffffffffff3"
	createdZoneID       = "ffffffff-ffff-4fff-8fff-fffffffffff9"
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
	zonesPath := client.OfficialPath("sites", firewallSiteID, "firewall", "zones")
	switch {
	case method == http.MethodPost && parsed.Path == zonesPath:
		return decodeFirewallInto(f.postResponse, out)
	case method == http.MethodPut && strings.HasPrefix(parsed.Path, zonesPath+"/"):
		if f.putResponse != nil {
			f.details[parsed.Path] = deepCloneTestFirewallMap(f.putResponse)
		}
		return decodeFirewallInto(f.putResponse, out)
	case method == http.MethodDelete && strings.HasPrefix(parsed.Path, zonesPath+"/"):
		if !f.retainDeletedDetail {
			delete(f.details, parsed.Path)
		}
		return nil
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

func deepCloneTestFirewallMap(in map[string]any) map[string]any {
	var out map[string]any
	data, _ := json.Marshal(in)
	_ = json.Unmarshal(data, &out)
	return out
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
	api.details[client.OfficialPath("sites", firewallSiteID, "firewall", "zones", internalZoneID)]["id"] = externalZoneID
	if _, err := domain.NewFirewallService(api).GetZone(ctx, "Internal"); !apperr.Is(err, apperr.Conflict) {
		t.Fatalf("mismatched zone detail error = %v, want conflict", err)
	}

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
		SourceFilter: &domain.FirewallTrafficFilter{
			Type: "ip_address",
			IPAddressFilter: &domain.FirewallIPAddressFilter{
				Type: "ip_addresses", Items: []domain.FirewallIPMatch{{Type: "subnet", Value: "192.0.2.0/24"}},
			},
			PortFilter: &domain.FirewallPortFilter{
				Type: "ports", Items: []domain.FirewallPortMatch{{Type: "port_number_range", Start: 1024, Stop: 65535}},
			},
		},
		DestinationFilter: &domain.FirewallTrafficFilter{Type: "domain"},
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

func TestFirewallExactCreateSendsOneOfficialIPAddressAndDestinationPort(t *testing.T) {
	api := newModernFirewallAPI(t)
	in := domain.FirewallInput{
		Name: "Allow one TCP service", Action: "allow", AllowReturnTraffic: true, SetAllowReturnTraffic: true,
		SourceZone: "Internal", DestinationZone: "External", IPVersion: "ipv4", Protocol: "tcp",
		SourceIP: "192.0.2.10", SetSourceIP: true,
		DestinationIP: "198.51.100.20", SetDestinationIP: true,
		DestinationPort: 1514, SetDestinationPort: true,
	}
	observed := map[string]any{
		"id": createdPolicyID, "name": "Allow one TCP service", "enabled": true, "index": float64(120),
		"action": map[string]any{"type": "ALLOW", "allowReturnTraffic": true},
		"source": map[string]any{
			"zoneId": internalZoneID,
			"trafficFilter": map[string]any{
				"type": "IP_ADDRESS",
				"ipAddressFilter": map[string]any{
					"type": "IP_ADDRESSES", "matchOpposite": false,
					"items": []any{map[string]any{"type": "IP_ADDRESS", "value": "192.0.2.10"}},
				},
			},
		},
		"destination": map[string]any{
			"zoneId": externalZoneID,
			"trafficFilter": map[string]any{
				"type": "IP_ADDRESS",
				"ipAddressFilter": map[string]any{
					"type": "IP_ADDRESSES", "matchOpposite": false,
					"items": []any{map[string]any{"type": "IP_ADDRESS", "value": "198.51.100.20"}},
				},
				"portFilter": map[string]any{
					"type": "PORTS", "matchOpposite": false,
					"items": []any{map[string]any{"type": "PORT_NUMBER", "value": float64(1514)}},
				},
			},
		},
		"ipProtocolScope": map[string]any{
			"ipVersion": "IPV4",
			"protocolFilter": map[string]any{
				"type": "NAMED_PROTOCOL", "protocol": map[string]any{"name": "tcp"}, "matchOpposite": false,
			},
		},
		"loggingEnabled": false, "metadata": map[string]any{"origin": "USER_DEFINED"},
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
	wantBody := cloneFirewallMap(t, observed)
	delete(wantBody, "id")
	delete(wantBody, "index")
	delete(wantBody, "metadata")
	gotBody := cloneFirewallMap(t, posts[0].body.(map[string]any))
	if !reflect.DeepEqual(gotBody, wantBody) {
		t.Fatalf("exact create body = %#v\nwant %#v", posts[0].body, wantBody)
	}
}

func TestFirewallExactCreateRejectsUnsupportedOrBroadenedInputWithoutWrite(t *testing.T) {
	valid := domain.FirewallInput{
		Name: "Allow one TCP service", Action: "allow", AllowReturnTraffic: true, SetAllowReturnTraffic: true,
		SourceZone: "Internal", DestinationZone: "External", IPVersion: "ipv4", Protocol: "tcp",
		SourceIP: "192.0.2.10", SetSourceIP: true,
		DestinationIP: "198.51.100.20", SetDestinationIP: true,
		DestinationPort: 1514, SetDestinationPort: true,
	}
	tests := []struct {
		name   string
		mutate func(*domain.FirewallInput)
	}{
		{name: "source only", mutate: func(in *domain.FirewallInput) {
			in.SetDestinationIP, in.SetDestinationPort = false, false
			in.DestinationIP, in.DestinationPort = "", 0
		}},
		{name: "destination only", mutate: func(in *domain.FirewallInput) { in.SetSourceIP = false; in.SourceIP = "" }},
		{name: "missing return traffic", mutate: func(in *domain.FirewallInput) { in.AllowReturnTraffic = false }},
		{name: "block action", mutate: func(in *domain.FirewallInput) { in.Action = "block" }},
		{name: "IPv6 scope", mutate: func(in *domain.FirewallInput) { in.IPVersion = "ipv6" }},
		{name: "dual stack scope", mutate: func(in *domain.FirewallInput) { in.IPVersion = "ipv4_and_ipv6" }},
		{name: "UDP protocol", mutate: func(in *domain.FirewallInput) { in.Protocol = "udp" }},
		{name: "all protocols", mutate: func(in *domain.FirewallInput) { in.Protocol = "all" }},
		{name: "ICMP protocol", mutate: func(in *domain.FirewallInput) { in.Protocol = "icmp" }},
		{name: "invalid source", mutate: func(in *domain.FirewallInput) { in.SourceIP = "192.0.2.999" }},
		{name: "source CIDR", mutate: func(in *domain.FirewallInput) { in.SourceIP = "192.0.2.0/24" }},
		{name: "source range", mutate: func(in *domain.FirewallInput) { in.SourceIP = "192.0.2.10-192.0.2.20" }},
		{name: "source hostname", mutate: func(in *domain.FirewallInput) { in.SourceIP = "source.example.test" }},
		{name: "source any", mutate: func(in *domain.FirewallInput) { in.SourceIP = "any" }},
		{name: "multiple source addresses", mutate: func(in *domain.FirewallInput) { in.SourceIP = "192.0.2.10,192.0.2.11" }},
		{name: "source whitespace", mutate: func(in *domain.FirewallInput) { in.SourceIP = " 192.0.2.10" }},
		{name: "source IPv6", mutate: func(in *domain.FirewallInput) { in.SourceIP = "2001:db8::10" }},
		{name: "destination CIDR", mutate: func(in *domain.FirewallInput) { in.DestinationIP = "198.51.100.0/24" }},
		{name: "destination range", mutate: func(in *domain.FirewallInput) { in.DestinationIP = "198.51.100.20-198.51.100.30" }},
		{name: "destination hostname", mutate: func(in *domain.FirewallInput) { in.DestinationIP = "destination.example.test" }},
		{name: "zero port", mutate: func(in *domain.FirewallInput) { in.DestinationPort = 0 }},
		{name: "port above maximum", mutate: func(in *domain.FirewallInput) { in.DestinationPort = 65536 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := newModernFirewallAPI(t)
			in := valid
			tt.mutate(&in)
			_, err := domain.NewFirewallService(api).Create(context.Background(), in)
			if !apperr.Is(err, apperr.ValidationFailed) {
				t.Fatalf("error = %v, want validation_failed", err)
			}
			if calls := firewallMutationCalls(api.calls); len(calls) != 0 {
				t.Fatalf("invalid exact input wrote: %+v", calls)
			}
		})
	}
}

func TestFirewallExactCreateVerificationFailsClosedForEverySecurityField(t *testing.T) {
	in := domain.FirewallInput{
		Name: "Allow one TCP service", Action: "allow", AllowReturnTraffic: true, SetAllowReturnTraffic: true,
		SourceZone: "Internal", DestinationZone: "External", IPVersion: "ipv4", Protocol: "tcp",
		SourceIP: "192.0.2.10", SetSourceIP: true,
		DestinationIP: "198.51.100.20", SetDestinationIP: true,
		DestinationPort: 1514, SetDestinationPort: true,
	}
	base := exactFirewallObservedPolicy()
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "enabled changed", mutate: func(observed map[string]any) { observed["enabled"] = false }},
		{name: "action changed", mutate: func(observed map[string]any) { observed["action"] = map[string]any{"type": "BLOCK"} }},
		{name: "return traffic changed", mutate: func(observed map[string]any) { mapFieldTest(observed, "action")["allowReturnTraffic"] = false }},
		{name: "source zone changed", mutate: func(observed map[string]any) { mapFieldTest(observed, "source")["zoneId"] = labZoneID }},
		{name: "destination zone changed", mutate: func(observed map[string]any) { mapFieldTest(observed, "destination")["zoneId"] = labZoneID }},
		{name: "IP version changed", mutate: func(observed map[string]any) { mapFieldTest(observed, "ipProtocolScope")["ipVersion"] = "IPV6" }},
		{name: "protocol changed", mutate: func(observed map[string]any) {
			mapFieldTest(mapFieldTest(observed, "ipProtocolScope"), "protocolFilter")["protocol"] = map[string]any{"name": "udp"}
		}},
		{name: "protocol opposite", mutate: func(observed map[string]any) {
			mapFieldTest(mapFieldTest(observed, "ipProtocolScope"), "protocolFilter")["matchOpposite"] = true
		}},
		{name: "source IP changed", mutate: func(observed map[string]any) {
			exactIPItems(observed, "source")[0].(map[string]any)["value"] = "192.0.2.11"
		}},
		{name: "destination IP changed", mutate: func(observed map[string]any) {
			exactIPItems(observed, "destination")[0].(map[string]any)["value"] = "198.51.100.21"
		}},
		{name: "destination port changed", mutate: func(observed map[string]any) { exactPortItems(observed)[0].(map[string]any)["value"] = float64(1515) }},
		{name: "source IP opposite", mutate: func(observed map[string]any) { exactIPAddressFilter(observed, "source")["matchOpposite"] = true }},
		{name: "destination IP opposite", mutate: func(observed map[string]any) { exactIPAddressFilter(observed, "destination")["matchOpposite"] = true }},
		{name: "destination port opposite", mutate: func(observed map[string]any) { exactPortFilter(observed)["matchOpposite"] = true }},
		{name: "additional source IP", mutate: func(observed map[string]any) {
			filter := exactIPAddressFilter(observed, "source")
			filter["items"] = append(exactIPItems(observed, "source"), map[string]any{"type": "IP_ADDRESS", "value": "192.0.2.12"})
		}},
		{name: "additional destination IP", mutate: func(observed map[string]any) {
			filter := exactIPAddressFilter(observed, "destination")
			filter["items"] = append(exactIPItems(observed, "destination"), map[string]any{"type": "IP_ADDRESS", "value": "198.51.100.22"})
		}},
		{name: "additional destination port", mutate: func(observed map[string]any) {
			filter := exactPortFilter(observed)
			filter["items"] = append(exactPortItems(observed), map[string]any{"type": "PORT_NUMBER", "value": float64(1515)})
		}},
		{name: "source subnet", mutate: func(observed map[string]any) {
			exactIPAddressFilter(observed, "source")["items"] = []any{map[string]any{"type": "SUBNET", "value": "192.0.2.0/24"}}
		}},
		{name: "destination range", mutate: func(observed map[string]any) {
			exactIPAddressFilter(observed, "destination")["items"] = []any{map[string]any{"type": "IP_ADDRESS_RANGE", "start": "198.51.100.20", "stop": "198.51.100.30"}}
		}},
		{name: "IP matching list", mutate: func(observed map[string]any) {
			mapFieldTest(mapFieldTest(observed, "source"), "trafficFilter")["ipAddressFilter"] = map[string]any{
				"type": "TRAFFIC_MATCHING_LIST", "matchOpposite": false,
				"trafficMatchingListId": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
			}
		}},
		{name: "port matching list", mutate: func(observed map[string]any) {
			mapFieldTest(mapFieldTest(observed, "destination"), "trafficFilter")["portFilter"] = map[string]any{
				"type": "TRAFFIC_MATCHING_LIST", "matchOpposite": false,
				"trafficMatchingListId": "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
			}
		}},
		{name: "source port added", mutate: func(observed map[string]any) {
			mapFieldTest(mapFieldTest(observed, "source"), "trafficFilter")["portFilter"] = map[string]any{
				"type": "PORTS", "matchOpposite": false,
				"items": []any{map[string]any{"type": "PORT_NUMBER", "value": float64(40000)}},
			}
		}},
		{name: "source MAC added", mutate: func(observed map[string]any) {
			mapFieldTest(mapFieldTest(observed, "source"), "trafficFilter")["macAddressFilter"] = "02:00:00:00:00:01"
		}},
		{name: "connection state added", mutate: func(observed map[string]any) { observed["connectionStateFilter"] = []any{"NEW"} }},
		{name: "IPsec filter added", mutate: func(observed map[string]any) { observed["ipsecFilter"] = "MATCH_NOT_ENCRYPTED" }},
		{name: "schedule added", mutate: func(observed map[string]any) {
			observed["schedule"] = map[string]any{"mode": "EVERY_DAY", "timeFilter": map[string]any{"startTime": "08:00", "stopTime": "18:00"}}
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := newModernFirewallAPI(t)
			observed := cloneFirewallMap(t, base)
			tt.mutate(observed)
			api.postResponse = map[string]any{"id": createdPolicyID}
			path := client.OfficialPath("sites", firewallSiteID, "firewall", "policies", createdPolicyID)
			api.details[path] = observed

			_, err := domain.NewFirewallService(api).ApplyCreate(context.Background(), in)
			if !apperr.Is(err, apperr.Conflict) || !strings.Contains(strings.ToLower(err.Error()), "verification") {
				t.Fatalf("error = %v, want verification conflict", err)
			}
			assertFirewallCall(t, api.calls, http.MethodPost, client.OfficialPath("sites", firewallSiteID, "firewall", "policies"), 1)
			assertFirewallCall(t, api.calls, http.MethodGet, path, 1)
		})
	}
}

func TestFirewallExactCreateRejectsInvalidReturnedIDBeforeDetailRead(t *testing.T) {
	api := newModernFirewallAPI(t)
	api.postResponse = map[string]any{"id": "not-a-policy-uuid"}
	in := domain.FirewallInput{
		Name: "Allow one TCP service", Action: "allow", AllowReturnTraffic: true, SetAllowReturnTraffic: true,
		SourceZone: "Internal", DestinationZone: "External", IPVersion: "ipv4", Protocol: "tcp",
		SourceIP: "192.0.2.10", SetSourceIP: true,
		DestinationIP: "198.51.100.20", SetDestinationIP: true,
		DestinationPort: 1514, SetDestinationPort: true,
	}

	_, err := domain.NewFirewallService(api).ApplyCreate(context.Background(), in)
	if !apperr.Is(err, apperr.Conflict) || !strings.Contains(strings.ToLower(err.Error()), "valid policy id") {
		t.Fatalf("error = %v, want invalid policy ID conflict", err)
	}
	if got := len(firewallCalls(api.calls, http.MethodPost)); got != 1 {
		t.Fatalf("POST attempts = %d, want 1", got)
	}
	if got := len(firewallCalls(api.calls, http.MethodGet)); got != 0 {
		t.Fatalf("detail GETs = %d, want 0", got)
	}
}

func TestFirewallExactCreateWriteFailureIsNeverRetried(t *testing.T) {
	api := newModernFirewallAPI(t)
	path := client.OfficialPath("sites", firewallSiteID, "firewall", "policies")
	api.errByMethodAndPath[http.MethodPost+" "+path] = errors.New("ambiguous controller write failure")
	in := domain.FirewallInput{
		Name: "Allow one TCP service", Action: "allow", AllowReturnTraffic: true, SetAllowReturnTraffic: true,
		SourceZone: "Internal", DestinationZone: "External", IPVersion: "ipv4", Protocol: "tcp",
		SourceIP: "192.0.2.10", SetSourceIP: true,
		DestinationIP: "198.51.100.20", SetDestinationIP: true,
		DestinationPort: 1514, SetDestinationPort: true,
	}

	_, err := domain.NewFirewallService(api).ApplyCreate(context.Background(), in)
	if err == nil || !strings.Contains(err.Error(), "ambiguous controller write failure") {
		t.Fatalf("error = %v, want original ambiguous write error", err)
	}
	if got := len(firewallCalls(api.calls, http.MethodPost)); got != 1 {
		t.Fatalf("POST attempts = %d, want 1", got)
	}
	if got := len(firewallCalls(api.calls, http.MethodGet)); got != 0 {
		t.Fatalf("detail GETs = %d, want 0", got)
	}
}

func TestFirewallExactFiltersAreFullyInspectableAfterNormalization(t *testing.T) {
	rule := domain.NormalizeFirewallRule(exactFirewallObservedPolicy())
	if rule.SourceFilter == nil || rule.SourceFilter.Type != "ip_address" || rule.SourceFilter.IPAddressFilter == nil {
		t.Fatalf("source filter = %#v, want typed IP-address filter", rule.SourceFilter)
	}
	sourceIP := rule.SourceFilter.IPAddressFilter
	if sourceIP.Type != "ip_addresses" || sourceIP.MatchOpposite || len(sourceIP.Items) != 1 ||
		sourceIP.Items[0].Type != "ip_address" || sourceIP.Items[0].Value != "192.0.2.10" {
		t.Fatalf("source IP filter = %#v", sourceIP)
	}
	if rule.SourceFilter.PortFilter != nil {
		t.Fatalf("source port filter = %#v, want absent", rule.SourceFilter.PortFilter)
	}

	if rule.DestinationFilter == nil || rule.DestinationFilter.Type != "ip_address" ||
		rule.DestinationFilter.IPAddressFilter == nil || rule.DestinationFilter.PortFilter == nil {
		t.Fatalf("destination filter = %#v, want typed IP and port filters", rule.DestinationFilter)
	}
	destinationIP := rule.DestinationFilter.IPAddressFilter
	if destinationIP.Type != "ip_addresses" || destinationIP.MatchOpposite || len(destinationIP.Items) != 1 ||
		destinationIP.Items[0].Type != "ip_address" || destinationIP.Items[0].Value != "198.51.100.20" {
		t.Fatalf("destination IP filter = %#v", destinationIP)
	}
	destinationPort := rule.DestinationFilter.PortFilter
	if destinationPort.Type != "ports" || destinationPort.MatchOpposite || len(destinationPort.Items) != 1 ||
		destinationPort.Items[0].Type != "port_number" || destinationPort.Items[0].Value != 1514 {
		t.Fatalf("destination port filter = %#v", destinationPort)
	}
}

func TestFirewallFilterNormalizationPreservesRangesSubnetsListsAndOppositeState(t *testing.T) {
	raw := exactFirewallObservedPolicy()
	sourceIP := exactIPAddressFilter(raw, "source")
	sourceIP["matchOpposite"] = true
	sourceIP["items"] = []any{
		map[string]any{"type": "SUBNET", "value": "192.0.2.0/24"},
		map[string]any{"type": "IP_ADDRESS_RANGE", "start": "192.0.2.10", "stop": "192.0.2.20"},
	}
	mapFieldTest(mapFieldTest(raw, "destination"), "trafficFilter")["ipAddressFilter"] = map[string]any{
		"type": "TRAFFIC_MATCHING_LIST", "matchOpposite": false,
		"trafficMatchingListId": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
	}
	mapFieldTest(mapFieldTest(raw, "destination"), "trafficFilter")["portFilter"] = map[string]any{
		"type": "PORTS", "matchOpposite": true,
		"items": []any{map[string]any{"type": "PORT_NUMBER_RANGE", "start": float64(1500), "stop": float64(1600)}},
	}

	rule := domain.NormalizeFirewallRule(raw)
	if rule.SourceFilter == nil || rule.SourceFilter.IPAddressFilter == nil || !rule.SourceFilter.IPAddressFilter.MatchOpposite ||
		!reflect.DeepEqual(rule.SourceFilter.IPAddressFilter.Items, []domain.FirewallIPMatch{
			{Type: "subnet", Value: "192.0.2.0/24"},
			{Type: "ip_address_range", Start: "192.0.2.10", Stop: "192.0.2.20"},
		}) {
		t.Fatalf("source normalized filter = %#v", rule.SourceFilter)
	}
	if rule.DestinationFilter == nil || rule.DestinationFilter.IPAddressFilter == nil ||
		rule.DestinationFilter.IPAddressFilter.Type != "traffic_matching_list" ||
		rule.DestinationFilter.IPAddressFilter.TrafficMatchingListID != "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" {
		t.Fatalf("destination normalized IP list = %#v", rule.DestinationFilter)
	}
	if rule.DestinationFilter.PortFilter == nil || !rule.DestinationFilter.PortFilter.MatchOpposite ||
		!reflect.DeepEqual(rule.DestinationFilter.PortFilter.Items, []domain.FirewallPortMatch{{Type: "port_number_range", Start: 1500, Stop: 1600}}) {
		t.Fatalf("destination normalized port range = %#v", rule.DestinationFilter)
	}
}

func TestFirewallZoneOnlyNormalizationOmitsTrafficFiltersFromJSON(t *testing.T) {
	raw := exactFirewallObservedPolicy()
	delete(mapFieldTest(raw, "source"), "trafficFilter")
	delete(mapFieldTest(raw, "destination"), "trafficFilter")
	encoded, err := json.Marshal(domain.NormalizeFirewallRule(raw))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatal(err)
	}
	if _, present := document["source_filter"]; present {
		t.Fatalf("zone-only policy contains source_filter: %s", encoded)
	}
	if _, present := document["destination_filter"]; present {
		t.Fatalf("zone-only policy contains destination_filter: %s", encoded)
	}
}

func TestFirewallStableReadsRejectMalformedKnownTrafficFilters(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "missing IP opposite", mutate: func(raw map[string]any) {
			delete(exactIPAddressFilter(raw, "source"), "matchOpposite")
		}},
		{name: "non-boolean IP opposite", mutate: func(raw map[string]any) {
			exactIPAddressFilter(raw, "source")["matchOpposite"] = "false"
		}},
		{name: "missing port opposite", mutate: func(raw map[string]any) {
			delete(exactPortFilter(raw), "matchOpposite")
		}},
		{name: "non-boolean port opposite", mutate: func(raw map[string]any) {
			exactPortFilter(raw)["matchOpposite"] = float64(0)
		}},
		{name: "fractional port", mutate: func(raw map[string]any) {
			exactPortItems(raw)[0].(map[string]any)["value"] = float64(1514.5)
		}},
		{name: "non-numeric port", mutate: func(raw map[string]any) {
			exactPortItems(raw)[0].(map[string]any)["value"] = "1514"
		}},
		{name: "port below range", mutate: func(raw map[string]any) {
			exactPortItems(raw)[0].(map[string]any)["value"] = float64(0)
		}},
		{name: "port above range", mutate: func(raw map[string]any) {
			exactPortItems(raw)[0].(map[string]any)["value"] = float64(65536)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := newModernFirewallAPI(t)
			raw := exactFirewallObservedPolicy()
			tt.mutate(raw)
			api.policies = []map[string]any{raw}

			if _, err := domain.NewFirewallService(api).List(context.Background()); !apperr.Is(err, apperr.Internal) || !strings.Contains(err.Error(), "malformed") {
				t.Fatalf("List error = %v, want malformed official filter failure", err)
			}
		})
	}
}

func exactFirewallObservedPolicy() map[string]any {
	return map[string]any{
		"id": createdPolicyID, "name": "Allow one TCP service", "enabled": true, "index": float64(120),
		"action": map[string]any{"type": "ALLOW", "allowReturnTraffic": true},
		"source": map[string]any{
			"zoneId": internalZoneID,
			"trafficFilter": map[string]any{
				"type": "IP_ADDRESS",
				"ipAddressFilter": map[string]any{
					"type": "IP_ADDRESSES", "matchOpposite": false,
					"items": []any{map[string]any{"type": "IP_ADDRESS", "value": "192.0.2.10"}},
				},
			},
		},
		"destination": map[string]any{
			"zoneId": externalZoneID,
			"trafficFilter": map[string]any{
				"type": "IP_ADDRESS",
				"ipAddressFilter": map[string]any{
					"type": "IP_ADDRESSES", "matchOpposite": false,
					"items": []any{map[string]any{"type": "IP_ADDRESS", "value": "198.51.100.20"}},
				},
				"portFilter": map[string]any{
					"type": "PORTS", "matchOpposite": false,
					"items": []any{map[string]any{"type": "PORT_NUMBER", "value": float64(1514)}},
				},
			},
		},
		"ipProtocolScope": map[string]any{
			"ipVersion": "IPV4",
			"protocolFilter": map[string]any{
				"type": "NAMED_PROTOCOL", "protocol": map[string]any{"name": "tcp"}, "matchOpposite": false,
			},
		},
		"loggingEnabled": false, "metadata": map[string]any{"origin": "USER_DEFINED"},
	}
}

func mapFieldTest(value map[string]any, key string) map[string]any {
	return value[key].(map[string]any)
}

func exactIPAddressFilter(observed map[string]any, endpoint string) map[string]any {
	return mapFieldTest(mapFieldTest(mapFieldTest(observed, endpoint), "trafficFilter"), "ipAddressFilter")
}

func exactIPItems(observed map[string]any, endpoint string) []any {
	return exactIPAddressFilter(observed, endpoint)["items"].([]any)
}

func exactPortFilter(observed map[string]any) map[string]any {
	return mapFieldTest(mapFieldTest(mapFieldTest(observed, "destination"), "trafficFilter"), "portFilter")
}

func exactPortItems(observed map[string]any) []any {
	return exactPortFilter(observed)["items"].([]any)
}

func TestFirewallBoundCreateDoesNotReresolveZoneNames(t *testing.T) {
	api := newModernFirewallAPI(t)
	svc := domain.NewFirewallService(api)
	in := domain.FirewallInput{Name: "Bound", Action: "block", SourceZone: "Internal", DestinationZone: "External"}
	_, binding, err := svc.PrepareCreate(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	for _, zone := range api.zones {
		if zone["id"] == internalZoneID {
			zone["name"] = "Renamed Internal"
		}
		if zone["id"] == externalZoneID {
			zone["name"] = "Renamed External"
		}
	}
	api.zones = append(api.zones,
		map[string]any{"id": "ffffffff-ffff-4fff-8fff-fffffffffff8", "name": "Internal", "networkIds": []any{}, "metadata": map[string]any{"origin": "USER_DEFINED"}},
		map[string]any{"id": "ffffffff-ffff-4fff-8fff-fffffffffff9", "name": "External", "networkIds": []any{}, "metadata": map[string]any{"origin": "USER_DEFINED"}},
	)
	observed := map[string]any{
		"id": createdPolicyID, "name": "Bound", "enabled": true, "index": float64(120),
		"action": map[string]any{"type": "BLOCK"},
		"source": map[string]any{"zoneId": internalZoneID}, "destination": map[string]any{"zoneId": externalZoneID},
		"ipProtocolScope": map[string]any{"ipVersion": "IPV4_AND_IPV6"}, "loggingEnabled": false,
		"metadata": map[string]any{"origin": "USER_DEFINED"},
	}
	api.postResponse = map[string]any{"id": createdPolicyID}
	api.details[client.OfficialPath("sites", firewallSiteID, "firewall", "policies", createdPolicyID)] = observed
	if _, err := svc.ApplyCreateBound(context.Background(), in, binding); err != nil {
		t.Fatal(err)
	}
	posts := firewallCalls(api.calls, http.MethodPost)
	body := posts[len(posts)-1].body.(map[string]any)
	source := body["source"].(map[string]any)
	destination := body["destination"].(map[string]any)
	if source["zoneId"] != internalZoneID || destination["zoneId"] != externalZoneID {
		t.Fatalf("bound create body = %#v", body)
	}
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

func TestFirewallMutationVerificationRejectsMismatchedObservedIdentity(t *testing.T) {
	const wrongID = "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeee8"
	t.Run("create", func(t *testing.T) {
		api := newModernFirewallAPI(t)
		api.postResponse = map[string]any{"id": createdPolicyID}
		observed := map[string]any{
			"id": wrongID, "name": "Allow HTTPS", "enabled": true,
			"action": map[string]any{"type": "ALLOW", "allowReturnTraffic": false},
			"source": map[string]any{"zoneId": internalZoneID}, "destination": map[string]any{"zoneId": externalZoneID},
			"ipProtocolScope": map[string]any{"ipVersion": "IPV4", "protocolFilter": map[string]any{"type": "NAMED_PROTOCOL", "protocol": map[string]any{"name": "tcp"}, "matchOpposite": false}},
			"loggingEnabled":  false,
		}
		api.details[client.OfficialPath("sites", firewallSiteID, "firewall", "policies", createdPolicyID)] = observed
		_, err := domain.NewFirewallService(api).ApplyCreate(context.Background(), domain.FirewallInput{
			Name: "Allow HTTPS", Action: "allow", SourceZone: "Internal", DestinationZone: "External", IPVersion: "ipv4", Protocol: "tcp",
		})
		if !apperr.Is(err, apperr.Conflict) || !strings.Contains(strings.ToLower(err.Error()), "id") {
			t.Fatalf("error = %v, want observed-ID conflict", err)
		}
		if got := len(firewallCalls(api.calls, http.MethodPost)); got != 1 {
			t.Fatalf("POST attempts = %d, want 1", got)
		}
	})

	t.Run("update", func(t *testing.T) {
		api := newModernFirewallAPI(t)
		observed := cloneFirewallMap(t, api.policies[0])
		observed["id"] = wrongID
		observed["name"] = "Allow Secure DNS"
		api.details[client.OfficialPath("sites", firewallSiteID, "firewall", "policies", allowDNSPolicyID)] = observed
		_, err := domain.NewFirewallService(api).ApplyUpdate(context.Background(), allowDNSPolicyID, domain.FirewallInput{Name: "Allow Secure DNS", SetName: true})
		if !apperr.Is(err, apperr.Conflict) || !strings.Contains(strings.ToLower(err.Error()), "id") {
			t.Fatalf("error = %v, want observed-ID conflict", err)
		}
		if got := len(firewallCalls(api.calls, http.MethodPut)); got != 1 {
			t.Fatalf("PUT attempts = %d, want 1", got)
		}
	})
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

func TestFirewallPlanIncludesAllowReturnTraffic(t *testing.T) {
	api := newModernFirewallAPI(t)
	p, current, err := domain.NewFirewallService(api).Update(context.Background(), allowDNSPolicyID, domain.FirewallInput{
		AllowReturnTraffic: true, SetAllowReturnTraffic: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if current.AllowReturnTraffic {
		t.Fatal("fixture current allow_return_traffic = true, want false")
	}
	before := p.Changes[0].Before.(map[string]any)
	after := p.Changes[0].After.(map[string]any)
	if value, ok := before["allow_return_traffic"]; !ok || value != false {
		t.Fatalf("before allow_return_traffic = %#v present=%t", value, ok)
	}
	if value, ok := after["allow_return_traffic"]; !ok || value != true {
		t.Fatalf("after allow_return_traffic = %#v present=%t", value, ok)
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

func TestFirewallIPVersionUpdateRejectsMalformedProtocolScopeWithoutPanic(t *testing.T) {
	tests := []struct {
		name  string
		value any
	}{
		{name: "missing", value: nil},
		{name: "scalar", value: "IPV4"},
		{name: "array", value: []any{"IPV4"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := newModernFirewallAPI(t)
			if tt.value == nil {
				delete(api.policies[0], "ipProtocolScope")
			} else {
				api.policies[0]["ipProtocolScope"] = tt.value
			}
			_, _, err := domain.NewFirewallService(api).Update(context.Background(), allowDNSPolicyID, domain.FirewallInput{
				IPVersion: "ipv6", SetIPVersion: true,
			})
			if !apperr.Is(err, apperr.Internal) {
				t.Fatalf("error = %v, want typed internal error", err)
			}
			if got := len(firewallCalls(api.calls, http.MethodPut)); got != 0 {
				t.Fatalf("PUT attempts = %d, want 0", got)
			}
		})
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

func TestFirewallUpdateRejectsMalformedControllerZoneEndpointsWithoutPanic(t *testing.T) {
	tests := []struct {
		name  string
		field string
		input domain.FirewallInput
	}{
		{name: "source", field: "source", input: domain.FirewallInput{SourceZone: "External", SetSourceZone: true}},
		{name: "destination", field: "destination", input: domain.FirewallInput{DestinationZone: "Internal", SetDestinationZone: true}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := newModernFirewallAPI(t)
			for _, policy := range api.policies {
				if policy["id"] == allowDNSPolicyID {
					policy[tt.field] = "malformed-controller-value"
				}
			}

			_, _, err := domain.NewFirewallService(api).Update(context.Background(), allowDNSPolicyID, tt.input)
			if !apperr.Is(err, apperr.Internal) || !strings.Contains(err.Error(), tt.field) {
				t.Fatalf("Update error = %v, want typed malformed-%s failure", err, tt.field)
			}
			if len(firewallMutationCalls(api.calls)) != 0 {
				t.Fatalf("malformed policy triggered mutation: %+v", api.calls)
			}
		})
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
