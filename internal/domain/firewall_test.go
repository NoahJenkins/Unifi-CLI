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
	zones              []map[string]any
	policies           []map[string]any
	details            map[string]map[string]any
	orderingReads      []domain.FirewallOrdering
	orderingReadIndex  int
	postResponse       map[string]any
	putResponse        map[string]any
	calls              []modernFirewallCall
	errByMethodAndPath map[string]error
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
		delete(f.details, parsed.Path)
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

	// Planning performs no writes. Apply is covered by the complete-document
	// test; this assertion names the nested field that must survive unchanged.
	// The requested plan snapshot must still report the protocol number.
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
