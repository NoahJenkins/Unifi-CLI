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

type stableDNSAPI struct {
	records         []map[string]any
	byID            map[string]map[string]any
	createResponse  map[string]any
	mutationErr     error
	keepDeletedName bool
	fetchCalls      []string
	calls           []dnsCall
}

func (f *stableDNSAPI) FetchOfficialObjects(_ context.Context, path string) ([]map[string]any, error) {
	f.fetchCalls = append(f.fetchCalls, path)
	return cloneDNSMaps(f.records), nil
}

func (f *stableDNSAPI) Do(_ context.Context, method, path string, in, out any) error {
	f.calls = append(f.calls, dnsCall{method: method, path: path, body: in})
	if method != http.MethodGet && f.mutationErr != nil {
		return f.mutationErr
	}
	id := path[strings.LastIndex(path, "/")+1:]
	switch method {
	case http.MethodGet:
		record, ok := f.byID[id]
		if !ok {
			return apperr.New(apperr.NotFound, "dns policy not found")
		}
		return decodeInto(record, out)
	case http.MethodPost:
		return decodeInto(f.createResponse, out)
	case http.MethodPut:
		return nil
	case http.MethodDelete:
		delete(f.byID, id)
		if !f.keepDeletedName {
			filtered := f.records[:0]
			for _, record := range f.records {
				if record["id"] != id {
					filtered = append(filtered, record)
				}
			}
			f.records = filtered
		}
		return nil
	default:
		return apperr.New(apperr.Internal, "unexpected method")
	}
}

func (f *stableDNSAPI) IntegrationSitePath(_ context.Context, parts ...string) (string, error) {
	return "/proxy/network/integration/v1/sites/site-uuid/" + strings.Join(parts, "/"), nil
}

func (f *stableDNSAPI) SitePath(parts ...string) string {
	return "/proxy/network/api/s/default/" + strings.Join(parts, "/")
}

func cloneDNSMaps(in []map[string]any) []map[string]any {
	out := make([]map[string]any, len(in))
	for i, record := range in {
		out[i] = make(map[string]any, len(record))
		for key, value := range record {
			out[i][key] = value
		}
	}
	return out
}

func officialDNSFixture(t *testing.T) []map[string]any {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	path := filepath.Join(filepath.Dir(file), "..", "client", "fixtures", "official", "dns-policies-all-types.json")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var page client.OfficialPage[map[string]any]
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatal(err)
	}
	return page.Data
}

func TestDNSListUsesOfficialCollectionFetcherAndPreservesAllPolicyFields(t *testing.T) {
	api := &stableDNSAPI{records: officialDNSFixture(t)}
	records, err := domain.NewDNSService(api).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(api.fetchCalls) != 1 || api.fetchCalls[0] != "/proxy/network/integration/v1/sites/site-uuid/dns/policies" {
		t.Fatalf("official collection calls = %#v", api.fetchCalls)
	}
	if len(records) != 7 {
		t.Fatalf("records = %d, want 7", len(records))
	}
	want := []domain.DNSRecord{
		{ID: "10000000-0000-4000-8000-000000000001", Type: "A_RECORD", Domain: "a.example.test", Enabled: true, IPv4Address: "192.0.2.10", TTLSeconds: 300, Name: "a.example.test", IP: "192.0.2.10"},
		{ID: "10000000-0000-4000-8000-000000000002", Type: "AAAA_RECORD", Domain: "aaaa.example.test", Enabled: true, IPv6Address: "2001:db8::10", TTLSeconds: 600, Name: "aaaa.example.test"},
		{ID: "10000000-0000-4000-8000-000000000003", Type: "CNAME_RECORD", Domain: "alias.example.test", Enabled: true, TargetDomain: "target.example.test", TTLSeconds: 900, Name: "alias.example.test"},
		{ID: "10000000-0000-4000-8000-000000000004", Type: "MX_RECORD", Domain: "example.test", Enabled: true, MailServerDomain: "mail.example.test", TTLSeconds: 1200, Priority: 10, Name: "example.test"},
		{ID: "10000000-0000-4000-8000-000000000005", Type: "TXT_RECORD", Domain: "txt.example.test", Enabled: false, Text: "verification=value", TTLSeconds: 1800, Name: "txt.example.test"},
		{ID: "10000000-0000-4000-8000-000000000006", Type: "SRV_RECORD", Domain: "_sip._tcp.example.test", Enabled: true, ServerDomain: "sip.example.test", TTLSeconds: 2400, Priority: 20, Service: "_sip", Protocol: "_tcp", Port: 5060, Weight: 5, Name: "_sip._tcp.example.test"},
		{ID: "10000000-0000-4000-8000-000000000007", Type: "FORWARD_DOMAIN", Domain: "forward.example.test", Enabled: true, ServerDomain: "resolver.example.test", IPAddress: "198.51.100.53", Name: "forward.example.test"},
	}
	if !reflect.DeepEqual(records, want) {
		t.Fatalf("records =\n%#v\nwant =\n%#v", records, want)
	}
	encoded, err := json.Marshal(records)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"type"`, `"domain"`, `"ipv4_address"`, `"ipv6_address"`, `"target_domain"`, `"mail_server_domain"`, `"text"`, `"server_domain"`, `"ip_address"`, `"ttl_seconds"`, `"priority"`, `"service"`, `"protocol"`, `"port"`, `"weight"`} {
		if !strings.Contains(string(encoded), field) {
			t.Fatalf("normalized JSON missing %s: %s", field, encoded)
		}
	}
}

func TestDNSSRVJSONRetainsApplicableZeroNumericFields(t *testing.T) {
	record := domain.NormalizeDNSRecord(map[string]any{
		"id": "dns-srv-zero", "type": "SRV_RECORD", "domain": "_sip._tcp.example.test", "enabled": true,
		"serverDomain": "sip.example.test", "ttlSeconds": float64(300), "priority": float64(0),
		"service": "_sip", "protocol": "_tcp", "port": float64(5060), "weight": float64(0),
	})
	encoded, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"priority":0`, `"weight":0`} {
		if !strings.Contains(string(encoded), field) {
			t.Fatalf("SRV JSON dropped applicable field %s: %s", field, encoded)
		}
	}
}

