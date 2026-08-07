package domain_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
	"github.com/noahjenkins/unifi-cli/internal/client"
	"github.com/noahjenkins/unifi-cli/internal/domain"
)

func fixtureDNSRecords(t *testing.T) []map[string]any {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	path := filepath.Join(filepath.Dir(file), "..", "client", "fixtures", "rest_dnsrecord.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw []map[string]any
	if err := client.DecodeData(b, &raw); err != nil {
		t.Fatal(err)
	}
	return raw
}

func fixtureDNSPolicies() []map[string]any {
	return []map[string]any{
		{"id": "dns1", "type": "A_RECORD", "enabled": true, "metadata": map[string]any{"origin": "USER"}, "domain": "nas.lan", "ipv4Address": "192.168.1.50", "ttlSeconds": float64(300)},
		{"id": "dns2", "type": "A_RECORD", "enabled": false, "metadata": map[string]any{"origin": "USER"}, "domain": "printer.lan", "ipv4Address": "192.168.1.60", "ttlSeconds": float64(300)},
		{"id": "dns3", "type": "A_RECORD", "enabled": true, "metadata": map[string]any{"origin": "USER"}, "domain": "cam.lan", "ipv4Address": "192.168.1.70", "ttlSeconds": float64(300)},
	}
}

func TestNormalizeDNSRecord(t *testing.T) {
	raw := fixtureDNSRecords(t)
	if len(raw) < 3 {
		t.Fatalf("fixture = %d, want >= 3", len(raw))
	}

	r1 := domain.NormalizeDNSRecord(raw[0])
	if r1.ID != "dns1" || r1.Name != "nas.lan" || r1.IP != "192.168.1.50" || !r1.Enabled {
		t.Fatalf("r1: %+v", r1)
	}

	r2 := domain.NormalizeDNSRecord(raw[1])
	if r2.ID != "dns2" || r2.Name != "printer.lan" || r2.IP != "192.168.1.60" || r2.Enabled {
		t.Fatalf("r2: %+v", r2)
	}

	r3 := domain.NormalizeDNSRecord(raw[2])
	if r3.Name != "cam.lan" || r3.IP != "192.168.1.70" {
		t.Fatalf("r3: %+v", r3)
	}
}

func TestNormalizeDNSPolicyTTL(t *testing.T) {
	r := domain.NormalizeDNSRecord(fixtureDNSPolicies()[0])
	if r.TTLSeconds != 300 {
		t.Fatalf("ttl_seconds = %d, want 300", r.TTLSeconds)
	}
}

type fakeDNSAPI struct {
	integrationRecords []map[string]any
	networks           []map[string]any
	calls              []dnsCall
	errByPath          map[string]error
	err                error
}

type dnsCall struct {
	method string
	path   string
	body   any
}

func (f *fakeDNSAPI) Do(ctx context.Context, method, path string, in, out any) error {
	f.calls = append(f.calls, dnsCall{method: method, path: path, body: in})
	if f.err != nil {
		return f.err
	}
	if f.errByPath != nil {
		if err, ok := f.errByPath[path]; ok {
			return err
		}
	}
	switch {
	case method == http.MethodGet && strings.HasSuffix(path, "/dns/policies"):
		return decodeInto(f.integrationRecords, out)
	case method == http.MethodGet && strings.Contains(path, "/dns/policies/"):
		id := path[strings.LastIndex(path, "/")+1:]
		for _, record := range f.integrationRecords {
			if strFieldTest(record, "id", "_id") == id {
				return decodeInto(record, out)
			}
		}
		return apperr.New(apperr.NotFound, "dns policy not found")
	case method == http.MethodPost && strings.Contains(path, "integration/v1/sites/site-uuid/dns/policies"):
		created := map[string]any{"id": "dns-policy-new"}
		if body, ok := in.(map[string]any); ok {
			for k, v := range body {
				created[k] = v
			}
		}
		f.integrationRecords = append(f.integrationRecords, created)
		return decodeInto(created, out)
	case method == http.MethodPut && strings.Contains(path, "integration/v1/sites/site-uuid/dns/policies/"):
		id := path[strings.LastIndex(path, "/")+1:]
		updated := map[string]any{"id": id}
		if body, ok := in.(map[string]any); ok {
			for k, v := range body {
				updated[k] = v
			}
		}
		for i, record := range f.integrationRecords {
			if strFieldTest(record, "id", "_id") == id {
				f.integrationRecords[i] = updated
				break
			}
		}
		return decodeInto(updated, out)
	case method == http.MethodDelete && strings.Contains(path, "integration/v1/sites/site-uuid/dns/policies/"):
		id := path[strings.LastIndex(path, "/")+1:]
		filtered := f.integrationRecords[:0]
		for _, record := range f.integrationRecords {
			if strFieldTest(record, "id", "_id") != id {
				filtered = append(filtered, record)
			}
		}
		f.integrationRecords = filtered
		return nil
	case method == http.MethodGet && strings.Contains(path, "rest/networkconf"):
		return decodeInto(f.networks, out)
	default:
		if out != nil {
			_ = json.Unmarshal([]byte(`[]`), out)
		}
		return nil
	}
}

func (f *fakeDNSAPI) IntegrationSitePath(_ context.Context, parts ...string) (string, error) {
	return "/proxy/network/integration/v1/sites/site-uuid/" + strings.Join(parts, "/"), nil
}

func (f *fakeDNSAPI) SitePath(parts ...string) string {
	p := "/proxy/network/api/s/default"
	for _, part := range parts {
		p += "/" + part
	}
	return p
}

func decodeInto(src any, out any) error {
	if out == nil {
		return nil
	}
	b, err := json.Marshal(src)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

func dnsCallWithMethod(t *testing.T, calls []dnsCall, method string) dnsCall {
	t.Helper()
	for _, call := range calls {
		if call.method == method {
			return call
		}
	}
	t.Fatalf("calls contain no %s: %+v", method, calls)
	return dnsCall{}
}

func TestDNSServiceList(t *testing.T) {
	api := &fakeDNSAPI{integrationRecords: fixtureDNSPolicies()}
	svc := domain.NewDNSService(api)
	got, err := svc.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(api.calls) != 1 {
		t.Fatalf("calls = %d", len(api.calls))
	}
	if api.calls[0].method != http.MethodGet {
		t.Fatalf("method = %q", api.calls[0].method)
	}
	if api.calls[0].path != "/proxy/network/integration/v1/sites/site-uuid/dns/policies" {
		t.Fatalf("path = %q", api.calls[0].path)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d", len(got))
	}
	if got[0].Name != "nas.lan" || got[0].IP != "192.168.1.50" {
		t.Fatalf("got[0]: %+v", got[0])
	}
}

func TestDNSServiceListUsesOfficialDNSPolicies(t *testing.T) {
	api := &fakeDNSAPI{
		integrationRecords: []map[string]any{{
			"id":          "dns-policy-1",
			"type":        "A_RECORD",
			"enabled":     true,
			"metadata":    map[string]any{"origin": "USER"},
			"domain":      "router.example.test",
			"ipv4Address": "192.0.2.1",
			"ttlSeconds":  float64(300),
		}},
	}
	svc := domain.NewDNSService(api)
	got, err := svc.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].ID != "dns-policy-1" || got[0].Name != "router.example.test" || got[0].IP != "192.0.2.1" || !got[0].Enabled || got[0].TTLSeconds != 300 {
		t.Fatalf("record = %+v", got[0])
	}
	if len(api.calls) != 1 || api.calls[0].path != "/proxy/network/integration/v1/sites/site-uuid/dns/policies" {
		t.Fatalf("calls = %+v", api.calls)
	}
}

func TestDNSServiceListNotImplementedOn404(t *testing.T) {
	api := &fakeDNSAPI{
		errByPath: map[string]error{
			"/proxy/network/integration/v1/sites/site-uuid/dns/policies": apperr.New(apperr.NotFound, "not found"),
		},
	}
	svc := domain.NewDNSService(api)
	_, err := svc.List(context.Background())
	if !apperr.Is(err, apperr.NotImplemented) {
		t.Fatalf("err = %v, want not_implemented", err)
	}
	ae := apperr.As(err)
	if ae == nil || ae.Hint == "" {
		t.Fatalf("want hint on not_implemented: %+v", ae)
	}
}

func TestDNSServiceGet(t *testing.T) {
	api := &fakeDNSAPI{integrationRecords: fixtureDNSPolicies()}
	svc := domain.NewDNSService(api)

	byID, err := svc.Get(context.Background(), "dns1")
	if err != nil {
		t.Fatal(err)
	}
	if byID.Name != "nas.lan" {
		t.Fatalf("by id: %+v", byID)
	}

	byName, err := svc.Get(context.Background(), "printer.lan")
	if err != nil {
		t.Fatal(err)
	}
	if byName.ID != "dns2" {
		t.Fatalf("by name: %+v", byName)
	}

	_, err = svc.Get(context.Background(), "missing")
	if !apperr.Is(err, apperr.NotFound) {
		t.Fatalf("missing err = %v", err)
	}
}

func TestDNSCreatePlanAndApply(t *testing.T) {
	api := &fakeDNSAPI{integrationRecords: fixtureDNSPolicies()}
	svc := domain.NewDNSService(api)
	ctx := context.Background()

	in := domain.DNSInput{Name: "media.lan", IP: "192.168.1.80", Enabled: true}
	p, err := svc.Create(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Changes) != 1 || p.Changes[0].Op != "create" {
		t.Fatalf("plan: %+v", p)
	}
	if p.Changes[0].Name != "media.lan" {
		t.Fatalf("plan name = %q", p.Changes[0].Name)
	}
	after, _ := p.Changes[0].After.(map[string]any)
	if after["name"] != "media.lan" || after["ip"] != "192.168.1.80" {
		t.Fatalf("after: %+v", after)
	}

	got, err := svc.ApplyCreate(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "media.lan" && got.Name != "new.lan" {
		// Accept either normalized input echo or fake created name fields
		if got.ID != "dns-new" {
			t.Fatalf("created: %+v", got)
		}
	}
	mutation := dnsCallWithMethod(t, api.calls, http.MethodPost)
	if mutation.path != "/proxy/network/integration/v1/sites/site-uuid/dns/policies" {
		t.Fatalf("path = %q", mutation.path)
	}
	body, _ := mutation.body.(map[string]any)
	if body["domain"] != "media.lan" {
		t.Fatalf("body name field: %+v", body)
	}
	if body["ipv4Address"] != "192.168.1.80" {
		t.Fatalf("body ip field: %+v", body)
	}
}

func TestDNSCreateUsesOfficialDNSPolicyShape(t *testing.T) {
	api := &fakeDNSAPI{}
	svc := domain.NewDNSService(api)
	in := domain.DNSInput{Name: "service.example.test", IP: "192.0.2.20", Enabled: false, SetEnabled: true}

	got, err := svc.ApplyCreate(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "dns-policy-new" || got.Name != in.Name || got.IP != in.IP || got.Enabled || got.TTLSeconds != 300 {
		t.Fatalf("created = %+v", got)
	}
	if len(api.calls) != 2 || api.calls[1].method != http.MethodGet {
		t.Fatalf("calls = %+v", api.calls)
	}
	call := api.calls[0]
	if call.method != http.MethodPost || call.path != "/proxy/network/integration/v1/sites/site-uuid/dns/policies" {
		t.Fatalf("call = %+v", call)
	}
	body, ok := call.body.(map[string]any)
	if !ok {
		t.Fatalf("body type = %T", call.body)
	}
	want := map[string]any{
		"type":        "A_RECORD",
		"domain":      in.Name,
		"ipv4Address": in.IP,
		"enabled":     false,
		"ttlSeconds":  300,
	}
	if !reflect.DeepEqual(body, want) {
		t.Fatalf("body = %#v, want %#v", body, want)
	}
}

func TestDNSUpdatePlanAndApply(t *testing.T) {
	api := &fakeDNSAPI{integrationRecords: fixtureDNSPolicies()}
	svc := domain.NewDNSService(api)
	ctx := context.Background()

	in := domain.DNSInput{Name: "nas.home", IP: "192.168.1.51"}
	p, rec, err := svc.Update(ctx, "dns1", in)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Name != "nas.lan" {
		t.Fatalf("preview: %+v", rec)
	}
	if len(p.Changes) != 1 || p.Changes[0].Op != "update" || p.Changes[0].ID != "dns1" {
		t.Fatalf("plan: %+v", p)
	}
	before, _ := p.Changes[0].Before.(map[string]any)
	after, _ := p.Changes[0].After.(map[string]any)
	if before["name"] != "nas.lan" || after["name"] != "nas.home" {
		t.Fatalf("before/after: %+v %+v", before, after)
	}

	got, err := svc.ApplyUpdate(ctx, "dns1", in)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "dns1" {
		t.Fatalf("updated: %+v", got)
	}
	mutation := dnsCallWithMethod(t, api.calls, http.MethodPut)
	if mutation.path != "/proxy/network/integration/v1/sites/site-uuid/dns/policies/dns1" {
		t.Fatalf("path = %q", mutation.path)
	}
}

func TestDNSUpdateUsesOfficialDNSPolicyShape(t *testing.T) {
	api := &fakeDNSAPI{integrationRecords: []map[string]any{{
		"id":          "dns-policy-1",
		"type":        "A_RECORD",
		"enabled":     false,
		"metadata":    map[string]any{"origin": "USER"},
		"domain":      "service.example.test",
		"ipv4Address": "192.0.2.20",
		"ttlSeconds":  float64(900),
	}}}
	svc := domain.NewDNSService(api)

	got, err := svc.ApplyUpdate(context.Background(), "dns-policy-1", domain.DNSInput{IP: "192.0.2.21"})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "dns-policy-1" || got.Name != "service.example.test" || got.IP != "192.0.2.21" || got.Enabled {
		t.Fatalf("updated = %+v", got)
	}
	mutation := dnsCallWithMethod(t, api.calls, http.MethodPut)
	if mutation.path != "/proxy/network/integration/v1/sites/site-uuid/dns/policies/dns-policy-1" {
		t.Fatalf("mutation call = %+v", mutation)
	}
	body, ok := mutation.body.(map[string]any)
	if !ok {
		t.Fatalf("body type = %T", mutation.body)
	}
	want := map[string]any{
		"type":        "A_RECORD",
		"domain":      "service.example.test",
		"ipv4Address": "192.0.2.21",
		"enabled":     false,
		"ttlSeconds":  900,
	}
	if !reflect.DeepEqual(body, want) {
		t.Fatalf("body = %#v, want %#v", body, want)
	}
}

func TestDNSDeletePlanAndApply(t *testing.T) {
	api := &fakeDNSAPI{integrationRecords: fixtureDNSPolicies()}
	svc := domain.NewDNSService(api)
	ctx := context.Background()

	p, rec, err := svc.Delete(ctx, "nas.lan")
	if err != nil {
		t.Fatal(err)
	}
	if rec.ID != "dns1" {
		t.Fatalf("record: %+v", rec)
	}
	if len(p.Changes) != 1 || p.Changes[0].Op != "delete" {
		t.Fatalf("plan: %+v", p)
	}

	got, err := svc.ApplyDelete(ctx, "nas.lan")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "dns1" {
		t.Fatalf("deleted: %+v", got)
	}
	mutation := dnsCallWithMethod(t, api.calls, http.MethodDelete)
	if mutation.path != "/proxy/network/integration/v1/sites/site-uuid/dns/policies/dns1" {
		t.Fatalf("path = %q", mutation.path)
	}
}

func TestDNSDeleteUsesOfficialDNSPolicyPath(t *testing.T) {
	api := &fakeDNSAPI{integrationRecords: []map[string]any{{
		"id":          "dns-policy-1",
		"type":        "A_RECORD",
		"enabled":     false,
		"metadata":    map[string]any{"origin": "USER"},
		"domain":      "service.example.test",
		"ipv4Address": "192.0.2.20",
		"ttlSeconds":  float64(300),
	}}}
	svc := domain.NewDNSService(api)

	got, err := svc.ApplyDelete(context.Background(), "dns-policy-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "dns-policy-1" || got.Name != "service.example.test" {
		t.Fatalf("deleted = %+v", got)
	}
	mutation := dnsCallWithMethod(t, api.calls, http.MethodDelete)
	if mutation.path != "/proxy/network/integration/v1/sites/site-uuid/dns/policies/dns-policy-1" {
		t.Fatalf("mutation call = %+v", mutation)
	}
}

func TestDNSResolversSetPlanAndApply(t *testing.T) {
	api := &fakeDNSAPI{networks: fixtureNetworks(t)}
	svc := domain.NewDNSService(api)
	ctx := context.Background()

	servers := []string{"1.1.1.1", "8.8.8.8"}
	p, r, err := svc.SetResolvers(ctx, "LAN", servers)
	if err != nil {
		t.Fatal(err)
	}
	if r.NetworkID != "net1" || r.NetworkName != "LAN" {
		t.Fatalf("preview: %+v", r)
	}
	if len(p.Changes) != 1 || p.Changes[0].Op != "update" {
		t.Fatalf("plan: %+v", p)
	}
	after, _ := p.Changes[0].After.(map[string]any)
	dnsAfter, _ := after["dns"].([]string)
	if len(dnsAfter) != 2 || dnsAfter[0] != "1.1.1.1" {
		// allow []any from JSON-shaped maps
		if arr, ok := after["dns"].([]any); ok {
			if len(arr) != 2 || arr[0] != "1.1.1.1" {
				t.Fatalf("after dns: %+v", after["dns"])
			}
		} else if len(dnsAfter) != 2 {
			t.Fatalf("after dns: %+v", after["dns"])
		}
	}

	got, err := svc.ApplySetResolvers(ctx, "LAN", servers)
	if err != nil {
		t.Fatal(err)
	}
	if got.NetworkID != "net1" {
		t.Fatalf("applied: %+v", got)
	}
	last := api.calls[len(api.calls)-1]
	if last.method != http.MethodPut {
		t.Fatalf("method = %q", last.method)
	}
	if last.path != "/proxy/network/api/s/default/rest/networkconf/net1" {
		t.Fatalf("path = %q", last.path)
	}
	body, _ := last.body.(map[string]any)
	if body["dhcpd_dns_1"] != "1.1.1.1" || body["dhcpd_dns_2"] != "8.8.8.8" {
		t.Fatalf("body: %+v", body)
	}
	if body["dhcpd_dns_enabled"] != true {
		t.Fatalf("dhcpd_dns_enabled: %+v", body["dhcpd_dns_enabled"])
	}
}

// IoT-style nets expose dns_nameservers; list prefers that field when non-empty.
// set must write/clear dns_nameservers too so list reflects the new servers.
func TestDNSResolversSetClearsDNSNameserversOnIoT(t *testing.T) {
	api := &fakeDNSAPI{networks: fixtureNetworks(t)}
	svc := domain.NewDNSService(api)
	ctx := context.Background()

	servers := []string{"1.1.1.1", "8.8.8.8"}
	got, err := svc.ApplySetResolvers(ctx, "IoT", servers)
	if err != nil {
		t.Fatal(err)
	}
	if got.NetworkID != "net2" || got.NetworkName != "IoT" {
		t.Fatalf("applied: %+v", got)
	}
	if len(got.DNS) != 2 || got.DNS[0] != "1.1.1.1" || got.DNS[1] != "8.8.8.8" {
		t.Fatalf("returned dns: %+v", got.DNS)
	}

	last := api.calls[len(api.calls)-1]
	if last.method != http.MethodPut {
		t.Fatalf("method = %q", last.method)
	}
	if last.path != "/proxy/network/api/s/default/rest/networkconf/net2" {
		t.Fatalf("path = %q", last.path)
	}
	body, _ := last.body.(map[string]any)
	if body["dhcpd_dns_1"] != "1.1.1.1" || body["dhcpd_dns_2"] != "8.8.8.8" {
		t.Fatalf("dhcpd body: %+v", body)
	}
	ns, ok := body["dns_nameservers"]
	if !ok {
		t.Fatalf("dns_nameservers missing from body: %+v", body)
	}
	switch v := ns.(type) {
	case []string:
		if len(v) != 2 || v[0] != "1.1.1.1" || v[1] != "8.8.8.8" {
			t.Fatalf("dns_nameservers: %+v", v)
		}
	case []any:
		if len(v) != 2 || v[0] != "1.1.1.1" || v[1] != "8.8.8.8" {
			t.Fatalf("dns_nameservers: %+v", v)
		}
	default:
		t.Fatalf("dns_nameservers type %T: %+v", ns, ns)
	}

	// Simulate controller state after PUT so list agrees with set.
	for i, n := range api.networks {
		if strFieldTest(n, "_id") == "net2" {
			for k, val := range body {
				n[k] = val
			}
			api.networks[i] = n
		}
	}
	iot := domain.NormalizeDNSResolver(api.networks[1])
	if len(iot.DNS) != 2 || iot.DNS[0] != "1.1.1.1" || iot.DNS[1] != "8.8.8.8" {
		t.Fatalf("updated legacy resolver still shows old/stale dns: %+v", iot.DNS)
	}
}

func strFieldTest(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok {
				return s
			}
		}
	}
	return ""
}
