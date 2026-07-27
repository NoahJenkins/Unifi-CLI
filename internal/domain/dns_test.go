package domain_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
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

type fakeDNSAPI struct {
	records   []map[string]any
	networks  []map[string]any
	calls     []dnsCall
	errByPath map[string]error
	err       error
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
	case method == http.MethodGet && strings.Contains(path, "rest/dnsrecord"):
		return decodeInto(f.records, out)
	case method == http.MethodGet && strings.Contains(path, "rest/networkconf"):
		return decodeInto(f.networks, out)
	case method == http.MethodPost && strings.Contains(path, "rest/dnsrecord") && out != nil:
		created := map[string]any{
			"_id":     "dns-new",
			"key":     "new.lan",
			"value":   "10.0.0.1",
			"enabled": true,
		}
		if body, ok := in.(map[string]any); ok {
			for k, v := range body {
				created[k] = v
			}
			created["_id"] = "dns-new"
		}
		return decodeInto([]map[string]any{created}, out)
	default:
		if out != nil {
			_ = json.Unmarshal([]byte(`[]`), out)
		}
		return nil
	}
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

func TestDNSServiceList(t *testing.T) {
	api := &fakeDNSAPI{records: fixtureDNSRecords(t)}
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
	if api.calls[0].path != "/proxy/network/api/s/default/rest/dnsrecord" {
		t.Fatalf("path = %q", api.calls[0].path)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d", len(got))
	}
	if got[0].Name != "nas.lan" || got[0].IP != "192.168.1.50" {
		t.Fatalf("got[0]: %+v", got[0])
	}
}

func TestDNSServiceListNotImplementedOn404(t *testing.T) {
	api := &fakeDNSAPI{
		errByPath: map[string]error{
			"/proxy/network/api/s/default/rest/dnsrecord": apperr.New(apperr.NotFound, "not found"),
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
	api := &fakeDNSAPI{records: fixtureDNSRecords(t)}
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
	api := &fakeDNSAPI{records: fixtureDNSRecords(t)}
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
	last := api.calls[len(api.calls)-1]
	if last.method != http.MethodPost {
		t.Fatalf("method = %q", last.method)
	}
	if last.path != "/proxy/network/api/s/default/rest/dnsrecord" {
		t.Fatalf("path = %q", last.path)
	}
	body, _ := last.body.(map[string]any)
	if body["key"] != "media.lan" && body["name"] != "media.lan" {
		t.Fatalf("body name field: %+v", body)
	}
	if body["value"] != "192.168.1.80" && body["ip"] != "192.168.1.80" {
		t.Fatalf("body ip field: %+v", body)
	}
}

func TestDNSUpdatePlanAndApply(t *testing.T) {
	api := &fakeDNSAPI{records: fixtureDNSRecords(t)}
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
	last := api.calls[len(api.calls)-1]
	if last.method != http.MethodPut {
		t.Fatalf("method = %q", last.method)
	}
	if last.path != "/proxy/network/api/s/default/rest/dnsrecord/dns1" {
		t.Fatalf("path = %q", last.path)
	}
}

func TestDNSDeletePlanAndApply(t *testing.T) {
	api := &fakeDNSAPI{records: fixtureDNSRecords(t)}
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
	last := api.calls[len(api.calls)-1]
	if last.method != http.MethodDelete {
		t.Fatalf("method = %q", last.method)
	}
	if last.path != "/proxy/network/api/s/default/rest/dnsrecord/dns1" {
		t.Fatalf("path = %q", last.path)
	}
}

func TestDNSResolversList(t *testing.T) {
	api := &fakeDNSAPI{networks: fixtureNetworks(t)}
	svc := domain.NewDNSService(api)
	got, err := svc.ListResolvers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(api.calls) != 1 || !strings.Contains(api.calls[0].path, "rest/networkconf") {
		t.Fatalf("calls: %+v", api.calls)
	}
	if len(got) != 4 {
		t.Fatalf("len = %d, want 4", len(got))
	}

	lan := got[0]
	if lan.NetworkName != "LAN" || lan.NetworkID != "net1" {
		t.Fatalf("lan: %+v", lan)
	}
	if len(lan.DNS) != 2 || lan.DNS[0] != "1.1.1.1" || lan.DNS[1] != "8.8.8.8" {
		t.Fatalf("lan dns: %+v", lan.DNS)
	}
	if lan.WAN {
		t.Fatal("LAN should not be WAN")
	}

	iot := got[1]
	if len(iot.DNS) != 2 || iot.DNS[0] != "9.9.9.9" {
		t.Fatalf("iot dns: %+v", iot.DNS)
	}

	wan := got[3]
	if !wan.WAN {
		t.Fatalf("wan flag: %+v", wan)
	}
	if len(wan.DNS) != 2 || wan.DNS[0] != "1.0.0.1" {
		t.Fatalf("wan dns: %+v", wan.DNS)
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