func TestDNSCreateRejectsInvalidInputBeforeMutation(t *testing.T) {
	tests := []struct {
		name string
		in   domain.DNSInput
	}{
		{name: "empty domain", in: domain.DNSInput{IP: "192.0.2.1"}},
		{name: "empty label", in: domain.DNSInput{Name: "bad..example.test", IP: "192.0.2.1"}},
		{name: "label too long", in: domain.DNSInput{Name: strings.Repeat("a", 64) + ".test", IP: "192.0.2.1"}},
		{name: "name too long", in: domain.DNSInput{Name: strings.Repeat("a.", 127) + "a", IP: "192.0.2.1"}},
		{name: "invalid character", in: domain.DNSInput{Name: "bad_name.example.test", IP: "192.0.2.1"}},
		{name: "not IPv4", in: domain.DNSInput{Name: "good.example.test", IP: "2001:db8::1"}},
		{name: "invalid IP", in: domain.DNSInput{Name: "good.example.test", IP: "999.0.2.1"}},
		{name: "zero TTL", in: domain.DNSInput{Name: "good.example.test", IP: "192.0.2.1", TTLSeconds: 0, SetTTL: true}},
		{name: "negative TTL", in: domain.DNSInput{Name: "good.example.test", IP: "192.0.2.1", TTLSeconds: -1, SetTTL: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := &stableDNSAPI{}
			_, err := domain.NewDNSService(api).Create(context.Background(), tt.in)
			if !apperr.Is(err, apperr.ValidationFailed) {
				t.Fatalf("err = %v, want validation_failed", err)
			}
			if len(api.calls) != 0 {
				t.Fatalf("invalid input reached controller: %#v", api.calls)
			}
		})
	}
}

func TestDNSUpdateRejectsNoChangedFieldsAndInvalidChangedFields(t *testing.T) {
	record := map[string]any{"id": "dns-a", "type": "A_RECORD", "domain": "a.example.test", "ipv4Address": "192.0.2.1", "enabled": true, "ttlSeconds": 300}
	tests := []struct {
		name string
		in   domain.DNSInput
	}{
		{name: "no flags", in: domain.DNSInput{}},
		{name: "empty changed domain", in: domain.DNSInput{SetName: true}},
		{name: "IPv6 changed address", in: domain.DNSInput{IP: "2001:db8::1", SetIP: true}},
		{name: "zero changed TTL", in: domain.DNSInput{TTLSeconds: 0, SetTTL: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := &stableDNSAPI{records: []map[string]any{record}, byID: map[string]map[string]any{"dns-a": record}}
			_, _, err := domain.NewDNSService(api).Update(context.Background(), "dns-a", tt.in)
			if !apperr.Is(err, apperr.ValidationFailed) {
				t.Fatalf("err = %v, want validation_failed", err)
			}
			if len(api.calls) != 0 {
				t.Fatalf("invalid update reached mutation endpoint: %#v", api.calls)
			}
		})
	}
}

func TestDNSUpdateRejectsEffectiveNoOpBeforePUT(t *testing.T) {
	record := map[string]any{"id": "dns-a", "type": "A_RECORD", "domain": "a.example.test", "ipv4Address": "192.0.2.1", "enabled": true, "ttlSeconds": 300}
	tests := []struct {
		name string
		in   domain.DNSInput
	}{
		{name: "same name", in: domain.DNSInput{Name: "a.example.test", SetName: true}},
		{name: "same IPv4", in: domain.DNSInput{IP: "192.0.2.1", SetIP: true}},
		{name: "same TTL", in: domain.DNSInput{TTLSeconds: 300, SetTTL: true}},
		{name: "same enabled", in: domain.DNSInput{Enabled: true, SetEnabled: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name+" plan", func(t *testing.T) {
			api := &stableDNSAPI{records: []map[string]any{record}, byID: map[string]map[string]any{"dns-a": record}}
			_, _, err := domain.NewDNSService(api).Update(context.Background(), "dns-a", tt.in)
			if !apperr.Is(err, apperr.ValidationFailed) {
				t.Fatalf("err = %v, want validation_failed", err)
			}
			if countMethod(api.calls, http.MethodPut) != 0 {
				t.Fatalf("effective no-op reached PUT: %#v", api.calls)
			}
		})

		t.Run(tt.name+" apply", func(t *testing.T) {
			api := &stableDNSAPI{records: []map[string]any{record}, byID: map[string]map[string]any{"dns-a": record}}
			_, err := domain.NewDNSService(api).ApplyUpdate(context.Background(), "dns-a", tt.in)
			if !apperr.Is(err, apperr.ValidationFailed) {
				t.Fatalf("err = %v, want validation_failed", err)
			}
			if countMethod(api.calls, http.MethodPut) != 0 {
				t.Fatalf("effective no-op reached PUT: %#v", api.calls)
			}
		})
	}
}

func TestDNSUpdateAndDeleteRejectNonARecordsBeforeMutation(t *testing.T) {
	record := map[string]any{"id": "dns-aaaa", "type": "AAAA_RECORD", "domain": "aaaa.example.test", "ipv6Address": "2001:db8::1", "enabled": true, "ttlSeconds": 300}
	for _, operation := range []struct {
		name string
		run  func(*domain.DNSService) error
	}{
		{name: "update", run: func(svc *domain.DNSService) error {
			_, _, err := svc.Update(context.Background(), "dns-aaaa", domain.DNSInput{IP: "192.0.2.2", SetIP: true})
			return err
		}},
		{name: "delete", run: func(svc *domain.DNSService) error {
			_, _, err := svc.Delete(context.Background(), "dns-aaaa")
			return err
		}},
	} {
		t.Run(operation.name, func(t *testing.T) {
			api := &stableDNSAPI{records: []map[string]any{record}, byID: map[string]map[string]any{"dns-aaaa": record}}
			err := operation.run(domain.NewDNSService(api))
			if !apperr.Is(err, apperr.ValidationFailed) {
				t.Fatalf("err = %v, want validation_failed", err)
			}
			for _, call := range api.calls {
				if call.method != http.MethodGet {
					t.Fatalf("non-A policy reached mutation: %#v", api.calls)
				}
			}
		})
	}
}

func TestDNSUpdateAndDeleteRejectResolvedRecordsWithoutIDs(t *testing.T) {
	record := map[string]any{"type": "A_RECORD", "domain": "missing-id.example.test", "ipv4Address": "192.0.2.1", "enabled": true, "ttlSeconds": 300}
	for _, operation := range []struct {
		name string
		run  func(*domain.DNSService) error
	}{
		{name: "update", run: func(svc *domain.DNSService) error {
			_, _, err := svc.Update(context.Background(), "missing-id.example.test", domain.DNSInput{IP: "192.0.2.2", SetIP: true})
			return err
		}},
		{name: "delete", run: func(svc *domain.DNSService) error {
			_, _, err := svc.Delete(context.Background(), "missing-id.example.test")
			return err
		}},
	} {
		t.Run(operation.name, func(t *testing.T) {
			api := &stableDNSAPI{records: []map[string]any{record}}
			err := operation.run(domain.NewDNSService(api))
			if !apperr.Is(err, apperr.Internal) {
				t.Fatalf("err = %v, want internal", err)
			}
			if len(api.calls) != 0 {
				t.Fatalf("record without ID reached controller: %#v", api.calls)
			}
		})
	}
}

func TestDNSCreateRequiresReturnedIDAndVerifiesByID(t *testing.T) {
	in := domain.DNSInput{Name: "new.example.test", IP: "192.0.2.20", TTLSeconds: 600, SetTTL: true}
	t.Run("missing ID", func(t *testing.T) {
		api := &stableDNSAPI{createResponse: map[string]any{"type": "A_RECORD", "domain": in.Name}}
		_, err := domain.NewDNSService(api).ApplyCreate(context.Background(), in)
		if !apperr.Is(err, apperr.Internal) {
			t.Fatalf("err = %v, want internal", err)
		}
		if len(api.calls) != 1 || api.calls[0].method != http.MethodPost {
			t.Fatalf("calls = %#v, want one POST and no retry", api.calls)
		}
	})

	t.Run("authoritative GET mismatch", func(t *testing.T) {
		api := &stableDNSAPI{
			createResponse: map[string]any{"id": "dns-new"},
			byID:           map[string]map[string]any{"dns-new": {"id": "dns-new", "type": "A_RECORD", "domain": in.Name, "ipv4Address": "192.0.2.99", "enabled": true, "ttlSeconds": 600}},
		}
		_, err := domain.NewDNSService(api).ApplyCreate(context.Background(), in)
		if !apperr.Is(err, apperr.Conflict) {
			t.Fatalf("err = %v, want conflict", err)
		}
		if got := methods(api.calls); !reflect.DeepEqual(got, []string{http.MethodPost, http.MethodGet}) {
			t.Fatalf("methods = %#v, want POST once then verification GET", got)
		}
	})

	t.Run("authoritative GET returns different ID", func(t *testing.T) {
		api := &stableDNSAPI{
			createResponse: map[string]any{"id": "dns-new"},
			byID:           map[string]map[string]any{"dns-new": {"id": "dns-other", "type": "A_RECORD", "domain": in.Name, "ipv4Address": in.IP, "enabled": true, "ttlSeconds": 600}},
		}
		_, err := domain.NewDNSService(api).ApplyCreate(context.Background(), in)
		if !apperr.Is(err, apperr.Conflict) {
			t.Fatalf("err = %v, want conflict", err)
		}
		if got := methods(api.calls); !reflect.DeepEqual(got, []string{http.MethodPost, http.MethodGet}) {
			t.Fatalf("methods = %#v, want POST once then verification GET", got)
		}
	})
}

func TestDNSUpdateVerifiesRequestedFieldsWithGETByIDAndDoesNotRetryMutation(t *testing.T) {
	before := map[string]any{"id": "dns-a", "type": "A_RECORD", "domain": "a.example.test", "ipv4Address": "192.0.2.1", "enabled": true, "ttlSeconds": 300}
	after := map[string]any{"id": "dns-a", "type": "A_RECORD", "domain": "a.example.test", "ipv4Address": "192.0.2.99", "enabled": true, "ttlSeconds": 300}
	api := &stableDNSAPI{records: []map[string]any{before}, byID: map[string]map[string]any{"dns-a": after}}
	_, err := domain.NewDNSService(api).ApplyUpdate(context.Background(), "dns-a", domain.DNSInput{IP: "192.0.2.2", SetIP: true})
	if !apperr.Is(err, apperr.Conflict) {
		t.Fatalf("err = %v, want conflict", err)
	}
	if got := methods(api.calls); !reflect.DeepEqual(got, []string{http.MethodPut, http.MethodGet}) {
		t.Fatalf("methods = %#v, want PUT once then verification GET", got)
	}
}

func TestDNSDeleteVerifiesIDAndExactNameAbsence(t *testing.T) {
	record := map[string]any{"id": "dns-a", "type": "A_RECORD", "domain": "a.example.test", "ipv4Address": "192.0.2.1", "enabled": true, "ttlSeconds": 300}
	t.Run("success", func(t *testing.T) {
		api := &stableDNSAPI{records: []map[string]any{record}, byID: map[string]map[string]any{"dns-a": record}}
		got, err := domain.NewDNSService(api).ApplyDelete(context.Background(), "dns-a")
		if err != nil {
			t.Fatal(err)
		}
		if got.ID != "dns-a" {
			t.Fatalf("deleted = %#v", got)
		}
		if gotMethods := methods(api.calls); !reflect.DeepEqual(gotMethods, []string{http.MethodDelete, http.MethodGet}) {
			t.Fatalf("methods = %#v, want DELETE once then GET by ID", gotMethods)
		}
		if len(api.fetchCalls) != 2 {
			t.Fatalf("collection fetches = %d, want resolve plus exact-name verification", len(api.fetchCalls))
		}
	})

	t.Run("same exact name remains", func(t *testing.T) {
		api := &stableDNSAPI{records: []map[string]any{record}, byID: map[string]map[string]any{"dns-a": record}, keepDeletedName: true}
		_, err := domain.NewDNSService(api).ApplyDelete(context.Background(), "dns-a")
		if !apperr.Is(err, apperr.Conflict) {
			t.Fatalf("err = %v, want conflict", err)
		}
		if got := methods(api.calls); !reflect.DeepEqual(got, []string{http.MethodDelete, http.MethodGet}) {
			t.Fatalf("methods = %#v, want no mutation retry", got)
		}
	})
}

func methods(calls []dnsCall) []string {
	out := make([]string, len(calls))
	for i, call := range calls {
		out[i] = call.method
	}
	return out
}

func countMethod(calls []dnsCall, method string) int {
	count := 0
	for _, call := range calls {
		if call.method == method {
			count++
		}
	}
	return count
}
